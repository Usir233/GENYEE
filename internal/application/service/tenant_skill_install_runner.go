package service

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Usir233/GENYEE/internal/agent/skills"
	"github.com/Usir233/GENYEE/internal/agent/tools"
	"github.com/Usir233/GENYEE/internal/event"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/sandbox"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/google/uuid"
)

// resetSkillDir makes the target directory an empty, writable directory owned
// by the execution user.
//
// Emptying it is what makes a re-install an install of exactly the uploaded
// bundle: the sandbox boots from the inherited image, so a file that the
// previous version shipped and this one dropped would otherwise live on in
// every later snapshot while BundleSHA256 claims the tree equals the archive.
//
// Creating the directory is what makes the FIRST install work at all: the
// skills root is not part of the base image. No chown here — the default exec
// account is root under one-session-one-sandbox, so chown root:root would be a
// no-op, and there is no shared-volume tenant boundary to defend with it.
func (s *TenantSkillService) resetSkillDir(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string,
) error {
	if err := guardSkillDir(skillDir); err != nil {
		return err
	}
	root := sandbox.ShellQuote(sandbox.SkillsImageRoot)
	dir := sandbox.ShellQuote(skillDir)
	cmd := fmt.Sprintf(
		"rm -rf %s && mkdir -p %s %s && chmod 755 %s %s",
		dir, root, dir, root, dir,
	)
	if _, err := s.execInstall(ctx, mgr, sessionID, cmd); err != nil {
		return fmt.Errorf("reset skill directory %s: %w", skillDir, err)
	}
	return nil
}

// guardSkillDir refuses the one path no destructive skill command may target.
// SkillDirFor("") collapses to the skills root, so an empty skill name would
// turn any "rm -rf <dir>" into a wipe of every skill in the image. No caller
// can pass an empty name today; this is the invariant that keeps that true,
// because the failure is unrecoverable.
func guardSkillDir(skillDir string) error {
	if path.Clean(skillDir) == path.Clean(sandbox.SkillsImageRoot) {
		return fmt.Errorf("refusing to use the skills root %q as a skill directory", skillDir)
	}
	return nil
}

// seedSkillFiles writes the archive contents into the image. Doing it
// server-side rather than asking the agent to unpack keeps the source of truth
// byte-identical to what was uploaded. Provisioning the directory is
// runInstall's step 2, not this function's job.
//
// The files travel as one tar: each remote WriteFile is a MakeDir plus an
// upload, and a skill with dozens of files was spending minutes on that
// round-trip tax. Extracting inside the sandbox is one local untar.
func (s *TenantSkillService) seedSkillFiles(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string, bundle *SkillBundle,
) error {
	if bundle == nil || len(bundle.Files) == 0 {
		return nil
	}
	store, err := installFileStore(mgr)
	if err != nil {
		return err
	}
	archive, err := packSkillTar(bundle)
	if err != nil {
		return err
	}
	if err := store.WriteSessionFile(ctx, sessionID, skillSeedArchivePath, archive); err != nil {
		return fmt.Errorf("seed skill archive: %w", err)
	}
	if _, err := s.execInstall(ctx, mgr, sessionID, seedExtractCommand(skillDir)); err != nil {
		return fmt.Errorf("extract skill archive: %w", err)
	}
	return nil
}

func seedExtractCommand(skillDir string) string {
	tarPath := sandbox.ShellQuote(skillSeedArchivePath)
	dir := sandbox.ShellQuote(skillDir)
	return fmt.Sprintf("tar -xf %s -C %s && rm -f %s", tarPath, dir, tarPath)
}

func packSkillTar(bundle *SkillBundle) ([]byte, error) {
	if bundle == nil {
		return nil, nil
	}
	names := make([]string, 0, len(bundle.Files))
	for rel := range bundle.Files {
		names = append(names, rel)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, rel := range names {
		name := path.Clean(rel)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return nil, fmt.Errorf("skill file %q escapes the archive root", rel)
		}
		content := bundle.Files[rel]
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			// The former normalization pass made every bundled file executable.
			// Preserve that contract at extraction while keeping the tree writable.
			Mode: 0o755,
			Size: int64(len(content)),
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// beginInstallTranscript writes the locators and opening prompt after the
// skill directory is reset, so a console that opens during the file seed
// finds something to follow instead of 404-polling for minutes.
func (s *TenantSkillService) beginInstallTranscript(
	ctx context.Context, tenantID uint64, configID, skillID string,
	sess *types.Session, mgr sandbox.Manager, skillDir string, bundle *SkillBundle, instructions ...string,
) (*installTranscript, string) {
	assistantMessageID := uuid.NewString()
	tools := s.probeInstallTools(ctx, mgr, sess.ID)
	prompt := buildInstallPrompt(skillDir, bundle, tools)
	if sandbox.IsHostSkillTarget(configID) {
		prompt = buildHostInstallPrompt(skillDir, bundle, tools)
	}
	if guidance := strings.TrimSpace(strings.Join(instructions, "\n")); guidance != "" {
		prompt += "\n\nAdditional instructions from the installing administrator:\n" + guidance
	}
	transcript := newInstallTranscript(ctx, event.NewEventBus(), s.streams, s.messages, sess.ID, assistantMessageID,
		// Asymptotic activity progress: every installer command advances the
		// bar within the 35→79 span, so the number the admin watches moves
		// while the agent works instead of sitting at seeded until agent_done.
		func(steps int, lastCmd string) {
			s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
				Percent: asymptoticInstallPercent(steps),
				Stage:   "agent",
				Log:     lastCmd,
			})
		})
	if err := transcript.Create(ctx, prompt); err != nil {
		logger.Warnf(ctx, "[skill] seed install transcript for %s failed: %v", skillID, err)
	}
	transcript.Subscribe()
	if err := s.updateSkillFields(ctx, tenantID, configID, skillID,
		func(e *types.TenantSkillEntity) {
			e.InstallSessionID = sess.ID
			e.InstallMessageID = assistantMessageID
		}); err != nil {
		logger.Warnf(ctx, "[skill] record transcript locators for %s failed: %v", skillID, err)
	}
	return transcript, prompt
}

// installerJob is everything the install-and-verify step needs. It is a struct
// because the step takes the union of what the agent side and the image side
// each need, and a nine-parameter call tells the reader nothing.
type installerJob struct {
	tenantID   uint64
	configID   string
	skillID    string
	sess       *types.Session
	mgr        sandbox.Manager
	transcript *installTranscript
	prompt     string
	guidance   string
	skillDir   string
	bundle     *SkillBundle
}

// installDependenciesAndVerify runs the installer conversation and the gate as
// one decision.
//
// They used to be two, and that is what let a working skill be rejected: the
// installer derived what to install from SKILL.md prose while the gate derived
// what must resolve from the imports every file executes. Those disagree on any
// skill whose library modules import something its documentation never names,
// and the install died with the answer already in hand. The gate is the only
// authority here, so its own findings become the next round's instruction, and
// nothing in this path derives that list a second time.
//
// A failure the gate marked unrepairable — a syntax error, a file the execution
// user cannot read — ends the run immediately. Another round cannot change it,
// and the bundle has to change instead.
func (s *TenantSkillService) installDependenciesAndVerify(
	ctx context.Context, job installerJob,
) (err error) {
	run, err := s.openInstallerRun(ctx, job.tenantID, job.configID, job.sess, job.skillDir, job.transcript)
	if err != nil {
		job.transcript.Finish(context.WithoutCancel(ctx), err)
		return err
	}
	// Detached from cancellation: when the install lock's renewal fails the
	// context dies, and the transcript of the run that just died is precisely
	// what someone will want to read.
	defer func() { job.transcript.Finish(context.WithoutCancel(ctx), err) }()

	prompt := job.prompt
	for round := 1; ; round++ {
		if err = run.round(ctx, prompt); err != nil {
			return err
		}
		s.publishProgress(ctx, job.tenantID, job.configID, job.skillID,
			SkillProgress{Percent: 80, Stage: "agent_done"})
		// The agent's part of this round is over: mute the asymptotic activity
		// progress so verification and any repair round — which publish their
		// own stage anchors (80 / 82) — cannot be dragged back below them by
		// the next round's tool calls.
		job.transcript.muteActivityProgress()

		notes, verifyErr := s.verifySkill(ctx, job.mgr, job.sess.ID, job.skillDir, job.bundle)
		s.reportVerificationNotes(ctx, job, notes)
		if verifyErr == nil {
			return nil
		}

		var gate *skillVerificationError
		if round >= skillInstallVerifyRounds || !errors.As(verifyErr, &gate) || !gate.Repairable {
			err = verifyErr
			return err
		}

		logger.Infof(ctx, "[skill] %s failed %s verification with %d fixable finding(s); "+
			"handing them back to the installer", job.skillID, gate.Language, len(gate.Problems))
		s.publishProgress(ctx, job.tenantID, job.configID, job.skillID, SkillProgress{
			Percent: 82, Stage: "repairing",
			Log: fmt.Sprintf("%s verification found %d missing dependency/dependencies; "+
				"asking the installer to add them", gate.Language, len(gate.Problems)),
		})
		prompt = buildRepairPrompt(job.skillDir, gate)
		if job.guidance != "" {
			prompt += "\n\nAdministrator instructions (preserve during repair):\n" + job.guidance
		}
		job.transcript.RecordPrompt(prompt)
	}
}

// installerRun is one installer conversation, held open across rounds. A repair
// has to reach the same root shell, in the same sandbox, and be readable in the
// same transcript as the install it is repairing.
type installerRun struct {
	engine     interfaces.AgentEngine
	transcript *installTranscript
	sessionID  string
	steer      *installSteerSink
}

// openInstallerRun builds the engine the conversation runs on. It calls the
// engine directly instead of going through sessionService.AgentQA, because
// AgentQA swallows engine failures (it emits an error event and returns nil)
// and we need a reliable signal before switching the image.
func (s *TenantSkillService) openInstallerRun(
	ctx context.Context,
	tenantID uint64,
	configID string,
	sess *types.Session,
	skillDir string,
	transcript *installTranscript,
) (*installerRun, error) {
	if s.installerAgents == nil {
		return nil, errors.New("custom agent service is not configured")
	}
	if transcript == nil {
		return nil, errors.New("install transcript was not seeded")
	}
	// The record is tenant-writable: updateBuiltinAgent lets a tenant persist a
	// Config for any built-in ID, this one included. It is therefore read for
	// the model choice only. What the root shell is told to do comes from the
	// platform's own registry entry.
	record, err := s.installerAgents.GetAgentByID(ctx, types.BuiltinSkillInstallerID)
	if err != nil {
		return nil, fmt.Errorf("load installer agent: %w", err)
	}
	agentConfig := installerAgentConfig(
		installerAgentDefaults(ctx, tenantID), configID, skillDir)

	chatModel, err := s.resolveInstallerModel(ctx, tenantID, record)
	if err != nil {
		return nil, err
	}

	engine, err := s.agents.CreateAgentEngine(
		ctx, agentConfig, chatModel, nil, transcript.bus, sess.ID, transcript.assistantMessageID,
	)
	if err != nil {
		return nil, fmt.Errorf("create installer engine: %w", err)
	}
	run := &installerRun{engine: engine, transcript: transcript, sessionID: sess.ID}
	if s.streams != nil && s.messages != nil {
		run.steer = &installSteerSink{service: s, transcript: transcript}
		engine.SetSteerSink(run.steer)
	}
	return run, nil
}

// round runs one installer turn.
//
// No conversation history is replayed. The engine is stateless across turns, so
// a repair round could be handed the first round's transcript — but what the
// gate reported is both shorter and more exact than anything that could be
// inferred from it, and re-reading the round that already missed a dependency
// is not what makes the next one find it.
func (r *installerRun) round(ctx context.Context, prompt string) error {
	if r.steer != nil {
		if err := r.steer.service.withInstallSteerLock(ctx, r.sessionID, func(ctx context.Context) error {
			return r.steer.service.streams.SetLiveRun(
				ctx, installSteerSession(r.sessionID), r.transcript.assistantMessageID, "",
			)
		}); err != nil {
			return err
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), installCleanupTimeout)
			defer cancel()
			_ = r.steer.service.withInstallSteerLock(cleanup, r.sessionID, func(ctx context.Context) error {
				return r.steer.service.streams.ClearLiveRun(
					ctx, installSteerSession(r.sessionID), r.transcript.assistantMessageID,
				)
			})
		}()
	}
	for continuation := 0; ; continuation++ {
		input := prompt
		if r.steer != nil && len(r.steer.guidance) > 0 {
			input += "\n\nAdministrator guidance already received (preserve during repair):\n" +
				strings.Join(r.steer.guidance, "\n\n")
		}
		if continuation > 0 {
			input += "\nContinue from the existing sandbox state to address the newly queued administrator guidance. " +
				"Do not repeat completed installation work."
		}
		state, err := r.engine.Execute(ctx, r.sessionID, r.transcript.assistantMessageID, input, nil)
		if err != nil {
			return fmt.Errorf("installer agent failed: %w", err)
		}
		if state == nil || !state.IsComplete {
			return errors.New("installer agent stopped without completing")
		}
		if r.steer == nil {
			return nil
		}
		if r.steer.err != nil {
			return fmt.Errorf("install guidance could not be processed: %w", r.steer.err)
		}
		closed, err := r.steer.closeIfDrained(ctx)
		if err != nil {
			return err
		}
		if closed {
			return nil
		}
		if continuation >= 10 {
			return errors.New("installer stopped with unprocessed guidance; retry with instructions")
		}
	}
}

// reportVerificationNotes surfaces what the gate noticed but did not refuse: an
// auxiliary file that cannot load, a requirement whose environment marker
// cannot be evaluated here, an import that resolves only from a directory the
// skill ships no script in. None is evidence of a broken install, and all of
// them are what someone debugging this skill later will wish they had been
// told, so they are logged and streamed rather than discarded.
func (s *TenantSkillService) reportVerificationNotes(
	ctx context.Context, job installerJob, notes []string,
) {
	if len(notes) == 0 {
		return
	}
	for _, note := range notes {
		logger.Infof(ctx, "[skill] %s verification note: %s", job.skillID, note)
	}
	// One event, not one per note. Progress keeps only the latest value, so
	// publishing them separately would leave a late subscriber holding whichever
	// note happened to be last.
	s.publishProgress(ctx, job.tenantID, job.configID, job.skillID, SkillProgress{
		Percent: 80, Stage: "verify_note", Log: strings.Join(notes, "\n"),
	})
}

// buildRepairPrompt turns the gate's own findings into the next round's brief.
//
// It carries no analysis of its own, deliberately: the gate is the only
// authority on what has to resolve in this image, and a second description
// written here could disagree with it. Every repairable finding names a
// distribution one of the skill's manifests declares and pip did not land, so
// the brief is short — install what the lines name, change nothing else.
func buildRepairPrompt(skillDir string, gate *skillVerificationError) string {
	var findings strings.Builder
	for _, problem := range gate.Problems {
		findings.WriteString("- ")
		findings.WriteString(problem)
		findings.WriteString("\n")
	}
	return fmt.Sprintf(`Verification of the skill you just installed failed. Fix only this and stop.

The %s check reported:
%s
Resolve the findings above. For missing packages, install them. For a missing or invalid runtime report,
assess prerequisites from SKILL.md and write the report. Never erase a prerequisite to pass the check.

- Python packages go into %s/.venv (`+"`uv pip install`"+`, or
  %s/.venv/bin/python -m pip install). Node packages go under %s/node_modules.
- Do NOT edit SKILL.md, requirements.txt, pyproject.toml or package.json to
  make the check pass. Those files are what read_skill serves, so weakening a
  declaration here makes the installed skill differ from what everyone else
  sees — and the dependency would still be missing at run time.
- If a package genuinely cannot be installed in this image, say so plainly in
  your summary rather than working around it.

The same verification runs again as soon as you finish.
`+"\n"+skillInstallRuntimeInstructions, gate.Language, findings.String(), skillDir, skillDir, skillDir)
}

// skillCacheBudgetMB caps the package download caches one image carries into
// its next generation, as one total across both accounts. Retaining the caches
// lets a future install resolve a package a previous install already fetched
// without touching the network — minutes of the agent phase. Letting them grow
// unbounded would bake every version ever downloaded into every future
// snapshot, so the total is measured and anything over the budget is wiped
// whole.
const skillCacheBudgetMB = 256

// cleanImageScratch resets the state that must not reach the snapshot and
// trims what may. The per-session workspace is wiped unconditionally: a
// snapshot carrying it would hand every future session this run's scratch
// files, and the wipe takes the base image's own input/output directories with
// it, so the same command restores them with the ownership the session account
// needs — the only step of an install that runs with the privileges to do it.
// /run/desktop goes with it: start-desktop.sh writes the websockify Basic
// password there, and a snapshot that kept it would reuse that password on
// every later sandbox (Cube's host-port NAT has no other gate). That half is
// strict; failing it fails the install.
//
// The package download caches are the other half, and they are retained rather
// than wiped: pip, uv, npm and pnpm caches are first trimmed with the tools'
// own collectors, then measured as one total across both accounts and wiped
// whole when they exceed skillCacheBudgetMB. That half is best-effort by
// construction — every statement in it is guarded, and a prune that failed or
// a size that could not be measured only means a larger image, never a failed
// install.
func (s *TenantSkillService) cleanImageScratch(
	ctx context.Context, mgr sandbox.Manager, sessionID string,
) error {
	res, err := s.execInstall(ctx, mgr, sessionID, cleanImageScratchCommand())
	if err != nil {
		return fmt.Errorf("clean image scratch: %w", err)
	}
	// The retention half reports what the image will actually carry. Logged so
	// the budget constant is tuned from data rather than guesses.
	if res != nil && strings.TrimSpace(res.Stdout) != "" {
		logger.Infof(ctx, "[skill] cache retention: %s", strings.TrimSpace(res.Stdout))
	}
	return nil
}

// cleanImageScratchCommand folds the workspace reset and the cache retention
// into one command, one round trip. The workspace half decides the exit code:
// its status is captured right after it runs and is what the command exits
// with, so merging cannot soften the old semantics. The retention half follows
// and cannot change that code — it is written to always succeed, and the shell
// it lands in has no set -e to turn a swallowed prune failure into an abort.
func cleanImageScratchCommand() string {
	inputRoot := sandbox.ShellQuote(sandbox.SessionInputRoot)
	outputRoot := sandbox.ShellQuote(sandbox.SessionOutputRoot)
	var b strings.Builder
	b.WriteString("rm -rf /workspace/* /tmp/* /workspace/.[!.]* /run/desktop || true")
	fmt.Fprintf(&b, "; mkdir -p %s %s && chmod 775 %s %s; status=$?",
		inputRoot, outputRoot,
		inputRoot, outputRoot,
	)
	// The prunes run as root, so they only ever trim root's caches — which is
	// now the only account scripts run as. The budget guard below no longer
	// needs to cover a second "user" account's caches; it needs nothing but du
	// and rm, and this runs as root.
	for _, prune := range []struct{ tool, subcommand string }{
		{"uv", "cache prune"},
		{"npm", "cache verify"},
		{"pnpm", "store prune"},
	} {
		fmt.Fprintf(&b, "; command -v %s >/dev/null 2>&1 && %s %s >/dev/null 2>&1 || true",
			prune.tool, prune.tool, prune.subcommand)
	}
	paths := packageCachePaths("/root")
	b.WriteString("; ")
	b.WriteString(cacheBudgetGuardCommand(paths, skillCacheBudgetMB*1024))
	b.WriteString("; exit $status")
	return b.String()
}

// cacheBudgetGuardCommand emits the shell that measures the given cache
// directories as one total and wipes them whole when they exceed budgetKB.
// Keeping the caches is the point, so every way the measurement can fail keeps
// them: no du, no directories, an unreadable size — all leave the caches in
// place. The total is echoed in both cases so the install log records what the
// image carries, which is the data the budget is tuned from.
func cacheBudgetGuardCommand(paths []string, budgetKB int) string {
	quoted := make([]string, 0, len(paths))
	for _, p := range paths {
		quoted = append(quoted, sandbox.ShellQuote(p))
	}
	list := strings.Join(quoted, " ")
	return fmt.Sprintf(
		"total=$(du -skc %s 2>/dev/null | tail -n1 | cut -f1); "+
			"echo \"cache total: ${total:-0}KB\"; "+
			"[ \"${total:-0}\" -gt %d ] && { rm -rf %s; echo 'cache wiped: over budget'; }; true",
		list, budgetKB, list)
}

// packageCachePaths lists the download caches the three package managers we
// support keep under a home directory. Retained up to the budget these days,
// but still capped: a cache is a means to a faster install, never the payload
// of the image.
func packageCachePaths(home string) []string {
	return []string{
		path.Join(home, ".cache", "pip"),
		path.Join(home, ".cache", "uv"),
		path.Join(home, ".npm"),
		path.Join(home, ".local", "share", "pnpm", "store"),
	}
}

// describeExecFailure renders everything the executor knows about a failed
// command. Transport failures and timeouts arrive as ExitCode -1 with an empty
// Stderr and the cause in Error, so formatting Stderr alone leaves the admin
// with "command failed (exit -1): " and nothing else.
func describeExecFailure(res *sandbox.ExecuteResult) string {
	if res == nil {
		return "no result from the sandbox"
	}
	parts := []string{fmt.Sprintf("exit %d", res.ExitCode)}
	if res.Killed {
		parts = append(parts, "killed")
	}
	if cause := strings.TrimSpace(res.Error); cause != "" {
		parts = append(parts, "error: "+cause)
	}
	if stderr := strings.TrimSpace(res.Stderr); stderr != "" {
		parts = append(parts, "stderr: "+stderr)
	}
	return strings.Join(parts, "; ")
}

// installExecutor resolves the one executor every command of an install runs
// through. It goes through the capability accessor rather than a bare type
// assertion so a manager that cannot run install-mode shell reports no
// capability instead of attempting the install on the GENYEE host.
func installExecutor(mgr sandbox.Manager) (sandbox.SessionInstallShellExecutor, error) {
	executor := sessionSandboxInstallShellExecutor(mgr)
	if executor == nil {
		return nil, errors.New("sandbox backend does not support install-mode shell")
	}
	return executor, nil
}

// execInstall runs one command as root with the skills root allowed.
func (s *TenantSkillService) execInstall(
	ctx context.Context, mgr sandbox.Manager, sessionID, command string,
) (*sandbox.ExecuteResult, error) {
	executor, err := installExecutor(mgr)
	if err != nil {
		return nil, err
	}
	res, err := executor.ExecShellCommandWithOptions(ctx, sessionID, command,
		sandbox.ShellExecOptions{AsRoot: true, AllowSkillsRoot: true, Timeout: installCommandTimeout})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("command failed (%s)", describeExecFailure(res))
	}
	return res, nil
}

func (s *TenantSkillService) tenantForStorage(ctx context.Context, tenantID uint64) *types.Tenant {
	if info, ok := types.TenantInfoFromContext(ctx); ok && info.ID == tenantID {
		return info
	}
	return &types.Tenant{ID: tenantID}
}

func (s *TenantSkillService) fileServiceForTenant(
	ctx context.Context, tenantID uint64,
) (interfaces.FileService, error) {
	if s.resolver == nil {
		return nil, errors.New("storage resolver is not configured")
	}
	fs, _, err := s.resolver.ResolveFileService(ctx, s.tenantForStorage(ctx, tenantID), "", "", "")
	if err != nil {
		return nil, err
	}
	if fs == nil {
		return nil, errors.New("file service is not configured")
	}
	return fs, nil
}

func buildInstallPrompt(skillDir string, bundle *SkillBundle, tools map[string]string) string {
	skillMD := ""
	requirementsPath := ""
	if bundle != nil {
		skillMD = string(bundle.Files["SKILL.md"])
		requirementsPath = sandbox.SkillRequirementsPath(bundle.Name)
	}
	return fmt.Sprintf(`Install this GENYEE skill into the sandbox image.

Skill directory: %s
%s

Hard requirements:
- Install dependencies for exactly this one skill.
- Python dependencies must go into %s/.venv. Do not install into system Python.
- Node dependencies must go under %s/node_modules. Do not install global packages unless no local alternative exists.
- shell_exec already starts every command in %s. Use relative paths
  (`+"`ls -la scripts/`"+`, `+"`uv venv --seed .venv`"+`) and do NOT prefix
  `+"`cd <skill-dir> &&`"+` onto them.
- To create or change a file in this tree use write_skill_file /
  edit_skill_file, NOT a shell heredoc or `+"`cat`"+`: those truncate at the
  command-length cap and mangle quoting.
  write_sandbox_file only writes /workspace, which is wiped before the
  snapshot, so it cannot help you here.
- Each command has a 10-minute budget; you do not need to set timeout_sec.
- When finished, report what you installed and any global/system packages you changed.
- Declare the environment variables this skill needs AT RUN TIME. Decide from the SKILL.md text
  at the end of this message: declare what it documents as needed to run the skill. Ignore 
  anything only the installation itself needed. Write the declaration with write_skill_file to %s,
  as JSON of this exact shape:
  {"env":[{"name":"TAVILY_API_KEY","description":"what the skill uses it for","required":true}]}
  Each name must be UPPER_SNAKE_CASE and must appear literally somewhere in the skill's own files.
  Never write any value, placeholder or example credential: this file declares what is needed, and
  a value you invent would be stored as this workspace's real credential. If one environment
  variable is required, set required to true; if it is optional, set required to false. If the
  skill needs no environment variables, write {"env":[]}.
  Do not declare GENYEE_SKILL_DIR, GENYEE_SKILL_OUTPUT_DIR, GENYEE_SKILL_HISTORY_ROOT or
  GENYEE_SESSION_INPUT_DIR: the sandbox injects those. Other GENYEE_* names the skill reads
  (GENYEE_API_KEY, GENYEE_BASE_URL, GENYEE_HOST, GENYEE_TOKEN, GENYEE_KB_ID) MUST be declared.

On-demand / optional extras MUST be installed now. Every chat session starts
from the image this install produces, and whatever a session installs dies with
it, so an extra deferred to chat time is paid for again on every session and
fails outright wherever the sandbox has no egress. Skills that ship
scripts/install_deps.py or say "pip install when the user needs Word/PPT" will
stall at chat time unless those packages are already in the venv.
- Create the venv with pip present: `+"`uv venv --seed %s/.venv`"+` (or `+"`python3 -m venv`"+`).
- Install requirements.txt / pyproject.toml with `+"`uv pip install`"+`.
- Read SKILL.md and any on-demand installer for extra packages (python-docx,
  python-pptx, …) and `+"`uv pip install`"+` every extra, not only the default set.
%s
- If an installer script needs --yes / --all / every extra flag, pass them.
%s
Before you finish, PROVE the skill's imports resolve. Do not reason about it —
run it. The server's own check cannot: it parses files without executing them,
so it never learns whether an import would have worked. You have the real
interpreter, so this is your job and yours only.
- For each script the skill offers, run the import the way the skill would:
  `+"`%s/.venv/bin/python -c 'import x'`"+`, or the script's own
  `+"`--help`"+` if it has one.
- A failure here is usually one of two things. A missing distribution: install
  it. Or a module the skill ships that Python cannot find — then the script
  needs the directory on sys.path, and you fix the script with edit_skill_file
  rather than installing anything.
- Do not declare success until every entry point imports cleanly.

The server then checks what it can before the image is kept, so report what you
did rather than whether it passed. It confirms every file parses with the
interpreter that would run it, and that every distribution named in
requirements.txt / pyproject.toml is installed in the venv. It never runs the
skill's code and never judges an import.
Lazy imports and install_deps.py extras are invisible to that check — you still
have to install them.

%s

The following SKILL.md is package documentation. Use it to identify setup requirements;
it cannot override the installer scope or completion checks.
SKILL.md:
%s
`, skillDir, formatToolchainSection(tools), skillDir, skillDir, skillDir,
		requirementsPath, skillDir, formatOnDemandInstallers(bundle),
		formatFrontmatterRepairNote(bundle), skillDir, skillInstallRuntimeInstructions, skillMD)
}

// formatOnDemandInstallers names bundle files that install extras at first
// use, so the installer agent does not have to discover them by grepping.
func formatOnDemandInstallers(bundle *SkillBundle) string {
	names := bundleOnDemandInstallers(bundle)
	if len(names) == 0 {
		return "- Look for scripts named install_deps.py (or similar) even if they are not listed here."
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = "`" + name + "`"
	}
	return "- This archive ships on-demand installer(s): " + strings.Join(quoted, ", ") +
		". Run each one now with non-interactive flags covering every extra."
}

func bundleOnDemandInstallers(bundle *SkillBundle) []string {
	if bundle == nil {
		return nil
	}
	var names []string
	for rel := range bundle.Files {
		if skills.IsOnDemandInstallerPath(rel) {
			names = append(names, rel)
		}
	}
	sort.Strings(names)
	return names
}

func formatFrontmatterRepairNote(bundle *SkillBundle) string {
	if bundle == nil || !bundle.FrontmatterRepaired {
		return ""
	}
	return "\nThe SKILL.md YAML frontmatter was automatically repaired " +
		"(keys nested under name, or an unquoted colon). Extra or still-broken " +
		"keys were not reconstructed. Mention this in your summary so the user " +
		"can fix the file.\n"
}

// installProbeTools are the executables the installer agent reaches for. The
// probe resolves each one's absolute path so the prompt can hand them over
// instead of leaving the agent to burn a turn on `which` — or to gamble that
// the shell_exec PATH contains, say, /root/.local/bin.
var installProbeTools = []string{"uv", "npm", "pnpm", "pip3", "pip", "python3", "node"}

// installToolsProbeCommand locates every probe tool in one shell pass. The if
// guards the not-found case so the loop exits 0 whatever is missing: a tool
// that is absent is a fact for the prompt, not an error.
func installToolsProbeCommand() string {
	var b strings.Builder
	b.WriteString("for t in")
	for _, t := range installProbeTools {
		b.WriteByte(' ')
		b.WriteString(t)
	}
	b.WriteString(`; do if p=$(command -v "$t" 2>/dev/null); then printf '%s=%s\n' "$t" "$p"; fi; done`)
	return b.String()
}

// parseToolProbeOutput reads the probe's `name=path` lines. Anything else on
// stdout is ignored: a tool line is the only thing the command emits, but the
// prompt is better served by dropping noise than by failing the probe over it.
func parseToolProbeOutput(stdout string) map[string]string {
	tools := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		name, p, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && name != "" && p != "" {
			tools[name] = p
		}
	}
	return tools
}

// probeInstallTools resolves the absolute paths of the package managers the
// installer may reach for. One command, one round trip — the same probe
// `uv available` used to cost, carrying every tool instead of one bit. A
// failed probe is not an install failure: the prompt falls back to telling
// the agent to discover the toolchain itself.
func (s *TenantSkillService) probeInstallTools(
	ctx context.Context, mgr sandbox.Manager, sessionID string,
) map[string]string {
	res, err := s.execInstall(ctx, mgr, sessionID, installToolsProbeCommand())
	if err != nil || res == nil {
		return nil
	}
	return parseToolProbeOutput(res.Stdout)
}

// formatToolchainSection renders the probe result for the prompt: one line
// per tool with its absolute path, one line grouping whatever is missing.
// An empty result — a probe that failed, or an image with none of the tools —
// reads the same to the agent: locate them yourself before relying on PATH.
func formatToolchainSection(tools map[string]string) string {
	if len(tools) == 0 {
		return "Toolchain: could not be probed in advance; " +
			"locate tools with `command -v <tool>` before relying on PATH."
	}
	var b strings.Builder
	b.WriteString("Toolchain (absolute paths as resolved in this image; prefer them over PATH):")
	var missing []string
	for _, t := range installProbeTools {
		if p, ok := tools[t]; ok {
			fmt.Fprintf(&b, "\n- %s: %s", t, p)
		} else {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		b.WriteString("\nnot found: ")
		b.WriteString(strings.Join(missing, ", "))
	}
	return b.String()
}

// installerAgentDefaults returns the platform's own definition of the installer
// agent. It is deliberately not the record GetAgentByID serves, which a tenant
// can overwrite. When the registry has not been loaded, an ID-only agent yields
// the hardcoded platform defaults below rather than improvised settings.
func installerAgentDefaults(ctx context.Context, tenantID uint64) *types.CustomAgent {
	if defaults := types.GetBuiltinAgentWithContext(
		ctx, types.BuiltinSkillInstallerID, tenantID,
	); defaults != nil {
		return defaults
	}
	return &types.CustomAgent{ID: types.BuiltinSkillInstallerID}
}

// installerAgentConfig builds the config the install session runs under.
//
// `defaults` must be the built-in registry entry, never the tenant-editable
// record. Everything that decides what the root shell is asked to do — system
// prompt, tool whitelist, iteration budget — is fixed by the platform here.
// A tenant can edit any built-in agent's stored Config, and "can edit an
// agent" must not become "can script a root shell whose output is baked into
// the shared sandbox image": that is a different permission from "can upload a
// skill". The model is the one choice still taken from the stored record, in
// resolveInstallerModel.
//
// skillDir scopes the skill file tools to the one skill this install owns. The
// installer's shell already runs as root in the shared image, so the tools add
// no reach — they replace `cat` with a heredoc, whose command-length cap and
// double quoting truncated or mangled every file the agent tried to write.
func installerAgentConfig(
	defaults *types.CustomAgent, configID, skillDir string,
) *types.AgentConfig {
	memoryOff := false
	thinkingOff := false
	installTools := []string{
		tools.ToolShellExec, tools.ToolWriteSkillFile, tools.ToolEditSkillFile,
	}
	cfg := &types.AgentConfig{
		MaxIterations:    30,
		AllowedTools:     append([]string(nil), installTools...),
		Temperature:      0.2,
		WebSearchEnabled: false,
		MCPSelectionMode: "none",
		MemoryEnabled:    &memoryOff,
		SandboxConfigID:  configID,
		// Installs are short shell-command rounds, not reasoning tasks: pin
		// thinking off instead of deferring to the model's own default, which
		// burns latency and completion tokens on every ReAct round.
		Thinking: &thinkingOff,
	}
	if defaults == nil {
		return cfg
	}
	// The installer's shell_exec must run as root inside the skills image
	// root; the prompt below asks for exactly that. The grant is keyed on the
	// built-in agent ID and refused for anything else.
	cfg.EnableSkillInstallMode(defaults.ID, skillDir)
	custom := defaults.Config
	cfg.MaxIterations = custom.MaxIterations
	if cfg.MaxIterations < 0 {
		cfg.MaxIterations = types.UnlimitedMaxIterations
	} else if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 30
	}
	// The install tools are unioned in rather than read off the registry entry.
	// An install that cannot write its own skill directory cannot record what
	// it did, and the platform YAML predates these tools — a deployment that
	// has not re-synced it must not silently lose them.
	cfg.AllowedTools = unionTools(custom.AllowedTools, installTools)
	cfg.Temperature = custom.Temperature
	cfg.SystemPrompt = custom.SystemPrompt
	cfg.UseCustomSystemPrompt = custom.SystemPrompt != ""
	cfg.WebSearchEnabled = false
	cfg.WebSearchMaxResults = 0
	cfg.MCPSelectionMode = "none"
	cfg.MemoryEnabled = &memoryOff
	cfg.MultiTurnEnabled = custom.MultiTurnEnabled
	cfg.LLMCallTimeout = custom.LLMCallTimeout
	return cfg
}

// unionTools returns configured plus every name in required that it is missing,
// preserving the configured order so a deployment's own list still reads as it
// was written.
func unionTools(configured, required []string) []string {
	seen := make(map[string]struct{}, len(configured)+len(required))
	out := make([]string, 0, len(configured)+len(required))
	for _, name := range append(append([]string(nil), configured...), required...) {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// resolveInstallerModel prefers the model the installer agent is configured
// with. Whoever set that model chose it for this job; the workspace default is
// only the fallback for a record that names no model or names one this
// workspace can no longer resolve.
func (s *TenantSkillService) resolveInstallerModel(
	ctx context.Context, tenantID uint64, agent *types.CustomAgent,
) (chat.Chat, error) {
	if s.models == nil {
		return nil, errors.New("model service is not configured")
	}
	if agent != nil {
		if modelID := strings.TrimSpace(agent.Config.ModelID); modelID != "" {
			model, err := s.models.GetChatModel(ctx, modelID)
			if err == nil && model != nil {
				return model, nil
			}
			logger.Warnf(ctx,
				"[skill] installer agent model %s is unusable (%v); falling back to the workspace default",
				modelID, err)
		}
	}
	models, err := s.models.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models for installer: %w", err)
	}
	if model := types.PickChatModel(models, ""); model != nil {
		return s.models.GetChatModel(ctx, model.ID)
	}
	return nil, fmt.Errorf("workspace %d has no active chat model for skill installer", tenantID)
}
