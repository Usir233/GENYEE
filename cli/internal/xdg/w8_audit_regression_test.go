package xdg_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/cli/internal/xdg"
)

func TestW8_Path_IgnoresRelativeXDGEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "relative/attacker/dir")

	got, err := xdg.Path("XDG_CONFIG_HOME", ".config", "config.yaml")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path per XDG spec, got %q", got)
	}
	wantPrefix := filepath.Join(home, ".config", "genyee")
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("relative XDG_CONFIG_HOME must fall back to %q, got %q", wantPrefix, got)
	}
}

func TestW8_WriteAtomicYAML_RejectsSymlinkDirAndTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test requires unix")
	}
	root := t.TempDir()
	outside := t.TempDir()

	// Case 1: parent directory is a symlink pointing outside root.
	symDir := filepath.Join(root, "genyee")
	if err := os.Symlink(outside, symDir); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	if err := xdg.WriteAtomicYAML(filepath.Join(symDir, "config.yaml"), map[string]string{"k": "v"}); err == nil {
		t.Fatal("expected WriteAtomicYAML to reject symlinked parent directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("WriteAtomicYAML wrote through symlinked parent directory")
	}

	// Case 2: target file itself is a symlink.
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	victim := filepath.Join(outside, "victim.yaml")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	symTarget := filepath.Join(realDir, "config.yaml")
	if err := os.Symlink(victim, symTarget); err != nil {
		t.Fatalf("symlink file: %v", err)
	}
	if err := xdg.WriteAtomicYAML(symTarget, map[string]string{"k": "v"}); err == nil {
		t.Fatal("expected WriteAtomicYAML to reject symlinked target file")
	}

	// Case 3: pre-existing permissive directory is tightened to 0700 on write.
	_ = os.Remove(symTarget)
	if err := xdg.WriteAtomicYAML(symTarget, map[string]string{"k": "v"}); err != nil {
		t.Fatalf("WriteAtomicYAML valid path: %v", err)
	}
	info, err := os.Lstat(realDir)
	if err != nil {
		t.Fatalf("lstat dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %04o, want 0700", perm)
	}
}

func TestW8_ReadSecureFile_SymlinkAndSizeCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test requires unix")
	}
	dir := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret.yaml")
	if err := os.WriteFile(secret, []byte("topsecret: true\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	sym := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(secret, sym); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := xdg.ReadSecureFile(sym, 1024); err == nil {
		t.Fatal("expected ReadSecureFile to reject symlink")
	}

	oversized := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(oversized, make([]byte, 2048), 0o600); err != nil {
		t.Fatalf("write big: %v", err)
	}
	if _, err := xdg.ReadSecureFile(oversized, 1024); err == nil {
		t.Fatal("expected ReadSecureFile to reject file exceeding maxBytes")
	}
}
