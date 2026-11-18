package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/common/redislock"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/sandbox/admission"
	"github.com/Usir233/GENYEE/internal/sandbox/allocation"
)

// --- internal helpers --------------------------------------------------------

// BeginSessionTurn opens one chat-turn hold for sessionID. The first resolve
// after this may rebuild a stale image; later resolves of the same turn keep
// the sandbox. end closes exactly this hold.
func (m *SessionBoundManager) BeginSessionTurn(ctx context.Context, sessionID string) (context.Context, func(), error) {
	noop := func() {}
	if m == nil {
		return ctx, noop, nil
	}
	leaser, ok := m.bindings.(sessionTurnLeaseStore)
	if !ok {
		return ctx, noop, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if err := leaser.BeginTurn(ctx, sc.key); err != nil {
		return nil, nil, err
	}
	endLease := func() {
		if err := leaser.EndTurn(context.WithoutCancel(ctx), sc.key); err != nil {
			logger.Warnf(ctx, "[sandbox] end turn for session %s failed: %v", sessionID, err)
		}
	}
	if !sc.user {
		var once sync.Once
		return ctx, func() { once.Do(endLease) }, nil
	}
	turnCtx, release, err := m.admitUser(ctx, sc, "turn")
	if err != nil {
		endLease()
		return nil, nil, err
	}
	var once sync.Once
	return turnCtx, func() {
		once.Do(func() {
			release()
			endLease()
		})
	}, nil
}

// HasActiveTurn reports whether an agent turn currently holds the session's
// sandbox lease. A store without turn-lease support is treated as not busy
// so fork can proceed.
func (m *SessionBoundManager) HasActiveTurn(ctx context.Context, sessionID string) (bool, error) {
	if m == nil {
		return false, nil
	}
	leaser, ok := m.bindings.(sessionTurnLeaseStore)
	if !ok {
		return false, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return false, err
	}
	active, _, err := leaser.TurnState(ctx, sc.key)
	return active, err
}

const rewindExclusiveWait = 5 * time.Second

// LockRewindSession takes the session's rewind lock and, on a shared
// machine, the session's exclusive window, so no other operation of the
// session touches its work tree during the reset. Run the reset with the
// returned context. On a per-session sandbox the context is ctx itself.
func (m *SessionBoundManager) LockRewindSession(
	ctx context.Context, sessionID string,
) (context.Context, func(), error) {
	unlockRewind, err := m.TryLockRewind(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	exCtx, release, err := m.acquireSessionExclusive(ctx, sessionID, m.rewindWait)
	if err != nil {
		unlockRewind()
		return nil, nil, err
	}
	return exCtx, func() {
		release()
		unlockRewind()
	}, nil
}

// TryLockRewind takes an exclusive rewind lock for sessionID. Stores that do
// not implement rewind locking succeed as a no-op so local tests without a
// lease store still rewind.
func (m *SessionBoundManager) TryLockRewind(ctx context.Context, sessionID string) (func(), error) {
	noop := func() {}
	if m == nil {
		return noop, nil
	}
	locker, ok := m.bindings.(interface {
		TryLockRewind(context.Context, SessionSandboxKey) (func(), error)
	})
	if !ok {
		return noop, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return locker.TryLockRewind(ctx, sc.key)
}

// HasRewindLock reports whether rewind currently holds sessionID.
func (m *SessionBoundManager) HasRewindLock(ctx context.Context, sessionID string) (bool, error) {
	if m == nil {
		return false, nil
	}
	reader, ok := m.bindings.(interface {
		HasRewindLock(context.Context, SessionSandboxKey) (bool, error)
	})
	if !ok {
		return false, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return false, err
	}
	return reader.HasRewindLock(ctx, sc.key)
}

var _ SessionTurnHolder = (*SessionBoundManager)(nil)

// resolveSession resolves (or lazily creates) the remote sandbox bound to
// sessionID. Persistent path only.
func (m *SessionBoundManager) resolveSession(
	ctx context.Context,
	sessionID string,
) (RemoteSandboxHandle, error) {
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return m.resolveScope(ctx, sc)
}

// BoundSandboxID returns the ID of the sandbox currently bound to sessionID.
// It never provisions and never Connects: a session with no live binding
// reports ok=false, which callers treat as "nothing to check point".
func (m *SessionBoundManager) BoundSandboxID(
	ctx context.Context, sessionID string,
) (string, bool) {
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return "", false
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return "", false
	}
	binding, err := m.bindings.Get(ctx, sc.machine())
	if err != nil {
		return "", false
	}
	if binding == nil || binding.Provider != m.client.Provider() {
		return "", false
	}
	sandboxID := strings.TrimSpace(binding.SandboxID)
	if sandboxID == "" {
		return "", false
	}
	return sandboxID, true
}

// lookupSessionHandle reads the authoritative binding and, when one exists
// with the current provider, connects to the remote sandbox without
// allocating. Used by artifact / staging paths that must never provision.
func (m *SessionBoundManager) lookupSessionHandle(
	ctx context.Context,
	sessionID string,
) (RemoteSandboxHandle, bool, error) {
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil, false, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, false, err
	}
	key := sc.machine()
	// Serialise scope lookups so even concurrent reads connect only once.
	// The scope is owned by one operation, not by this long-lived manager.
	if scope, ok := ctx.Value(sessionFileOperationKey{}).(*sessionFileOperation); ok {
		scope.mu.Lock()
		defer scope.mu.Unlock()
		cacheKey := sessionFileHandleKey{manager: m, session: key}
		if handle := scope.handles[cacheKey]; handle != nil {
			return handle, true, nil
		}
		handle, found, err := m.lookupSessionHandleForKey(ctx, key, "")
		if err == nil && found {
			scope.handles[cacheKey] = handle
		}
		return handle, found, err
	}
	return m.lookupSessionHandleForKey(ctx, key, "")
}

func (m *SessionBoundManager) lookupSessionHandleForKey(
	ctx context.Context, key SessionSandboxKey, expectedID string,
) (RemoteSandboxHandle, bool, error) {
	binding, err := m.bindings.Get(ctx, key)
	if err != nil {
		return nil, false, fmt.Errorf("sandbox: read session binding: %w", err)
	}
	if binding == nil || binding.Provider != m.client.Provider() {
		return nil, false, nil
	}
	if expectedID != "" && binding.SandboxID != expectedID {
		return nil, false, ErrBoundSandboxChanged
	}
	handle, err := m.client.Connect(ctx, RemoteConnectRequest{
		SandboxID:          binding.SandboxID,
		TrafficAccessToken: binding.TrafficAccessToken,
	})
	if err != nil {
		if CanReplaceRemoteBinding(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("sandbox: connect session sandbox: %w", err)
	}
	if handle == nil || handle.ID() != binding.SandboxID ||
		handle.Provider() != m.client.Provider() {
		return nil, false, errors.New("sandbox: remote handle does not match binding")
	}
	persistInboundToken(ctx, m.bindings, key, *binding, handle)
	return handle, true, nil
}

// sessionScope is where one session's state lives.
type sessionScope struct {
	key        SessionSandboxKey
	layout     WorkspaceLayout
	assignment allocation.Assignment
	user       bool
}

func (s sessionScope) machine() SessionSandboxKey { return s.key.Machine() }

func (s sessionScope) sessionKeyOnly() SessionSandboxKey {
	return SessionSandboxKey{TenantID: s.key.TenantID, SessionID: s.key.SessionID}
}

func (s sessionScope) digest(namespace string) string { return s.key.User.Digest(namespace) }

// addressFor returns the session's recorded placement. User-mode rows are
// cached as addresses; session-mode rows and misses are not, because a turn
// may record the placement after this manager was built.
func (m *SessionBoundManager) addressFor(ctx context.Context, sessionID string) (allocation.Assignment, bool, error) {
	if m == nil || m.allocations == nil || strings.TrimSpace(sessionID) == "" {
		return allocation.Assignment{}, false, nil
	}
	if cached, ok := m.addresses.Load(sessionID); ok {
		return cached.(allocation.Assignment), true, nil
	}
	a, ok, err := m.allocations.SessionAllocation(ctx, sessionID)
	if err != nil {
		return allocation.Assignment{}, false, fmt.Errorf("sandbox: read session allocation: %w", err)
	}
	if ok && a.IsUser() {
		m.addresses.Store(sessionID, a)
	}
	return a, ok, nil
}

// scope resolves where sessionID's state lives. Every exported per-session
// method goes through here.
func (m *SessionBoundManager) scope(ctx context.Context, sessionID string) (sessionScope, error) {
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return sessionScope{}, err
	}
	a, ok, err := m.addressFor(ctx, key.SessionID)
	if err != nil {
		return sessionScope{}, err
	}
	if !ok || !a.IsUser() {
		return sessionScope{key: key, layout: withValidatedSkillOutputDir(RemoteWorkspaceLayout()), assignment: a}, nil
	}
	// Eligibility is not in the assignment (C17); admitUser verifies it
	// from the session row before any provider call.
	if a.Machine.OwnerTenantID != key.TenantID {
		return sessionScope{}, fmt.Errorf("sandbox: session %s belongs to tenant %d, not %d",
			key.SessionID, a.Machine.OwnerTenantID, key.TenantID)
	}
	if NormalizeConfigID(a.ConfigID) != NormalizeConfigID(m.configID) {
		return sessionScope{}, fmt.Errorf("sandbox: session %s is assigned to config %s, not %s",
			key.SessionID, a.ConfigID, m.configID)
	}
	if m.admission == nil {
		return sessionScope{}, errors.New("sandbox: a user-mode session requires the admission registry")
	}
	layout, err := a.Layout()
	if err != nil {
		return sessionScope{}, err
	}
	machine, _ := a.UserMachine()
	return sessionScope{
		key:        SessionSandboxKey{TenantID: key.TenantID, SessionID: key.SessionID, User: machine},
		layout:     layout,
		assignment: a,
		user:       true,
	}, nil
}

type exclusiveSessionKey struct{}

// withExclusiveSession marks ctx as the holder of scope's exclusive window,
// so the holder's own operations skip the session lease it closed.
func withExclusiveSession(ctx context.Context, scope admission.Scope) context.Context {
	return context.WithValue(ctx, exclusiveSessionKey{}, scope)
}

func holdsExclusive(ctx context.Context, scope admission.Scope) bool {
	held, ok := ctx.Value(exclusiveSessionKey{}).(admission.Scope)
	return ok && held == scope
}

// holdWithWait registers member on scope, waiting out a closed scope for at
// most admissionWait.
func (m *SessionBoundManager) holdWithWait(
	ctx context.Context, scope admission.Scope, member string,
) (admission.Lease, error) {
	deadline := time.Now().Add(m.admissionWait)
	var ticker *time.Ticker
	for {
		lease, err := m.admission.Hold(ctx, scope, member, admission.DefaultHoldTTL)
		if err == nil {
			return lease, nil
		}
		if !errors.Is(err, admission.ErrClosed) || time.Now().After(deadline) {
			return nil, fmt.Errorf("sandbox: shared machine is busy: %w", err)
		}
		if ticker == nil {
			ticker = time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// verifyLive re-reads, from the database, everything that makes the cached
// address usable now (C4, C15): the same live assignment, and a session that
// exists, is not retired, and whose current owner is the assignment's owner.
func (m *SessionBoundManager) verifyLive(ctx context.Context, sc sessionScope) error {
	if err := m.allocations.VerifySessionAllocation(ctx, sc.key.TenantID, sc.key.SessionID, sc.assignment); err != nil {
		if errors.Is(err, ErrSandboxSessionDeleted) || errors.Is(err, ErrUserAllocationRejected) {
			return err
		}
		return fmt.Errorf("sandbox: verify session allocation: %w", err)
	}
	return nil
}

// admitUser registers one operation of a user-mode session (C4): machine
// lease, then session lease, then a fresh validity check. The returned
// context is cancelled when either lease is lost (C3); pass it to every
// provider call of the operation. It adds this operation's member to the
// exemptions already on ctx (a turn's own members), never a member guessed
// from the session id (C6). Session-mode scopes pass through untouched.
func (m *SessionBoundManager) admitUser(
	ctx context.Context, sc sessionScope, kind string,
) (context.Context, func(), error) {
	if !sc.user {
		return ctx, func() {}, nil
	}
	token, err := redislock.NewToken()
	if err != nil {
		return nil, nil, err
	}
	member := admission.SessionMember(sc.key.SessionID, kind, token)
	digest := sc.digest(m.namespace)
	machineLease, err := m.holdWithWait(ctx, admission.MachineScope(digest), member)
	if err != nil {
		return nil, nil, err
	}
	leases := []admission.Lease{machineLease}
	sessionLeaseScope := admission.SessionScope(digest, sc.key.SessionID)
	if !holdsExclusive(ctx, sessionLeaseScope) {
		sessionLease, err := m.holdWithWait(ctx, sessionLeaseScope, member)
		if err != nil {
			machineLease.Release()
			if errors.Is(err, admission.ErrClosed) {
				return nil, nil, fmt.Errorf("%w: %v", ErrSessionWorkspaceBusy, err)
			}
			return nil, nil, err
		}
		leases = append(leases, sessionLease)
	}
	releaseLeases := func() {
		for i := len(leases) - 1; i >= 0; i-- {
			leases[i].Release()
		}
	}
	if err := m.verifyLive(ctx, sc); err != nil {
		releaseLeases()
		return nil, nil, err
	}
	signals := make([]admission.Signal, 0, len(leases))
	for _, l := range leases {
		signals = append(signals, l)
	}
	opCtx, stop := admission.Bind(ctx, signals...)
	// Exempt this operation; a turn's own members arrive through ctx.
	opCtx = withAdmissionExempt(opCtx, member)
	var once sync.Once
	return opCtx, func() {
		once.Do(func() {
			stop()
			releaseLeases()
		})
	}, nil
}

// acquireExclusive closes sessionID's session scope and waits (bounded) for
// its registered operations to leave. The returned context lets this
// caller's own operations run inside the window.
func (m *SessionBoundManager) acquireExclusive(
	ctx context.Context, digest, sessionID string, wait time.Duration,
) (context.Context, admission.Barrier, error) {
	scope := admission.SessionScope(digest, sessionID)
	barrier, err := m.admission.Close(ctx, scope, admission.DefaultHoldTTL)
	if errors.Is(err, admission.ErrClosed) {
		return nil, nil, ErrSessionWorkspaceBusy
	}
	if err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(wait)
	var ticker *time.Ticker
	for {
		n, err := m.admission.ActiveExcept(ctx, scope)
		if err != nil {
			barrier.Release()
			return nil, nil, err
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			barrier.Release()
			return nil, nil, ErrSessionWorkspaceBusy
		}
		if ticker == nil {
			ticker = time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
		}
		select {
		case <-ctx.Done():
			barrier.Release()
			return nil, nil, ctx.Err()
		case <-ticker.C:
		}
	}
	return withExclusiveSession(ctx, scope), barrier, nil
}

// AcquireSessionExclusive is acquireExclusive for one user-mode session.
// Use the returned context for the exclusive work; it is cancelled with
// admission.ErrLeaseLost if the window is lost. Call the function to end the
// window. Session-mode sessions get ctx back unchanged.
func (m *SessionBoundManager) AcquireSessionExclusive(
	ctx context.Context, sessionID string,
) (context.Context, func(), error) {
	return m.acquireSessionExclusive(ctx, sessionID, m.admissionWait)
}

func (m *SessionBoundManager) acquireSessionExclusive(
	ctx context.Context, sessionID string, wait time.Duration,
) (context.Context, func(), error) {
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if !sc.user {
		return ctx, func() {}, nil
	}
	// C4: the exclusive window is itself a machine member. Rebuild and reclaim
	// only count the machine scope, so a session barrier alone does not stop
	// them deleting the machine under a rewind or a directory delete.
	token, err := redislock.NewToken()
	if err != nil {
		return nil, nil, err
	}
	digest := sc.digest(m.namespace)
	lease, err := m.holdWithWait(ctx, admission.MachineScope(digest),
		admission.SessionMember(sc.key.SessionID, "exclusive", token))
	if err != nil {
		return nil, nil, err
	}
	exCtx, barrier, err := m.acquireExclusive(ctx, digest, sc.key.SessionID, wait)
	if err != nil {
		lease.Release()
		return nil, nil, err
	}
	// The machine lease stays out of the exemption list: this request's own
	// later exec must not rebuild the machine out from under the window.
	bound, stop := admission.Bind(exCtx, lease, barrier)
	var once sync.Once
	return bound, func() {
		once.Do(func() {
			stop()
			lease.Release()
			barrier.Release()
		})
	}, nil
}

// HoldSessionOperation registers a long-lived operation (desktop relay) of
// sessionID; ctx is cancelled if the lease is lost. A no-op for per-session
// machines.
func (m *SessionBoundManager) HoldSessionOperation(
	ctx context.Context, sessionID, kind string,
) (context.Context, func(), error) {
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	return m.admitUser(ctx, sc, kind)
}

// resolveScope resolves (or lazily creates) the scope's machine. Callers
// admit user-mode scopes first; a deleted user-mode session never reaches
// the lifecycle, which never destroys a shared machine for one session.
func (m *SessionBoundManager) resolveScope(ctx context.Context, sc sessionScope) (RemoteSandboxHandle, error) {
	return m.lifecycle.Resolve(ctx, sc.key)
}

// Namespace is the deployment namespace user machine digests use.
func (m *SessionBoundManager) Namespace() string { return m.namespace }

// UserMachineKey is the machine-level key of a shared user machine.
func (m *SessionBoundManager) UserMachineKey(machine allocation.UserMachine) SessionSandboxKey {
	return SessionSandboxKey{TenantID: machine.OwnerTenantID, User: machine}
}

const sessionWorkspaceCleanupTimeout = 60 * time.Second

// ErrMachineAbsenceUnprovable is returned when a user machine has no binding
// and the provider cannot list by metadata, so it may still exist. Directory
// cleanups keep their task instead of treating the machine as gone.
var ErrMachineAbsenceUnprovable = errors.New("sandbox: cannot prove the user machine is gone")

// DeleteSessionWorkspaces removes the work trees and checkpoint repositories
// of retired sessions from their shared machine. It registers on the machine
// first, then takes each session's scope exclusively so work admitted before
// the retirement finishes before its directory goes; a session whose
// operations do not leave in time is skipped and reported as not deleted.
// Every hold is bound into the command's context, so a lost hold stops the
// deletion (C3). A machine whose binding was lost is re-adopted by metadata,
// never created; only a provider that lists nothing proves it is gone (C5).
// Connect resumes a paused machine: a deleted session's files must not
// linger on its disk.
func (m *SessionBoundManager) DeleteSessionWorkspaces(
	ctx context.Context, machine allocation.UserMachine, sessionIDs []string,
) ([]string, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, err
	}
	if m.admission == nil {
		return nil, errors.New("sandbox: user machines require the admission registry")
	}
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	for _, id := range sessionIDs {
		if _, err := UserWorkspaceLayout(id); err != nil {
			return nil, err
		}
	}
	digest := machine.Digest(m.namespace)
	token, err := redislock.NewToken()
	if err != nil {
		return nil, err
	}
	lease, err := m.holdWithWait(ctx, admission.MachineScope(digest),
		admission.SessionMember(sessionIDs[0], "cleanup", token))
	if err != nil {
		return nil, err
	}
	defer lease.Release()

	signals := []admission.Signal{lease}
	var barriers []admission.Barrier
	defer func() {
		for _, b := range barriers {
			b.Release()
		}
	}()
	var ready []string
	for _, id := range sessionIDs {
		_, barrier, err := m.acquireExclusive(ctx, digest, id, m.admissionWait)
		if errors.Is(err, ErrSessionWorkspaceBusy) {
			continue
		}
		if err != nil {
			return nil, err
		}
		barriers = append(barriers, barrier)
		signals = append(signals, barrier)
		ready = append(ready, id)
	}
	if len(ready) == 0 {
		return nil, nil
	}
	guarded, stop := admission.Bind(ctx, signals...)
	defer stop()
	lost := func(err error) error {
		if cause := context.Cause(guarded); errors.Is(cause, admission.ErrLeaseLost) {
			return cause
		}
		return err
	}

	key := m.UserMachineKey(machine)
	handle, found, err := m.lookupSessionHandleForKey(guarded, key, "")
	if err != nil {
		return nil, lost(err)
	}
	if !found {
		handle, found, err = m.lifecycle.recoverWithoutCreating(guarded, key)
		if err != nil {
			return nil, lost(err)
		}
		if !found {
			return ready, nil
		}
	}
	var targets []string
	var releases []string
	for _, id := range ready {
		l, err := UserWorkspaceLayout(id)
		if err != nil {
			return nil, err
		}
		targets = append(targets, ShellQuote(l.Root), ShellQuote(l.GitDir), ShellQuote(l.BrowserRoot))
		// Idle disconnect keeps the screen so the same session can resume.
		// Delete is what drops it, so a removed session does not leave an X
		// server on the shared machine. Missing gy-display is a no-op.
		releases = append(releases, "gy-display release "+ShellQuote(id))
	}
	// Refuse a symlinked parent instead of following it: an agent on this
	// machine runs as root and may have replaced one.
	script := `set -eu
for p in /workspace /workspace/sessions /var/lib/genyee /var/lib/genyee/sessions; do
  if [ -L "$p" ]; then echo "refusing to clean through symlinked $p" >&2; exit 4; fi
done
for t in ` + strings.Join(targets, " ") + `; do
  if [ -L "$t" ]; then rm -f -- "$t"; else rm -rf -- "$t"; fi
done
if command -v gy-display >/dev/null 2>&1; then
  ` + strings.Join(releases, "\n  ") + `
fi`
	result, err := m.client.Exec(guarded, handle, RemoteExecRequest{
		Shell: true, Command: script, User: "root", Timeout: sessionWorkspaceCleanupTimeout,
	})
	if err != nil {
		return nil, lost(fmt.Errorf("sandbox: delete session workspaces: %w", err))
	}
	if result == nil || result.ExitCode != 0 || result.Killed {
		detail := "no result"
		if result != nil {
			detail = fmt.Sprintf("exit=%d stderr=%s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		return nil, fmt.Errorf("sandbox: delete session workspaces: %s", detail)
	}
	if err := lost(nil); err != nil {
		return nil, err
	}
	// A re-adopted machine whose token could not be recovered is bound only
	// once the directories are actually gone. ensureBinding is a no-op when
	// the binding was already there.
	if err := m.lifecycle.ensureBinding(guarded, key, handle); err != nil {
		return nil, lost(err)
	}
	if err := lost(nil); err != nil {
		return nil, err
	}
	return ready, nil
}

// ReclaimUserMachine deletes a shared user machine; see
// remoteSessionLifecycle.reclaimUserMachine for the safety rules.
func (m *SessionBoundManager) ReclaimUserMachine(
	ctx context.Context, machine allocation.UserMachine, check func(context.Context) (bool, error),
) (bool, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return false, err
	}
	return m.lifecycle.reclaimUserMachine(ctx, m.UserMachineKey(machine), check)
}
