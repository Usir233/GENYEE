package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/sandbox/workspace"
)

// CreateSnapshot forwards provider snapshot creation for the live sandbox bound
// to sessionID. Session execution never uses this optional capability; it is
// reserved for skill image maintenance.
func (m *SessionBoundManager) CreateSnapshot(
	ctx context.Context, sessionID string, name string,
) (RemoteSnapshotRef, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return RemoteSnapshotRef{}, err
	}
	snapshots, ok := SnapshotManagerFrom(m.client)
	if !ok || !m.client.Capabilities().SupportsSnapshots {
		return RemoteSnapshotRef{}, errors.New("sandbox: remote provider does not support snapshots")
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return RemoteSnapshotRef{}, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "exec")
	if err != nil {
		return RemoteSnapshotRef{}, err
	}
	defer release()
	handle, err := m.resolveScope(ctx, sc)
	if err != nil {
		return RemoteSnapshotRef{}, err
	}
	return snapshots.CreateSnapshot(ctx, handle.ID(), name)
}

// DeleteSnapshot forwards provider snapshot deletion. The skill install path
// uses it to abandon an orphan when the pointer switch fails; the reaper uses
// it to prune superseded snapshots that have aged past retention.
func (m *SessionBoundManager) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	snapshots, ok := SnapshotManagerFrom(m.client)
	if !ok || !m.client.Capabilities().SupportsSnapshots {
		return errors.New("sandbox: remote provider does not support snapshots")
	}
	return snapshots.DeleteSnapshot(ctx, snapshotID)
}

// DeleteForkSnapshot removes a fork snapshot. sessionID is accepted so the
// method matches SessionForkSandboxPort; deletion is by snapshot ID.
func (m *SessionBoundManager) DeleteForkSnapshot(
	ctx context.Context, _ /* sessionID */, snapshotID string,
) error {
	return m.DeleteSnapshot(ctx, snapshotID)
}

// ListSnapshots forwards provider snapshot listing for audit and later cleanup
// tasks.
func (m *SessionBoundManager) ListSnapshots(
	ctx context.Context, sandboxID string,
) ([]RemoteSnapshotRef, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, err
	}
	snapshots, ok := SnapshotManagerFrom(m.client)
	if !ok || !m.client.Capabilities().SupportsSnapshots {
		return nil, errors.New("sandbox: remote provider does not support snapshots")
	}
	return snapshots.ListSnapshots(ctx, sandboxID)
}

// SessionTerminalManager advertises the interactive-terminal capability while
// a real remote backend is active and the provider implements PTY streaming
// (E2B, Cube and Docker all do).
func (m *SessionBoundManager) SessionTerminalManager() SessionTerminalManager {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	if _, ok := TerminalManagerFrom(m.client); !ok {
		return nil
	}
	return m
}

// OpenSessionTerminal opens a PTY on the sandbox currently bound to the
// session. It is strictly lookup-only: with no live binding it returns
// ErrNoLiveSessionSandbox instead of provisioning, because the terminal
// entry point lacks the config-pin context that agent-driven creation
// relies on. A bound sandbox that is not confirmed running returns
// ErrSandboxPaused unless opts.AllowResume is set — Connect would wake a
// paused instance. A backend that cannot stream PTYs returns
// ErrTerminalUnsupported, not "no sandbox".
func (m *SessionBoundManager) OpenSessionTerminal(
	ctx context.Context,
	sessionID string,
	opts RemoteTerminalOptions,
) (RemoteTerminalSession, error) {
	terminal, ok := TerminalManagerFrom(m.client)
	if !ok {
		return nil, ErrTerminalUnsupported
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sc.user {
		if strings.TrimSpace(opts.Cwd) == "" {
			opts.Cwd = sc.layout.Root
		} else {
			cwd, err := workspace.CleanWorkDirIn(sc.layout, opts.Cwd, false)
			if err != nil {
				return nil, err
			}
			opts.Cwd = cwd
		}
		opts.Envs = withSessionEnv(opts.Envs, sc.layout)
		// C10: PIDs on a shared machine belong to every session of the
		// owner; a client-supplied PID could attach another session's shell.
		opts.AttachPID = 0
	} else if strings.TrimSpace(opts.Cwd) != "" {
		cwd, err := cleanSessionWorkDir(opts.Cwd, false)
		if err != nil {
			return nil, err
		}
		opts.Cwd = cwd
	}
	opts.Envs = withBrowserEnv(opts.Envs, sc.key.SessionID, sc.layout)
	// The PTY outlives this call: admit it on a context detached from the
	// HTTP request; heldTerminalSession releases it on Close.
	opCtx, release, err := m.admitUser(context.WithoutCancel(ctx), sc, "pty")
	if err != nil {
		return nil, err
	}
	// The dial and the stream both stop if the lease is lost. callCtx lives
	// as long as the terminal: cancelling it when this function returns
	// would close a PTY that has only just opened.
	callCtx, cancelCall := context.WithCancelCause(ctx)
	stopCall := context.AfterFunc(opCtx, func() { cancelCall(context.Cause(opCtx)) })
	abandon := func(err error) (RemoteTerminalSession, error) {
		stopCall()
		cancelCall(err)
		release()
		return nil, err
	}
	if !opts.AllowResume {
		if err := m.RequireRunningSessionSandbox(callCtx, sessionID); err != nil {
			return abandon(err)
		}
	}
	handle, found, err := m.lookupSessionHandle(callCtx, sessionID)
	if err != nil || !found {
		if err != nil {
			return abandon(err)
		}
		return abandon(ErrNoLiveSessionSandbox)
	}
	// A shared machine already exists for the owner's other sessions. This
	// session's directory is created by the first shell command, not by
	// attaching a PTY, so opening the terminal first would start a shell in
	// a directory that is not there yet.
	if sc.user {
		if err := m.prepareSessionDirs(
			callCtx, handle, DefaultSandboxExecUser,
			sc.layout.InputDir, sc.layout.OutputDir, opts.Cwd,
		); err != nil {
			return abandon(err)
		}
	}
	session, err := terminal.OpenTerminal(callCtx, handle, opts)
	if err != nil {
		return abandon(err)
	}
	if !sc.user {
		stopCall()
		release()
		return session, nil
	}
	held := &heldTerminalSession{
		RemoteTerminalSession: session,
		release:               release,
		cancelStream:          cancelCall,
		stopLeaseWatch:        stopCall,
	}
	if exempt := admissionExemptFrom(opCtx); len(exempt) > 0 {
		held.member = exempt[len(exempt)-1]
	}
	// C3: once the lease is lost another replica may rebuild or reclaim the
	// machine, so the PTY must not keep writing to it.
	held.stopWatch = context.AfterFunc(opCtx, func() { _ = held.Close() })
	return held, nil
}

// heldTerminalSession keeps the shared machine's admission for as long as
// the PTY is attached, so a rebuild or reclaim waits for the user to close
// the terminal.
type heldTerminalSession struct {
	RemoteTerminalSession
	once           sync.Once
	closeErr       error
	release        func()
	cancelStream   func(error)
	stopLeaseWatch func() bool
	stopWatch      func() bool
	member         string // the PTY's admission member; tests use it to inject loss
}

// Close closes the PTY and releases its admission. It runs at most once,
// whether the user closed the terminal or the lease was lost.
func (s *heldTerminalSession) Close() error {
	s.once.Do(func() {
		if s.stopWatch != nil {
			s.stopWatch()
		}
		if s.stopLeaseWatch != nil {
			s.stopLeaseWatch()
		}
		s.closeErr = s.RemoteTerminalSession.Close()
		if s.cancelStream != nil {
			s.cancelStream(context.Canceled)
		}
		s.release()
	})
	return s.closeErr
}

var _ SessionTerminalProvider = (*SessionBoundManager)(nil)

// SessionDesktopManager advertises the graphical-desktop capability while a
// real remote backend is active, the provider can relay a data-plane
// WebSocket (E2B and Cube do; Docker is not scheduled), and this config's
// base image is a desktop template. DesktopEnabled is the stored bit that
// survives skill snapshots replacing template_id with a UUID; without it the
// tab would Exec ensure.sh on a CLI image and only then report unsupported.
func (m *SessionBoundManager) SessionDesktopManager() SessionDesktopManager {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	if m.config == nil || !m.config.DesktopEnabled {
		return nil
	}
	if _, ok := DesktopManagerFrom(m.client); !ok {
		return nil
	}
	return m
}

// RequireRunningSessionSandbox reports ErrNoLiveSessionSandbox or
// ErrSandboxPaused without Connect. Lookup-only terminal and desktop opens
// use it so opening a panel cannot resume (and re-bill) a paused instance.
// Only a List-confirmed running sandbox is safe: paused/transitioning
// Connect resumes, and a list miss with a stale binding is not "no sandbox".
func (m *SessionBoundManager) RequireRunningSessionSandbox(
	ctx context.Context,
	sessionID string,
) error {
	if m == nil {
		return ErrNoLiveSessionSandbox
	}
	state, bound, err := m.peekBoundSandboxState(ctx, sessionID)
	if err != nil {
		return err
	}
	if !bound {
		return ErrNoLiveSessionSandbox
	}
	if state != RemoteStateRunning {
		if m.client.Provider() == SandboxTypeDocker {
			return ErrSandboxStopped
		}
		return ErrSandboxPaused
	}
	return nil
}

// OpenSessionDesktop dials the desktop of the sandbox currently bound to the
// session.
//
// Unlike OpenSessionTerminal there is no AllowResume branch: by the time this
// runs, SandboxDesktopService has already driven the same
// resolveSandboxForExecution path a chat turn uses, so the sandbox is live
// and — critically — its inbound token is registered on THIS replica.
// Reaching here with no binding means the sandbox went away in between,
// which is an error, not a reason to provision.
func (m *SessionBoundManager) OpenSessionDesktop(
	ctx context.Context,
	sessionID string,
	opts RemoteDesktopOptions,
) (*SessionDesktopConn, error) {
	if m.SessionDesktopManager() == nil {
		return nil, ErrDesktopUnsupported
	}
	desktop, ok := DesktopManagerFrom(m.client)
	if !ok {
		return nil, ErrDesktopUnsupported
	}
	handle, found, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoLiveSessionSandbox
	}
	conn, err := desktop.DialDesktop(ctx, handle, opts)
	if err != nil {
		return nil, err
	}
	out := &SessionDesktopConn{Conn: conn, SandboxID: handle.ID()}
	if refresher, ok := DesktopTTLRefresherFrom(m.client); ok {
		bound := handle
		out.StartTTLRefresh = func(ttlCtx context.Context) {
			refresher.StartDesktopTTLRefresh(ttlCtx, bound)
		}
	}
	return out, nil
}

var _ SessionDesktopProvider = (*SessionBoundManager)(nil)

// DesktopEnabledForPrompt is true when the sandbox configuration turns the
// desktop on and the backend can serve it. Unlike SessionDesktopManager it
// does consult Config.DesktopEnabled.
func (m *SessionBoundManager) DesktopEnabledForPrompt() bool {
	if m == nil || m.config == nil || !m.config.DesktopEnabled {
		return false
	}
	return m.SessionDesktopManager() != nil
}

var _ SessionDesktopPromptReporter = (*SessionBoundManager)(nil)

// CreateForkSnapshot snapshots the session's already-bound sandbox. It never
// provisions: an unbound session or a backend without snapshots returns an
// error so fork can degrade.
func (m *SessionBoundManager) CreateForkSnapshot(
	ctx context.Context, sessionID, name string,
) (string, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return "", err
	}
	snapshots, ok := SnapshotManagerFrom(m.client)
	if !ok || !m.client.Capabilities().SupportsSnapshots {
		return "", errors.New("sandbox: remote provider does not support snapshots")
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if sc.user {
		return "", errors.New("sandbox: shared machines are forked by copying the session workspace")
	}
	sandboxID, bound := m.BoundSandboxID(ctx, sessionID)
	if !bound || sandboxID == "" {
		return "", errors.New("sandbox: session has no bound sandbox")
	}
	ref, err := createForkOrProviderSnapshot(ctx, m.client, snapshots, sandboxID, name)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(ref.ID)
	if id == "" {
		return "", errors.New("sandbox: snapshot returned empty id")
	}
	return id, nil
}

type forkSnapshotCreator interface {
	CreateForkSnapshot(ctx context.Context, sandboxID, name string) (RemoteSnapshotRef, error)
}

// createForkOrProviderSnapshot uses a fork-specific commit when the client
// has one (Docker's genyee-fork/ namespace). Cube and E2B have no extra
// namespace, so they keep using CreateSnapshot.
func createForkOrProviderSnapshot(
	ctx context.Context,
	client RemoteSandboxClient,
	snapshots RemoteSnapshotManager,
	sandboxID, name string,
) (RemoteSnapshotRef, error) {
	if creator, ok := client.(forkSnapshotCreator); ok {
		return creator.CreateForkSnapshot(ctx, sandboxID, name)
	}
	return snapshots.CreateSnapshot(ctx, sandboxID, name)
}

// peekBoundSandboxState reads provider listing for the bound sandbox without
// Connect. The bool is "a binding exists for this provider", not "List
// returned a row": a list miss still reports bound so lookup cannot pretend
// the session has no sandbox. E2B/Cube Connect resumes a paused instance,
// so the lookup-only terminal path must List first.
func (m *SessionBoundManager) peekBoundSandboxState(
	ctx context.Context,
	sessionID string,
) (RemoteSandboxState, bool, error) {
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return "", false, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return "", false, err
	}
	key := sc.machine()
	binding, err := m.bindings.Get(ctx, key)
	if err != nil {
		return "", false, fmt.Errorf("sandbox: read session binding: %w", err)
	}
	if binding == nil || binding.Provider != m.client.Provider() {
		return "", false, nil
	}
	summaries, err := m.client.List(ctx, RemoteListFilter{
		Metadata: m.lifecycle.stateFilter(sc.key),
		States: []RemoteSandboxState{
			RemoteStateRunning,
			RemoteStatePaused,
			RemoteStateTransitioning,
		},
	})
	if err != nil {
		return "", false, fmt.Errorf("sandbox: list session sandbox: %w", err)
	}
	for _, summary := range summaries {
		if summary.ID == binding.SandboxID {
			return summary.State, true, nil
		}
	}
	// Binding exists but the provider list did not return it (lag, metadata
	// mismatch, or a state outside the filter). That is not "no sandbox":
	// the UI should ask before Connect, which would resume a paused VM.
	return RemoteStateUnknown, true, nil
}

// DesktopMachine names the physical machine behind sessionID's desktop and
// the sandbox it currently is. Sessions on one shared machine contend for one
// control slot; a rebuilt machine, or a sandbox of another deployment,
// provider or config, never shares a key. Call it while holding the session's
// operation so the binding cannot change underneath the caller.
func (m *SessionBoundManager) DesktopMachine(ctx context.Context, sessionID string) (string, string, error) {
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if !sc.user {
		// A per-session machine rebuilds a stale image on the next resolve.
		// Pinning the current sandbox id would make the desktop's ensure
		// command lookup-only and skip that rebuild.
		return sessionID, "", nil
	}
	binding, err := m.bindings.Get(ctx, sc.machine())
	if err != nil {
		return "", "", err
	}
	if binding == nil || binding.SandboxID == "" || binding.Provider != m.client.Provider() {
		return "", "", ErrNoLiveSessionSandbox
	}
	parts := []string{
		m.namespace,
		string(m.client.Provider()),
		strconv.FormatUint(binding.ConfigTenantID, 10),
		NormalizeConfigID(m.configID),
		binding.SandboxID,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:]), binding.SandboxID, nil
}

// sessionDisplayPrefix runs at the start of an agent shell command. The seat
// number lives only inside the sandbox, so the model cannot be told it ahead
// of time, and an unset DISPLAY makes X clients fail closed rather than open
// the shared :0 screen. A seat is claimed only where screen intent exists:
// agent-browser starting a browser (headed unless asked otherwise), the
// Desktop tab connect (ensure.sh), or the model running gy-display claim
// itself. This prefix never claims; it pins the shell to the seat the session
// already holds and refreshes that seat's idleness clock (mtime), so a
// session that nobody watches never pays for a screen. A missing gy-display
// (older image, local shell) leaves the command untouched.
const sessionDisplayPrefix = `if command -v gy-display >/dev/null 2>&1 && [ -n "${GENYEE_SESSION_ID:-}" ]; then
  _gy_seat=$(gy-display where "$GENYEE_SESSION_ID" 2>/dev/null) || true
  case "${_gy_seat:-}" in
    ''|*[!0-9]*) unset DISPLAY GY_SEAT ;;
    *) export DISPLAY=":$_gy_seat" GY_SEAT="$_gy_seat" ;;
  esac
  unset _gy_seat
fi
`

// WithSessionDisplay pins the command to the screen this session holds.
// The command itself still runs when the lookup fails.
func WithSessionDisplay(command string) string {
	return sessionDisplayPrefix + command
}

var forkCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

const (
	forkCopyTimeout   = 5 * time.Minute
	forkMaxCandidates = 2000
)

// WorkspaceFork is an open fork of a session on a shared machine. It holds
// the machine's and the source session's admission until Release, so the
// machine named by SandboxID is the one every step runs on. Its steps fail
// with admission.ErrLeaseLost once that hold is lost.
type WorkspaceFork interface {
	SandboxID() string
	// Context is cancelled with admission.ErrLeaseLost once the hold is
	// lost. Run every step of the fork with it, database steps included.
	Context() context.Context
	// Copy materializes sha of the source's checkpoint repository as
	// targetSessionID's workspace and reports which of candidates are sha
	// or its ancestors, i.e. which checkpoints exist in the new repository.
	Copy(targetSessionID, sha string, candidates []string) (map[string]bool, error)
	// Remove deletes targetSessionID's work tree and repository.
	Remove(targetSessionID string) error
	Release()
}

type workspaceFork struct {
	m       *SessionBoundManager
	ctx     context.Context
	source  WorkspaceLayout
	handle  RemoteSandboxHandle
	member  string
	release func()
}

// BeginWorkspaceFork opens a fork of sourceSessionID on its shared machine.
// It never creates the machine: a source without one has nothing to copy.
func (m *SessionBoundManager) BeginWorkspaceFork(ctx context.Context, sourceSessionID string) (WorkspaceFork, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, err
	}
	sc, err := m.scope(ctx, sourceSessionID)
	if err != nil {
		return nil, err
	}
	if !sc.user {
		return nil, ErrSharedSandbox
	}
	opCtx, release, err := m.admitUser(ctx, sc, "fork")
	if err != nil {
		// admitUser releases anything it acquired and returns a nil func.
		if release != nil {
			release()
		}
		return nil, err
	}
	handle, found, err := m.lookupSessionHandleForKey(opCtx, sc.machine(), "")
	if err != nil {
		release()
		return nil, err
	}
	if !found {
		release()
		return nil, ErrNoLiveSessionSandbox
	}
	exempt := admissionExemptFrom(opCtx)
	return &workspaceFork{
		m: m, ctx: opCtx, source: sc.layout, handle: handle,
		member: exempt[len(exempt)-1], release: release,
	}, nil
}

func (f *workspaceFork) SandboxID() string        { return f.handle.ID() }
func (f *workspaceFork) Context() context.Context { return f.ctx }
func (f *workspaceFork) Member() string           { return f.member }
func (f *workspaceFork) Release()                 { f.release() }

func (f *workspaceFork) alive() error {
	if err := context.Cause(f.ctx); err != nil {
		return err
	}
	return nil
}

func (f *workspaceFork) run(script string) (*RemoteExecResult, error) {
	if err := f.alive(); err != nil {
		return nil, err
	}
	result, err := f.m.client.Exec(f.ctx, f.handle, RemoteExecRequest{
		Shell: true, Command: script, User: "root", Timeout: forkCopyTimeout,
	})
	if cause := f.alive(); cause != nil {
		return nil, cause
	}
	if err != nil {
		return nil, err
	}
	if result == nil || result.ExitCode != 0 || result.Killed {
		detail := "no result"
		if result != nil {
			detail = fmt.Sprintf("exit=%d stderr=%s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		return nil, errors.New(detail)
	}
	return result, nil
}

func (f *workspaceFork) Copy(targetSessionID, sha string, candidates []string) (map[string]bool, error) {
	if !forkCommitPattern.MatchString(sha) {
		return nil, fmt.Errorf("sandbox: invalid fork commit %q", sha)
	}
	target, err := UserWorkspaceLayout(targetSessionID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var quoted []string
	for _, c := range candidates {
		if forkCommitPattern.MatchString(c) && !seen[c] && len(quoted) < forkMaxCandidates {
			seen[c] = true
			quoted = append(quoted, ShellQuote(c))
		}
	}
	script := fmt.Sprintf(`set -eu
SRC_GIT=%s
DST_TREE=%s
DST_GIT=%s
SHA=%s
for p in /workspace /workspace/sessions /var/lib/genyee /var/lib/genyee/sessions; do
  if [ -L "$p" ]; then echo "refusing symlinked $p" >&2; exit 4; fi
done
if [ -e "$DST_TREE" ] || [ -e "$DST_GIT" ]; then echo "fork target exists" >&2; exit 5; fi
mkdir -p /var/lib/genyee/sessions /workspace/sessions
tmp=$(mktemp -d /var/lib/genyee/sessions/.fork.XXXXXX)
trap 'rm -rf -- "$tmp"' EXIT
g() { git -c safe.directory='*' -c core.hooksPath=/dev/null "$@"; }
g --git-dir="$SRC_GIT" cat-file -e "$SHA^{commit}"
if g --git-dir="$SRC_GIT" ls-tree -r "$SHA" | awk '$1=="160000"{f=1} END{exit !f}'; then
  echo "checkpoint contains a gitlink" >&2; exit 6
fi
g init -q --bare "$tmp/repo.git"
g -c uploadpack.allowAnySHA1InWant=true --git-dir="$tmp/repo.git" fetch -q --no-tags "$SRC_GIT" "$SHA:refs/heads/main"
g --git-dir="$tmp/repo.git" symbolic-ref HEAD refs/heads/main
g --git-dir="$tmp/repo.git" config core.bare false
g --git-dir="$tmp/repo.git" config user.email agent@genyee.local
g --git-dir="$tmp/repo.git" config user.name 'GENYEE Agent'
mkdir -p "$tmp/tree"
g --git-dir="$tmp/repo.git" --work-tree="$tmp/tree" checkout -q -f main
mkdir -p "$tmp/tree/input" "$tmp/tree/output"
mv -- "$tmp/repo.git" "$DST_GIT"
mv -- "$tmp/tree" "$DST_TREE"
for c in %s; do
  if g --git-dir="$DST_GIT" merge-base --is-ancestor "$c" "$SHA" 2>/dev/null; then echo "R $c"; fi
done
`, ShellQuote(f.source.GitDir), ShellQuote(target.Root), ShellQuote(target.GitDir), ShellQuote(sha),
		strings.Join(quoted, " "))
	result, err := f.run(script)
	if err != nil {
		return nil, fmt.Errorf("sandbox: copy session workspace: %w", err)
	}
	reachable := map[string]bool{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if c, ok := strings.CutPrefix(strings.TrimSpace(line), "R "); ok && seen[c] {
			reachable[c] = true
		}
	}
	return reachable, nil
}

func (f *workspaceFork) Remove(targetSessionID string) error {
	target, err := UserWorkspaceLayout(targetSessionID)
	if err != nil {
		return err
	}
	_, err = f.run(fmt.Sprintf(`set -eu
for p in /workspace /workspace/sessions /var/lib/genyee /var/lib/genyee/sessions; do
  if [ -L "$p" ]; then echo "refusing symlinked $p" >&2; exit 4; fi
done
rm -rf -- %s %s`, ShellQuote(target.Root), ShellQuote(target.GitDir)))
	if err != nil {
		return fmt.Errorf("sandbox: remove forked workspace: %w", err)
	}
	return nil
}
