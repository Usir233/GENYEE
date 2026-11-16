package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Usir233/GENYEE/cli/internal/config"
)

func TestW8_Load_RejectsSymlinkAndOversizedConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test requires unix")
	}
	xdgRoot := t.TempDir()
	outside := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)

	cfgDir := filepath.Join(xdgRoot, "genyee")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	extFile := filepath.Join(outside, "stolen.yaml")
	if err := os.WriteFile(extFile, []byte("current_profile: pwned\n"), 0o600); err != nil {
		t.Fatalf("write external: %v", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.Symlink(extFile, cfgPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := config.Load(); err == nil {
		t.Fatal("expected config.Load to reject symlinked config.yaml")
	}

	_ = os.Remove(cfgPath)
	if err := os.WriteFile(cfgPath, make([]byte, (1<<20)+16), 0o600); err != nil {
		t.Fatalf("write oversized config: %v", err)
	}
	if _, err := config.Load(); err == nil {
		t.Fatal("expected config.Load to reject oversized config.yaml")
	}
}
