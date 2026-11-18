//go:build unix

package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The file tools run unsandboxed. Check-then-os.WriteFile follows a parent
// that is swapped for a symlink in the window between the two.
func TestPathGuardWriteFileDoesNotFollowReplacedParent(t *testing.T) {
	root, g := guardFixture(t)
	inner := filepath.Join(root, "out")
	require.NoError(t, os.Mkdir(inner, 0o755))
	secrets := filepath.Join(filepath.Dir(root), "secrets")

	resolved, err := g.CheckWrite(filepath.Join(inner, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(inner, "a.txt"), resolved)

	require.NoError(t, os.Remove(inner))
	require.NoError(t, os.Symlink(secrets, inner))

	err = g.WriteFile(filepath.Join(root, "out", "a.txt"), []byte("pwned"), 0o644)
	require.ErrorIs(t, err, ErrPathDenied)

	_, statErr := os.Stat(filepath.Join(secrets, "a.txt"))
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
}

func TestPathGuardWriteFileDoesNotFollowInWorkspaceSymlink(t *testing.T) {
	root, g := guardFixture(t)
	realDir := filepath.Join(root, "real")
	require.NoError(t, os.Mkdir(realDir, 0o755))
	require.NoError(t, os.Symlink(realDir, filepath.Join(root, "link")))

	err := g.WriteFile(filepath.Join(root, "link", "x.txt"), []byte("x"), 0o644)
	require.ErrorIs(t, err, ErrPathDenied)
	_, statErr := os.Stat(filepath.Join(realDir, "x.txt"))
	require.True(t, os.IsNotExist(statErr))
}

// Seatbelt denies unlinking the workspace directory so it cannot be swapped
// for a symlink. PathGuard still has to refuse if that happens anyway: file
// tools do not run inside sandbox-exec.
func TestPathGuardWriteFileDoesNotFollowReplacedRoot(t *testing.T) {
	root, g := guardFixture(t)
	outside := filepath.Join(filepath.Dir(root), "outside")
	require.NoError(t, os.Mkdir(outside, 0o755))

	require.NoError(t, os.RemoveAll(root))
	require.NoError(t, os.Symlink(outside, root))

	err := g.WriteFile(filepath.Join(root, "pwned.txt"), []byte("x"), 0o644)
	require.ErrorIs(t, err, ErrPathDenied)
	_, statErr := os.Stat(filepath.Join(outside, "pwned.txt"))
	require.True(t, os.IsNotExist(statErr))
}

func TestPathGuardReadFileDoesNotFollowSymlinkEscape(t *testing.T) {
	root, g := guardFixture(t)
	secrets := filepath.Join(filepath.Dir(root), "secrets", "id_rsa")
	require.NoError(t, os.WriteFile(secrets, []byte("key"), 0o644))
	require.NoError(t, os.Symlink(secrets, filepath.Join(root, "stolen")))

	_, err := g.ReadFile(filepath.Join(root, "stolen"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardLstatDoesNotFollowReplacedParent(t *testing.T) {
	root, g := guardFixture(t)
	inner := filepath.Join(root, "out")
	require.NoError(t, os.Mkdir(inner, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(inner, "a.txt"), []byte("in"), 0o644))
	secrets := filepath.Join(filepath.Dir(root), "secrets")
	require.NoError(t, os.WriteFile(filepath.Join(secrets, "a.txt"), []byte("KEY"), 0o644))

	info, err := g.Lstat(filepath.Join(inner, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, int64(2), info.Size())

	require.NoError(t, os.RemoveAll(inner))
	require.NoError(t, os.Symlink(secrets, inner))

	_, err = g.Lstat(filepath.Join(root, "out", "a.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardReadFileReadableFileRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	root := filepath.Join(base, "My Project")
	require.NoError(t, os.MkdirAll(root, 0o755))
	rc := filepath.Join(base, "notes.txt")
	require.NoError(t, os.WriteFile(rc, []byte("ok"), 0o644))

	p := Policy{
		WritableRoots: []WritableRoot{{Path: root}},
		ReadableRoots: []string{rc},
		Cwd:           root,
	}
	require.NoError(t, p.Validate())
	g := NewPathGuard(p)
	got, err := g.ReadFile(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("ok"), got)
}

func TestPathGuardLstatReportsFinalSymlink(t *testing.T) {
	root, g := guardFixture(t)
	target := filepath.Join(root, "real.txt")
	require.NoError(t, os.WriteFile(target, []byte("hi"), 0o644))
	link := filepath.Join(root, "alias.txt")
	require.NoError(t, os.Symlink(target, link))

	info, err := g.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestPathGuardCheckDirDoesNotFollowReplacedParent(t *testing.T) {
	root, g := guardFixture(t)
	inner := filepath.Join(root, "out")
	require.NoError(t, os.Mkdir(inner, 0o755))
	secrets := filepath.Join(filepath.Dir(root), "secrets")

	got, err := g.CheckDir(inner)
	require.NoError(t, err)
	require.Equal(t, inner, got)

	require.NoError(t, os.Remove(inner))
	require.NoError(t, os.Symlink(secrets, inner))

	_, err = g.CheckDir(inner)
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardRejectsFIFOWithoutBlockingAndCapsOversizeReads(t *testing.T) {
	root, g := guardFixture(t)
	fifo := filepath.Join(root, "pipe")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))
	t.Cleanup(func() {
		if fd, err := unix.Open(fifo, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0); err == nil {
			_ = unix.Close(fd)
		}
	})

	readDone := make(chan error, 1)
	go func() {
		_, err := g.ReadFile(fifo)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("PathGuard.ReadFile blocked on FIFO")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- g.WriteFile(fifo, []byte("nope"), 0o644)
	}()
	select {
	case err := <-writeDone:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("PathGuard.WriteFile blocked on FIFO")
	}

	// Even when a peer holds the FIFO open so open(O_NONBLOCK) succeeds, Fstat must reject non-regular files.
	rfd, err := unix.Open(fifo, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	require.NoError(t, err)
	defer func() { _ = unix.Close(rfd) }()
	require.Error(t, g.WriteFile(fifo, []byte("nope"), 0o644))
	_, err = g.ReadFile(fifo)
	require.Error(t, err)

	// Sparse oversize file (> 64 MiB) must be refused without reading multi-GiB into memory.
	huge := filepath.Join(root, "huge.bin")
	f, err := os.Create(huge)
	require.NoError(t, err)
	require.NoError(t, f.Truncate((64<<20)+1))
	require.NoError(t, f.Close())
	_, err = g.ReadFile(huge)
	require.Error(t, err)
}

type failingCloseWriter struct {
	closeErr error
	closed   bool
}

func (w *failingCloseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *failingCloseWriter) Close() error {
	w.closed = true
	return w.closeErr
}

func TestPathGuardWriteOwnedFilePropagatesCloseError(t *testing.T) {
	flushErr := errors.New("enospc on close flush")
	w := &failingCloseWriter{closeErr: flushErr}
	err := writeOwnedFile(w, []byte("payload"))
	require.True(t, w.closed)
	require.ErrorIs(t, err, flushErr, "write path must propagate Close flush errors")
}
