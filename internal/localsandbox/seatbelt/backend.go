//go:build darwin

// Package seatbelt implements the sandbox backend for macOS on top of
// /usr/bin/sandbox-exec. The policy compiler in this package carries no build
// tag so it stays testable on every platform.
package seatbelt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Usir233/GENYEE/internal/localsandbox/core"
	"github.com/Usir233/GENYEE/internal/logger"
)

// seatbeltExecPath is hard-coded rather than resolved through PATH: a PATH
// lookup would let anything earlier in PATH impersonate the sandbox. If this
// binary itself is tampered with, the attacker already has root.
const seatbeltExecPath = "/usr/bin/sandbox-exec"

type seatbeltBackend struct{}

// New returns the macOS Seatbelt backend.
func New() (core.Backend, error) {
	return &seatbeltBackend{}, nil
}

func (b *seatbeltBackend) Name() string { return "seatbelt" }

func (b *seatbeltBackend) Available() error {
	info, err := os.Stat(seatbeltExecPath)
	if err != nil {
		logger.Errorf(context.Background(), "[LocalSandbox] seatbelt missing: %v", err)
		return fmt.Errorf("%w: %s is missing", core.ErrUnsupportedPlatform, seatbeltExecPath)
	}
	if info.Mode()&0o111 == 0 {
		logger.Errorf(context.Background(), "[LocalSandbox] seatbelt not executable: %s", seatbeltExecPath)
		return fmt.Errorf("%w: %s is not executable", core.ErrUnsupportedPlatform, seatbeltExecPath)
	}
	return nil
}

// EnsureReady is a no-op: Seatbelt needs no installation and no elevation.
// That is what makes it suitable for a double-click desktop app.
func (b *seatbeltBackend) EnsureReady(context.Context) error { return nil }

// TearDown is a no-op for the same reason: nothing persistent was created.
func (b *seatbeltBackend) TearDown(context.Context) error { return nil }

type seatbeltPrepared struct {
	fingerprint string
	program     seatbeltProgram
}

func (p *seatbeltPrepared) Fingerprint() string { return p.fingerprint }
func (p *seatbeltPrepared) Close() error        { return nil }

func (b *seatbeltBackend) Prepare(ctx context.Context, p core.Policy) (core.Prepared, error) {
	if err := b.Available(); err != nil {
		return nil, err
	}
	resolved, err := resolvePolicyPaths(p)
	if err != nil {
		logger.Errorf(ctx, "[LocalSandbox] seatbelt resolve policy paths: %v", err)
		return nil, err
	}
	program, err := compileSeatbelt(resolved)
	if err != nil {
		logger.Errorf(ctx, "[LocalSandbox] seatbelt compile: %v", err)
		return nil, err
	}
	logger.Debugf(ctx, "[LocalSandbox] seatbelt prepared fingerprint=%s params=%d",
		p.Fingerprint(), len(program.Params))
	return &seatbeltPrepared{fingerprint: p.Fingerprint(), program: program}, nil
}

func (b *seatbeltBackend) Spawn(
	ctx context.Context, prep core.Prepared, cmd core.Command,
) (core.Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	prepared, ok := prep.(*seatbeltPrepared)
	if !ok {
		return nil, fmt.Errorf("localsandbox: prepared handle is not a seatbelt program")
	}

	argv := []string{"-p", prepared.program.Profile}
	argv = append(argv, prepared.program.Params...)
	argv = append(argv, "--")
	argv = append(argv, cmd.Argv...)

	execCmd := buildSeatbeltExecCmd(seatbeltExecPath, argv, cmd)
	return spawnExecCmd(ctx, execCmd, cmd.Cwd, cmd.Argv[0])
}

func resolvePolicyPaths(p core.Policy) (core.Policy, error) {
	out := p
	out.WritableRoots = make([]core.WritableRoot, len(p.WritableRoots))
	for i, root := range p.WritableRoots {
		path, err := resolveExisting(root.Path)
		if err != nil {
			return core.Policy{}, err
		}
		subs := make([]string, len(root.ReadOnlySubpaths))
		for j, sub := range root.ReadOnlySubpaths {
			subs[j], err = resolveExisting(sub)
			if err != nil {
				return core.Policy{}, err
			}
		}
		out.WritableRoots[i] = core.WritableRoot{
			Path: path, ReadOnlySubpaths: subs, ProtectGitDirs: root.ProtectGitDirs,
			ProtectBareGitDirs: root.ProtectBareGitDirs,
		}
	}
	out.WritableFiles = make([]string, len(p.WritableFiles))
	for i, file := range p.WritableFiles {
		var err error
		out.WritableFiles[i], err = resolveExisting(file)
		if err != nil {
			return core.Policy{}, err
		}
	}
	out.ReadableRoots = make([]string, len(p.ReadableRoots))
	for i, root := range p.ReadableRoots {
		var err error
		out.ReadableRoots[i], err = resolveExisting(root)
		if err != nil {
			return core.Policy{}, err
		}
	}
	// The kernel matches the resolved path, so an unresolved private root
	// silently protects nothing: /tmp/... never matches /private/tmp/...
	out.PrivateRoots = make([]string, len(p.PrivateRoots))
	for i, private := range p.PrivateRoots {
		var err error
		out.PrivateRoots[i], err = resolveExisting(private)
		if err != nil {
			return core.Policy{}, err
		}
	}
	out.DenyRead = make([]string, len(p.DenyRead))
	for i, deny := range p.DenyRead {
		var err error
		out.DenyRead[i], err = resolveExisting(deny)
		if err != nil {
			return core.Policy{}, err
		}
	}
	out.DenyWrite = make([]string, len(p.DenyWrite))
	for i, deny := range p.DenyWrite {
		var err error
		out.DenyWrite[i], err = resolveExisting(deny)
		if err != nil {
			return core.Policy{}, err
		}
	}
	cwd, err := resolveExisting(p.Cwd)
	if err != nil {
		return core.Policy{}, err
	}
	out.Cwd = cwd
	return out, nil
}

// resolveExisting EvalSymlinks the longest existing prefix. /tmp/foo becomes
// /private/tmp/foo; a missing ~/.ssh keeps the realpath of the parent.
func resolveExisting(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return cleaned, nil
	}
	resolvedParent, err := resolveExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(cleaned)), nil
}
