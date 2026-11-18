package browserskill

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeBaseDirAvoidsWorldWritableTmp(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	t.Setenv("HOME", home)
	t.Setenv(RuntimeDirEnv, "")

	dir, err := RuntimeBaseDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".genyee", "bsk"), dir)
	require.NotEqual(t, "/tmp", dir)
	require.False(t, strings.HasPrefix(dir, "/tmp/gyb-"))

	for _, invalid := range []string{"relative/path", "/", "/tmp", "/private/tmp", "/var/tmp"} {
		t.Setenv(RuntimeDirEnv, invalid)
		_, err := RuntimeBaseDir()
		require.Error(t, err, "expected invalid %s=%q to be rejected", RuntimeDirEnv, invalid)
	}

	custom := filepath.Join(home, "private-bsk")
	t.Setenv(RuntimeDirEnv, custom)
	got, err := RuntimeBaseDir()
	require.NoError(t, err)
	require.Equal(t, custom, got)
	require.NoError(t, ensurePrivateDir(got))
	info, err := os.Lstat(got)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestBuildDaemonEnvStripsSensitiveSecrets(t *testing.T) {
	rawEnv := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HOME=/home/alice",
		"LANG=en_US.UTF-8",
		"SYSTEM_AES_KEY=0123456789abcdef0123456789abcdef",
		"JWT_SECRET=top-secret-jwt-key",
		"DB_PASSWORD=super-secret-db-pass",
		"BROWSERSKILL_CLUSTER_SECRET=cluster-secret",
		"OPENAI_API_KEY=sk-secret",
		"LD_PRELOAD=/tmp/evil.so",
		"DYLD_INSERT_LIBRARIES=/tmp/evil.dylib",
		"TMPDIR=/tmp",
		"BSK_HOME=/tmp/attacker",
	}
	env := buildDaemonEnv("/home/alice/.genyee/bsk/d-1/b", "/home/alice/.genyee/bsk/d-1/t", rawEnv)
	envMap := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		require.True(t, ok)
		envMap[k] = v
	}

	require.Equal(t, "/usr/local/bin:/usr/bin", envMap["PATH"])
	require.Equal(t, "/home/alice", envMap["HOME"])
	require.Equal(t, "en_US.UTF-8", envMap["LANG"])
	require.Equal(t, "/home/alice/.genyee/bsk/d-1/b", envMap["BSK_HOME"])
	require.Equal(t, "/home/alice/.genyee/bsk/d-1/t", envMap["TMPDIR"])

	for _, forbidden := range []string{
		"SYSTEM_AES_KEY",
		"JWT_SECRET",
		"DB_PASSWORD",
		"BROWSERSKILL_CLUSTER_SECRET",
		"OPENAI_API_KEY",
		"LD_PRELOAD",
		"DYLD_INSERT_LIBRARIES",
	} {
		_, exists := envMap[forbidden]
		require.False(t, exists, "sensitive env var %s leaked to child process", forbidden)
	}
}

func TestDaemonSocketGatekeeperEnforcesSharedSecret(t *testing.T) {
	// Keep Unix socket path short for macOS sun_path limit.
	base, err := os.MkdirTemp("", "bsk-gk-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	innerDir := filepath.Join(base, "b", "run")
	require.NoError(t, os.MkdirAll(innerDir, 0o700))
	innerSock := filepath.Join(innerDir, "daemon.sock")
	innerLn, err := net.Listen("unix", innerSock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = innerLn.Close() })

	upstreamRequests := make(chan map[string]any, 4)
	go func() {
		for {
			conn, err := innerLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				var req map[string]any
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				upstreamRequests <- req
				id, _ := req["id"].(string)
				_ = json.NewEncoder(c).Encode(rpcReply{
					ID:     id,
					Result: json.RawMessage(`{"ok":true}`),
				})
			}(conn)
		}
	}()

	secret, err := generateDaemonSecret()
	require.NoError(t, err)
	outerSock := filepath.Join(base, "run", "daemon.sock")
	gk, err := startDaemonSocketGatekeeper(outerSock, innerSock, secret)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gk.Close() })

	info, err := os.Stat(outerSock)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// 1. Unauthenticated caller must be rejected without reaching upstream.
	_, err = rpc(
		context.Background(),
		&device{runtime: &daemon{home: base}},
		"tool.snapshot",
		map[string]any{"session_id": "s1"},
	)
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, "unauthorized", rpcErr.Code)
	require.Empty(t, upstreamRequests)

	// 2. Wrong secret must be rejected without reaching upstream.
	_, err = rpc(
		context.Background(),
		&device{runtime: &daemon{
			home:   base,
			secret: "wrong-secret",
		}},
		"tool.snapshot",
		map[string]any{"session_id": "s1"},
	)
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, "unauthorized", rpcErr.Code)
	require.Empty(t, upstreamRequests)

	// 3. Valid secret must succeed and strip auth_token before forwarding to upstream bsk.
	res, err := rpc(
		context.Background(),
		&device{runtime: &daemon{
			home:   base,
			secret: secret,
		}},
		"tool.snapshot",
		map[string]any{"session_id": "s1"},
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(res))

	forwarded := <-upstreamRequests
	_, leakedToken := forwarded["auth_token"]
	require.False(t, leakedToken, "auth_token must be stripped before reaching upstream bsk")
	require.Equal(t, "tool.snapshot", forwarded["method"])
}
