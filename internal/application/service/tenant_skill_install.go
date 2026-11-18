package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/sandbox"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
)

const (
	installCommandTimeout = 10 * time.Minute

	// installCleanupTimeout bounds the compensating work that must outlive the
	// install context: destroying the sandbox and writing the terminal row.
	installCleanupTimeout = 30 * time.Second

	// The terminal "ready" write happens after the pointer has moved, so it is
	// retried: the skill is already installed and serving, and the only thing
	// still missing is the row that says so.
	readySkillWriteAttempts = 3
	readySkillWriteDelay    = 100 * time.Millisecond

	// skillSeedArchivePath is the single remote write used to land a skill's
	// files. Writing each file with MakeDir+WriteFile is two round trips per
	// entry, which is why a 50-file skill crawled through "seeding 12/56".
	skillSeedArchivePath = sandbox.SkillsImageRoot + "/.genyee-seed.tar"

	// skillInstallVerifyRounds bounds the installer conversation.
	//
	// The first round works from SKILL.md. A second is driven by the gate's own
	// findings, and exists because those are two different descriptions of what
	// a skill needs: the official office toolkit imports defusedxml and lxml at
	// module level while its SKILL.md names neither, so an installer working
	// from prose alone cannot know to install them. One round of reconciliation
	// closes that gap; more would only be a retry loop over an answer that is
	// not going to change.
	skillInstallVerifyRounds = 2
)

// skillArchiveOrigin says what an install's archive is to the workspace.
//
// Only an upload or a source pull is a new definition of the skill. A retry
// replays the archive its row already names, and a catalog install copies the
// definition's own bytes. Letting either write the definition back is how
// retrying a sandbox that was still on v1 moved the workspace from v2 back to
// v1 and deleted v2's archive.
type skillArchiveOrigin int

const (
	skillArchiveUploaded skillArchiveOrigin = iota
	skillArchiveStored
)

// InstallSkill validates an uploaded archive, records it, and kicks off the
// install in the background. It returns the skill ID so the caller can answer
// 202 and let the UI subscribe to progress.
func (s *TenantSkillService) InstallSkill(
	ctx context.Context, tenantID uint64, configID string, archive []byte,
) (string, error) {
	return s.installSkillArchive(ctx, tenantID, configID, archive, skillArchiveUploaded)
}

func (s *TenantSkillService) installSkillArchive(
	ctx context.Context, tenantID uint64, configID string, archive []byte,
	origin skillArchiveOrigin, instructions ...string,
) (string, error) {
	if err := s.requireSkillTarget(ctx, tenantID, configID); err != nil {
		return "", err
	}

	bundle, err := ParseSkillBundle(archive)
	if err != nil {
		return "", err
	}
	return s.installParsedSkill(ctx, tenantID, configID, bundle, archive, origin, instructions...)
}

func (s *TenantSkillService) installParsedSkill(
	ctx context.Context, tenantID uint64, configID string, bundle *SkillBundle, archive []byte,
	origin skillArchiveOrigin, instructions ...string,
) (string, error) {
	if bundle == nil {
		return "", fmt.Errorf("skill bundle is required")
	}

	// Re-uploading a skill by the same name is an upgrade of that skill, not a
	// second row: the unique (config, name) index would reject the insert, and
	// the image directory is the name, so it stays put.
	existing, err := s.skills.GetSkillByName(ctx, tenantID, configID, bundle.Name)
	if err != nil {
		return "", err
	}
	guidance := strings.TrimSpace(strings.Join(instructions, "\n"))
	if len([]rune(guidance)) > 10000 {
		return "", apperrors.NewBadRequestError("install instructions exceed 10000 characters")
	}
	if guidance != "" && existing != nil &&
		(existing.Status == types.SkillStatusInstalling || existing.Status == types.SkillStatusRemoving) {
		return "", apperrors.NewConflictError(
			"skill is busy; send guidance to the active install or wait for it to finish",
		)
	}
	keepPin := false
	if origin == skillArchiveStored {
		keepPin, err = s.storedArchiveKeepsItsPin(ctx, tenantID, existing, bundle)
		if err != nil {
			return "", err
		}
	}
	if guidance == "" && s.canSkipInstall(ctx, existing, bundle) {
		if keepPin {
			return existing.ID, nil
		}
		catalog, catalogErr := s.catalogForArchive(ctx, tenantID, bundle, archive, origin)
		if catalogErr != nil {
			return "", fmt.Errorf("store bundle for skill %s: %w", existing.ID, catalogErr)
		}
		if err := s.pointInstallAtCatalog(ctx, existing, catalog); err != nil {
			return "", fmt.Errorf("store bundle for skill %s: %w", existing.ID, err)
		}
		return existing.ID, nil
	}

	skillID := uuid.NewString()
	lockTargetID := bundle.Name
	if existing != nil {
		lockTargetID = existing.ID
	}
	now := s.now()
	if err := s.w3bWithSkillRowLock(ctx, tenantID, configID, lockTargetID, func() error {
		if existing != nil {
			skillID = existing.ID
			existing.Served = s.servedVersionOf(ctx, existing)
			takeSkillRowForInstall(existing, bundle, now)
			return s.skills.UpdateSkill(ctx, existing)
		}
		if err := s.skills.CreateSkill(ctx, &types.TenantSkillEntity{
			ID: skillID, TenantID: tenantID, SandboxConfigID: configID,
			Name: bundle.Name, Version: bundle.Version,
			Description: bundle.Description, Instructions: bundle.Instructions,
			BundleSHA256: bundle.SHA256, Enabled: true,
			Status: types.SkillStatusInstalling, InstallingSince: &now,
		}); err != nil {
			if !isSkillNameConflict(err) {
				return err
			}
			// Two first-time uploads of the same name raced the unique index.
			// Take the row that won rather than surfacing a 500.
			winner, lookupErr := s.skills.GetSkillByName(ctx, tenantID, configID, bundle.Name)
			if lookupErr != nil {
				return lookupErr
			}
			if winner == nil {
				return err
			}
			skillID = winner.ID
			winner.Served = s.servedVersionOf(ctx, winner)
			takeSkillRowForInstall(winner, bundle, now)
			return s.skills.UpdateSkill(ctx, winner)
		}
		return nil
	}); err != nil {
		return "", err
	}

	attemptID := uuid.NewString()
	if s.redis != nil {
		_ = s.redis.Set(ctx, w3bSkillInstallAttemptKey(tenantID, configID, skillID), attemptID, 24*time.Hour).Err()
	}

	// The zip lives on the catalog, not on this sandbox: uninstalling from
	// the last config must not take the definition's files with it. The
	// install row only stores CatalogID; readers follow that to the zip.
	// A pinned retry is the exception: its row already names its own archive.
	if !keepPin {
		if err := s.bindInstallToCatalog(ctx, tenantID, configID, skillID, bundle, archive, origin); err != nil {
			return "", err
		}
	}

	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
		Percent: 10, Stage: "accepted", Status: types.SkillStatusInstalling,
	})

	// The install outlives the HTTP request, so it must not inherit its
	// cancellation. It is not durable across a restart either - StopSkill
	// rewrites the row, and the stuck-run reaper is the backup.
	go func() {
		bgCtx := w3bWithSkillInstallAttempt(context.WithoutCancel(ctx), attemptID)
		if err := s.withSkillRunLock(bgCtx, tenantID, configID, skillID, func(lockCtx context.Context) error {
			return s.runInstall(lockCtx, tenantID, configID, skillID, bundle, guidance)
		}); err != nil {
			logger.Errorf(bgCtx, "[skill] install %s failed: %v", skillID, err)
		}
	}()

	return skillID, nil
}

// catalogForArchive returns the definition an install of these bytes follows.
// An upload or a source pull becomes the definition. Stored bytes reuse the
// definition that already holds them, untouched: storedArchiveKeepsItsPin has
// refused any other version by now, so what is left to write is only a
// definition that does not exist yet or an object that was lost.
func (s *TenantSkillService) catalogForArchive(
	ctx context.Context, tenantID uint64, bundle *SkillBundle, archive []byte, origin skillArchiveOrigin,
) (*types.TenantSkillCatalogEntity, error) {
	if origin == skillArchiveStored {
		catalog, err := s.skills.GetCatalogByName(ctx, tenantID, bundle.Name)
		if err != nil {
			return nil, err
		}
		if catalog != nil && catalog.BundleSHA256 == bundle.SHA256 &&
			s.catalogBundleStillHeld(ctx, tenantID, catalog) {
			return catalog, nil
		}
	}
	return s.upsertCatalogFromBundle(ctx, tenantID, bundle, archive, true)
}

// bindInstallToCatalog records the archive on the definition and points the
// install at it. A failure marks the row failed, because the row has already
// been handed to this run.
func (s *TenantSkillService) bindInstallToCatalog(
	ctx context.Context, tenantID uint64, configID, skillID string, bundle *SkillBundle, archive []byte,
	origin skillArchiveOrigin,
) error {
	catalog, err := s.catalogForArchive(ctx, tenantID, bundle, archive, origin)
	if err == nil {
		err = s.pointInstallAtCatalog(ctx, &types.TenantSkillEntity{
			ID: skillID, TenantID: tenantID, SandboxConfigID: configID,
		}, catalog)
	}
	if err == nil {
		return nil
	}
	failCtx, cancelFail := s.cleanupContext(ctx)
	defer cancelFail()
	logger.Errorf(ctx, "[skill] store bundle failed tenant=%d config=%s skill=%s name=%s: %v",
		tenantID, configID, skillID, bundle.Name, err)
	s.failSkill(failCtx, tenantID, configID, skillID, bundle, fmt.Errorf("store bundle: %w", err))
	return fmt.Errorf("store bundle for skill %s: %w", skillID, err)
}

// storedArchiveKeepsItsPin decides whether an install of bytes the workspace
// already holds has to leave the definition alone.
//
// It answers true when the definition has moved on and the install's own row
// still pins these bytes: a retry of a sandbox that is still on v1. Writing v1
// to the definition there is a rollback, so the row keeps its pin and the
// definition stays on v2. Bytes that neither the definition nor the row holds
// are a catalog that moved between reading its archive and installing it,
// which is refused rather than written back.
func (s *TenantSkillService) storedArchiveKeepsItsPin(
	ctx context.Context, tenantID uint64, existing *types.TenantSkillEntity, bundle *SkillBundle,
) (bool, error) {
	catalog, err := s.skills.GetCatalogByName(ctx, tenantID, bundle.Name)
	if err != nil {
		return false, err
	}
	// No definition yet is a row older than the catalog, and the same digest is
	// the definition's own bytes: storing either is a creation or a repair.
	if catalog == nil || catalog.BundleSHA256 == bundle.SHA256 {
		return false, nil
	}
	if existing != nil && strings.TrimSpace(existing.BundleRef) != "" &&
		existing.BundleSHA256 == bundle.SHA256 {
		return true, nil
	}
	// A definition that never recorded its digest cannot be told apart from
	// these bytes, and they are the only ones anyone can name.
	if strings.TrimSpace(catalog.BundleSHA256) == "" {
		return false, nil
	}
	return false, apperrors.NewConflictError(
		"this skill was updated in the catalog while the install was starting; try again")
}

// servedVersionOf is what the image goes on serving once row is handed to a new
// install. A ready row describes the image, so it becomes the served version.
// A row that is already mid-install or failed keeps the version it recorded
// when it last stopped being ready: this attempt replaces the previous attempt,
// not what the image carries. Any other row has nothing in the image.
func (s *TenantSkillService) servedVersionOf(
	ctx context.Context, row *types.TenantSkillEntity,
) *types.SkillServedVersion {
	switch row.Status {
	case types.SkillStatusReady:
		return &types.SkillServedVersion{
			Version:      row.Version,
			Description:  row.Description,
			Instructions: row.Instructions,
			BundleSHA256: row.BundleSHA256,
			BundleRef:    s.servedBundleRef(ctx, row),
			SnapshotID:   row.InstalledSnapshotID,
		}
	case types.SkillStatusInstalling, types.SkillStatusFailed:
		return row.Served
	default:
		return nil
	}
}

// servedBundleRef names the object a ready row's files are read from. A row
// that follows the catalog names none itself, and the catalog is exactly what
// an upload is about to replace, so the definition's object is named instead
// while it still holds these bytes. Recording it is what makes the served
// version a reader that the replacement cannot reclaim from under it.
func (s *TenantSkillService) servedBundleRef(ctx context.Context, row *types.TenantSkillEntity) string {
	if ref := strings.TrimSpace(row.BundleRef); ref != "" {
		return ref
	}
	cid := strings.TrimSpace(row.CatalogID)
	sha := strings.TrimSpace(row.BundleSHA256)
	if cid == "" || sha == "" {
		return ""
	}
	catalog, err := s.skills.GetCatalog(ctx, row.TenantID, cid)
	if err != nil || catalog == nil || strings.TrimSpace(catalog.BundleSHA256) != sha {
		return ""
	}
	return strings.TrimSpace(catalog.BundleRef)
}

// restoreServedVersion makes the served version the row's own again, for the
// paths that conclude the image never moved past it. It returns the archive
// the abandoned attempt had pinned, which nothing names once the row is back.
func restoreServedVersion(e *types.TenantSkillEntity) string {
	served := e.Served
	if served == nil {
		return ""
	}
	abandoned := strings.TrimSpace(e.BundleRef)
	e.Version = served.Version
	e.Description = served.Description
	e.Instructions = served.Instructions
	e.BundleSHA256 = served.BundleSHA256
	e.BundleRef = served.BundleRef
	e.InstalledSnapshotID = served.SnapshotID
	e.Served = nil
	if abandoned == strings.TrimSpace(served.BundleRef) {
		return ""
	}
	return abandoned
}

// takeSkillRowForInstall hands an existing row to the run about to start.
//
// The transcript locators are cleared along with the error. They name the
// session and message of the run that just ended, and a row that says
// "installing" while still pointing at them tells every reader that the
// finished conversation is this one's live output — which is how a retry came
// to replay the previous attempt's report before its own agent had started.
// They are rewritten by beginInstallTranscript once this run has a message of
// its own; until then the honest answer is that there is nothing to show yet.
func takeSkillRowForInstall(row *types.TenantSkillEntity, bundle *SkillBundle, now time.Time) {
	row.Version = bundle.Version
	row.Description = bundle.Description
	row.Instructions = bundle.Instructions
	row.BundleSHA256 = bundle.SHA256
	row.Status = types.SkillStatusInstalling
	row.Error = ""
	row.InstallingSince = &now
	row.InstallSessionID = ""
	row.InstallMessageID = ""
}

// ReinstallSkill runs the install again from the archive already stored for
// this skill. Most failed installs have nothing to do with the archive — an
// unreachable sandbox, a package index that timed out, a checker that has
// since been corrected — and making the operator find the original zip again
// (or the registry URL it came from) is a poor answer to any of them.
//
// It deliberately shares the upload path rather than jumping to runInstall.
// That path owns the in-flight check, the row ownership handover and the
// per-config lock, and a retry is exactly the moment two installs of one
// config are most likely to overlap.
func (s *TenantSkillService) ReinstallSkill(
	ctx context.Context, tenantID uint64, configID, skillID string, instructions ...string,
) (string, error) {
	skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return "", err
	}
	if skill == nil {
		return "", apperrors.NewNotFoundError("skill not found")
	}
	// The zip is owned by the catalog. An empty install BundleRef is not
	// itself a failure — fall through to skillBundleArchive, which is what
	// reports a definition whose files are actually gone.
	archive, err := s.skillBundleArchive(ctx, tenantID, configID, skillID)
	if err != nil {
		return "", err
	}
	if len(archive) == 0 {
		return "", apperrors.NewBadRequestError(
			"the archive of this skill is no longer stored; install it again from the original bundle",
		)
	}
	return s.installSkillArchive(ctx, tenantID, configID, archive, skillArchiveStored, instructions...)
}

func (s *TenantSkillService) runInstall(
	ctx context.Context, tenantID uint64, configID, skillID string, bundle *SkillBundle, instructions ...string,
) (err error) {
	ctx, span := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name:     "skill.install",
		Metadata: map[string]interface{}{"tenant_id": tenantID, "sandbox_config_id": configID, "skill_id": skillID},
	})
	defer func() { span.Finish(nil, nil, err) }()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	ctx = types.WithSandboxTenantID(ctx, tenantID)

	// Cleanup runs on a context that cannot be cancelled by whatever it is
	// compensating for. withConfigLock cancels ctx the moment lock renewal
	// fails, and the two things that must still happen then are a provider
	// call (destroy the sandbox, or it stays running and billed) and a DB write
	// (mark the skill failed, or the row sits at "installing" with the cause
	// only in a log line).
	//
	// Only cancellation is detached here; the deadline is NOT. An install runs
	// for minutes — a single agent command may take installCommandTimeout — so
	// a timeout started at this line would already be spent by the time any
	// compensating work begins. Each consumer calls cleanupContext to start its
	// own budget at the moment it needs one.
	cleanupBase := context.WithoutCancel(ctx)
	handle := s.lookupSkillRun(tenantID, configID, skillID)

	// pointerSwitched marks the point of no return. Past it the skill is
	// installed, snapshotted and serving every new session, so a later failure
	// is a bookkeeping failure and must not be reported as a failed install.
	pointerSwitched := false
	defer func() {
		if err == nil {
			return
		}
		if pointerSwitched {
			logger.Errorf(cleanupBase,
				"[skill] %s is installed and serving but its bookkeeping is incomplete: %v",
				skillID, err)
			return
		}
		// StopSkill (or a retry) may already own the row; stamping failed on
		// that owner would hide the run the operator just started.
		if !s.skillRunStillBound(tenantID, configID, skillID, handle) {
			return
		}
		// The image pointer is deliberately untouched on failure: the previous
		// snapshot keeps serving every session.
		failCtx, cancelFail := s.cleanupContext(cleanupBase)
		defer cancelFail()
		s.failSkill(failCtx, tenantID, configID, skillID, bundle, err)
	}()

	// InstallSkill queues this run before the per-config lock. A remove that
	// won the lock (or a newer upload of the same name) already owns the row;
	// snapshotting anyway would bake a skill the ledger no longer names.
	owned, err := s.installStillOwnsTheRow(ctx, tenantID, configID, skillID, bundle)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}

	// From here on this run is the row's owner, and everything below can take
	// minutes. The heartbeat is what tells a second upload of the same archive
	// (and the reaper) that those minutes are work rather than a dead process.
	// It is deferred before it is stopped explicitly below, so a failure path
	// still stops it ahead of the deferred failSkill.
	stopHeartbeat := s.startInstallHeartbeat(ctx, tenantID, configID, skillID)
	defer stopHeartbeat()

	if sandbox.IsHostSkillTarget(configID) {
		return s.runHostInstall(ctx, hostInstallRun{
			tenantID: tenantID, configID: configID, skillID: skillID,
			bundle: bundle, instructions: instructions,
			handle: handle, cleanupBase: cleanupBase,
			stopHeartbeat: stopHeartbeat, activated: &pointerSwitched,
		})
	}

	// The name comes from SKILL.md and is already validated on parse, so a
	// rejection here means the bundle was accepted by a looser rule than the
	// one the image path enforces. Failing before any sandbox work keeps that
	// disagreement cheap.
	skillDir, err := sandbox.SkillDirFor(bundle.Name)
	if err != nil {
		return err
	}

	cfgEntity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("load sandbox config %s: %w", configID, err)
	}
	if cfgEntity == nil {
		return fmt.Errorf("sandbox config %s not found", configID)
	}
	if err := ensureUsableImage(cfgEntity); err != nil {
		return err
	}
	// Snapshots are private to the provider account that created them. The
	// fingerprint is captured here, from the credentials this sandbox will
	// actually use, and switchImagePointer refuses to stamp a later rotation
	// onto that ID — that would make the session layer trust a snapshot the
	// live key cannot resolve.
	builtFingerprint := skillOwnerFingerprint(cfgEntity.Config)
	if strings.TrimSpace(builtFingerprint) == "" {
		return fmt.Errorf(
			"sandbox config %s has no usable owner fingerprint for a skill image", configID,
		)
	}

	// 1. Install session + sandbox. ResolveEffectiveConfig has already turned
	//    the current snapshot into the template, so this sandbox boots from the
	//    existing image and this install stacks on top of it.
	sess, mgr, err := s.startMaintenanceSession(ctx, tenantID, configID, "install")
	if err != nil {
		return err
	}
	// Only the sandbox is released. The session and its messages stay so the
	// agent's install transcript can be read back when something goes wrong.
	defer func() {
		releaseCtx, cancelRelease := s.cleanupContext(cleanupBase)
		defer cancelRelease()
		s.releaseSandbox(releaseCtx, mgr, sess.ID)
	}()

	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{Percent: 25, Stage: "sandbox_ready"})

	// 2. Provision the target directory, then seed the source files
	//    server-side. The agent only installs dependencies; it never has to
	//    reconstruct the skill itself.
	if err := s.resetSkillDir(ctx, mgr, sess.ID, skillDir); err != nil {
		return err
	}

	// Locators must land before the file seed. A large skill is copied file by
	// file over the sandbox API and can take minutes; the console attaches to
	// the transcript as soon as the directory is ready, not after that copy.
	transcript, prompt := s.beginInstallTranscript(
		ctx, tenantID, configID, skillID, sess, mgr, skillDir, bundle, instructions...)

	fileCount := 0
	if bundle != nil {
		fileCount = len(bundle.Files)
	}
	if fileCount > 0 {
		logger.Infof(ctx, "[skill] seeding %d files for %s as one archive", fileCount, skillID)
		s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
			Percent: 28, Stage: "seeding",
			Log: fmt.Sprintf("seeding %d files", fileCount),
		})
	}
	if err := s.seedSkillFiles(ctx, mgr, sess.ID, skillDir, bundle); err != nil {
		return err
	}
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{Percent: 35, Stage: "seeded"})

	// 3-5. Install dependencies and verify the result. "The agent said it
	//      worked" is a sentence, not evidence, and this is the last gate
	//      before the image is switched — but the gate and the agent must not
	//      answer "what does this skill need" separately, so a failure it can
	//      still fix goes back to the same agent before the run is failed.
	if err := s.installDependenciesAndVerify(ctx, installerJob{
		tenantID: tenantID, configID: configID, skillID: skillID,
		sess: sess, mgr: mgr, transcript: transcript,
		prompt: prompt, skillDir: skillDir, bundle: bundle,
		guidance: strings.TrimSpace(strings.Join(instructions, "\n")),
	}); err != nil {
		return err
	}
	if err := s.writeManifestEntry(ctx, mgr, sess.ID, skillID, bundle); err != nil {
		return err
	}
	// Read before the scratch wipe. requirements.json lives under skillDir and
	// is not scratch, but reading it first removes an implicit dependency on
	// what cleanImageScratch happens to delete. It is stored only once the
	// pointer has moved: the declaration replaces the previous one and drops
	// values of variables the new version no longer reads, and until the
	// pointer moves it is the previous version that runs and reads them.
	declaredEnvs, envsDeclared := s.readEnvDeclaration(ctx, mgr, sess.ID, skillID, bundle)
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{Percent: 90, Stage: "verified"})

	// 6. Wipe the scratch state. It must happen BEFORE the snapshot, or the
	//    per-session workspace and every package cache land in the image.
	if err := s.cleanImageScratch(ctx, mgr, sess.ID); err != nil {
		return err
	}

	// 7. Snapshot. This is the point of no return: everything above is
	//    confined to a sandbox we are about to destroy, everything below
	//    creates provider resources and moves the pointer. withConfigLock
	//    cancels this context when the lock's renewal fails, and a lost lock
	//    means another install may already be building on the same config, so
	//    stop here rather than race it. The ledger row is written before the
	//    snapshot: a snapshot with no ledger entry is a resource nobody knows
	//    exists.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("install lock lost before snapshot: %w", err)
	}
	// Re-check after the minutes-long agent run: InstallSkill writes the row
	// outside this lock, so a newer upload or a queued remove may already own
	// it. Snapshotting anyway would bake a tree the ledger no longer names.
	if !s.skillRunStillBound(tenantID, configID, skillID, handle) {
		return nil
	}
	owned, err = s.installStillOwnsTheRow(ctx, tenantID, configID, skillID, bundle)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	// Generation is max(live pointer, ledger)+1 so a build that died after
	// the commit but before the pointer moved cannot share a name with the
	// next install. withConfigLock still serialises writers of SkillImage;
	// the ledger read is what closes the crash window the lock cannot see.
	ledger, err := s.skills.ListSnapshotsByConfig(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("list snapshots of config %s: %w", configID, err)
	}
	generation := nextSnapshotGeneration(currentGeneration(cfgEntity), ledger)
	installRowID := uuid.NewString()
	// The name is recorded with the row rather than derived at the call below,
	// so a run that dies during the commit still leaves the ledger able to
	// name what it was building. Without it the snapshot would be a provider
	// resource nothing could ever address, let alone reclaim. The row id is
	// in the name so two rows of the same generation cannot share a tag.
	snapshotName := skillSnapshotBuildName(tenantID, configID, generation, installRowID)
	if err := s.skills.CreateSnapshotRow(ctx, &types.TenantSkillSnapshotEntity{
		ID: installRowID, TenantID: tenantID, SandboxConfigID: configID, SkillID: skillID,
		ParentSnapshotID: currentSnapshotID(cfgEntity), Generation: generation,
		Trigger: types.SkillSnapshotTriggerInstall, State: types.SkillSnapshotStateBuilding,
		PlannedName: snapshotName,
	}); err != nil {
		return err
	}
	ref, err := s.createSnapshot(ctx, mgr, sess.ID, snapshotName)
	if err != nil {
		return err
	}
	if err := s.skills.MarkSnapshotState(
		ctx, tenantID, installRowID, types.SkillSnapshotStateActive, ref.ID,
	); err != nil {
		// The snapshot's ID exists nowhere but this function's locals now, so
		// it is as unreachable as one nothing points at.
		s.abandonSnapshot(cleanupBase, tenantID, mgr, installRowID, ref.ID)
		return err
	}

	// 8. Switch the pointer. One DB write; everything after this is cleanup.
	if err := s.switchImagePointer(ctx, tenantID, configID, ref.ID, generation, builtFingerprint); err != nil {
		s.abandonSnapshot(cleanupBase, tenantID, mgr, installRowID, ref.ID)
		return err
	}
	pointerSwitched = true
	// The heartbeat writes the whole row, so it must be gone before the
	// terminal "ready" write below: a beat landing after it would put the row
	// back to installing and have the reaper fail a skill that is serving.
	stopHeartbeat()
	s.markPreviousSnapshotsSuperseded(ctx, tenantID, configID, installRowID)

	// The terminal write is the one that must not be best-effort: the pointer
	// already moved, so a row left at "installing" would be reaped as failed
	// even though the skill is installed and serving.
	readyCtx, cancelReady := s.cleanupContext(cleanupBase)
	defer cancelReady()
	if err := s.writeReadySkillState(readyCtx, tenantID, configID, skillID, ref.ID, bundle); err != nil {
		return err
	}
	if envsDeclared {
		s.storeEnvDeclaration(readyCtx, tenantID, configID, skillID, bundle, declaredEnvs)
	}
	s.markConfigSandboxesStale(ctx, tenantID, configID)
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
		Percent: 100, Stage: "done", Status: types.SkillStatusReady,
	})
	return nil
}

// ensureUsableImage refuses to grow the image when the stored snapshot is one
// the live credentials cannot resolve.
//
// Snapshots are private to a provider account, so a rotated credential turns
// the recorded pointer into an ID that no longer exists for us: the session
// layer notices via the fingerprint and boots the BASE TEMPLATE instead. A run
// that ignored that would do its work in a sandbox carrying none of the
// tenant's skills, snapshot that, and switch the pointer to it with a
// fingerprint that now matches - so it sticks, while every skill row still
// claims ready. Until then the old image is still recoverable by restoring the
// credential, which is why both flows stop here instead of committing the loss.
func ensureUsableImage(cfgEntity *types.TenantSandboxConfigEntity) error {
	if cfgEntity == nil || cfgEntity.Config == nil {
		return nil
	}
	image := cfgEntity.Config.SkillImage
	if image == nil || strings.TrimSpace(image.SnapshotID) == "" {
		return nil
	}
	// An empty fingerprint on either side is the same condition: the session
	// layer keeps the base template for both.
	live := skillOwnerFingerprint(cfgEntity.Config)
	if live != "" && image.OwnerFingerprint == live {
		return nil
	}
	return fmt.Errorf(
		"skill image %s of sandbox config %s belongs to another provider account; "+
			"restore the credentials it was built with or rebuild the image",
		image.SnapshotID, cfgEntity.ID,
	)
}

// abandonSnapshot disposes of a snapshot that was created but never became
// reachable - either because the ledger could not be told its ID or because
// the pointer switch failed. Both leave a provider resource that is billed and
// that nothing can ever boot again, and this is the only snapshot delete
// either flow performs: an ordinary switch leaves the previous image reachable
// for the ledger.
//
// It runs on a detached context because a cancelled one is among the reasons
// the caller failed, and that is exactly when a leaked snapshot is most likely.
func (s *TenantSkillService) abandonSnapshot(
	cleanupBase context.Context, tenantID uint64, mgr sandbox.Manager, rowID, snapshotID string,
) {
	ctx, cancel := s.cleanupContext(cleanupBase)
	defer cancel()
	nextState := types.SkillSnapshotStateSuperseded
	if s.deleteSnapshotBestEffort(ctx, mgr, snapshotID) {
		nextState = types.SkillSnapshotStateDeleted
	}
	if err := s.retrySkillBookkeeping(ctx, func() error {
		return s.skills.MarkSnapshotState(ctx, tenantID, rowID, nextState, snapshotID)
	}); err != nil {
		logger.Warnf(ctx, "[skill] mark abandoned snapshot %s state=%s failed: %v",
			snapshotID, nextState, err)
	}
}

// cleanupContext bounds one piece of compensating work, starting the budget
// when that work starts. Anchoring it earlier would hand every consumer an
// already-expired deadline, because an install takes minutes to reach them.
func (s *TenantSkillService) cleanupContext(
	base context.Context,
) (context.Context, context.CancelFunc) {
	timeout := s.cleanupTimeout
	if timeout <= 0 {
		timeout = installCleanupTimeout
	}
	return context.WithTimeout(base, timeout)
}

// pointInstallAtCatalog attaches the sandbox row to the definition that owns
// the zip. The install does not copy BundleRef: readers follow CatalogID, and
// uninstalling this sandbox must not be able to delete the definition object.
func (s *TenantSkillService) pointInstallAtCatalog(
	ctx context.Context, skill *types.TenantSkillEntity, catalog *types.TenantSkillCatalogEntity,
) error {
	if skill == nil {
		return nil
	}
	if catalog == nil || strings.TrimSpace(catalog.BundleRef) == "" {
		return fmt.Errorf("catalog archive is missing")
	}
	superseded := ""
	if err := s.updateSkillFields(ctx, skill.TenantID, skill.SandboxConfigID, skill.ID,
		func(e *types.TenantSkillEntity) {
			superseded = strings.TrimSpace(e.BundleRef)
			e.CatalogID = catalog.ID
			e.BundleRef = ""
		}); err != nil {
		return err
	}
	// This install now reads the definition's copy, so whatever it named before
	// — a pre-catalog object of its own, or an archive pinned by an earlier
	// replacement — has one reader fewer.
	if superseded != "" && superseded != strings.TrimSpace(catalog.BundleRef) {
		s.releaseInstallBundle(ctx, skill.TenantID, superseded)
	}
	return nil
}

// updateSkillFields loads, mutates and writes back one skill row. It logs on
// failure for the progress-only call sites and returns the error for the one
// call site where the row is the record of a completed install.
func (s *TenantSkillService) updateSkillFields(
	ctx context.Context,
	tenantID uint64,
	configID, skillID string,
	mutate func(*types.TenantSkillEntity),
) error {
	return s.w3bWithSkillRowLock(ctx, tenantID, configID, skillID, func() error {
		skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
		if err != nil {
			logger.Warnf(ctx, "[skill] load %s for update failed: %v", skillID, err)
			return fmt.Errorf("load skill %s: %w", skillID, err)
		}
		if skill == nil {
			return fmt.Errorf("skill %s not found", skillID)
		}
		mutate(skill)
		if err := s.skills.UpdateSkill(ctx, skill); err != nil {
			logger.Warnf(ctx, "[skill] update %s failed: %v", skillID, err)
			return fmt.Errorf("update skill %s: %w", skillID, err)
		}
		return nil
	})
}

// writeReadySkillState records the finished install. It is retried because it
// runs after the pointer switch: the skill is installed, snapshotted and being
// served, so a transient write failure here is a bookkeeping gap, not a failed
// install, and re-running the whole install to fix a row would be absurd.
//
// The write is skipped when a newer upload or a queued remove already owns
// the row: the pointer still moved, but stamping ready (or this run's
// snapshot id) on that row would lie about which bundle is serving.
func (s *TenantSkillService) writeReadySkillState(
	ctx context.Context, tenantID uint64, configID, skillID, snapshotID string, bundle *SkillBundle,
) error {
	retired := ""
	if err := s.retrySkillBookkeeping(ctx, func() error {
		owned, err := s.installStillOwnsTheRow(ctx, tenantID, configID, skillID, bundle)
		if err != nil {
			return err
		}
		if !owned {
			return nil
		}
		return s.updateSkillFields(ctx, tenantID, configID, skillID,
			func(e *types.TenantSkillEntity) {
				// The pointer has moved, so the version kept for the length
				// of the upgrade is no longer in any image new sessions boot.
				if e.Served != nil {
					retired = strings.TrimSpace(e.Served.BundleRef)
				}
				e.Served = nil
				e.Status = types.SkillStatusReady
				e.Error = ""
				e.InstalledSnapshotID = snapshotID
				e.InstallingSince = nil
			})
	}); err != nil {
		return fmt.Errorf("skill %s is installed and serving but could not be marked ready: %w",
			skillID, err)
	}
	s.releaseInstallBundle(ctx, tenantID, retired)
	return nil
}

// retrySkillBookkeeping runs one terminal bookkeeping write past the point of
// no return, where the image already changed and only the row recording it is
// missing. Both callers are in that position, so both retry instead of
// reporting the whole run as failed.
func (s *TenantSkillService) retrySkillBookkeeping(
	ctx context.Context, write func() error,
) error {
	var lastErr error
	for attempt := 1; attempt <= readySkillWriteAttempts; attempt++ {
		if attempt > 1 {
			if err := sleepWithContext(ctx, readySkillWriteDelay); err != nil {
				return err
			}
		}
		if lastErr = write(); lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (s *TenantSkillService) failSkill(
	ctx context.Context, tenantID uint64, configID, skillID string, bundle *SkillBundle, cause error,
) {
	owned, err := s.installStillOwnsTheRow(ctx, tenantID, configID, skillID, bundle)
	if err != nil || !owned {
		return
	}
	if err := s.retrySkillBookkeeping(ctx, func() error {
		return s.updateSkillFields(ctx, tenantID, configID, skillID, func(e *types.TenantSkillEntity) {
			e.Status = types.SkillStatusFailed
			e.Error = cause.Error()
			e.InstallingSince = nil
		})
	}); err != nil {
		logger.Warnf(ctx, "[skill] mark failed skill %s failed: %v", skillID, err)
	}
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
		Percent: 100, Stage: "failed", Status: types.SkillStatusFailed, Log: cause.Error(),
	})
}

// installStillOwnsTheRow is the lock-side counterpart of InstallSkill's
// optimistic row write. A remove that ran first deleted the row; StopSkill
// flipped it to failed; a newer upload of the same name replaced BundleSHA256;
// a queued remove flipped the status; a sibling retry of the same archive
// found the first run had already landed in the live image. Any of those means
// this run must not snapshot — failSkill would stamp the newer owner's row,
// and a snapshot with no matching row is an orphan the ledger cannot name.
func (s *TenantSkillService) installStillOwnsTheRow(
	ctx context.Context, tenantID uint64, configID, skillID string, bundle *SkillBundle,
) (bool, error) {
	if !s.w3bAttemptStillActive(ctx, tenantID, configID, skillID) {
		return false, nil
	}
	current, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return false, fmt.Errorf("load skill %s: %w", skillID, err)
	}
	if current == nil {
		return false, nil
	}
	if current.Status == types.SkillStatusRemoving || current.Status == types.SkillStatusFailed {
		return false, nil
	}
	if bundle != nil && current.BundleSHA256 != "" && current.BundleSHA256 != bundle.SHA256 {
		return false, nil
	}
	if current.Status == types.SkillStatusReady {
		_, inImage, ok := s.skillFilesInLiveImage(ctx, current)
		if ok && inImage {
			return false, nil
		}
	}
	return true, nil
}

// canSkipInstall reports whether this upload is a no-op. Re-uploading the
// exact archive of a skill that is already ready (and still in the live image)
// must not boot a billed sandbox or grow a new snapshot. An install of the
// same bytes that is still beating is the same situation: the first run owns
// the work.
//
// Only a ready row is answered from the image. An installing row is answered
// from the heartbeat alone, deliberately: the ledger records which skill an
// install snapshotted, not which archive, so a row that is installing bundle
// B while the image still carries the earlier bundle A would look "already
// installed" and this upload would report a success that never happened.
//
// A failed skill with the same digest is a retry: the previous attempt never
// made it into the image. A removal in flight is not a skip either — taking
// the row back to installing is how an upload cancels it.
func (s *TenantSkillService) canSkipInstall(
	ctx context.Context, existing *types.TenantSkillEntity, bundle *SkillBundle,
) bool {
	if existing == nil || bundle == nil {
		return false
	}
	if existing.BundleSHA256 == "" || existing.BundleSHA256 != bundle.SHA256 {
		return false
	}
	switch existing.Status {
	case types.SkillStatusInstalling:
		return s.installIsInFlight(existing)
	case types.SkillStatusReady:
		_, inImage, ok := s.skillFilesInLiveImage(ctx, existing)
		return ok && inImage
	default:
		return false
	}
}

// installIsInFlight reports whether an installing row still belongs to a live
// process. The answer is the heartbeat: a running install restamps
// InstallingSince every skillInstallHeartbeatInterval, so silence past
// skillInstallInFlightSkip means the process is gone and the next upload must
// be allowed to start a new run rather than wait for the stuck-run reaper.
//
// Reading the submission time instead would force a choice between calling a
// slow install dead — a single agent command may take installCommandTimeout,
// and an install runs several — and leaving a dead one unrecoverable.
func (s *TenantSkillService) installIsInFlight(existing *types.TenantSkillEntity) bool {
	if existing == nil || existing.InstallingSince == nil {
		return false
	}
	return !existing.InstallingSince.Before(s.clock()().Add(-skillInstallInFlightSkip))
}
