package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileStoreSetRestoresPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	root := t.TempDir()
	store := &FileStore{root: root}
	dir := filepath.Join(root, "profile")
	require.NoError(t, os.Mkdir(dir, 0o700))
	path := filepath.Join(dir, "access")
	require.NoError(t, os.WriteFile(path, []byte("old-token"), 0o644))
	require.NoError(t, os.Chmod(path, 0o644))
	require.NoError(t, store.Set("profile", "access", "new-token"))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	token, err := store.Get("profile", "access")
	require.NoError(t, err)
	require.Equal(t, "new-token", token)
}

func TestFileStoreSetReplacesCredentialSymlink(t *testing.T) {
	root := t.TempDir()
	store := &FileStore{root: root}
	dir := filepath.Join(root, "profile")
	require.NoError(t, os.Mkdir(dir, 0o700))
	target := filepath.Join(t.TempDir(), "other-file")
	require.NoError(t, os.WriteFile(target, []byte("keep-this-file"), 0o600))
	path := filepath.Join(dir, "access")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	require.NoError(t, store.Set("profile", "access", "new-token"))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "keep-this-file", string(data))
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.Zero(t, info.Mode()&os.ModeSymlink)
}
