package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usir233/GENYEE/internal/handler"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestDesktopSigningKeyKeepsExistingAESHMAC(t *testing.T) {
	dir := t.TempDir()
	aes := "abcdefghijklmnopqrstuvwxyz123456"
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("JWT_SECRET", "weknora-jwt-secret")
	t.Setenv("SYSTEM_AES_KEY", aes)
	require.Equal(t, aes, string(utils.SystemHMACKey()))
	require.NoError(t, ensureDesktopSigningKeyInDir(dir))
	require.Equal(t, aes, string(utils.SystemHMACKey()))
	_, err := os.Stat(filepath.Join(dir, "signing.key"))
	require.True(t, os.IsNotExist(err), "the AES key must not be copied into signing.key")
	require.NotEqual(t, aes, os.Getenv("JWT_SECRET"))
	require.GreaterOrEqual(t, len(os.Getenv("JWT_SECRET")), 32)
}

func TestDesktopLocalFilesDir(t *testing.T) {
	require.Equal(t, filepath.Join("/Users/dev", ".genyee", "data", "files"), desktopLocalFilesDir("/Users/dev"))

	// An unrelated application directory must not influence our path.
	base := t.TempDir()
	legacyDir := filepath.Join(base, ".other-product", "data", "files")
	require.NoError(t, os.MkdirAll(legacyDir, 0o755))
	require.Equal(t, filepath.Join(base, ".genyee", "data", "files"), desktopLocalFilesDir(base))
}

func TestUseDesktopFilesDirReplacesDockerDefault(t *testing.T) {
	require.True(t, useDesktopFilesDir(""))
	require.True(t, useDesktopFilesDir("/data/files"))
	require.True(t, useDesktopFilesDir("./data/files"))
	require.False(t, useDesktopFilesDir("/Users/dev/custom-files"))
}

func TestConfigureDesktopFileStorageUsesHomeWeKnora(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOCAL_STORAGE_BASE_DIR", "/data/files")
	configureDesktopFileStorage("")
	require.Equal(t, filepath.Join(home, ".genyee", "data", "files"), os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	info, err := os.Stat(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestConfigureDesktopFileStorageDoesNotAdoptOtherApplicationFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOCAL_STORAGE_BASE_DIR", "custom/files")
	old := filepath.Join(home, "Library", "Application Support", "OtherProduct", "custom", "files")
	require.NoError(t, os.MkdirAll(filepath.Join(old, "1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(old, "1", "a.txt"), []byte("a"), 0o600))

	configureDesktopFileStorage("/Applications/GENYEE.app/Contents/MacOS/GENYEE")

	require.NoFileExists(t, filepath.Join(home, ".genyee", "data", "files", "1", "a.txt"))
	require.FileExists(t, filepath.Join(old, "1", "a.txt"))
}

func TestEnsureDesktopLiteEditionOverridesDefaultStandard(t *testing.T) {
	old := handler.Edition
	t.Cleanup(func() { handler.Edition = old })
	handler.Edition = "standard"
	ensureDesktopLiteEdition()
	require.Equal(t, "lite", handler.Edition)
}

func TestApprovalModeDefaultsToAsk(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.Equal(t, "ask", LoadApprovalMode())
}

func TestSaveApprovalModeRejectsUnknownValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.Error(t, SaveApprovalMode("yolo"))
}

// "full" makes Build return ErrFullAccessHasNoPolicy, i.e. nothing runs at
// all, and it must never become a saved default.
func TestSaveApprovalModeRejectsFull(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.Error(t, SaveApprovalMode("full"))
}

func TestSaveApprovalModeRoundTrips(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, mode := range []string{"auto", "ask"} {
		require.NoError(t, SaveApprovalMode(mode))
		require.Equal(t, mode, LoadApprovalMode())
	}
}

func TestProjectDirsRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dirs := []string{"/Users/dev/My Project", "/Users/dev/other"}
	require.NoError(t, SaveProjectDirs(dirs))
	require.Equal(t, dirs, LoadProjectDirs())
}

func TestSaveProjectDirsRejectsRelativePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.Error(t, SaveProjectDirs([]string{"relative/dir"}))
}

func TestAppendApprovedProjectDirDedupes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	got, err := appendApprovedProjectDir(dir)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir), got)
	again, err := appendApprovedProjectDir(dir)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir), again)
	require.Equal(t, []string{filepath.Clean(dir)}, LoadProjectDirs())
}

func TestAppendApprovedProjectDirFromFileParent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	got, err := appendApprovedProjectDir(filepath.Dir(file))
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir), got)
	require.Equal(t, []string{filepath.Clean(dir)}, LoadProjectDirs())
}

func TestRemoveApprovedProjectDirRemovesOnlyThatPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keep := t.TempDir()
	drop := t.TempDir()
	_, err := appendApprovedProjectDir(keep)
	require.NoError(t, err)
	_, err = appendApprovedProjectDir(drop)
	require.NoError(t, err)

	require.NoError(t, removeApprovedProjectDir(drop+"/"))
	require.Equal(t, []string{filepath.Clean(keep)}, LoadProjectDirs())
}

func TestRemoveApprovedProjectDirIsIdempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	require.NoError(t, removeApprovedProjectDir(dir))
	require.Empty(t, LoadProjectDirs())
}

func TestSetProjectDirsIsNotAWailsMethod(t *testing.T) {
	typ := reflect.TypeOf(&App{})
	_, ok := typ.MethodByName("SetProjectDirs")
	require.False(t, ok, "replacing the allowlist from the webview is not an authorization path")
	_, ok = typ.MethodByName("RemoveProjectDir")
	require.True(t, ok)
	_, ok = typ.MethodByName("PickProjectDir")
	require.True(t, ok)
}

func TestDesktopAppSupportDirDoesNotAdoptOtherApplicationData(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)

	// Fresh install defaults to "GENYEE Lite"
	dir, err := desktopPrefsDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(base, "GENYEE Lite"), dir)

	// Another application's existing preferences must not be adopted.
	baseLegacy := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", baseLegacy)
	legacyDir := filepath.Join(baseLegacy, "Other Product Lite")
	require.NoError(t, os.MkdirAll(legacyDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, desktopPrefsFileName), []byte(`{"http_port":18080}`), 0o600))
	require.Equal(t, 0, LoadDesktopPrefsHTTPPort())
	require.Equal(t, filepath.Join(baseLegacy, "GENYEE Lite"), resolveDesktopAppSupportDir(baseLegacy))
}

func TestUpdateEndpointAndAutoUpdateDefaults(t *testing.T) {
	t.Setenv("GENYEE_UPDATE_URL", "")
	t.Setenv("GENYEE_UPDATE_REPO", "")
	t.Setenv("GENYEE_AUTO_UPDATE", "")

	url, ok := resolveUpdateReleaseURL()
	require.False(t, ok)
	require.Empty(t, url)
	require.False(t, isAutoUpdateEnabled())

	t.Setenv("GENYEE_UPDATE_REPO", "acme/genyee")
	url, ok = resolveUpdateReleaseURL()
	require.True(t, ok)
	require.Equal(t, "https://api.github.com/repos/acme/genyee/releases/latest", url)
	require.False(t, isAutoUpdateEnabled(), "auto-update remains off unless GENYEE_AUTO_UPDATE is enabled")

	t.Setenv("GENYEE_AUTO_UPDATE", "true")
	require.True(t, isAutoUpdateEnabled())

	t.Setenv("GENYEE_UPDATE_URL", "https://updates.example.com/releases/latest")
	url, ok = resolveUpdateReleaseURL()
	require.True(t, ok)
	require.Equal(t, "https://updates.example.com/releases/latest", url)
}
