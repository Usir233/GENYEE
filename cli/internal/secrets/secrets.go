// Package secrets stores and retrieves credentials. Production wires
// KeyringStore (OS keychain, primary) with FileStore (0600 plaintext under
// $XDG_CONFIG_HOME/genyee/secrets/, used as a fallback when no keyring
// backend is available).
//
// Namespace convention: "genyee:<profile>:<key>" where key is "access",
// "refresh", or "api_key".
package secrets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Usir233/GENYEE/cli/internal/xdg"
)

// ErrNotFound is returned when the requested secret does not exist.
var ErrNotFound = errors.New("secret: not found")

// Store is the abstraction CLI commands depend on; tests inject in-memory impls.
//
// Ref returns a stable URI (e.g. file://<profile>/<key> or
// keychain://genyee/<profile>/<key>) describing where a saved secret lives.
// Backends own their scheme so commands never need to type-assert the
// concrete implementation.
type Store interface {
	Get(profile, key string) (string, error)
	Set(profile, key, value string) error
	Delete(profile, key string) error
	Ref(profile, key string) string
}

// FileStore writes 0600 plain-text files under $XDG_CONFIG_HOME/genyee/secrets/<profile>.
// It is the headless / CI default and the keychain fallback.
type FileStore struct {
	root string
}

// NewFileStore returns a FileStore rooted at $XDG_CONFIG_HOME/genyee/secrets
// (or ~/.config/genyee/secrets if XDG_CONFIG_HOME is unset). Same convention
// as config.Path - see that file for the rationale (CLI convention).
func NewFileStore() (*FileStore, error) {
	root, err := defaultRoot()
	if err != nil {
		return nil, err
	}
	return &FileStore{root: root}, nil
}

func defaultRoot() (string, error) {
	return xdg.Path("XDG_CONFIG_HOME", ".config", "secrets")
}

func validateSecretSegment(label, seg string) error {
	if seg == "" || seg == "." || seg == ".." ||
		strings.ContainsAny(seg, `/\`) || strings.ContainsRune(seg, 0) {
		return fmt.Errorf("invalid secret %s %q", label, seg)
	}
	return nil
}

func (f *FileStore) safePath(profile, key string) (string, error) {
	if err := validateSecretSegment("profile", profile); err != nil {
		return "", err
	}
	if err := validateSecretSegment("key", key); err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(f.root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absRoot, profile, key)
	rel, err := filepath.Rel(absRoot, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("secret path escapes store root")
	}
	return target, nil
}

func (f *FileStore) path(profile, key string) string {
	if p, err := f.safePath(profile, key); err == nil {
		return p
	}
	return filepath.Join(f.root, "_invalid_")
}

func checkNoDirSymlinks(dirs ...string) error {
	for _, d := range dirs {
		info, err := os.Lstat(d)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("stat secrets dir: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("secrets directory %q must be a real directory, not a symlink or file", d)
		}
	}
	return nil
}

// Get reads the secret stored for (profile, key) from the 0600 file store.
func (f *FileStore) Get(profile, key string) (string, error) {
	p, err := f.safePath(profile, key)
	if err != nil {
		return "", err
	}
	profDir := filepath.Dir(p)
	rootDir := filepath.Dir(profDir)
	if err := checkNoDirSymlinks(rootDir, profDir); err != nil {
		return "", err
	}
	linfo, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("stat secret: %w", err)
	}
	if linfo.Mode()&os.ModeSymlink != 0 || !linfo.Mode().IsRegular() {
		return "", fmt.Errorf("read secret: credential file must be a regular non-symlink file")
	}
	fd, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	defer func() { _ = fd.Close() }()
	finfo, err := fd.Stat()
	if err != nil {
		return "", fmt.Errorf("stat open secret: %w", err)
	}
	if !os.SameFile(linfo, finfo) || !finfo.Mode().IsRegular() {
		return "", fmt.Errorf("read secret: credential file changed during open")
	}
	data, err := io.ReadAll(io.LimitReader(fd, 64<<10))
	if err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	return string(data), nil
}

// Set atomically writes value under (profile, key) in the 0600 file store.
func (f *FileStore) Set(profile, key, value string) error {
	p, err := f.safePath(profile, key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	rootDir := filepath.Dir(dir)
	if err := checkNoDirSymlinks(rootDir, dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir secrets dir: %w", err)
	}
	if err := checkNoDirSymlinks(rootDir, dir); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(rootDir, 0o700); err != nil {
			return fmt.Errorf("chmod secrets root: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("chmod secrets dir: %w", err)
		}
	}
	// A new 0600 inode also repairs permissions left by restored files and
	// replaces a credential symlink without overwriting its target.
	tmp, err := os.CreateTemp(dir, ".credential-*")
	if err != nil {
		return fmt.Errorf("create secret file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.WriteString(value); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write secret: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close secret: %w", err)
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return fmt.Errorf("replace secret: %w", err)
	}
	return nil
}

// Delete removes any secret file stored for (profile, key).
func (f *FileStore) Delete(profile, key string) error {
	p, err := f.safePath(profile, key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	rootDir := filepath.Dir(dir)
	if err := checkNoDirSymlinks(rootDir, dir); err != nil {
		return err
	}
	err = os.Remove(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Best-effort cleanup of `<root>/<profile>` once its last credential file is gone.
	_ = os.Remove(dir)
	return nil
}

// Ref returns the file:// URI under which a secret would be stored. Path
// component is forward-slash-normalized per RFC 8089 (file:///path on Unix,
// file:///C:/... on Windows) - the wire format must not depend on platform
// path separator since this string is persisted to config.yaml and may be
// consumed by tooling on a different OS.
func (f *FileStore) Ref(profile, key string) string {
	p := filepath.ToSlash(f.path(profile, key))
	if !strings.HasPrefix(p, "/") {
		// Windows absolute path "C:/..." needs a leading slash to form a
		// proper file:/// URI ("file:///C:/...").
		p = "/" + p
	}
	return "file://" + p
}

// MemStore is an in-memory implementation of Store used by tests across
// packages. It is intentionally exported (not _test.go-only) so that
// downstream packages can compose it without copying the same 20 lines.
type MemStore struct{ m map[string]string }

// NewMemStore returns an empty in-memory secrets store.
func NewMemStore() *MemStore { return &MemStore{m: map[string]string{}} }

func (m *MemStore) k(c, key string) string { return c + ":" + key }

// Get returns the in-memory secret for (c, key) or ErrNotFound.
func (m *MemStore) Get(c, key string) (string, error) {
	v, ok := m.m[m.k(c, key)]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set stores value under (c, key) in memory.
func (m *MemStore) Set(c, key, value string) error { m.m[m.k(c, key)] = value; return nil }

// Delete deletes any in-memory secret stored under (c, key).
func (m *MemStore) Delete(c, key string) error { delete(m.m, m.k(c, key)); return nil }

// Ref returns a mem:// URI; meaningful only inside tests.
func (m *MemStore) Ref(c, key string) string { return "mem://" + c + "/" + key }
