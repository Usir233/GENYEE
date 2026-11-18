package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/sandbox/workspace"
)

// EnsureSessionDir creates dir inside the session's live sandbox when one is
// bound. It is a no-op when the session has no live binding; the skill
// framework will materialise the directory during the next Execute call.
func (m *SessionBoundManager) EnsureSessionDir(ctx context.Context, sessionID, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil || !ok {
		return err
	}
	if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, dir)); err != nil {
		return fmt.Errorf("sandbox: ensure session dir %s: %w", dir, err)
	}
	return nil
}

// WriteSessionInputFile writes a durable attachment path into the session's
// remote sandbox, provisioning the sandbox on first call. It is refused when
// the manager has fallen back to Local (writing to the host would leak
// attachments outside the tenant's isolation boundary).
func (m *SessionBoundManager) WriteSessionInputFile(
	ctx context.Context, sessionID, filePath string, content []byte,
) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("sandbox: session ID required for input staging")
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	clean, err := cleanSessionInputPath(filePath)
	if sc.user {
		clean, err = workspace.CleanInputPathIn(sc.layout, filePath)
	}
	if err != nil {
		return err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	var handle RemoteSandboxHandle
	for attempt := 0; attempt < 2; attempt++ {
		handle, err = m.resolveScope(ctx, sc)
		if err != nil {
			return err
		}
		err = m.writeSessionInputOnHandle(ctx, handle, clean, content)
		if err == nil || attempt == 1 || !sessionBindingLost(err) {
			return err
		}
		// The idle sweep can delete the container after resolve and before
		// mkdir. The binding still points at that container, so drop it and
		// provision once more. A missing path inside a live container does
		// not take this branch.
		if dropErr := m.dropBoundSandbox(ctx, sc.machine(), handle.ID()); dropErr != nil {
			return err
		}
	}
	return err
}

func (m *SessionBoundManager) writeSessionInputOnHandle(
	ctx context.Context, handle RemoteSandboxHandle, clean string, content []byte,
) error {
	if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, path.Dir(clean))); err != nil {
		return fmt.Errorf("sandbox: create input directory: %w", err)
	}
	if err := m.client.WriteFile(ctx, handle, clean, content); err != nil {
		return fmt.Errorf("sandbox: write session input %s: %w", clean, err)
	}
	return nil
}

// sessionBindingLost reports a sandbox that is gone. A missing path inside a
// live container is also NotFound ("does not exist"); rebinding on that
// would discard the session.
func sessionBindingLost(err error) bool {
	if !CanReplaceRemoteBinding(err) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "does not exist") && !strings.Contains(msg, "vanished") {
		return false
	}
	return true
}

func (m *SessionBoundManager) dropBoundSandbox(ctx context.Context, key SessionSandboxKey, sandboxID string) error {
	binding, err := m.bindings.Get(ctx, key)
	if err != nil || binding == nil || binding.SandboxID != sandboxID || binding.Provider != m.client.Provider() {
		return err
	}
	_, err = m.bindings.DeleteIfMatch(ctx, key, binding.Provider, binding.SandboxID)
	return err
}

// WriteSessionWorkspaceFile writes a model-authored file into the session's
// remote sandbox, provisioning the sandbox on first call. Paths must sit
// inside the session sandbox and must not land in /workspace/input.
func (m *SessionBoundManager) WriteSessionWorkspaceFile(
	ctx context.Context, sessionID, filePath string, content []byte,
) error {
	return m.WriteSessionWorkspaceFiles(ctx, sessionID, []SessionWorkspaceFile{{
		Path:    filePath,
		Content: content,
	}})
}

// WriteSessionWorkspaceFiles prepares the session workspace once, then writes
// every file. Staging a host skill tree must not re-run directory bootstrap
// or walk the parent path for each entry.
func (m *SessionBoundManager) WriteSessionWorkspaceFiles(
	ctx context.Context, sessionID string, files []SessionWorkspaceFile,
) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("sandbox: session ID required for workspace write")
	}
	if len(files) == 0 {
		return nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	type item struct {
		path    string
		content []byte
	}
	items := make([]item, 0, len(files))
	parents := make(map[string]struct{}, len(files))
	for _, file := range files {
		// Session mode keeps the historical cleaner, which is pinned to
		// /workspace and ignores a host GENYEE_SKILL_OUTPUT_DIR overlay.
		clean, err := cleanSessionWorkspaceWritePath(file.Path)
		if sc.user {
			clean, err = workspace.CleanWriteFileIn(sc.layout, file.Path)
		}
		if err != nil {
			return err
		}
		items = append(items, item{path: clean, content: file.Content})
		parents[path.Dir(clean)] = struct{}{}
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	handle, err := m.resolveScope(ctx, sc)
	if err != nil {
		return err
	}
	if sc.user {
		if err := m.prepareSessionDirs(
			ctx, handle, DefaultSandboxExecUser, sc.layout.InputDir, sc.layout.OutputDir,
		); err != nil {
			return err
		}
	} else if err := m.ensureSessionWorkspaceDirs(ctx, handle, SessionOutputRoot); err != nil {
		return err
	}
	for parent := range parents {
		if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, parent)); err != nil {
			return fmt.Errorf("sandbox: create workspace directory: %w", err)
		}
	}
	for _, file := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.client.WriteFile(ctx, handle, file.path, file.content); err != nil {
			return fmt.Errorf("sandbox: write session file %s: %w", file.path, err)
		}
	}
	return nil
}

// RemoveSessionInputPath deletes a staged attachment. It is a no-op when the
// session has no live sandbox and never provisions one.
func (m *SessionBoundManager) RemoveSessionInputPath(
	ctx context.Context, sessionID, targetPath string,
) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		if _, err := cleanSessionInputPath(targetPath); err != nil {
			return err
		}
		return nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	clean, err := cleanSessionInputPath(targetPath)
	if sc.user {
		clean, err = workspace.CleanInputPathIn(sc.layout, targetPath)
	}
	if err != nil {
		return err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil || !ok {
		return err
	}
	if err := m.client.Remove(ctx, handle, clean); err != nil {
		return fmt.Errorf("sandbox: remove session input %s: %w", clean, err)
	}
	return nil
}

// ListSessionFiles walks dir under the session's live sandbox recursively.
// Returns nil (no error) when the session has no bound sandbox so callers can
// treat "no sandbox" and "empty output" uniformly.
func (m *SessionBoundManager) ListSessionFiles(
	ctx context.Context, sessionID, dir string,
) ([]RemoteDirEntry, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("sandbox: dir required for ListSessionFiles")
	}
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return nil, err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil || !ok {
		return nil, err
	}
	return m.walkSessionFiles(ctx, handle, dir)
}

// StatSessionFile returns metadata for a single file without downloading
// contents. Returns an error when no sandbox is bound: callers of this
// method already hold a path from a prior ListSessionFiles call and should
// not race with reaper/destroy.
func (m *SessionBoundManager) StatSessionFile(
	ctx context.Context, sessionID, filePath string,
) (*RemoteStatEntry, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, errors.New("sandbox: path required for StatSessionFile")
	}
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return nil, err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	return m.client.Stat(ctx, handle, filePath)
}

// ReadSessionFile downloads a file from the session's live sandbox. Errors
// when no sandbox is bound for the same reason as StatSessionFile.
func (m *SessionBoundManager) ReadSessionFile(
	ctx context.Context, sessionID, filePath string,
) ([]byte, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, errors.New("sandbox: path required for ReadSessionFile")
	}
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return nil, err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	return m.client.ReadFile(ctx, handle, filePath)
}

// WriteSessionFile writes an install/maintenance file into the session's live
// sandbox. It is deliberately narrower than a general remote write: only the
// tenant skills image root is accepted, because ordinary attachments must keep
// using WriteSessionInputFile and its /workspace/input guard.
func (m *SessionBoundManager) WriteSessionFile(
	ctx context.Context, sessionID, filePath string, content []byte,
) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("sandbox: session ID required for file staging")
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	clean := path.Clean(strings.TrimSpace(filePath))
	if clean != SkillsImageRoot && !strings.HasPrefix(clean, SkillsImageRoot+"/") {
		return fmt.Errorf("sandbox: install file path %q is outside %s", filePath, SkillsImageRoot)
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	ctx = withMaintenanceFilesystem(ctx)
	handle, err := m.resolveScope(ctx, sc)
	if err != nil {
		return err
	}
	// resetSkillDir already created this folder with mkdir -p. Cube's MakeDir
	// then reports the existing directory as an error; ignoreExistingDir keeps
	// that from aborting the seed of SKILL.md.
	if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, path.Dir(clean))); err != nil {
		return fmt.Errorf("sandbox: create install directory: %w", err)
	}
	if err := m.client.WriteFile(ctx, handle, clean, content); err != nil {
		return fmt.Errorf("sandbox: write install file %s: %w", clean, err)
	}
	return nil
}

// ShellExecOptions carries per-call shell execution knobs. The install-only
// flags select the installer working-directory allowlist and bootstrap. Both
// ordinary and install calls currently execute as root.
type ShellExecOptions struct {
	// ExpectedSandboxID makes maintenance lookup-only: never allocate/rebuild,
	// and fail if the binding changed since the caller selected this sandbox.
	ExpectedSandboxID string
	OnOutput          func(stream string, chunk []byte)

	WorkDir string
	Timeout time.Duration
	Env     map[string]string

	// AllowSkillsRoot selects the installer workspace/skills working-directory scope.
	// See cleanSessionWorkDir for why the work_dir allowlist is lexical only.
	// Never set this from a model-authored tool such as shell_exec.
	AllowSkillsRoot bool
	// AsRoot forces root and selects the maintenance bootstrap: only WorkDir
	// is prepared, without requiring /workspace/input or /workspace/output.
	// The default account is already root, but the bootstrap still differs.
	// AllowSkillsRoot separately selects the installer working-directory scope;
	// it is not a filesystem boundary for commands running as root.
	AsRoot bool
	// SkipWorkspacePrep omits prepareSessionDirs. Desktop maintenance and
	// workspace checkpoints operate on an existing layout. Never set this from a model-authored tool
	// such as shell_exec — agents still need the workspace contract.
	SkipWorkspacePrep bool
}

// ExecShellCommand runs a shell one-liner inside the session's persistent
// sandbox. work_dir may name any absolute directory inside that sandbox.
func (m *SessionBoundManager) ExecShellCommand(
	ctx context.Context,
	sessionID string,
	command string,
	workDir string,
	timeout time.Duration,
	env map[string]string,
) (*ExecuteResult, error) {
	return m.ExecShellCommandWithOptions(ctx, sessionID, command, ShellExecOptions{
		WorkDir: workDir,
		Timeout: timeout,
		Env:     env,
	})
}

// ExecShellCommandWithOptions runs a shell command with install-only options.
// Fallback is explicitly refused so even privileged installer calls never
// escape onto the GENYEE host machine.
func (m *SessionBoundManager) ExecShellCommandWithOptions(
	ctx context.Context,
	sessionID string,
	command string,
	opts ShellExecOptions,
) (*ExecuteResult, error) {
	result, _, err := m.execShellCommandWithOutputSnapshot(ctx, sessionID, command, opts, "")
	return result, err
}

// ShellOutputSnapshot contains two complete metadata listings of the same sandbox.
// A nil snapshot means inspection failed; an empty snapshot means no output files.
type ShellOutputSnapshot struct {
	Before []RemoteDirEntry
	After  []RemoteDirEntry
}

// ExecShellCommandWithOutputSnapshot resolves the session once for the command
// and both artifact probes. Handles never escape this call or survive a rebuild.
func (m *SessionBoundManager) ExecShellCommandWithOutputSnapshot(
	ctx context.Context, sessionID, command string, opts ShellExecOptions, outputDir string,
) (*ExecuteResult, *ShellOutputSnapshot, error) {
	return m.execShellCommandWithOutputSnapshot(ctx, sessionID, command, opts, outputDir)
}

func (m *SessionBoundManager) execShellCommandWithOutputSnapshot(
	ctx context.Context, sessionID, command string, opts ShellExecOptions, outputDir string,
) (*ExecuteResult, *ShellOutputSnapshot, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, nil, fmt.Errorf(
			"sandbox: shell_exec requires the remote sandbox provider (current mode: %s)",
			m.GetType(),
		)
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil, errors.New("sandbox: session_id required for ExecShellCommand")
	}
	if strings.TrimSpace(command) == "" {
		return nil, nil, errors.New("sandbox: command required for ExecShellCommand")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = m.config.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if outputDir != "" {
		clean, ok := ValidatedOutputDirIn(sc.layout, outputDir)
		if !sc.user {
			clean, ok = ValidatedSessionOutputDir(outputDir)
		}
		if !ok {
			return nil, nil, errors.New("sandbox: invalid output directory")
		}
		outputDir = clean
	}
	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		workDir = sc.layout.Root
	}
	workDir, err = workspace.CleanWorkDirIn(sc.layout, workDir, opts.AllowSkillsRoot)
	if !sc.user {
		fallback := SessionWorkspaceRoot
		if strings.TrimSpace(opts.WorkDir) != "" {
			fallback = strings.TrimSpace(opts.WorkDir)
		}
		workDir, err = cleanSessionWorkDir(fallback, opts.AllowSkillsRoot)
	}
	if err != nil {
		return nil, nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "shell")
	if err != nil {
		return nil, nil, err
	}
	defer release()

	var handle RemoteSandboxHandle
	if opts.ExpectedSandboxID != "" {
		if !sc.user {
			exists, checkErr := m.checker.SessionExists(ctx, sc.sessionKeyOnly())
			if checkErr != nil {
				return nil, nil, checkErr
			}
			if !exists {
				return nil, nil, ErrSandboxSessionDeleted
			}
		}
		var found bool
		// Maintenance must recheck the binding even inside a file-operation scope.
		handle, found, err = m.lookupSessionHandleForKey(ctx, sc.machine(), opts.ExpectedSandboxID)
		if err != nil {
			return nil, nil, err
		}
		if !found || handle.ID() != opts.ExpectedSandboxID {
			return nil, nil, ErrBoundSandboxChanged
		}
	} else {
		handle, err = m.resolveScope(ctx, sc)
		if err != nil {
			return nil, nil, err
		}
	}
	var snapshot *ShellOutputSnapshot
	if outputDir != "" {
		if before, err := m.snapshotOutputFiles(ctx, handle, outputDir); err == nil {
			snapshot = &ShellOutputSnapshot{Before: before}
		}
	}
	user := DefaultSandboxExecUser
	if opts.AsRoot {
		user = "root"
	}
	if !opts.SkipWorkspacePrep {
		if opts.AsRoot {
			// Installation owns the skill directory and does not depend on a
			// writable session workspace (which is cleaned before snapshotting).
			if err := m.prepareSessionDirs(ctx, handle, user, workDir); err != nil {
				return nil, nil, err
			}
		} else if sc.user {
			if err := m.prepareSessionDirs(
				ctx, handle, user, sc.layout.InputDir, sc.layout.OutputDir, workDir,
			); err != nil {
				return nil, nil, err
			}
		} else if err := m.prepareSessionDirs(
			ctx, handle, user, SessionInputRoot, SessionOutputRoot, workDir,
		); err != nil {
			return nil, nil, err
		}
	}
	env := opts.Env
	if sc.user {
		env = withSessionEnv(opts.Env, sc.layout)
	}
	env = withBrowserEnv(env, sc.key.SessionID, sc.layout)

	start := time.Now()
	execResult, execErr := m.client.Exec(ctx, handle, RemoteExecRequest{
		Command:  command,
		OnOutput: commandOutputCallback(ctx, opts.OnOutput),
		Shell:    true,
		Env:      env,
		WorkDir:  workDir,
		User:     user,
		Timeout:  timeout,
	})
	duration := time.Since(start)
	if snapshot != nil {
		if after, err := m.snapshotOutputFiles(ctx, handle, outputDir); err == nil {
			snapshot.After = after
		} else {
			snapshot = nil
		}
	}
	return remoteExecuteResult(execResult, execErr, duration), snapshot, nil
}

// SessionShellExecutor advertises the shell-execution capability while the
// manager is open.
func (m *SessionBoundManager) SessionShellExecutor() SessionShellExecutor {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	return m
}

// SessionInstallShellExecutor advertises the privileged install-mode shell.
func (m *SessionBoundManager) SessionInstallShellExecutor() SessionInstallShellExecutor {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	return m
}

// SessionWorkspaceLayout reports where sessionID's files live. A session-owned
// machine keeps the /workspace contract (a validated GENYEE_SKILL_OUTPUT_DIR
// overlays OutputDir). A shared machine reports that session's directory.
// Lookups do not admit and do not require a tenant: tool descriptions call
// this before any provider operation.
func (m *SessionBoundManager) SessionWorkspaceLayout(ctx context.Context, sessionID string) (WorkspaceLayout, error) {
	a, ok, err := m.addressFor(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return WorkspaceLayout{}, err
	}
	if !ok || !a.IsUser() {
		return withValidatedSkillOutputDir(RemoteWorkspaceLayout()), nil
	}
	return a.Layout()
}

func withValidatedSkillOutputDir(layout WorkspaceLayout) WorkspaceLayout {
	raw := strings.TrimSpace(os.Getenv(skillOutputEnvVar))
	if raw == "" {
		return layout
	}
	clean, ok := ValidatedSessionOutputDir(raw)
	if !ok {
		return layout
	}
	previous := layout.OutputDir
	layout.OutputDir = clean
	if previous == clean || len(layout.ReadRoots) == 0 {
		return layout
	}
	roots := append([]string(nil), layout.ReadRoots...)
	for i, root := range roots {
		if root == previous {
			roots[i] = clean
		}
	}
	layout.ReadRoots = roots
	return layout
}

var _ SessionWorkspaceLayoutProvider = (*SessionBoundManager)(nil)

// SessionFileStore advertises the session-scoped filesystem capability while
// a real remote backend is active and the provider implements the enumeration
// operations (ListDir / Stat / MakeDir / Remove).
func (m *SessionBoundManager) SessionFileStore() SessionFileStore {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	if !m.client.Capabilities().SupportsFilesystemEnumeration {
		return nil
	}
	return m
}

// snapshotOutputFiles does not probe with Stat: a missing output directory is
// an empty baseline, while permission and transport errors remain failures.
func (m *SessionBoundManager) snapshotOutputFiles(
	ctx context.Context, handle RemoteSandboxHandle, dir string,
) ([]RemoteDirEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return m.walkSessionFiles(ctx, handle, dir)
}

func (m *SessionBoundManager) walkSessionFiles(
	ctx context.Context, handle RemoteSandboxHandle, dir string,
) ([]RemoteDirEntry, error) {
	stack := []string{dir}
	var files []RemoteDirEntry
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := m.client.ListDir(ctx, handle, cur)
		if err != nil {
			// Only a missing root is an empty listing. A vanished subdirectory
			// makes this snapshot incomplete and must not publish partial links.
			if cur == dir && IsRemoteNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.Path == "" {
				entry.Path = path.Join(cur, entry.Name)
			}
			if entry.Type == RemoteEntryDir {
				stack = append(stack, entry.Path)
				continue
			}
			if entry.Type == RemoteEntryFile {
				files = append(files, entry)
			}
		}
	}
	return files, nil
}

// withSessionEnv overlays the session's input/output paths. On a shared
// machine they are reserved: no config or caller value may redirect them.
func withSessionEnv(env map[string]string, layout WorkspaceLayout) map[string]string {
	out := make(map[string]string, len(env)+2)
	for k, v := range env {
		out[k] = v
	}
	out[skillOutputEnvVar] = layout.OutputDir
	out[sessionInputEnvVar] = layout.InputDir
	return out
}

// withBrowserEnv overlays the session identity and browser state root. Unlike
// withSessionEnv it applies in both modes: a session-mode machine has one
// session, but the wrapper still requires the variables (it fails closed).
func withBrowserEnv(env map[string]string, sessionID string, layout WorkspaceLayout) map[string]string {
	if layout.BrowserRoot == "" || sessionID == "" {
		return env
	}
	out := make(map[string]string, len(env)+2)
	for k, v := range env {
		out[k] = v
	}
	out[sessionIDEnvVar] = sessionID
	out[browserRootEnvVar] = layout.BrowserRoot
	return out
}

// userScriptDir is where an uploaded skill script lands on a shared machine.
func userScriptDir(layout WorkspaceLayout) string {
	return path.Join(layout.Root, ".genyee", "scripts")
}

func cleanSessionInputPath(filePath string) (string, error) {
	return workspace.CleanInputPathIn(RemoteWorkspaceLayout(), filePath)
}

// cleanSessionWorkspaceWritePath normalizes model-authored sandbox writes and
// protects staged attachments. The remote session binding isolates the files.
func cleanSessionWorkspaceWritePath(filePath string) (string, error) {
	return workspace.CleanWriteFileIn(RemoteWorkspaceLayout(), filePath)
}

// cleanSessionWorkDir requires an absolute sandbox-local working directory.
// Ordinary calls may work anywhere; the install/maintenance option retains its
// explicit workspace/skills scope. Neither check changes filesystem privileges.
func cleanSessionWorkDir(workDir string, allowSkillsRoot bool) (string, error) {
	return workspace.CleanWorkDirIn(RemoteWorkspaceLayout(), workDir, allowSkillsRoot)
}
