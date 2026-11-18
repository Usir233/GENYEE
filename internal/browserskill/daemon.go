package browserskill

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
)

// RuntimeDirEnv is the optional environment override for the private
// BrowserSkill runtime base directory.
const RuntimeDirEnv = "GENYEE_BSK_RUNTIME_DIR"

var daemonAllowedEnvKeys = map[string]struct{}{
	"PATH":            {},
	"HOME":            {},
	"USER":            {},
	"LOGNAME":         {},
	"SHELL":           {},
	"LANG":            {},
	"LC_ALL":          {},
	"LC_CTYPE":        {},
	"TZ":              {},
	"DISPLAY":         {},
	"WAYLAND_DISPLAY": {},
	"XDG_RUNTIME_DIR": {},
	"SYSTEMROOT":      {},
	"WINDIR":          {},
	"COMSPEC":         {},
	"PATHEXT":         {},
	"LOCALAPPDATA":    {},
	"APPDATA":         {},
	"USERPROFILE":     {},
}

// One native process per app instance. Devices only own their connection and tasks.
type daemon struct {
	home       string
	bskHome    string
	secret     string
	gatekeeper net.Listener
	port       int
	cmd        *exec.Cmd
	done       chan struct{}
}

// RuntimeBaseDir resolves the controlled private base directory (mode 0700)
// used for BrowserSkill runtime state and daemon sockets. World-writable shared
// temp directories (/tmp, /private/tmp, /var/tmp) are never used.
func RuntimeBaseDir() (string, error) {
	if raw := strings.TrimSpace(os.Getenv(RuntimeDirEnv)); raw != "" {
		if !filepath.IsAbs(raw) {
			return "", fmt.Errorf("%s must be an absolute path", RuntimeDirEnv)
		}
		cleaned := filepath.Clean(raw)
		if filepath.Dir(cleaned) == cleaned {
			return "", fmt.Errorf("%s must not be filesystem root", RuntimeDirEnv)
		}
		switch cleaned {
		case "/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp":
			return "", fmt.Errorf("%s must not be a shared temp directory", RuntimeDirEnv)
		}
		return cleaned, nil
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		cleanedHome := filepath.Clean(home)
		if filepath.IsAbs(cleanedHome) && filepath.Dir(cleanedHome) != cleanedHome {
			return filepath.Join(cleanedHome, ".genyee", "bsk"), nil
		}
	}
	if cfgDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(cfgDir) != "" {
		cleanedCfg := filepath.Clean(cfgDir)
		if filepath.IsAbs(cleanedCfg) && filepath.Dir(cleanedCfg) != cleanedCfg {
			return filepath.Join(cleanedCfg, "genyee", "bsk"), nil
		}
	}
	return "", errors.New("could not resolve private BrowserSkill runtime directory")
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("BrowserSkill runtime path %q must be a real directory", dir)
	}
	return os.Chmod(dir, 0o700)
}

// buildDaemonEnv constructs a minimal allowlisted environment for the bsk
// child process so application secrets (SYSTEM_AES_KEY, JWT_SECRET, DB_PASSWORD,
// cluster secrets, dynamic loader variables, etc.) never leak to child processes.
func buildDaemonEnv(bskHome, tmpDir string, environ []string) []string {
	seen := make(map[string]int, len(daemonAllowedEnvKeys)+4)
	out := make([]string, 0, len(daemonAllowedEnvKeys)+4)
	for _, kv := range environ {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || key == "" {
			continue
		}
		if _, allowed := daemonAllowedEnvKeys[key]; !allowed {
			continue
		}
		entry := key + "=" + val
		if idx, exists := seen[key]; exists {
			out[idx] = entry
			continue
		}
		seen[key] = len(out)
		out = append(out, entry)
	}
	out = append(out,
		"BSK_HOME="+bskHome,
		"TMPDIR="+tmpDir,
		"TMP="+tmpDir,
		"TEMP="+tmpDir,
	)
	return out
}

func generateDaemonSecret() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func startDaemonSocketGatekeeper(outerSockPath, innerSockPath, secret string) (net.Listener, error) {
	if secret == "" {
		return nil, errors.New("daemon socket secret is required")
	}
	if err := ensurePrivateDir(filepath.Dir(outerSockPath)); err != nil {
		return nil, err
	}
	_ = os.Remove(outerSockPath)
	ln, err := net.Listen("unix", outerSockPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(outerSockPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go handleDaemonSocketConn(conn, innerSockPath, secret)
		}
	}()
	return ln, nil
}

func handleDaemonSocketConn(clientConn net.Conn, innerSockPath, secret string) {
	defer func() { _ = clientConn.Close() }()
	_ = clientConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(clientConn, maxFrame)).ReadBytes('\n')
	if err != nil {
		return
	}
	var frame map[string]json.RawMessage
	if json.Unmarshal(line, &frame) != nil || frame == nil {
		_ = clientConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = json.NewEncoder(clientConn).Encode(rpcReply{
			ID:    "0",
			Error: &RPCError{Code: "protocol_error", Message: "invalid request"},
		})
		return
	}
	var reqID string
	_ = json.Unmarshal(frame["id"], &reqID)
	var authToken string
	_ = json.Unmarshal(frame["auth_token"], &authToken)
	if subtle.ConstantTimeCompare([]byte(authToken), []byte(secret)) != 1 {
		_ = clientConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = json.NewEncoder(clientConn).Encode(rpcReply{
			ID:    reqID,
			Error: &RPCError{Code: "unauthorized", Message: "invalid or missing daemon socket auth token"},
		})
		return
	}
	delete(frame, "auth_token")
	cleanPayload, err := json.Marshal(frame)
	if err != nil {
		return
	}

	_ = clientConn.SetReadDeadline(time.Time{})
	innerConn, err := (&net.Dialer{Timeout: 5 * time.Second}).Dial("unix", innerSockPath)
	if err != nil {
		_ = clientConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = json.NewEncoder(clientConn).Encode(rpcReply{
			ID:    reqID,
			Error: &RPCError{Code: "unavailable", Message: "BrowserSkill daemon unavailable"},
		})
		return
	}
	defer func() { _ = innerConn.Close() }()

	replyDone := make(chan struct{})
	go func() {
		var buf [1]byte
		_, _ = clientConn.Read(buf[:])
		select {
		case <-replyDone:
		default:
			_ = innerConn.Close()
		}
	}()

	_ = innerConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := innerConn.Write(append(cleanPayload, '\n')); err != nil {
		close(replyDone)
		return
	}
	_ = innerConn.SetWriteDeadline(time.Time{})

	replyLine, err := bufio.NewReader(io.LimitReader(innerConn, maxFrame)).ReadBytes('\n')
	close(replyDone)
	if err != nil {
		return
	}
	_ = clientConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, _ = clientConn.Write(replyLine)
}

func connectionLimit() int {
	if n, err := strconv.Atoi(os.Getenv("BROWSERSKILL_MAX_CONNECTIONS")); err == nil && n > 0 {
		return n
	}
	return 32
}

func (d *daemon) exited() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

func (d *daemon) stop() {
	if d.gatekeeper != nil {
		_ = d.gatekeeper.Close()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
	}
	if d.done != nil {
		<-d.done
	}
	if d.home != "" {
		_ = os.RemoveAll(d.home)
	}
}

// Only startup waiters block. Status, existing tools and revocations never wait
// on process startup under the manager mutex.
func (m *Manager) ensureDaemon(ctx context.Context) (*daemon, error) {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("browser manager is closed")
		}
		if m.daemon != nil && !m.daemon.exited() {
			d := m.daemon
			m.mu.Unlock()
			return d, nil
		}
		if pending := m.starting; pending != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		m.starting = pending
		previous := m.daemon
		m.daemon = nil
		m.mu.Unlock()
		if previous != nil {
			previous.stop()
		}
		d, err := m.start(ctx)
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			if d != nil {
				d.stop()
			}
			m.mu.Lock()
			err = errors.New("browser manager is closed")
		} else if err == nil {
			m.daemon = d
		}
		m.starting = nil
		close(pending)
		m.mu.Unlock()
		return d, err
	}
}

// Close disconnects all users and stops the single process owned by this manager.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.ensureStopChLocked())
	for _, d := range m.devices {
		d.mu.Lock()
		disconnectDeviceLocked(d)
		d.expires = time.Time{}
		d.mu.Unlock()
	}
	m.devices = map[string]*device{}
	runtime, pending := m.daemon, m.starting
	m.daemon = nil
	m.mu.Unlock()
	m.bgWorkers.Wait()
	if m.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := m.store.releaseOwner(ctx, m.nodeID); err != nil {
			logger.Warnf(ctx, "[BrowserSkill] release owner %s failed: %v", m.nodeID, err)
		}
		cancel()
	}
	if runtime != nil {
		runtime.stop()
	}
	if pending != nil {
		<-pending
	}
}

func (m *Manager) start(ctx context.Context) (*daemon, error) {
	baseDir, err := RuntimeBaseDir()
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(baseDir); err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp(baseDir, "d-")
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(home); err != nil {
		_ = os.RemoveAll(home)
		return nil, err
	}
	bskHome := filepath.Join(home, "b")
	tmpDir := filepath.Join(home, "t")
	if err := ensurePrivateDir(bskHome); err != nil {
		_ = os.RemoveAll(home)
		return nil, err
	}
	if err := ensurePrivateDir(tmpDir); err != nil {
		_ = os.RemoveAll(home)
		return nil, err
	}
	secret, err := generateDaemonSecret()
	if err != nil {
		_ = os.RemoveAll(home)
		return nil, err
	}
	gatekeeper, err := startDaemonSocketGatekeeper(
		filepath.Join(home, "run", "daemon.sock"),
		filepath.Join(bskHome, "run", "daemon.sock"),
		secret,
	)
	if err != nil {
		_ = os.RemoveAll(home)
		return nil, err
	}
	cmd := exec.Command(
		m.binary,
		"daemon",
		"start",
		"--foreground",
		"--port",
		"0",
		"--daemon-idle",
		"24h",
		"--session-idle",
		"30m",
	)
	cmd.Env = buildDaemonEnv(bskHome, tmpDir, os.Environ())
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		_ = gatekeeper.Close()
		_ = os.RemoveAll(home)
		return nil, errors.New("could not start BrowserSkill daemon")
	}
	d := &daemon{
		home:       home,
		bskHome:    bskHome,
		secret:     secret,
		gatekeeper: gatekeeper,
		cmd:        cmd,
		done:       make(chan struct{}),
	}
	go func() { _ = cmd.Wait(); close(d.done) }()
	started := false
	defer func() {
		if !started {
			_ = gatekeeper.Close()
			_ = cmd.Process.Kill()
			<-d.done
			_ = os.RemoveAll(home)
		}
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, ctx.Err()
		case <-d.done:
			return nil, errors.New("BrowserSkill daemon exited during startup")
		case <-timer.C:
			_ = cmd.Process.Kill()
			return nil, errors.New("BrowserSkill daemon startup timed out")
		case <-tick.C:
			data, e := os.ReadFile(filepath.Join(bskHome, "daemon.json"))
			if e != nil {
				continue
			}
			var info struct {
				Port int `json:"ws_port"`
			}
			if json.Unmarshal(data, &info) == nil && info.Port > 0 {
				d.port = info.Port
				started = true
				return d, nil
			}
		}
	}
}
