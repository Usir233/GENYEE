//go:build !bindings

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config", "config.yaml"), nil, 0o644))
}

func TestDesktopConfigRootPrefersThePackagedResources(t *testing.T) {
	base := t.TempDir()
	contents := filepath.Join(base, "GENYEE Lite.app", "Contents")
	writeConfig(t, filepath.Join(contents, "Resources"))
	writeConfig(t, base)

	root, ok := desktopConfigRoot(filepath.Join(contents, "MacOS", "GENYEE Lite"))
	require.True(t, ok)
	require.Equal(t, filepath.Join(contents, "Resources"), root)
}

func TestDesktopConfigRootFindsTheRepoFromADevBundle(t *testing.T) {
	repo := t.TempDir()
	writeConfig(t, repo)
	exe := filepath.Join(repo, "cmd", "desktop", "build", "bin",
		"GENYEE Lite.app", "Contents", "MacOS", "GENYEE Lite")

	root, ok := desktopConfigRoot(exe)
	require.True(t, ok)
	require.Equal(t, repo, root)
}

func TestDesktopConfigRootIgnoresTheWorkingDirectory(t *testing.T) {
	untrusted := t.TempDir()
	writeConfig(t, untrusted)
	t.Chdir(untrusted)

	_, ok := desktopConfigRoot(filepath.Join(t.TempDir(), "bin", "GENYEE Lite"))
	require.False(t, ok)
}

func TestLoadDesktopEnvReadsTheRootNotTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	untrusted := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env.lite"), []byte("GY_A=lite\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("GY_A=env\nGY_B=env\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(untrusted, ".env.lite"), []byte("GY_C=planted\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(untrusted, ".env"), []byte("GY_C=planted\n"), 0o644))
	t.Chdir(untrusted)
	for _, name := range []string{"GY_A", "GY_B", "GY_C", "ENV_FILE"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}

	require.NoError(t, loadDesktopEnv(root, true))
	require.Equal(t, "lite", os.Getenv("GY_A"), ".env.lite wins on shared keys")
	require.Equal(t, "env", os.Getenv("GY_B"), ".env still fills in the rest")
	require.Empty(t, os.Getenv("GY_C"))
}

func TestLoadDesktopEnvWithoutARootReadsNoFiles(t *testing.T) {
	untrusted := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(untrusted, ".env"), []byte("GY_C=planted\n"), 0o644))
	t.Chdir(untrusted)
	t.Setenv("GY_C", "")
	require.NoError(t, os.Unsetenv("GY_C"))
	t.Setenv("ENV_FILE", "")

	require.NoError(t, loadDesktopEnv("", false))
	require.Empty(t, os.Getenv("GY_C"))
}

func TestLoadDesktopEnvHonoursAnExplicitFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "custom.env")
	require.NoError(t, os.WriteFile(file, []byte("GY_D=custom\n"), 0o644))
	t.Setenv("ENV_FILE", file)
	t.Setenv("GY_D", "")
	require.NoError(t, os.Unsetenv("GY_D"))

	require.NoError(t, loadDesktopEnv("", false))
	require.Equal(t, "custom", os.Getenv("GY_D"))

	t.Setenv("ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	require.Error(t, loadDesktopEnv("", false), "a file asked for by name must exist")
}
