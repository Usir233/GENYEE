package skillscmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	skillsfs "github.com/Usir233/GENYEE/cli/skills"
)

func TestW8_SkillsInstall_RejectsDanglingSymlinkAndSymlinkSubdir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test requires unix")
	}
	skills, err := skillsfs.List()
	if err != nil || len(skills) == 0 || len(skills[0].Files) == 0 {
		t.Fatalf("skillsfs.List: err=%v len=%d", err, len(skills))
	}
	firstRel := skills[0].Files[0]

	// Case 1: dangling symlink at destination file without --force must NOT follow the symlink!
	target1 := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "pwned.md")

	dst1 := filepath.Join(target1, firstRel)
	if err := os.MkdirAll(filepath.Dir(dst1), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(victim, dst1); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := writeSkills(skills[:1], target1, false); err == nil {
		if _, serr := os.Stat(victim); serr == nil {
			t.Fatal("writeSkills followed dangling symlink and created arbitrary file outside target!")
		}
		t.Fatal("expected writeSkills to reject symlinked destination")
	}

	// Case 2: --force on symlinked destination must NOT overwrite external target!
	if err := os.WriteFile(victim, []byte("DO NOT OVERWRITE"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	if _, err := writeSkills(skills[:1], target1, true); err == nil {
		t.Fatal("expected writeSkills(--force) to refuse symlink destination")
	}
	gotBytes, _ := os.ReadFile(victim)
	if string(gotBytes) != "DO NOT OVERWRITE" {
		t.Fatalf("writeSkills(--force) clobbered external symlink target: %q", string(gotBytes))
	}

	// Case 3: symlinked skill subdirectory inside target must be rejected.
	target2 := t.TempDir()
	topSub := filepath.Join(target2, skills[0].Name)
	if err := os.Symlink(outside, topSub); err != nil {
		t.Fatalf("symlink subdir: %v", err)
	}
	if _, err := writeSkills(skills[:1], target2, true); err == nil {
		t.Fatal("expected writeSkills to reject symlinked skill subdirectory")
	}
}
