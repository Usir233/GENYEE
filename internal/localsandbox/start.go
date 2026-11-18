package localsandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Usir233/GENYEE/internal/logger"
)

// ErrAsyncCommandRefused is a background command that would need an approval.
// A job has no turn to ask in, so it is refused rather than run unasked.
var ErrAsyncCommandRefused = errors.New("localsandbox: background command refused")

// StartForSession resolves the session policy and starts a managed job
// process. Unlike Run it takes no root lock, applies no timeout and reads no
// pipes — the caller owns draining and Wait.
func (s *Service) StartForSession(ctx context.Context, req RunRequest) (Process, error) {
	ws, policy, err := s.workspaceFor(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	cwd, err := commandCwd(policy, req.WorkDir)
	if err != nil {
		return nil, err
	}
	if req.Spec.Command == "" {
		req.Spec.Command = req.Command
	}
	req.Approver, req.Reviewer = nil, nil
	_, refused, err := s.approveDelete(ctx, s.newTurn(ctx, req, ws), policy, cwd)
	if err != nil {
		return nil, err
	}
	if refused != nil {
		if refused.Notice == dangerousDeleteNotice {
			return nil, fmt.Errorf("%w: %s", ErrAsyncCommandRefused, refused.Notice)
		}
		return nil, fmt.Errorf("%w: %s run it with shell_exec, which can ask for approval",
			ErrAsyncCommandRefused, refused.Notice)
	}
	return s.Start(ctx, policy, req)
}

// Start spawns one command under a prepared policy. The returned Process wraps
// the prepared handle so its resources are released exactly once at Wait; a
// failed Spawn closes it here. ctx cancellation kills the whole process group,
// so callers must pass an application-level ctx.
func (s *Service) Start(ctx context.Context, policy Policy, req RunRequest) (Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.ensureBackend(ctx); err != nil {
		return nil, err
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	cwd := policy.Cwd
	if req.WorkDir != "" {
		resolved, err := NewPathGuard(policy).CheckDir(req.WorkDir)
		if err != nil {
			return nil, err
		}
		cwd = resolved
	}
	prep, err := s.backend.Prepare(ctx, policy)
	if err != nil {
		return nil, fmt.Errorf("prepare sandbox: %w", err)
	}
	argv := append(append([]string(nil), s.shell...), req.Command)
	var bins []string
	if s.builder != nil {
		bins = s.builder.ToolchainBins()
	}
	proc, err := s.backend.Spawn(ctx, prep, Command{
		Argv: argv, Env: BuildCommandEnv(req.Env, bins), Cwd: cwd,
	})
	if err != nil {
		if closeErr := prep.Close(); closeErr != nil {
			logger.Warnf(ctx, "[LocalSandbox] close prepared sandbox after spawn failure: %v", closeErr)
		}
		return nil, fmt.Errorf("spawn sandboxed command: %w", err)
	}
	pp := &preparedProcess{
		Process:  proc,
		prepared: prep,
		done:     make(chan struct{}),
	}
	pp.stopStartWatch = context.AfterFunc(ctx, func() {
		pp.killOnCancel(ctx)
	})
	return pp, nil
}

type preparedProcess struct {
	Process
	prepared       Prepared
	done           chan struct{}
	doneOnce       sync.Once
	closeOnce      sync.Once
	closeErr       error
	stopStartWatch func() bool
}

func (p *preparedProcess) killOnCancel(ctx context.Context) {
	select {
	case <-p.done:
		return
	default:
		if err := p.Kill(); err != nil {
			logger.Warnf(ctx, "[LocalSandbox] kill async process on cancel: %v", err)
		}
	}
}

func (p *preparedProcess) Wait(ctx context.Context) (ExitStatus, error) {
	if ctx != nil {
		stopWait := context.AfterFunc(ctx, func() {
			p.killOnCancel(ctx)
		})
		defer stopWait()
	}
	status, err := p.Process.Wait(ctx)
	p.doneOnce.Do(func() { close(p.done) })
	if p.stopStartWatch != nil {
		p.stopStartWatch()
	}
	p.closeOnce.Do(func() { p.closeErr = p.prepared.Close() })
	return status, errors.Join(err, p.closeErr)
}
