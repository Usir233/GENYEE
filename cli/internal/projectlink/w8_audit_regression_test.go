package projectlink_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Usir233/GENYEE/cli/internal/projectlink"
)

func TestW8_ProjectLink_RejectsSymlinkDiscoverLoadAndRemove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test requires unix")
	}
	repo := t.TempDir()
	outside := t.TempDir()

	victim := filepath.Join(outside, "project.yaml")
	if err := os.WriteFile(victim, []byte("kb_id: kb_ext\n"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}

	// Malicious repo with `.genyee` symlinked to `outside`.
	dotGenyee := filepath.Join(repo, projectlink.DirName)
	if err := os.Symlink(outside, dotGenyee); err != nil {
		t.Fatalf("symlink .genyee: %v", err)
	}

	if got, found, err := projectlink.Discover(repo); err != nil || found {
		t.Fatalf("Discover must ignore symlinked .genyee, got path=%q found=%v err=%v", got, found, err)
	}
	candidate := filepath.Join(dotGenyee, projectlink.FileName)
	if _, err := projectlink.Load(candidate); err == nil {
		t.Fatal("Load must reject project.yaml reached via symlinked .genyee")
	}
	if err := projectlink.Remove(candidate); err == nil {
		t.Fatal("Remove must refuse to delete through symlinked .genyee")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("external victim file was deleted through symlinked .genyee: %v", err)
	}
}
