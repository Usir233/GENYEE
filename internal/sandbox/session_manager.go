// Package sandbox provides session-bound remote sandbox management.
//
// SessionBoundManager keeps one persistent remote sandbox per tenant session.
// It delegates all provider-specific work to a RemoteSandboxClient adapter and
// treats the authoritative session→sandbox binding as external state that
// lives in SessionSandboxBindingStore (Redis in production, memory in tests
// and single-process deployments). This makes the manager provider-neutral
// (Cube , E2B) and multi-instance safe: two GENYEE processes
// concurrently servicing the same session never allocate duplicate sandboxes,
// and a restart never loses the session's remote resource.
//
// Semantics:
//   - An Execute call with a non-empty ExecuteConfig.SessionID resolves the
//     session's remote sandbox (creating it lazily) and runs the script on
//     the resolved handle. All resolution goes through the lifecycle
//     coordinator so create/recover/replace/delete are serialised by the
//     distributed lifecycle lock.
//   - An Execute call with an empty SessionID falls through to a stateless
//     RemoteSandbox, which allocates a fresh sandbox, runs the script, and
//     tears the sandbox down after Execute returns.
//   - Cube and E2B reap idle sandboxes themselves. Docker has no provider TTL,
//     so that backend runs its own idle sweep against activity-marker mtimes.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/sandbox/admission"
	"github.com/Usir233/GENYEE/internal/sandbox/allocation"
	"github.com/Usir233/GENYEE/internal/sandbox/workspace"
	"github.com/Usir233/GENYEE/internal/types"
)

// SessionInputRoot is reserved for durable user attachments restored from
// file storage. Generated artifacts must remain under SessionOutputRoot.
const SessionInputRoot = workspace.InputDir

// SessionOutputRoot is where skill scripts write artifacts for collection.
// The skills manager injects this path via skillOutputEnvVar; Execute
// materialises the directory via envd before the script runs so scripts
// do not depend on the template user being able to mkdir under /workspace.
const SessionOutputRoot = workspace.OutputDir

// skillOutputEnvVar matches the skills manager's GENYEE_SKILL_OUTPUT_DIR.
const skillOutputEnvVar = "GENYEE_SKILL_OUTPUT_DIR"

// sessionInputEnvVar matches the skills manager's GENYEE_SESSION_INPUT_DIR.
// Both names are injected into the sandbox environment itself, not only into
// the skill-script Execute call, so an agent exploring with shell_exec reads
// the same paths the skills framework uses.
const sessionInputEnvVar = "GENYEE_SESSION_INPUT_DIR"

// Injected into every exec so the agent-browser wrapper can isolate one
// session from the others on a shared machine. Set last: neither config nor
// model-supplied env may redirect them.
const (
	sessionIDEnvVar   = "GENYEE_SESSION_ID"
	browserRootEnvVar = "GENYEE_BROWSER_ROOT"
)

// SessionWorkspaceRoot is the writable workspace root inside remote sandboxes.
// shell_exec work_dir must stay underneath this path.
const SessionWorkspaceRoot = workspace.Root

// SessionGitDir is the git metadata directory for per-turn workspace
// checkpoints. It lives on the sandbox root filesystem so a fork snapshot
// still copies the object store, but outside SessionWorkspaceRoot so
// `rm -rf /workspace` (or an agent cleaning the work tree) cannot drop
// checkpoint history that rewind and fork later reset to.
//
// Checkpoints used to live in SessionWorkspaceRoot/.git, and sandboxes
// provisioned before this constant existed still hold theirs there. The
// shared git preamble adopts that repository on first use so SHAs recorded
// before the move keep resolving; see gitWorkspaceAdoptLegacyRepo.
const SessionGitDir = workspace.GitDir

// sessionArtifactDirBootstrapTimeout bounds directory creation and access
// checks, performed with the execution identity.
const sessionArtifactDirBootstrapTimeout = 15 * time.Second

// sessionLifecycleCleanupTimeout bounds the lifecycle coordinator's own
// bookkeeping deletions (loser cleanup, orphan cleanup after session
// disappearance).
const sessionLifecycleCleanupTimeout = 30 * time.Second

// ErrBoundSandboxChanged confirms that a maintenance command's original
// sandbox is gone. Transport failures must not be treated as this condition.
var ErrBoundSandboxChanged = errors.New("sandbox: bound sandbox changed before maintenance command")

// SessionBoundManager is a sandbox.Manager that binds one remote sandbox per
// tenant session. Concrete provider work is delegated to RemoteSandboxClient;
// this type owns validation and the mapping between application concepts
// (ExecuteConfig, session-scoped shell/file APIs) and the provider-neutral
// RemoteSandboxClient contract.
type SessionBoundManager struct {
	config    *Config
	validator *ScriptValidator

	client    RemoteSandboxClient
	bindings  SessionSandboxBindingStore
	checker   SessionExistenceChecker
	lifecycle *remoteSessionLifecycle
	ephemeral *RemoteSandbox

	// activeType is the effective sandbox type callers observe.
	activeType SandboxType

	configID    string
	allocations AllocationDirectory
	admission   admission.Registry
	namespace   string

	// addresses caches user-mode assignments for this manager's lifetime
	// (one request). Only the address is trusted from it; validity is read
	// fresh by admitUser.
	addresses sync.Map

	// admissionWait bounds waiting for a closed scope (rebuild, exclusive).
	admissionWait time.Duration

	// rewindWait bounds the exclusive window rewind takes on a shared machine.
	// It is shorter than admissionWait: an open terminal should fail rewind
	// quickly instead of holding the HTTP request for a rebuild-length wait.
	rewindWait time.Duration

	// mu guards Cleanup's idempotency flag.
	mu     sync.RWMutex
	closed bool
}

// AllocationDirectory resolves a session's recorded placement. Implemented by
// internal/application/service/sandboxalloc; nil keeps every session on the
// historical per-session path.
type AllocationDirectory interface {
	// SessionAllocation is the session's address. The manager caches it for
	// one request; it is never trusted for validity.
	SessionAllocation(ctx context.Context, sessionID string) (allocation.Assignment, bool, error)
	// VerifySessionAllocation reads the database on every call and returns
	// nil only if expected is still the session's live assignment and the
	// session exists, is not retired, and its current owner is expected's
	// owner and still eligible to share. It returns ErrSandboxSessionDeleted
	// or ErrUserAllocationRejected for those cases, other errors for
	// failures to verify.
	VerifySessionAllocation(
		ctx context.Context, sessionTenantID uint64, sessionID string, expected allocation.Assignment,
	) error
	UserMachinesByConfig(ctx context.Context, configTenantID uint64, configID string) ([]allocation.UserMachine, error)
}

// SessionBoundManagerConfig bundles the wired dependencies. Test helpers and
// the production container use it so callers only have to name the moving
// parts they actually override.
type SessionBoundManagerConfig struct {
	Config  *Config
	Client  RemoteSandboxClient
	Store   SessionSandboxBindingStore
	Checker SessionExistenceChecker

	// ConfigID identifies the tenant sandbox config this manager serves. It is
	// stamped onto sandbox metadata so cleanup can target one config without
	// touching another that shares the same provider account.
	ConfigID string

	// ConfigTenantID is the workspace that owns ConfigID. The resolver fills
	// it so provider creates register on that config's admission scope.
	ConfigTenantID uint64

	// SkipHealthProbe skips the construction-time Health() round-trip.
	// Set by the per-tenant resolver, which builds a manager per request.
	// See NewSessionBoundManager.
	SkipHealthProbe bool

	// Bootstrapper customises the first sandbox create of individual sessions
	// (session fork). Optional: nil is the ordinary path.
	Bootstrapper SessionBootstrapper

	// Allocations, Admission and Namespace enable shared user machines. All
	// three are optional; a user-mode assignment is refused without them.
	Allocations AllocationDirectory
	Admission   admission.Registry
	Namespace   string
}

// NewSessionBoundManager wires the manager with an explicit RemoteSandboxClient
// backend, binding store, and session existence checker. Every persistent
// operation flows through these three dependencies; the manager never keeps
// authoritative session→sandbox state locally.
//
// Provider identity comes from deps.Client.Provider() — not Config.Type —
// so test harnesses and custom wiring that inject a different client backend
// always project the correct template, TTL, and health timeout.
func NewSessionBoundManager(deps SessionBoundManagerConfig) (*SessionBoundManager, error) {
	cfg := deps.Config
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid sandbox config: %w", err)
	}
	if deps.Client == nil {
		return nil, errors.New("session bound manager requires a RemoteSandboxClient")
	}
	if deps.Store == nil {
		return nil, errors.New("session bound manager requires a SessionSandboxBindingStore")
	}
	if deps.Checker == nil {
		return nil, errors.New("session bound manager requires a SessionExistenceChecker")
	}

	provider := deps.Client.Provider()
	if !isRemoteProvider(provider) {
		return nil, fmt.Errorf("sandbox: unsupported remote provider %q", provider)
	}

	// Apply the provider's tuning defaults so downstream code reads only
	// non-zero TTL / timeout fields. Endpoint defaults are deliberately not
	// applied here: this constructor also serves named configs, which must be
	// told what they are missing rather than handed a built-in localhost value.
	switch provider {
	case SandboxTypeCube:
		applyCubeRuntimeDefaults(cfg)
	case SandboxTypeE2B:
		applyE2BRuntimeDefaults(cfg)
	case SandboxTypeDocker:
		applyDockerRuntimeDefaults(cfg)
	}

	// Build the provider-specific neutral create request using the
	// provider's own template and TTL fields.
	createRequest, err := buildSessionCreateRequest(provider, cfg)
	if err != nil {
		return nil, fmt.Errorf("session bound manager: %w", err)
	}
	// An empty template for the selected provider means the deployment is
	// misconfigured. Fail early so operators get a clear message instead of
	// a remote API error at the first sandbox allocation.
	if strings.TrimSpace(createRequest.TemplateID) == "" {
		return nil, fmt.Errorf(
			"sandbox: %s template ID is required but not configured",
			provider,
		)
	}

	client := wrapLangfuseRemoteClient(deps.Client)

	bootstrapper := deps.Bootstrapper
	if withClient, ok := bootstrapper.(SessionBootstrapperWithClient); ok {
		bootstrapper = withClient.WithClient(client)
	}

	var lifecycleOpts []lifecycleOption
	if deps.Admission != nil {
		lifecycleOpts = append(lifecycleOpts, withUserMachines(deps.Namespace, deps.Admission))
		lifecycleOpts = append(lifecycleOpts, withConfigCreateGate(deps.Admission, deps.ConfigTenantID))
	}
	lifecycle, err := newRemoteSessionLifecycle(
		client,
		deps.Store,
		deps.Checker,
		createRequest,
		sessionLifecycleCleanupTimeout,
		deps.ConfigID,
		bootstrapper,
		lifecycleOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("session bound manager: %w", err)
	}

	m := &SessionBoundManager{
		config:        cfg,
		validator:     NewScriptValidator(),
		client:        client,
		bindings:      deps.Store,
		checker:       deps.Checker,
		lifecycle:     lifecycle,
		ephemeral:     NewRemoteSandbox(client, createRequest),
		activeType:    provider,
		configID:      deps.ConfigID,
		allocations:   deps.Allocations,
		admission:     deps.Admission,
		namespace:     deps.Namespace,
		admissionWait: 30 * time.Second,
		rewindWait:    rewindExclusiveWait,
	}

	// Per-tenant managers are rebuilt on every request, so probing here would
	// add a remote round-trip to each one. When a tenant explicitly configures
	// a backend, an unreachable provider must fail at first use rather than
	// substituting a different execution environment.
	if deps.SkipHealthProbe {
		return m, nil
	}

	// Health probe uses the provider's own HTTP timeout.
	probeCtx, cancel := context.WithTimeout(
		context.Background(),
		effectiveHTTPTimeout(provider, cfg),
	)
	defer cancel()
	if err := deps.Client.Health(probeCtx); err != nil {
		return nil, fmt.Errorf("remote sandbox provider unavailable: %w", err)
	}
	return m, nil
}

// GetType reports the current effective sandbox type.
func (m *SessionBoundManager) GetType() SandboxType {
	if m == nil {
		return SandboxTypeDisabled
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeType
}

// TerminalIdleDisconnect is how long an open PTY or desktop relay may sit
// idle before the WebSocket is closed. Missing or out-of-range workspace
// values are clamped onto the built-in default so a stored 0 still disconnects.
func (m *SessionBoundManager) TerminalIdleDisconnect() time.Duration {
	if m == nil || m.config == nil {
		return DefaultTerminalIdleDisconnect
	}
	return EffectiveTerminalIdleDisconnect(m.config.TerminalIdleDisconnect)
}

// GetSandbox exposes a diagnostic Sandbox for callers that need to inspect
// availability. Returns a stateless RemoteSandbox surface for the current
// provider.
func (m *SessionBoundManager) GetSandbox() Sandbox {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ephemeral
}

// Execute is the shared entry point used by the DefaultManager compatibility
// layer, the skills manager, and the ephemeral tool path. It applies script
// security validation, then dispatches to the session-bound path (non-empty
// SessionID) or the ephemeral path.
func (m *SessionBoundManager) Execute(ctx context.Context, cfg *ExecuteConfig) (*ExecuteResult, error) {
	if m == nil {
		return nil, ErrSandboxDisabled
	}
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrSandboxDisabled
	}
	m.mu.RUnlock()

	if cfg == nil {
		return nil, ErrInvalidScript
	}
	if !cfg.SkipValidation {
		if err := runScriptValidation(m.validator, cfg); err != nil {
			log.Printf("[sandbox] security validation failed: %v", err)
			return &ExecuteResult{
				ExitCode: -1,
				Error:    err.Error(),
				Stderr:   fmt.Sprintf("Security validation failed: %v", err),
			}, ErrSecurityViolation
		}
	}

	if strings.TrimSpace(cfg.SessionID) == "" {
		return m.ephemeral.Execute(ctx, cfg)
	}
	sc, err := m.scope(ctx, cfg.SessionID)
	if err != nil {
		return nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "exec")
	if err != nil {
		return nil, err
	}
	defer release()
	handle, err := m.resolveScope(ctx, sc)
	if err != nil {
		return nil, err
	}
	outputDir := executionOutputDir(cfg)
	if sc.user {
		outputDir = sc.layout.OutputDir
	}
	if err := m.prepareSessionDirs(ctx, handle, DefaultSandboxExecUser, sc.layout.InputDir, outputDir); err != nil {
		return nil, err
	}
	if !sc.user {
		sessCfg := *cfg
		sessCfg.Env = withBrowserEnv(cfg.Env, sc.key.SessionID, sc.layout)
		return m.ephemeral.ExecuteOnHandle(ctx, handle, &sessCfg)
	}
	userCfg := *cfg
	userCfg.Env = withBrowserEnv(withSessionEnv(cfg.Env, sc.layout), sc.key.SessionID, sc.layout)
	return m.ephemeral.ExecuteOnHandleIn(ctx, handle, &userCfg, sc.layout.Root, userScriptDir(sc.layout))
}

// ensureSessionWorkspaceDirs prepares the shared execution layout as the same
// account that runs commands. Preparation failures abort the operation; hiding
// them sends the agent into repeated writes through different tools.
func (m *SessionBoundManager) ensureSessionWorkspaceDirs(
	ctx context.Context, handle RemoteSandboxHandle, outputDir string,
) error {
	return m.prepareSessionDirs(ctx, handle, DefaultSandboxExecUser, SessionInputRoot, outputDir)
}

// prepareSessionDirs never renames or deletes existing data to "repair" access.
// A bad image or an inaccessible directory needs an explicit diagnosis, not an
// apparently empty replacement directory and missing attachments/artifacts.
func (m *SessionBoundManager) prepareSessionDirs(
	ctx context.Context, handle RemoteSandboxHandle, user string, dirs ...string,
) (prepErr error) {
	ctx, span := startSandboxSpan(ctx, "sandbox.ensure_workspace",
		map[string]interface{}{"directories": dirs, "user": user}, sandboxHandleMeta(handle))
	defer func() { span.Finish(nil, nil, prepErr) }()
	result, err := m.client.Exec(ctx, handle, RemoteExecRequest{
		Shell:   true,
		Command: workspaceBootstrapCommand(dirs...),
		User:    user,
		Timeout: sessionArtifactDirBootstrapTimeout,
	})
	if err != nil {
		return fmt.Errorf("sandbox: workspace preparation failed for user %s: %w; command was not started", user, err)
	}
	if result == nil || result.ExitCode != 0 || result.Killed {
		detail := "provider returned no result"
		if result != nil {
			detail = fmt.Sprintf(
				"exit=%d killed=%t stderr=%s",
				result.ExitCode, result.Killed, strings.TrimSpace(result.Stderr),
			)
		}
		return fmt.Errorf("sandbox: workspace preparation failed for user %s at %s: %s. "+
			"Command was not started; existing files were preserved. "+
			"Use an accessible directory under /workspace. If /workspace itself is inaccessible, "+
			"the sandbox image/template must provide /workspace owned by %s. "+
			"Switching tools or retrying the same operation will not change filesystem permissions",
			user, strings.Join(dirs, ", "), detail, DefaultSandboxExecUser)
	}
	return nil
}

func workspaceBootstrapCommand(dirs ...string) string {
	quoted := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		quoted = append(quoted, ShellQuote(dir))
	}
	return fmt.Sprintf(
		`set -e; for d in %s; do `+
			`if [ -L "$d" ]; then echo "workspace directory is a symlink: $d" >&2; exit 1; fi; `+
			`mkdir -p -- "$d"; `+
			`if [ ! -d "$d" ] || [ ! -w "$d" ] || [ ! -x "$d" ]; then `+
			`echo "workspace directory is not writable/searchable: $d" >&2; exit 1; fi; done`,
		strings.Join(quoted, " "),
	)
}

// withWorkspaceEnvDefaults stamps the workspace paths onto the sandbox's own
// environment. A tenant-configured value wins: an operator who points the
// artifact directory somewhere else must not have it overwritten here.
func withWorkspaceEnvDefaults(env map[string]string) map[string]string {
	if env == nil {
		env = make(map[string]string, 2)
	}
	if strings.TrimSpace(env[skillOutputEnvVar]) == "" {
		env[skillOutputEnvVar] = SessionOutputRoot
	}
	if strings.TrimSpace(env[sessionInputEnvVar]) == "" {
		env[sessionInputEnvVar] = SessionInputRoot
	}
	return env
}

// executionOutputDir resolves the artifact directory for this Execute call.
// It prefers GENYEE_SKILL_OUTPUT_DIR from cfg.Env when the path stays under
// SessionWorkspaceRoot; otherwise it falls back to SessionOutputRoot.
func executionOutputDir(cfg *ExecuteConfig) string {
	if cfg != nil && cfg.Env != nil {
		if dir := strings.TrimSpace(cfg.Env[skillOutputEnvVar]); dir != "" {
			if clean, ok := ValidatedSessionOutputDir(dir); ok {
				return clean
			}
		}
	}
	return SessionOutputRoot
}

// DestroySession removes the remote sandbox bound to sessionID (if any) and
// the authoritative binding. Idempotent: succeeds on absent sessions.
func (m *SessionBoundManager) DestroySession(ctx context.Context, sessionID string) error {
	if m == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if m.remoteDisabled() {
		return nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	if sc.user {
		return ErrSharedSandbox
	}
	return m.lifecycle.Destroy(ctx, sc.key)
}

// InvalidateConfigSandboxes marks every session sandbox this config owns stale,
// so each session rebuilds its sandbox from the config's current image on its
// next use, and reports how many bindings were marked.
//
// It is the image-maintenance counterpart to DestroySession: nothing is torn
// down here, so marking cannot delete a sandbox that is executing right now.
// The replacement happens at the session's next resolve, which may be the next
// operation of a turn already in flight; see resolveLocked for that limitation.
func (m *SessionBoundManager) InvalidateConfigSandboxes(
	ctx context.Context, tenantID uint64, configID string,
) (int, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return 0, err
	}
	marked, err := m.bindings.InvalidateByConfig(ctx, tenantID, configID)
	if m.allocations == nil {
		return marked, err
	}
	// tenantID is the config's owner, so this covers every borrower's machine.
	machines, listErr := m.allocations.UserMachinesByConfig(ctx, tenantID, NormalizeConfigID(configID))
	if listErr != nil {
		return marked, errors.Join(err, fmt.Errorf("list user machines: %w", listErr))
	}
	n, markErr := m.InvalidateConfigSandboxesForMachines(ctx, configID, machines)
	return marked + n, errors.Join(err, markErr)
}

// InvalidateConfigSandboxesForMachines marks the listed user machines stale.
func (m *SessionBoundManager) InvalidateConfigSandboxesForMachines(
	ctx context.Context, configID string, machines []allocation.UserMachine,
) (int, error) {
	scanner, ok := m.bindings.(tenantBindingScanner)
	if !ok {
		return 0, nil
	}
	keys := make([]SessionSandboxKey, 0, len(machines))
	for _, machine := range machines {
		keys = append(keys, m.UserMachineKey(machine))
	}
	return markBindingKeysStale(ctx, scanner, keys, configID)
}

// Cleanup marks the manager closed. Session sandboxes are not force-deleted
// here: their lifecycle is authoritative in the binding store and would
// leak to any other GENYEE replica if this replica reaped them on shutdown.
// Providers reclaim idle sandboxes via their own timeout/pause policies.
func (m *SessionBoundManager) Cleanup(_ context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()
	return nil
}

// sessionKey resolves the tenant-scoped binding key. Tenant ID comes from the
// request context; empty tenant is treated as a caller error to keep session
// bindings globally addressable in Redis.
//
// It reads the session-owner tenant rather than the ambient request tenant so
// that a shared agent — which runs under the agent owner's workspace so its
// models, KBs and named sandbox configs resolve there — still binds its sandbox
// under the session's own tenant. Session deletion tears the sandbox down from a
// request that knows only that tenant, so any other choice would strand the
// MicroVM. SandboxTenantIDFromContext falls back to the request tenant, which
// is already the session owner on every non-borrowed path.
func (m *SessionBoundManager) sessionKey(
	ctx context.Context,
	sessionID string,
) (SessionSandboxKey, error) {
	tenantID, ok := types.SandboxTenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return SessionSandboxKey{}, errors.New("sandbox: tenant ID missing from context")
	}
	key := SessionSandboxKey{TenantID: tenantID, SessionID: strings.TrimSpace(sessionID)}
	if err := key.Validate(); err != nil {
		return SessionSandboxKey{}, err
	}
	return key, nil
}

func (m *SessionBoundManager) remoteDisabled() bool {
	if m == nil {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closed
}

func (m *SessionBoundManager) requireRemoteBackend() error {
	if m == nil {
		return ErrSandboxDisabled
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrSandboxDisabled
	}
	return nil
}

// buildSessionCreateRequest projects Config into a provider-neutral remote
// create request. The metadata block is populated per-session by the
// lifecycle coordinator; env vars propagate as-is.
//
// The provider parameter (derived from RemoteSandboxClient.Provider()) is the
// authoritative source of identity — it selects the correct Config fields so
// Cube and E2B never read each other's templates or TTLs.
func buildSessionCreateRequest(provider RemoteProvider, cfg *Config) (RemoteCreateRequest, error) {
	envVars := withWorkspaceEnvDefaults(cloneMetadata(cfg.EnvVars))

	switch provider {
	case SandboxTypeCube:
		ttl := cfg.CubeSandboxTTL
		if ttl <= 0 {
			ttl = DefaultCubeSandboxTTL
		}
		return RemoteCreateRequest{
			TemplateID:   cfg.CubeTemplate,
			FromSnapshot: cfg.TemplateFromSnapshot,
			EnvVars:      envVars,
			Network:      cfg.Network,
			Timeout: RemoteTimeoutPolicy{
				Mode:       RemoteTimeoutExplicit,
				Value:      ttl,
				Action:     RemoteOnTimeoutPause,
				AutoResume: true,
			},
		}, nil

	case SandboxTypeE2B:
		ttl := cfg.E2BSandboxTTL
		if ttl <= 0 {
			ttl = DefaultE2BSandboxTTL
		}
		return RemoteCreateRequest{
			TemplateID:   cfg.E2BTemplate,
			FromSnapshot: cfg.TemplateFromSnapshot,
			EnvVars:      envVars,
			Network:      cfg.Network,
			Timeout: RemoteTimeoutPolicy{
				Mode:       RemoteTimeoutExplicit,
				Value:      ttl,
				Action:     RemoteOnTimeoutPause,
				AutoResume: true,
			},
		}, nil

	case SandboxTypeDocker:
		ttl := cfg.DockerIdleTTL
		if ttl <= 0 {
			ttl = DefaultDockerIdleTTL
		}
		// Docker can only honour the overall egress switch (see
		// DockerRemoteClient.networkMode); the allow / deny lists are
		// rejected at save time so they cannot arrive here.
		return RemoteCreateRequest{
			TemplateID: cfg.DockerImage,
			EnvVars:    envVars,
			Network:    cfg.Network,
			Timeout: RemoteTimeoutPolicy{
				Mode:  RemoteTimeoutExplicit,
				Value: ttl,
				// Docker's pause keeps the container's memory resident on the
				// host, so pausing an abandoned sandbox would reclaim nothing.
				// Idle containers are deleted; the lifecycle rebinds the
				// session exactly as it does for a provider-reaped sandbox.
				Action:     RemoteOnTimeoutKill,
				AutoResume: false,
			},
		}, nil

	default:
		return RemoteCreateRequest{}, fmt.Errorf(
			"sandbox: unsupported remote provider %q for session create request",
			provider,
		)
	}
}

// effectiveHTTPTimeout returns the HTTP timeout for health probes and API
// calls against the provider's control plane. The provider parameter is
// authoritative: Cube and E2B each read their own timeout field and fall back
// to their own package-level default.
func effectiveHTTPTimeout(provider RemoteProvider, cfg *Config) time.Duration {
	switch provider {
	case SandboxTypeCube:
		if cfg.CubeHTTPTimeout > 0 {
			return cfg.CubeHTTPTimeout
		}
		return DefaultCubeHTTPTimeout
	case SandboxTypeE2B:
		if cfg.E2BHTTPTimeout > 0 {
			return cfg.E2BHTTPTimeout
		}
		return DefaultE2BHTTPTimeout
	case SandboxTypeDocker:
		if cfg.DockerHTTPTimeout > 0 {
			return cfg.DockerHTTPTimeout
		}
		return DefaultDockerHTTPTimeout
	default:
		return DefaultCubeHTTPTimeout
	}
}

var (
	_ SessionCapabilityProvider        = (*SessionBoundManager)(nil)
	_ SessionShellExecutor             = (*SessionBoundManager)(nil)
	_ SessionFileStore                 = (*SessionBoundManager)(nil)
	_ SessionInstallCapabilityProvider = (*SessionBoundManager)(nil)
	_ SessionInstallShellExecutor      = (*SessionBoundManager)(nil)
)

// PermissiveSessionExistenceChecker accepts every session. It is safe in
// deployments where GENYEE's own DestroySession is the only session-delete
// path (single-process memory binding store); the Redis-authoritative
// deployment must inject a real checker consulting the session repository.
type PermissiveSessionExistenceChecker struct{}

// SessionExists always returns true.
func (PermissiveSessionExistenceChecker) SessionExists(
	context.Context, SessionSandboxKey,
) (bool, error) {
	return true, nil
}
