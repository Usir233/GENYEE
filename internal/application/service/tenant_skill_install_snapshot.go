package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/application/service/sandboxalloc"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/sandbox"
	"github.com/Usir233/GENYEE/internal/sandbox/allocation"
	"github.com/Usir233/GENYEE/internal/types"
	"gorm.io/gorm"
)

// startMaintenanceSession opens the session one image operation runs in. The
// operation name is carried into the session because the transcript is kept
// deliberately, for troubleshooting: filing a removal's under "Skill install"
// would send whoever reads it looking at the wrong operation.
//
// The description marker is what keeps this row out of the console's session
// list, and the owner is the admin who started the operation, so a transcript
// is readable by the person who caused it rather than by the whole workspace.
func (s *TenantSkillService) startMaintenanceSession(
	ctx context.Context, tenantID uint64, configID, operation string,
) (*types.Session, sandbox.Manager, error) {
	if s.sessions == nil {
		return nil, nil, errors.New("session service is not configured")
	}
	// Honour the workspace kill switch before creating a billed sandbox or a
	// session row. resolveTenantSandboxForConfig is the same choke point every
	// other sandbox caller uses; going through TenantSandboxResolver.Resolve
	// directly would let an install run while scripts are disabled.
	mgr, err := resolveTenantSandboxForConfig(ctx, s.sandboxes, nil, tenantID, configID, s.sandboxPolicy)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve sandbox config: %w", err)
	}
	if mgr == nil {
		return nil, nil, errors.New("sandbox resolver returned nil manager")
	}
	if mgr.GetType() == sandbox.SandboxTypeDisabled {
		return nil, nil, errors.New("sandbox execution is disabled for this workspace")
	}
	sess, err := s.sessions.CreateSession(ctx, &types.Session{
		TenantID:        tenantID,
		UserID:          sessionUserIDFromContext(ctx),
		Title:           "Skill " + operation,
		Description:     types.SkillMaintenanceSessionMarker + operation,
		SandboxConfigID: configID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create %s session: %w", operation, err)
	}
	if sess == nil {
		return nil, nil, fmt.Errorf("create %s session returned nil", operation)
	}
	if s.placer != nil {
		if _, err := s.placer.Place(types.WithSandboxTenantID(ctx, tenantID), sandboxalloc.PlaceRequest{
			SessionTenantID: tenantID, SessionID: sess.ID,
			ConfigTenantID: tenantID, ConfigID: configID, Purpose: allocation.PurposeMaintenance,
		}); err != nil {
			// The row was created for this operation only and nothing has run
			// in it; leaving it would keep a hidden, pinned session around.
			if delErr := s.sessions.DeleteSession(context.WithoutCancel(ctx), sess.ID); delErr != nil {
				logger.Warnf(ctx, "[skill] remove %s session %s after a failed placement: %v",
					operation, sess.ID, delErr)
			}
			return nil, nil, fmt.Errorf("record %s sandbox placement: %w", operation, err)
		}
	}
	return sess, mgr, nil
}

func (s *TenantSkillService) releaseSandbox(ctx context.Context, mgr sandbox.Manager, sessionID string) {
	destroyer, ok := mgr.(sandbox.SessionDestroyer)
	if !ok {
		return
	}
	if err := destroyer.DestroySession(ctx, sessionID); err != nil {
		logger.Warnf(ctx, "[skill] destroy install sandbox for session %s failed: %v", sessionID, err)
	}
}

func (s *TenantSkillService) createSnapshot(
	ctx context.Context, mgr sandbox.Manager, sessionID, name string,
) (sandbox.RemoteSnapshotRef, error) {
	snapshots, ok := mgr.(sandbox.RemoteSnapshotManager)
	if !ok {
		return sandbox.RemoteSnapshotRef{}, errors.New("sandbox backend does not support snapshots")
	}
	ref, err := snapshots.CreateSnapshot(ctx, sessionID, name)
	if err != nil {
		return sandbox.RemoteSnapshotRef{}, fmt.Errorf("create snapshot: %w", err)
	}
	if strings.TrimSpace(ref.ID) == "" {
		return sandbox.RemoteSnapshotRef{}, errors.New("create snapshot returned empty id")
	}
	return ref, nil
}

func (s *TenantSkillService) deleteSnapshotBestEffort(
	ctx context.Context, mgr sandbox.Manager, snapshotID string,
) bool {
	snapshots, ok := mgr.(sandbox.RemoteSnapshotManager)
	if !ok {
		return false
	}
	if err := snapshots.DeleteSnapshot(ctx, snapshotID); err != nil {
		if sandbox.IsRemoteNotFound(err) {
			return true
		}
		logger.Warnf(ctx, "[skill] delete orphan snapshot %s failed: %v", snapshotID, err)
		return false
	}
	return true
}

// switchImagePointer is the single DB write that makes the new snapshot the
// image every future session boots from.
//
// The config is re-read here rather than reused from the read at the top of
// runInstall, which happened minutes and one whole agent conversation ago. The
// repository Update overwrites name, description, sandbox type and the entire
// config blob, and config edits are serialised by the config service's cordon
// rather than by this task's lock — so writing back the stale entity would
// silently revert an admin's rename or credential rotation.
//
// builtFingerprint is the account the maintenance sandbox (and therefore the
// snapshot) actually belongs to. A live fingerprint that no longer matches
// means the credentials rotated while this run was in flight: stamping the
// new fingerprint onto the old snapshot would make the session layer trust an
// ID the live key cannot resolve. Abort instead, and leave the previous image
// in place.
func (s *TenantSkillService) switchImagePointer(
	ctx context.Context,
	tenantID uint64,
	configID string,
	snapshotID string,
	generation int,
	builtFingerprint string,
) error {
	cfgEntity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("re-read sandbox config %s: %w", configID, err)
	}
	if cfgEntity == nil || cfgEntity.Config == nil {
		return fmt.Errorf("sandbox config %s disappeared during the install", configID)
	}

	if strings.TrimSpace(builtFingerprint) == "" {
		return fmt.Errorf(
			"sandbox config %s has no usable owner fingerprint for a skill image", configID,
		)
	}
	live := skillOwnerFingerprint(cfgEntity.Config)
	if live != builtFingerprint {
		return fmt.Errorf(
			"sandbox config %s credentials changed during the image build; "+
				"the snapshot belongs to the previous provider account",
			configID,
		)
	}

	// Only the SkillImage portion is touched; everything else in the entity is
	// whatever the latest read says it is.
	cfgEntity.Config.SkillImage = &types.SkillImageConfig{
		SnapshotID:       snapshotID,
		Generation:       generation,
		BuiltAt:          s.now(),
		BaseTemplateID:   effectiveBaseTemplate(cfgEntity),
		OwnerFingerprint: builtFingerprint,
	}
	cfgEntity.TenantID = tenantID
	return s.configs.Update(ctx, cfgEntity)
}

// effectiveBaseTemplate returns the template the image chain grew from. Once
// recorded it is never re-derived, because the config's own template field is
// overwritten with the snapshot ID whenever a session resolves the config.
func effectiveBaseTemplate(cfgEntity *types.TenantSandboxConfigEntity) string {
	if cfgEntity == nil || cfgEntity.Config == nil {
		return ""
	}
	if image := cfgEntity.Config.SkillImage; image != nil &&
		strings.TrimSpace(image.BaseTemplateID) != "" {
		return image.BaseTemplateID
	}
	return currentBaseTemplate(cfgEntity.Config)
}

func (s *TenantSkillService) markPreviousSnapshotsSuperseded(
	ctx context.Context, tenantID uint64, configID, currentRowID string,
) {
	rows, err := s.skills.ListSnapshotsByConfig(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx, "[skill] list snapshots for supersede failed: %v", err)
		return
	}
	for _, row := range rows {
		if row == nil || row.ID == currentRowID || row.State != types.SkillSnapshotStateActive {
			continue
		}
		if err := s.skills.MarkSnapshotState(
			ctx, tenantID, row.ID, types.SkillSnapshotStateSuperseded, row.SnapshotID,
		); err != nil {
			logger.Warnf(ctx, "[skill] mark snapshot %s superseded failed: %v", row.ID, err)
		}
	}
}

func currentGeneration(cfgEntity *types.TenantSandboxConfigEntity) int {
	if cfgEntity == nil || cfgEntity.Config == nil || cfgEntity.Config.SkillImage == nil {
		return 0
	}
	return cfgEntity.Config.SkillImage.Generation
}

func currentSnapshotID(cfgEntity *types.TenantSandboxConfigEntity) string {
	if cfgEntity == nil || cfgEntity.Config == nil || cfgEntity.Config.SkillImage == nil {
		return ""
	}
	return cfgEntity.Config.SkillImage.SnapshotID
}

func skillSnapshotNamePrefix(tenantID uint64, configID string) string {
	return fmt.Sprintf("genyee-sk-t%d-%s", tenantID, compactConfigID(configID))
}

// nextSnapshotGeneration is one past both the live pointer and every ledger
// row. An abandoned building row still occupies its generation; reusing it
// would mint the same planned name and let the reaper delete a later install's
// snapshot.
func nextSnapshotGeneration(live int, rows []*types.TenantSkillSnapshotEntity) int {
	highest := live
	for _, row := range rows {
		if row != nil && row.Generation > highest {
			highest = row.Generation
		}
	}
	if highest < 0 {
		highest = 0
	}
	return highest + 1
}

func compactSnapshotToken(id string) string {
	s := compactConfigID(id)
	const n = 8
	if len(s) > n {
		return s[:n]
	}
	if s == "" {
		return "row"
	}
	return s
}

// skillSnapshotBuildName is the name every generation of a config's image
// chain is committed under. It is recorded on the ledger row before the
// provider call so an abandoned build stays identifiable. Tenant and the
// full config id are in the name because Cube, E2B and Docker all list
// snapshots across a shared account or daemon; the row token stops two
// builds of the same generation from sharing a tag.
func skillSnapshotBuildName(tenantID uint64, configID string, generation int, rowID string) string {
	prefix := skillSnapshotNamePrefix(tenantID, configID)
	return fmt.Sprintf("%s-g%d-%s", prefix, generation, compactSnapshotToken(rowID))
}

func compactConfigID(id string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
}

// genyeeSkillSnapshotName pulls the genyee-sk-… token out of a provider
// listing. Cube and E2B echo it in Names; Docker embeds it in the image tag.
func genyeeSkillSnapshotName(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "docker.io/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if cut := strings.IndexByte(s, ':'); cut >= 0 {
		s = s[:cut]
	}
	if strings.HasPrefix(s, "genyee-sk-") {
		return s
	}
	return ""
}

// snapshotsNotFromOtherConfig drops provider listings that already name a
// different GENYEE config. Cube, E2B and Docker all ListSnapshots across the
// whole account/daemon, so without this a reconcile of one config would treat
// every other config's image as an extra, and an abandoned-build match could
// bind to the wrong snapshot.
func snapshotsNotFromOtherConfig(
	listed []sandbox.RemoteSnapshotRef, prefix string,
) []sandbox.RemoteSnapshotRef {
	if strings.TrimSpace(prefix) == "" {
		return listed
	}
	out := make([]sandbox.RemoteSnapshotRef, 0, len(listed))
	for _, snap := range listed {
		if snapshotBelongsToOtherConfig(snap, prefix) {
			continue
		}
		out = append(out, snap)
	}
	return out
}

func snapshotBelongsToOtherConfig(snap sandbox.RemoteSnapshotRef, prefix string) bool {
	if strings.TrimSpace(prefix) == "" {
		return false
	}
	needle := prefix + "-g"
	sawForeign := false
	for _, candidate := range append([]string{snap.ID}, snap.Names...) {
		name := genyeeSkillSnapshotName(candidate)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, needle) {
			return false
		}
		// A named snapshot not owned by this tenant/config is foreign.
		sawForeign = true
	}
	return sawForeign
}

func (s *TenantSkillService) writeManifestEntry(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillID string, bundle *SkillBundle,
) error {
	store, err := installFileStore(mgr)
	if err != nil {
		return err
	}
	manifest := skillImageManifest{}
	if reader, ok := mgr.(sandbox.SessionFileReader); ok {
		if raw, err := reader.ReadSessionFile(ctx, sessionID, sandbox.SkillsManifestPath); err == nil {
			_ = json.Unmarshal(raw, &manifest)
		}
	}
	entry := skillImageManifestEntry{
		ID:          skillID,
		Name:        bundle.Name,
		Version:     bundle.Version,
		SHA256:      bundle.SHA256,
		InstalledAt: s.now(),
	}
	replaced := false
	for i := range manifest.Skills {
		if manifest.Skills[i].ID == skillID {
			manifest.Skills[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		manifest.Skills = append(manifest.Skills, entry)
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return store.WriteSessionFile(ctx, sessionID, sandbox.SkillsManifestPath, payload)
}

// readEnvDeclaration reads the environment variables the installer agent
// declared and keeps the ones that survive validation. ok is false when there
// is nothing to store.
//
// None of its failures is a failed install. The agent may not have written the
// file, may have written prose, or may have listed nothing that exists in the
// bundle; in every case the skill is installed and working, and the missing
// declaration costs an admin one manual entry in the settings page. Failing the
// install over it would throw away the minutes of dependency installation that
// already succeeded.
func (s *TenantSkillService) readEnvDeclaration(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillID string, bundle *SkillBundle,
) (types.SkillEnvVars, bool) {
	if bundle == nil {
		return nil, false
	}
	reader, ok := mgr.(sandbox.SessionFileReader)
	if !ok {
		logger.Warnf(ctx,
			"[skill] sandbox backend cannot read files back; skill %s keeps no env declaration",
			skillID)
		return nil, false
	}
	requirementsPath := sandbox.SkillRequirementsPath(bundle.Name)
	if requirementsPath == "" {
		return nil, false
	}
	raw, err := reader.ReadSessionFile(ctx, sessionID, requirementsPath)
	if err != nil {
		// A skill that needs no credentials writes no file at all, so an
		// absent one is normal. Any other read failure means a declaration
		// may exist and was lost, which an operator has to be able to see.
		if sandbox.IsRemoteNotFound(err) {
			logger.Infof(ctx, "[skill] %s declared no environment variables (no %s)",
				skillID, requirementsPath)
			return nil, false
		}
		logger.Warnf(ctx, "[skill] %s: reading its env declaration at %s failed: %v",
			skillID, requirementsPath, err)
		return nil, false
	}
	declared, err := parseEnvDeclaration(raw)
	if err != nil {
		logger.Warnf(ctx, "[skill] %s wrote an unreadable env declaration: %v", skillID, err)
		return nil, false
	}
	envs := validateEnvDeclarations(declared, bundle)
	if len(envs) == 0 && len(declared) > 0 {
		// The original count is the only clue an admin has for "why is my
		// variable not in the list": every name was rejected, not ignored.
		// Learning nothing usable must not erase a value already stored.
		logger.Warnf(ctx,
			"[skill] all %d environment variable(s) declared for %s were rejected "+
				"(bad name, not mentioned anywhere in the bundle, or reserved)",
			len(declared), skillID)
		return nil, false
	}
	return envs, true
}

// storeEnvDeclaration folds a declaration read by readEnvDeclaration onto the
// row. It writes only onto the row this run just made ready: a newer upload or
// a queued remove that took the row over owns its declaration too.
func (s *TenantSkillService) storeEnvDeclaration(
	ctx context.Context, tenantID uint64, configID, skillID string, bundle *SkillBundle, envs types.SkillEnvVars,
) {
	skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil || skill == nil {
		logger.Warnf(ctx, "[skill] load %s to store its env declaration failed: %v", skillID, err)
		return
	}
	if skill.Status != types.SkillStatusReady || skill.BundleSHA256 != bundle.SHA256 {
		return
	}
	merged := mergeEnvDeclaration(skill.Envs, envs)
	if err := s.skills.UpdateSkillEnvs(ctx, tenantID, configID, skillID, merged); err != nil {
		logger.Warnf(ctx, "[skill] store the env declaration of %s failed: %v", skillID, err)
	}
}

type skillImageManifest struct {
	Skills []skillImageManifestEntry `json:"skills"`
}

type skillImageManifestEntry struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version,omitempty"`
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installed_at"`
}

// sessionInstallFileStore is the narrow write surface the install flow needs:
// SessionBoundManager.WriteSessionFile only permits paths under the skills
// image root.
type sessionInstallFileStore interface {
	WriteSessionFile(ctx context.Context, sessionID, filePath string, content []byte) error
}

// installFileStore is the single place the image-write capability is checked.
// Local and disabled managers do not implement it, so an install that resolved
// to one fails here rather than half-writing an image.
func installFileStore(mgr sandbox.Manager) (sessionInstallFileStore, error) {
	store, ok := mgr.(sessionInstallFileStore)
	if !ok || store == nil {
		return nil, errors.New("sandbox backend cannot write files into the skills image")
	}
	return store, nil
}

// installerAgentSource is the one thing the install flow needs from the agent
// side: the stored installer record, read for its model choice.
// interfaces.AgentService does not carry this method — it belongs to
// interfaces.CustomAgentService — so the dependency is injected separately and
// narrowed to this method here rather than fished out of the agent service at
// runtime, which could only ever fail.
type installerAgentSource interface {
	GetAgentByID(ctx context.Context, id string) (*types.CustomAgent, error)
}

// The concrete type the container wires must keep satisfying the narrow
// contract, so a future move of GetAgentByID off the custom agent service
// breaks the build instead of the install flow.
var _ installerAgentSource = (*customAgentService)(nil)

func currentBaseTemplate(cfg *types.TenantSandboxConfig) string {
	if cfg == nil {
		return ""
	}
	switch sandbox.SandboxType(cfg.SandboxType) {
	case sandbox.SandboxTypeCube:
		if cfg.Cube != nil {
			return cfg.Cube.TemplateID
		}
	case sandbox.SandboxTypeE2B:
		if cfg.E2B != nil {
			return cfg.E2B.TemplateID
		}
	case sandbox.SandboxTypeDocker:
		if cfg.Docker != nil {
			return cfg.Docker.Image
		}
	}
	return ""
}

// isSkillNameConflict recognises the unique (config, name) violation two
// concurrent first-time uploads of the same skill produce.
func isSkillNameConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique constraint")
}

func skillOwnerFingerprint(cfg *types.TenantSandboxConfig) string {
	return sandbox.SkillOwnerFingerprint(cfg)
}

// configSandboxInvalidator is the narrow capability marking bound sandboxes
// needs. It is reached by type assertion rather than declared on Manager
// because only the session-bound remote manager owns bindings to mark: a
// disabled backend has none, and for that doing nothing is correct.
type configSandboxInvalidator interface {
	InvalidateConfigSandboxes(ctx context.Context, tenantID uint64, configID string) (int, error)
}

// The manager the resolver hands out must keep satisfying this, so a rename on
// the sandbox side breaks the build instead of silently skipping every mark.
var _ configSandboxInvalidator = (*sandbox.SessionBoundManager)(nil)

// markConfigSandboxesStale tells every session already holding a sandbox of
// this config that the image underneath it has been replaced.
//
// The pointer switch alone only reaches FUTURE sessions: one that is open right
// now is bound to a sandbox booted from the previous image, and without this it
// would keep serving that image until the session ends - so a skill installed
// minutes ago would be missing from exactly the sessions whose user just asked
// for it. Nothing is destroyed here; the binding is marked and the session's
// next resolve rebuilds it.
//
// It is best-effort on purpose. It runs after the pointer switch, so the
// install has already succeeded, and a binding that could not be marked is a
// session serving a stale image - an annoyance, not a corruption. Turning that
// into a failed install would be a lie about an image that is live and serving.
//
// The context is detached because the caller's is cancelled exactly when this
// matters most: withConfigLock cancels the run's context the moment lock
// renewal fails, and marking is ordinary Redis traffic that a dead context
// fails outright.
func (s *TenantSkillService) markConfigSandboxesStale(
	ctx context.Context, tenantID uint64, configID string,
) {
	if s.sandboxes == nil {
		return
	}
	markCtx, cancel := s.cleanupContext(context.WithoutCancel(ctx))
	defer cancel()

	if s.configs != nil {
		entity, err := s.configs.GetByID(markCtx, tenantID, configID)
		if err != nil {
			logger.Warnf(markCtx,
				"[skill] read config %s skill_rollout before marking sandboxes stale failed: %v",
				configID, err)
		} else if entity != nil && !entity.Config.RebuildsExistingOnSkillChange() {
			logger.Infof(markCtx,
				"[skill] config %s keeps existing sandboxes on the previous image after skill changes",
				configID)
			return
		}
	}

	mgr, err := s.sandboxes.Resolve(markCtx, tenantID, configID)
	if err != nil || mgr == nil {
		logger.Warnf(markCtx,
			"[skill] resolve sandbox config %s to mark its sandboxes stale failed: %v",
			configID, err)
		return
	}
	invalidator, ok := mgr.(configSandboxInvalidator)
	if !ok {
		return
	}
	marked, err := invalidator.InvalidateConfigSandboxes(markCtx, tenantID, configID)
	if err != nil {
		logger.Warnf(markCtx,
			"[skill] mark live sandboxes of config %s stale failed: %v", configID, err)
		return
	}
	if marked == 0 {
		return
	}
	logger.Infof(markCtx,
		"[skill] marked %d live sandbox binding(s) of config %s stale "+
			"(this run's own maintenance session included); each remaining session "+
			"rebuilds from the new image on its next use",
		marked, configID)
}

// startInstallHeartbeat keeps this run's liveness visible while it works, and
// returns the stop function that must be called before any terminal write.
//
// The heartbeat writes the whole row, so it would otherwise race the "ready"
// write past the pointer switch and revive an installing status. Both callers
// stop it before that point: runInstall stops it the moment the pointer moves,
// and the deferred stop runs before the deferred failSkill.
func (s *TenantSkillService) startInstallHeartbeat(
	ctx context.Context, tenantID uint64, configID, skillID string,
) func() {
	interval := s.installHeartbeat
	if interval <= 0 {
		interval = skillInstallHeartbeatInterval
	}
	beatCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-beatCtx.Done():
				return
			case <-ticker.C:
				s.beatInstallHeartbeat(beatCtx, tenantID, configID, skillID)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			stop()
			<-done
		})
	}
}

// beatInstallHeartbeat stamps InstallingSince for a row this run still owns.
// A row that has left the installing status belongs to a newer upload, a
// queued removal, or a finished run, and reviving its timestamp would hide
// one of those from the reaper.
func (s *TenantSkillService) beatInstallHeartbeat(
	ctx context.Context, tenantID uint64, configID, skillID string,
) {
	if !s.w3bAttemptStillActive(ctx, tenantID, configID, skillID) {
		s.cancelSkillRun(tenantID, configID, skillID)
		return
	}
	_ = s.w3bWithSkillRowLock(ctx, tenantID, configID, skillID, func() error {
		current, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
		if err != nil {
			logger.Warnf(ctx, "[skill] load %s for install heartbeat failed: %v", skillID, err)
			return err
		}
		if current == nil || current.Status != types.SkillStatusInstalling {
			return nil
		}
		at := s.clock()()
		current.InstallingSince = &at
		if err := s.skills.UpdateSkill(ctx, current); err != nil {
			logger.Warnf(ctx, "[skill] install heartbeat for %s failed: %v", skillID, err)
			return err
		}
		return nil
	})
}
