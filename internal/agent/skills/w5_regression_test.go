package skills

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoaderConcurrentAndContainmentSafety(t *testing.T) {
	skillsRoot := t.TempDir()
	outsideDir := t.TempDir()

	// Outside host secret and symlinked directory/SKILL.md escape fixtures.
	outsideSecret := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(outsideSecret, []byte("host-secret"), 0o600))

	outsideSkillDir := filepath.Join(outsideDir, "outside-skill")
	require.NoError(t, os.MkdirAll(outsideSkillDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(outsideSkillDir, "SKILL.md"),
		[]byte("---\nname: escaped-skill\ndescription: escaped\n---\n# Escaped\n"),
		0o644,
	))

	// 1. Symlinked skill directory inside skillsRoot pointing outside skillsRoot.
	require.NoError(t, os.Symlink(outsideSkillDir, filepath.Join(skillsRoot, "escaped-skill")))

	// 2. Real directory whose SKILL.md is a symlink to outside host file.
	symlinkMDDir := filepath.Join(skillsRoot, "symlink-md")
	require.NoError(t, os.MkdirAll(symlinkMDDir, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(outsideSkillDir, "SKILL.md"), filepath.Join(symlinkMDDir, "SKILL.md")))

	// 3. Directory named "alias-dir" whose SKILL.md actually declares name: real-name,
	// plus another directory "real-dir" declaring name: alias-dir.
	aliasDir := filepath.Join(skillsRoot, "alias-dir")
	require.NoError(t, os.MkdirAll(aliasDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(aliasDir, "SKILL.md"),
		[]byte("---\nname: different-name\ndescription: mismatch\n---\n# Wrong\n"),
		0o644,
	))
	realDir := filepath.Join(skillsRoot, "real-holder")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(realDir, "SKILL.md"),
		[]byte("---\nname: alias-dir\ndescription: canonical match\n---\n# Right\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "helper.py"), []byte("print('ok')\n"), 0o644))
	// Symlink inside real-holder pointing to outside secret: ListSkillFiles and LoadSkillFile must not expose it.
	require.NoError(t, os.Symlink(outsideSecret, filepath.Join(realDir, "leak.txt")))

	loader := NewLoader([]string{skillsRoot})

	// Traversal skillName must be rejected even before directory joins.
	_, err := loader.LoadSkillInstructions("../outside-skill")
	require.Error(t, err)

	// Symlinked skill dir and symlinked SKILL.md must not be discovered or loaded.
	discovered, err := loader.DiscoverSkills()
	require.NoError(t, err)
	for _, meta := range discovered {
		assert.NotEqual(t, "escaped-skill", meta.Name, "symlinked skill dir outside root must not be discovered")
	}
	_, err = loader.LoadSkillInstructions("escaped-skill")
	require.Error(t, err)
	_, err = loader.LoadSkillInstructions("symlink-md")
	require.Error(t, err)

	// Cold loader fast-path must verify parsed skill.Name == skillName and fall through to real-holder.
	coldLoader := NewLoader([]string{skillsRoot})
	loaded, err := coldLoader.LoadSkillInstructions("alias-dir")
	require.NoError(t, err)
	assert.Equal(t, "alias-dir", loaded.Name)
	assert.Contains(t, loaded.Instructions, "# Right")

	// ListSkillFiles must exclude symlinks, and LoadSkillFile must reject symlink / directory targets.
	files, err := coldLoader.ListSkillFiles("alias-dir")
	require.NoError(t, err)
	assert.Contains(t, files, "SKILL.md")
	assert.Contains(t, files, "helper.py")
	assert.NotContains(t, files, "leak.txt", "ListSkillFiles must not advertise symlink entries")

	_, err = coldLoader.LoadSkillFile("alias-dir", "leak.txt")
	require.Error(t, err)

	// Concurrent DiscoverSkills / LoadSkillInstructions / LoadSkillFile / ListSkillFiles / Reload must be race-free.
	concurrentLoader := NewLoader([]string{skillsRoot})
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				_, _ = concurrentLoader.DiscoverSkills()
				_, _ = concurrentLoader.LoadSkillInstructions("alias-dir")
				_, _ = concurrentLoader.LoadSkillFile("alias-dir", "helper.py")
				_, _ = concurrentLoader.ListSkillFiles("alias-dir")
				_, _ = concurrentLoader.GetSkillByName("alias-dir")
				_, _ = concurrentLoader.GetSkillBasePath("alias-dir")
				_, _ = concurrentLoader.Reload()
			}
		}()
	}
	wg.Wait()
}

func TestApplyResolvedEnvBlocksReservedVarsAndHostClearsInputDir(t *testing.T) {
	// Simulate shell_exec / shell_exec_async calling ApplyResolvedEnv on an initially
	// empty caller env map before PrepareShellEnvironment (or with SkillName == "").
	env := map[string]string{}
	ApplyResolvedEnv(env, map[string]string{
		"TAVILY_API_KEY":            "ok-secret",
		"GENYEE_API_KEY":            "ok-genyee-cred",
		"GENYEE_SKILL_OUTPUT_DIR":   "/tmp/hijack-out",
		"GENYEE_SESSION_INPUT_DIR":  "/tmp/hijack-in",
		"GENYEE_SKILL_HISTORY_ROOT": "/tmp/hijack-hist",
		"GENYEE_SKILL_DIR":          "/tmp/hijack-dir",
		"GENYEE_SKILL_FUTURE_VAR":   "hijack-future",
		"GENYEE_SESSION_ID":         "forged-session",
		"GENYEE_BROWSER_ROOT":       "/tmp/forged-browser",
		"PYTHONPATH":                "/tmp/evil-py",
		"NODE_PATH":                 "/tmp/evil-node",
		"NODE_OPTIONS":              "--require=/tmp/evil.js",
		"LD_PRELOAD":                "/tmp/evil.so",
		"LD_LIBRARY_PATH":           "/tmp/evil-lib",
		"PATH":                      "/tmp/evil-bin",
		"BASH_ENV":                  "/tmp/evil-rc",
	})

	assert.Equal(t, "ok-secret", env["TAVILY_API_KEY"])
	assert.Equal(t, "ok-genyee-cred", env["GENYEE_API_KEY"])
	for _, blocked := range []string{
		"GENYEE_SKILL_OUTPUT_DIR",
		"GENYEE_SESSION_INPUT_DIR",
		"GENYEE_SKILL_HISTORY_ROOT",
		"GENYEE_SKILL_DIR",
		"GENYEE_SKILL_FUTURE_VAR",
		"GENYEE_SESSION_ID",
		"GENYEE_BROWSER_ROOT",
		"PYTHONPATH",
		"NODE_PATH",
		"NODE_OPTIONS",
		"LD_PRELOAD",
		"LD_LIBRARY_PATH",
		"PATH",
		"BASH_ENV",
	} {
		assert.NotContains(t, env, blocked, "reserved variable %s must not be injected from resolved env", blocked)
	}

	// Host layout must explicitly clear any caller-supplied GENYEE_SESSION_INPUT_DIR.
	root := "/Users/dev/.genyee/skills"
	rows := []*types.TenantSkillEntity{{Name: "pdf", Status: types.SkillStatusReady, Enabled: true}}
	m := NewManager(&ManagerConfig{Enabled: true}, hostLayoutManager{root: "/Users/dev/My Project"}).
		WithSkillsRoot(root).
		WithTenantSource(NewTenantSkillSourceAt(root, rows, nil))

	_, runtimeEnv, err := m.PrepareShellEnvironment(
		context.Background(),
		"s1",
		"pdf",
		"python3 run.py",
		map[string]string{"GENYEE_SESSION_INPUT_DIR": "/etc"},
	)
	require.NoError(t, err)
	assert.NotContains(t, runtimeEnv, "GENYEE_SESSION_INPUT_DIR",
		"Host layout must clear caller-supplied GENYEE_SESSION_INPUT_DIR")
}
