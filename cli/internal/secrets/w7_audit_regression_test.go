package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestW7FileStoreRejectsSymlinkReadAndDirSymlinksAndRepairsDirPerms(t *testing.T) {
	t.Run("rejects leaf credential symlink on Get", func(t *testing.T) {
		root := t.TempDir()
		store := &FileStore{root: root}
		profDir := filepath.Join(root, "prod")
		require.NoError(t, os.MkdirAll(profDir, 0o700))

		external := filepath.Join(t.TempDir(), "outside-secret")
		require.NoError(t, os.WriteFile(external, []byte("SENSITIVE_DATA"), 0o600))
		if err := os.Symlink(external, filepath.Join(profDir, "access")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}

		val, err := store.Get("prod", "access")
		require.Error(t, err, "FileStore.Get must refuse to read through a symlinked credential file")
		assert.Empty(t, val)
		assert.NotErrorIs(t, err, ErrNotFound)
	})

	t.Run("rejects symlinked profile directory on Get and Set", func(t *testing.T) {
		root := t.TempDir()
		store := &FileStore{root: root}
		externalDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(externalDir, "access"), []byte("EXT_SECRET"), 0o600))
		if err := os.Symlink(externalDir, filepath.Join(root, "prod")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}

		_, err := store.Get("prod", "access")
		require.Error(t, err)
		require.Error(t, store.Set("prod", "api_key", "gy_test"))
		_, statErr := os.Stat(filepath.Join(externalDir, "api_key"))
		assert.True(t, os.IsNotExist(statErr), "must not write into symlinked profile directory")
	})

	t.Run("rejects symlinked store root on Get and Set", func(t *testing.T) {
		parent := t.TempDir()
		externalRoot := t.TempDir()
		linkRoot := filepath.Join(parent, "secrets-link")
		if err := os.Symlink(externalRoot, linkRoot); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		store := &FileStore{root: linkRoot}
		require.Error(t, store.Set("prod", "access", "tok"))
		_, err := store.Get("prod", "access")
		require.Error(t, err)
	})

	t.Run("tightens pre-existing permissive directory permissions to 0700", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits")
		}
		root := filepath.Join(t.TempDir(), "secrets")
		profDir := filepath.Join(root, "prod")
		require.NoError(t, os.MkdirAll(profDir, 0o755))
		require.NoError(t, os.Chmod(root, 0o755))
		require.NoError(t, os.Chmod(profDir, 0o755))

		store := &FileStore{root: root}
		require.NoError(t, store.Set("prod", "access", "tok"))

		rootInfo, err := os.Stat(root)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), rootInfo.Mode().Perm())

		profInfo, err := os.Stat(profDir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), profInfo.Mode().Perm())
	})
}

func TestW7KeyringStoreRejectsTraversalSegments(t *testing.T) {
	k := NewKeyringStore()
	_, err := k.Get("../bad", "access")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid secret profile")

	err = k.Set("prod", "a/b", "val")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid secret key")

	err = k.Delete("", "access")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid secret profile")
}
