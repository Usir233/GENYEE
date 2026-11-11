package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSkillFileRejectsSiblingPrefixCollisionAndTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	evilSiblingDir := filepath.Join(tmpDir, "test-skill-evil")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(evilSiblingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	skillMD := "---\nname: test-skill\ndescription: prefix check\n---\n# Test\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evilSiblingDir, "secret.txt"), []byte("top-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader([]string{tmpDir})
	if _, err := loader.LoadSkillInstructions("test-skill"); err != nil {
		t.Fatalf("load skill: %v", err)
	}

	for _, bad := range []string{
		"../test-skill-evil/secret.txt",
		"..\\test-skill-evil\\secret.txt",
		"/etc/passwd",
	} {
		if _, err := loader.LoadSkillFile("test-skill", bad); err == nil {
			t.Fatalf("expected LoadSkillFile(%q) to fail", bad)
		}
	}

	// Even if skill.BasePath lacked a trailing slash and cleanPath were forged,
	// verify filepath.Rel boundary check rejects sibling prefix directories.
	absSkill, _ := filepath.Abs(skillDir)
	absEvil, _ := filepath.Abs(filepath.Join(evilSiblingDir, "secret.txt"))
	rel, err := filepath.Rel(absSkill, absEvil)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "../test-skill-evil/secret.txt" && rel != `..\test-skill-evil\secret.txt` {
		t.Fatalf("unexpected rel path %q", rel)
	}
}
