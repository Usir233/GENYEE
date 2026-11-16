// Package xdg consolidates the XDG-rooted file path lookup and atomic-write
// idioms used by config / compat / secrets / projectlink. Centralizing means
// a single place to fix behavior (mode bits, fallback dirs, mkdir order,
// error wrapping) instead of copy-pasting across stores.
package xdg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Path resolves an XDG-rooted file. envVar is one of "XDG_CONFIG_HOME" /
// "XDG_CACHE_HOME" / "XDG_DATA_HOME". fallbackDir is the dot-prefixed dir
// under $HOME used when the env var is unset or relative (".config" / ".cache"
// / etc., per the XDG Base Directory Specification). parts join under
// "genyee/" inside the chosen root.
//
// Honors the XDG vars on every OS, even macOS - where os.UserConfigDir
// would otherwise return ~/Library/Application Support.
func Path(envVar, fallbackDir string, parts ...string) (string, error) {
	if x := os.Getenv(envVar); x != "" && filepath.IsAbs(x) {
		return filepath.Join(append([]string{x, "genyee"}, parts...)...), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(append([]string{home, fallbackDir, "genyee"}, parts...)...), nil
}

// ReadSecureFile reads up to maxBytes from a regular, non-symlink file whose
// immediate parent directory is also a real (non-symlink) directory. Missing
// files or parent directories preserve os.ErrNotExist so callers can treat
// uninitialized config/cache files as zero-value state.
func ReadSecureFile(p string, maxBytes int64) ([]byte, error) {
	dir := filepath.Dir(p)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("lstat %s: %w", dir, err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return nil, fmt.Errorf("refusing to read from symlink or non-directory %s", dir)
	}

	linfo, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("lstat %s: %w", p, err)
	}
	if linfo.Mode()&os.ModeSymlink != 0 || !linfo.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing to read symlink or non-regular file %s", p)
	}
	if maxBytes > 0 && linfo.Size() > maxBytes {
		return nil, fmt.Errorf("file %s exceeds %d-byte limit", p, maxBytes)
	}

	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	defer f.Close()

	finfo, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("fstat %s: %w", p, err)
	}
	if !finfo.Mode().IsRegular() || !os.SameFile(linfo, finfo) {
		return nil, fmt.Errorf("file %s changed during open", p)
	}

	var r io.Reader = f
	if maxBytes > 0 {
		r = io.LimitReader(f, maxBytes+1)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file %s exceeds %d-byte limit", p, maxBytes)
	}
	return data, nil
}

// WriteAtomicYAML marshals v to YAML and writes it atomically at p with mode
// 0600 (user-only). Creates parent dirs with mode 0700. Atomicity via
// CreateTemp + chmod + rename so partial writes never expose a half-baked
// file and concurrent writers never trample each other's tmp.
//
// 0600 is the appropriate floor even for non-secret stores (cache, project
// link) since they may sit alongside secrets in the same dir tree.
//
// On any failure after the tmp is created, the tmp is cleaned up so we don't
// litter the dir with `*.tmp.NNNN` artifacts on crash / cross-device errors.
func WriteAtomicYAML(p string, v any) error {
	dir := filepath.Dir(p)
	if dirInfo, err := os.Lstat(dir); err == nil {
		if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
			return fmt.Errorf("refusing to write into symlink or non-directory %s", dir)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lstat %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("lstat %s: %w", dir, err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return fmt.Errorf("refusing to write into symlink or non-directory %s", dir)
	}
	if runtime.GOOS != "windows" && dirInfo.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("chmod %s: %w", dir, err)
		}
	}
	if targetInfo, err := os.Lstat(p); err == nil {
		if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.Mode().IsRegular() {
			return fmt.Errorf("refusing to overwrite symlink or non-regular file %s", p)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lstat %s: %w", p, err)
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	// CreateTemp picks a unique name in dir so concurrent calls never collide
	// on a shared `<p>.tmp`. Pattern reserves the suffix slot.
	tmp, err := os.CreateTemp(dir, filepath.Base(p)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create tmp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup on any failure path; no-op once Rename succeeds.
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, p, err)
	}
	committed = true
	return nil
}
