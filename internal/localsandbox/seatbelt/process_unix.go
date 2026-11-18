//go:build unix

package seatbelt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Usir233/GENYEE/internal/localsandbox/core"
	"github.com/Usir233/GENYEE/internal/logger"
)

func buildSeatbeltExecCmd(execPath string, argv []string, cmd core.Command) *exec.Cmd {
	// Not CommandContext: cancellation must kill the whole process group, and
	// CommandContext only signals the root process.
	execCmd := exec.Command(execPath, argv...)
	execCmd.Dir = cmd.Cwd
	execCmd.Env = withSandboxTemp(mergedEnv(cmd.Env), cmd.Cwd, cmd.Env)
	if len(cmd.Stdin) > 0 {
		execCmd.Stdin = bytes.NewReader(cmd.Stdin)
	}
	// Setpgid puts the child in its own process group so descendants can be
	// terminated together.
	execCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return execCmd
}

func spawnExecCmd(ctx context.Context, execCmd *exec.Cmd, cwd, argv0 string) (*seatbeltProcess, error) {
	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("localsandbox: stdout pipe: %w", err)
	}
	stderr, err := execCmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, fmt.Errorf("localsandbox: stderr pipe: %w", err)
	}
	started := time.Now()
	if err := execCmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		logger.Errorf(ctx, "[LocalSandbox] seatbelt start cwd=%s: %v", cwd, err)
		return nil, fmt.Errorf("localsandbox: start sandboxed command: %w", err)
	}
	pid := 0
	if execCmd.Process != nil {
		pid = execCmd.Process.Pid
	}
	logger.Infof(ctx, "[LocalSandbox] seatbelt spawn pid=%d cwd=%s argv0=%s", pid, cwd, argv0)

	proc := &seatbeltProcess{
		cmd:      execCmd,
		pid:      pid,
		stdout:   stdout,
		stderr:   stderr,
		started:  started,
		waitDone: make(chan struct{}),
	}
	go proc.watchContext(ctx)
	return proc, nil
}

func mergedEnv(env map[string]string) []string {
	// Nil is a direct-Spawn fallback: filtered inherit. Service always
	// passes a map already run through BuildCommandEnv.
	if env == nil {
		return core.FilterInheritedEnv(os.Environ())
	}
	return core.EnvSlice(env)
}

func withSandboxTemp(env []string, cwd string, explicit map[string]string) []string {
	// Command.Env is the only way a caller opts into a custom temp dir. An
	// inherited host TMPDIR (Go tests set one under /var/folders) would
	// otherwise skip injection and leave python/git writing outside the
	// writable root.
	for k := range explicit {
		if strings.EqualFold(k, "TMPDIR") {
			return env
		}
	}
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, "TMPDIR") ||
			strings.EqualFold(name, "TMP") ||
			strings.EqualFold(name, "TEMP") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"TMPDIR="+cwd,
		"TMP="+cwd,
		"TEMP="+cwd,
	)
}

const seatbeltKillDrainGrace = 100 * time.Millisecond

type seatbeltProcess struct {
	cmd      *exec.Cmd
	pid      int
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	started  time.Time
	waitDone chan struct{}

	mu        sync.Mutex
	killed    bool
	reaped    bool
	closeOnce sync.Once
}

func (p *seatbeltProcess) Stdout() io.Reader { return p.stdout }
func (p *seatbeltProcess) Stderr() io.Reader { return p.stderr }
func (p *seatbeltProcess) PID() int          { return p.pid }

func (p *seatbeltProcess) watchContext(ctx context.Context) {
	select {
	case <-p.waitDone:
		return
	case <-ctx.Done():
	}
	// ctx fired. If Wait already reaped the process, skip Kill: SIGKILL to
	// -pid after wait has a PID-reuse window. waitDone is closed first.
	select {
	case <-p.waitDone:
		return
	default:
		if err := p.Kill(); err != nil {
			logger.Warnf(ctx, "[LocalSandbox] seatbelt kill pid=%d on context cancel: %v", p.pid, err)
		}
	}
}

// Kill terminates the entire process group. Killing only the root would leave
// descendants running after a timeout. Holding mu across syscall.Kill closes
// the race window between Wait reaping the PID and Kill signalling -pid.
func (p *seatbeltProcess) Kill() error {
	p.mu.Lock()
	// reaped is set under this lock as soon as Wait returns. Cmd.ProcessState
	// is written by Wait itself, so another goroutine must not read it.
	if p.reaped {
		p.mu.Unlock()
		return nil
	}
	p.killed = true
	pid := p.pid
	var killErr error
	if pid != 0 {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			killErr = fmt.Errorf("localsandbox: kill process group: %w", err)
		}
	}
	p.mu.Unlock()

	p.closeOnce.Do(func() {
		go func() {
			timer := time.NewTimer(seatbeltKillDrainGrace)
			defer timer.Stop()
			select {
			case <-p.waitDone:
				return
			case <-timer.C:
				_ = p.stdout.Close()
				_ = p.stderr.Close()
			}
		}()
	})
	return killErr
}

func (p *seatbeltProcess) Wait(ctx context.Context) (core.ExitStatus, error) {
	if ctx != nil {
		stop := context.AfterFunc(ctx, func() {
			select {
			case <-p.waitDone:
				return
			default:
				if killErr := p.Kill(); killErr != nil {
					logger.Warnf(ctx, "[LocalSandbox] seatbelt kill pid=%d on wait cancel: %v", p.pid, killErr)
				}
			}
		})
		defer stop()
	}

	err := p.cmd.Wait()
	p.mu.Lock()
	p.reaped = true
	killed := p.killed
	p.mu.Unlock()
	close(p.waitDone)
	duration := time.Since(p.started)

	exitCode := -1
	if p.cmd.ProcessState != nil {
		exitCode = p.cmd.ProcessState.ExitCode()
	}
	status := core.ExitStatus{Code: exitCode, Killed: killed, Duration: duration}
	// A non-zero exit is a normal result, not a transport failure: only errors
	// other than *exec.ExitError mean the execution itself went wrong.
	var exitErr *exec.ExitError
	if err == nil || errors.As(err, &exitErr) {
		return status, nil
	}
	return status, fmt.Errorf("localsandbox: wait: %w", err)
}
