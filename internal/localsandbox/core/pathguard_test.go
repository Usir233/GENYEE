package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// guardFixture builds a real on-disk workspace so EvalSymlinks has something
// to resolve. The directory name contains a space on purpose.
func guardFixture(t *testing.T) (root string, g *PathGuard) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	root = filepath.Join(base, "My Project")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	secrets := filepath.Join(base, "secrets")
	require.NoError(t, os.MkdirAll(secrets, 0o755))

	p := Policy{
		WritableRoots: []WritableRoot{{
			Path:             root,
			ReadOnlySubpaths: []string{filepath.Join(root, ".git")},
		}},
		DenyRead: []string{secrets},
		Cwd:      root,
	}
	require.NoError(t, p.Validate())
	return root, NewPathGuard(p)
}

func TestPathGuardAllowsWriteInsideRoot(t *testing.T) {
	root, g := guardFixture(t)
	got, err := g.CheckWrite(filepath.Join(root, "sub", "new.txt"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "sub", "new.txt"), got)
}

func TestPathGuardRejectsWriteOutsideRoot(t *testing.T) {
	root, g := guardFixture(t)
	_, err := g.CheckWrite(filepath.Join(filepath.Dir(root), "outside.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardRejectsWriteToReadOnlySubpath(t *testing.T) {
	root, g := guardFixture(t)
	_, err := g.CheckWrite(filepath.Join(root, ".git", "config"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardRejectsDotDotEscape(t *testing.T) {
	root, g := guardFixture(t)
	_, err := g.CheckWrite(filepath.Join(root, "..", "escaped.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
}

// A symlink inside the workspace pointing out of it must not widen the guard.
func TestPathGuardRejectsSymlinkEscape(t *testing.T) {
	root, g := guardFixture(t)
	target := filepath.Dir(root)
	link := filepath.Join(root, "escape")
	require.NoError(t, os.Symlink(target, link))

	_, err := g.CheckWrite(filepath.Join(link, "pwned.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardRejectsReadOfDeniedPath(t *testing.T) {
	root, g := guardFixture(t)
	secrets := filepath.Join(filepath.Dir(root), "secrets", "id_rsa")
	_, err := g.CheckRead(secrets)
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardAllowsReadOfReadOnlySubpath(t *testing.T) {
	root, g := guardFixture(t)
	_, err := g.CheckRead(filepath.Join(root, ".git", "config"))
	require.NoError(t, err)
}

func TestPathGuardRejectsRelativePath(t *testing.T) {
	_, g := guardFixture(t)
	_, err := g.CheckRead("relative/path.txt")
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardWriteFileCreatesInsideRoot(t *testing.T) {
	root, g := guardFixture(t)
	path := filepath.Join(root, "sub", "new.txt")
	require.NoError(t, g.WriteFile(path, []byte("hello"), 0o644))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), got)
}

func TestPathGuardWriteFileRejectsOutsideRoot(t *testing.T) {
	root, g := guardFixture(t)
	err := g.WriteFile(filepath.Join(filepath.Dir(root), "outside.txt"), []byte("nope"), 0o644)
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardReadFileReturnsContents(t *testing.T) {
	root, g := guardFixture(t)
	path := filepath.Join(root, "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("hi"), 0o644))
	got, err := g.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("hi"), got)
}

func TestPathGuardWriteFileAndMkdirAllEnforceDenyWrite(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	root := filepath.Join(base, "proj")
	bsk := filepath.Join(root, ".bsk")
	require.NoError(t, os.MkdirAll(root, 0o755))

	p := Policy{
		WritableRoots: []WritableRoot{{Path: root}},
		DenyWrite:     []string{bsk},
		Cwd:           root,
	}
	require.NoError(t, p.Validate())
	g := NewPathGuard(p)

	_, err = g.CheckWrite(filepath.Join(bsk, "daemon.json"))
	require.ErrorIs(t, err, ErrPathDenied)
	require.ErrorIs(t, g.MkdirAll(bsk, 0o755), ErrPathDenied)
	require.ErrorIs(t, g.WriteFile(filepath.Join(bsk, "daemon.json"), []byte("x"), 0o644), ErrPathDenied)
	require.NoDirExists(t, bsk)

	// DenyWrite must still allow reads when the path is not in DenyRead.
	require.NoError(t, os.MkdirAll(bsk, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bsk, "readable.json"), []byte("ok"), 0o644))
	got, err := g.ReadFile(filepath.Join(bsk, "readable.json"))
	require.NoError(t, err)
	require.Equal(t, []byte("ok"), got)
}

func TestPathGuardEnforcesSymlinkedDenyAndReadOnlyAliases(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	realHome := filepath.Join(base, "real-home")
	aliasHome := filepath.Join(base, "alias-home")
	root := filepath.Join(realHome, "proj")
	secrets := filepath.Join(realHome, ".ssh")
	bsk := filepath.Join(realHome, "bsk")
	gitMeta := filepath.Join(root, "gitmeta")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(secrets, 0o700))
	require.NoError(t, os.MkdirAll(bsk, 0o755))
	require.NoError(t, os.MkdirAll(gitMeta, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(secrets, "id_rsa"), []byte("KEY"), 0o600))
	require.NoError(t, os.Symlink(realHome, aliasHome))

	p := Policy{
		WritableRoots: []WritableRoot{
			{Path: realHome},
			{Path: root, ReadOnlySubpaths: []string{filepath.Join(aliasHome, "proj", "gitmeta")}},
		},
		DenyRead:  []string{filepath.Join(aliasHome, ".ssh")},
		DenyWrite: []string{filepath.Join(aliasHome, "bsk")},
		Cwd:       root,
	}
	g := NewPathGuard(p)

	_, err = g.CheckRead(filepath.Join(realHome, ".ssh", "id_rsa"))
	require.ErrorIs(t, err, ErrPathDenied)
	_, err = g.ReadFile(filepath.Join(realHome, ".ssh", "id_rsa"))
	require.ErrorIs(t, err, ErrPathDenied)

	_, err = g.CheckWrite(filepath.Join(realHome, "bsk", "state.json"))
	require.ErrorIs(t, err, ErrPathDenied)
	require.ErrorIs(t, g.WriteFile(filepath.Join(realHome, "bsk", "state.json"), []byte("x"), 0o644), ErrPathDenied)

	_, err = g.CheckWrite(filepath.Join(gitMeta, "HEAD"))
	require.ErrorIs(t, err, ErrPathDenied)
	require.ErrorIs(t, g.WriteFile(filepath.Join(gitMeta, "HEAD"), []byte("x"), 0o644), ErrPathDenied)
}

func TestPolicyAndPathGuardRejectNULBytes(t *testing.T) {
	root, g := guardFixture(t)
	bad := root + "\x00/extra"
	p := Policy{
		WritableRoots: []WritableRoot{{Path: bad}},
		Cwd:           bad,
	}
	require.Error(t, p.Validate())
	_, err := g.CheckRead(bad)
	require.ErrorIs(t, err, ErrPathDenied)
	require.ErrorIs(t, g.WriteFile(bad, []byte("x"), 0o644), ErrPathDenied)
}
