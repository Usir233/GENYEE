package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	apprepo "github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"golang.org/x/sync/errgroup"
)

// followUpTriggerCount estimates useful new work from claimable documents and
// running slots. reserveFollowUpTasks additionally bounds all outstanding
// follow-ups (scheduled, queued, running and retrying) across batch completions.
// The +1 allows the completing batch to replace itself before releasing its slot.
func followUpTriggerCount(pending, batchSize, activeSlots, maxInflight int) int {
	if pending <= 0 || batchSize <= 0 {
		return 0
	}
	batchesNeeded := (pending + batchSize - 1) / batchSize
	if activeSlots < 0 || maxInflight <= 1 {
		return 1
	}
	freeSlots := maxInflight - activeSlots + 1 // caller's slot releases on return
	if freeSlots < 1 {
		freeSlots = 1
	}
	return min(batchesNeeded, freeSlots)
}

// scheduleFollowUp replenishes the KB's bounded supply of follow-up tasks.
// Lite mode and rate-limited completions request only one successor. A 429
// delays this batch's successor; it does not pause other batches in the KB.
func (s *wikiIngestService) scheduleFollowUp(
	ctx context.Context,
	payload WikiIngestPayload,
	delay time.Duration,
	batchSize, maxInflight int,
	rateLimited bool,
) bool {
	if s.pendingRepo == nil {
		return false
	}
	count, err := s.pendingRepo.PendingCount(ctx, wikiTaskType, wikiTaskScope, payload.KnowledgeBaseID)
	if err != nil || count == 0 {
		return false
	}

	if s.redisClient != nil {
		if counter, ok := s.pendingRepo.(interfaces.TaskPendingOpsClaimableCounter); ok {
			count, err = counter.ClaimableCount(ctx, wikiTaskType, wikiTaskScope,
				payload.KnowledgeBaseID, time.Now().Add(-wikiClaimStaleAfter))
			if err != nil {
				logger.Warnf(ctx, "wiki ingest: claimable count failed: %v", err)
				return s.scheduleFollowUpRecheck(ctx, payload)
			}
			if count == 0 {
				return s.scheduleStaleClaimRecheck(ctx, payload)
			}
		}
		// ClaimBatch caps the number of distinct keys at 1000.
		batchSize = min(batchSize, 1000)
	}

	// activeSlots stays -1 (unknown → single follow-up) in Lite mode and on
	// a rate-limited exit.
	activeSlots := -1
	if !rateLimited {
		activeSlots = s.activeInflightSlots(ctx, payload.KnowledgeBaseID)
	}
	n := followUpTriggerCount(int(count), batchSize, activeSlots, maxInflight)

	var taskIDs []string
	if s.redisClient != nil {
		taskIDs, err = s.reserveFollowUpTasks(ctx, payload.KnowledgeBaseID, n, maxInflight)
		if err != nil {
			logger.Warnf(ctx, "wiki ingest: follow-up reservation failed: %v", err)
			// Keep the pre-fan-out behavior while Redis is unavailable.
			n = 1
		} else {
			n = len(taskIDs)
			if n == 0 {
				return s.scheduleFollowUpRecheck(ctx, payload)
			}
		}
	}
	logger.Infof(ctx, "wiki ingest: %d claimable documents for KB %s, scheduling %d follow-up(s) in %s",
		count, payload.KnowledgeBaseID, n, delay)

	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, _ := json.Marshal(payload)
	scheduled := false
	needsRecheck := false
	for i := 0; i < n; i++ {
		opts := []asynq.Option{
			asynq.Queue(types.QueueWiki),
			asynq.MaxRetry(wikiIngestMaxRetry),
			asynq.Timeout(60 * time.Minute),
			asynq.ProcessIn(delay),
		}
		if len(taskIDs) > 0 {
			opts = append(opts, asynq.TaskID(taskIDs[i]))
		}
		t := asynq.NewTask(types.TypeWikiIngest, payloadBytes, opts...)
		if _, err := s.task.Enqueue(t); err != nil {
			logger.Warnf(ctx, "wiki ingest: follow-up enqueue %d/%d failed: %v", i+1, n, err)
			// A timeout may mean the enqueue succeeded. Keep its reservation
			// until reconciliation can establish the actual queue state.
			needsRecheck = true
			continue
		}
		scheduled = true
	}
	if needsRecheck {
		scheduled = s.scheduleFollowUpRecheck(ctx, payload) || scheduled
	}
	return scheduled
}

func (s *wikiIngestService) ProcessWikiIngest(ctx context.Context, t *asynq.Task) (retErr error) {
	taskStartedAt := time.Now()
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)

	var payload WikiIngestPayload
	exitStatus := "success"
	mode := "redis"
	pendingOpsCount := 0
	ingestOps := 0
	retractOps := 0
	ingestSucceeded := 0
	ingestFailed := 0
	retractHandled := 0
	followUpScheduled := false
	totalPagesAffected := 0
	docPreview := make([]string, 0, 6)
	// Tunables resolved from KB.WikiConfig once we've loaded the KB.
	// Captured up here so the deferred stats log can observe them
	// regardless of which exit path we took. (Index-intro rebuild moved to
	// the debounced wiki:finalize task in Phase 1, so it's logged there, not
	// here; the exclusive per-KB lock was removed in Phase 3.)
	loggedBatchSize := 0
	loggedMapPar := 0
	loggedReducePar := 0
	loggedMaxInflight := 0

	defer func() {
		logger.Infof(
			ctx,
			"wiki ingest stats: kb=%s tenant=%d retry=%d/%d status=%s elapsed=%s mode=%s pending_ops=%d "+
				"ops(ingest=%d,retract=%d) ingest(success=%d,failed=%d) retract_handled=%d pages(total=%d) "+
				"followup=%v tunables(batch=%d,map_par=%d,reduce_par=%d,max_inflight=%d) preview=%s",
			payload.KnowledgeBaseID,
			payload.TenantID,
			retryCount,
			maxRetry,
			exitStatus,
			time.Since(taskStartedAt).Round(time.Millisecond),
			mode,
			pendingOpsCount,
			ingestOps,
			retractOps,
			ingestSucceeded,
			ingestFailed,
			retractHandled,
			totalPagesAffected,
			followUpScheduled,
			loggedBatchSize,
			loggedMapPar,
			loggedReducePar,
			loggedMaxInflight,
			previewStringSlice(docPreview, 6),
		)
	}()

	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		exitStatus = "invalid_payload"
		return fmt.Errorf("wiki ingest: unmarshal payload: %w", err)
	}
	if !validWikiStartKnowledgeID(payload.KnowledgeID) {
		exitStatus = "invalid_payload"
		return errors.New("wiki ingest: invalid selected knowledge ID")
	}

	defer func() {
		if p := recover(); p != nil {
			// Asynq retries panics too: retain the outstanding reservation.
			panic(p)
		}
		if retErr == nil {
			s.releaseFollowUpTask(ctx, payload.KnowledgeBaseID)
		}
	}()

	// Inject context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// A soft-deleted tenant owns no reachable workspace anymore; its KBs and
	// durable pending ops survive the deletion, so without this guard the
	// batch would keep issuing model requests (and finalize would rebuild
	// index pages) for a tenant nobody can see (#3593). Drop the KB's queue.
	if s.tenantIsDeleted(ctx, payload.TenantID) {
		exitStatus = "tenant_deleted"
		if err := s.clearDeletedKnowledgeBasePendingOps(ctx, payload.KnowledgeBaseID); err != nil {
			return fmt.Errorf("wiki ingest: clear deleted tenant queue: %w", err)
		}
		return nil
	}

	// Concurrency model (Phase 3):
	//
	//   - Standard (Redis) mode: NO exclusive per-KB lock. Multiple batches
	//     for the same KB may run at once, each claiming a DISJOINT set of
	//     rows via claimPendingList (below). This lets one KB's backlog fan
	//     out across the whole wiki worker pool instead of draining one
	//     5-doc batch at a time. Same-slug reduce safety is provided by
	//     withSlugLock, not by a batch-wide lock.
	//
	//   - Lite mode (no Redis, single process): keep the in-process
	//     liteLocks guard so exactly one batch per KB runs at a time. Lite
	//     targets small/local scale where serial-per-KB is simplest and the
	//     claiming machinery (which needs FOR UPDATE SKIP LOCKED) buys
	//     nothing.
	if s.redisClient == nil {
		mode = "lite"
		if _, loaded := s.liteLocks.LoadOrStore(payload.KnowledgeBaseID, struct{}{}); loaded {
			exitStatus = "active_lock_conflict"
			logger.Infof(ctx, "wiki ingest: another batch active for KB %s (lite lock), deferring to asynq retry",
				payload.KnowledgeBaseID)
			return ErrWikiIngestConcurrent
		}
		defer s.liteLocks.Delete(payload.KnowledgeBaseID)
	}

	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if errors.Is(err, apprepo.ErrKnowledgeBaseNotFound) || (err == nil && kb == nil) {
		exitStatus = "kb_deleted"
		if cleanupErr := s.clearDeletedKnowledgeBasePendingOps(ctx, payload.KnowledgeBaseID); cleanupErr != nil {
			return fmt.Errorf("wiki ingest: clear deleted KB queue: %w", cleanupErr)
		}
		return nil
	}
	if err != nil {
		exitStatus = "get_kb_failed"
		return fmt.Errorf("wiki ingest: get KB: %w", err)
	}
	if kb.TenantID != 0 {
		if payload.TenantID != 0 && payload.TenantID != kb.TenantID {
			exitStatus = "tenant_mismatch"
			return fmt.Errorf("wiki ingest: payload tenant %d does not match KB tenant %d", payload.TenantID,
				kb.TenantID)
		}
		if payload.TenantID == 0 {
			payload.TenantID = kb.TenantID
			ctx = context.WithValue(ctx, types.TenantIDContextKey, kb.TenantID)
			if s.tenantIsDeleted(ctx, kb.TenantID) {
				exitStatus = "tenant_deleted"
				if cleanupErr := s.clearDeletedKnowledgeBasePendingOps(ctx,
					payload.KnowledgeBaseID); cleanupErr != nil {
					return fmt.Errorf("wiki ingest: clear deleted tenant queue: %w", cleanupErr)
				}
				return nil
			}
		}
	}
	if !kb.IsWikiEnabled() {
		exitStatus = "kb_not_wiki_enabled"
		return s.releaseIngestForUnavailableWiki(ctx, kb.ID, "wiki disabled")
	}
	if !kb.IsWikiAutoGenerateEnabled() {
		exitStatus = "auto_generation_disabled"
		if err := s.releaseIngestForUnavailableWiki(ctx, kb.ID, "automatic generation disabled"); err != nil {
			return err
		}
		// Source deletion already reconciles page references synchronously.
		// Its queued prose rewrites must not run later and overwrite manual pages.
		discarder, ok := s.pendingRepo.(interfaces.TaskPendingOpsDiscarder)
		if !ok {
			return errors.New("wiki manual mode requires unclaimed operation cleanup")
		}
		return discarder.DiscardUnclaimed(ctx, wikiTaskType, wikiTaskScope, kb.ID,
			WikiOpRetract, time.Now().Add(-wikiClaimStaleAfter))
	}

	var synthesisModelID string
	if kb.WikiConfig != nil {
		synthesisModelID = kb.WikiConfig.SynthesisModelID
	}
	if synthesisModelID == "" {
		synthesisModelID = kb.SummaryModelID
	}
	if synthesisModelID == "" {
		exitStatus = "missing_synthesis_model"
		return s.releaseIngestForUnavailableWiki(ctx, kb.ID, "no synthesis model configured")
	}
	chatModel, err := s.modelService.GetChatModel(ctx, synthesisModelID)
	if errors.Is(err, ErrModelNotFound) {
		exitStatus = "synthesis_model_not_found"
		return s.releaseIngestForUnavailableWiki(ctx, kb.ID, "synthesis model "+synthesisModelID+" not found")
	}
	if err != nil {
		exitStatus = "get_chat_model_failed"
		if isFinalAsynqAttempt(ctx) {
			// The model row exists but cannot be built (a base URL the SSRF
			// guard rejects, an unknown provider, ...). That fails the same
			// way on every trigger and before any op is claimed, so no op
			// ever spends its fail_count budget: without this the KB's
			// documents stayed in "finalizing" forever while housekeeping
			// kept re-arming the trigger.
			return s.releaseIngestForUnavailableWiki(ctx, kb.ID,
				"synthesis model "+synthesisModelID+" unusable: "+err.Error())
		}
		return fmt.Errorf("wiki ingest: get chat model: %w", err)
	}

	// Resolve per-KB tunables once. WikiConfig.IngestBatchSize /
	// IngestMapParallel / IngestReduceParallel let operators on
	// 4w-document KBs raise the throughput knob (more docs per batch +
	// more concurrent LLM calls) without a code deploy. Zero falls back
	// to the historical defaults so existing KBs see no behaviour
	// change until they opt in.
	batchSize := kb.WikiConfig.IngestBatchSizeOrDefault(wikiMaxDocsPerBatch)
	mapParallel := kb.WikiConfig.IngestMapParallelOrDefault(10)
	reduceParallel := kb.WikiConfig.IngestReduceParallelOrDefault(10)
	loggedBatchSize = batchSize
	loggedMapPar = mapParallel
	loggedReducePar = reduceParallel

	lang := types.LanguageNameFromContext(ctx)

	// Per-KB in-flight cap (Phase 4, standard mode): keep one KB's bulk
	// import from monopolizing the shared wiki pool. If the KB is already at
	// its cap we schedule a coalesced retry and bail without claiming, so the
	// rows stay available for whichever running batch frees a slot first.
	maxInflight := kb.WikiConfig.IngestMaxInflightOrDefault(wikiInflightDefault)
	loggedMaxInflight = maxInflight
	releaseSlot, gotSlot := s.reserveInflightSlot(ctx, payload.KnowledgeBaseID, maxInflight)
	if !gotSlot {
		exitStatus = "inflight_cap"
		logger.Infof(ctx, "wiki ingest: KB %s at in-flight cap (%d), rescheduling", payload.KnowledgeBaseID,
			maxInflight)
		s.scheduleCappedRetry(ctx, payload)
		return nil
	}
	defer releaseSlot()

	// Standard mode claims rows (marks claimed_at, disjoint across concurrent
	// batches); ordinary Lite batches peek under their in-process lock.
	// A selected-document trigger claims that whole dedup key in either mode.
	var pendingOps []WikiPendingOp
	var peekedIDs []int64
	var loadErr error
	switch {
	case payload.KnowledgeID != "":
		claimer, ok := s.pendingRepo.(interfaces.TaskPendingOpsKnowledgeClaimer)
		if !ok {
			return errors.New("wiki ingest: selected document claims are unavailable")
		}
		rows, err := claimer.ClaimKnowledge(ctx, wikiTaskType, wikiTaskScope, payload.KnowledgeBaseID,
			payload.KnowledgeID, time.Now().Add(-wikiClaimStaleAfter))
		loadErr = err
		if err == nil {
			pendingOps, peekedIDs = s.decodePendingRows(ctx, rows)
		}
	case s.redisClient != nil:
		pendingOps, peekedIDs, loadErr = s.claimPendingList(ctx, payload.KnowledgeBaseID, batchSize)
	default:
		pendingOps, peekedIDs, loadErr = s.peekPendingList(ctx, payload.KnowledgeBaseID, batchSize)
	}
	if loadErr != nil {
		// Transient failure loading the pending list. Return an error so
		// asynq retries this trigger with backoff — acking here would strand
		// the queue until an unrelated upload happened to re-trigger it.
		exitStatus = "load_pending_failed"
		logger.Warnf(ctx, "wiki ingest: failed to load pending list for KB %s: %v", payload.KnowledgeBaseID, loadErr)
		return fmt.Errorf("wiki ingest: load pending list: %w", loadErr)
	}
	pendingOpsCount = len(pendingOps)
	if len(pendingOps) == 0 {
		exitStatus = "no_pending_ops"
		logger.Infof(ctx, "wiki ingest: no pending operations for KB %s", payload.KnowledgeBaseID)
		// We claimed nothing, but rows may still exist held by a FRESH claim
		// (a concurrent batch that is still running, or one that crashed
		// mid-flight and left claimed_at stamped). This no-op return does not
		// chain a follow-up, so without a safety net a crashed batch's rows
		// would sit unclaimable until wikiClaimStaleAfter AND never get
		// re-triggered afterwards — stranding the KB indefinitely. Schedule a
		// coalesced recheck timed past the stale threshold so those rows are
		// reclaimed automatically once eligible.
		followUpScheduled = s.scheduleStaleClaimRecheck(ctx, payload)
		return nil
	}

	logger.Infof(ctx, "wiki ingest: batch processing %d ops for KB %s", len(pendingOps), payload.KnowledgeBaseID)
	// The parse attempt each document's op was queued under, for the slot
	// release after reduce (see finalizeWikiSubtask).
	opAttempts := make(map[string]int, len(pendingOps))
	for _, op := range pendingOps {
		if op.Op == WikiOpIngest {
			opAttempts[op.KnowledgeID] = op.Attempt
		}
	}

	// Crash/abort safety net (standard/claim mode only). If this batch exits
	// abnormally — panic, ctx timeout, or an early error return — BEFORE it
	// settles its claimed rows (trim + requeueFailedOps), release the claims
	// so the next trigger can re-claim within seconds instead of waiting out
	// wikiClaimStaleAfter (~90m). On the normal path claimsSettled flips true
	// once the rows reach their terminal state, making this a no-op. Uses a
	// bounded detached cleanup context because ctx may already be cancelled on
	// the timeout path. Ordinary Lite batches peek without claiming.
	claimsSettled := false
	if (s.redisClient != nil || payload.KnowledgeID != "") && len(peekedIDs) > 0 {
		defer func() {
			if claimsSettled {
				return
			}
			releaseCtx, releaseCancel := wikiIngestCleanupContext(ctx)
			defer releaseCancel()
			if err := s.pendingRepo.ReleaseByIDs(releaseCtx, peekedIDs); err != nil {
				logger.Warnf(ctx, "wiki ingest: failed to release %d claims on abnormal exit for KB %s: %v",
					len(peekedIDs), payload.KnowledgeBaseID, err)
				return
			}
			logger.Warnf(ctx,
				"wiki ingest: released %d claimed rows on abnormal exit for KB %s (re-claimable immediately)",
				len(peekedIDs), payload.KnowledgeBaseID)
		}()
	}

	// Resolve extraction granularity once per batch. Historical rows with
	// empty/unknown values fall back to Standard via Normalize(). Failures
	// to load the KB (unlikely since we're already acting on it) also
	// degrade gracefully to Standard.
	wikiConfig := kb.WikiConfig
	if freshKB, kbErr := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID); kbErr == nil &&
		freshKB != nil && freshKB.WikiConfig != nil {
		wikiConfig = freshKB.WikiConfig
	}

	batchCtx := s.newWikiBatchContext(payload.KnowledgeBaseID, wikiConfig)

	// 1. MAP PHASE (Parallel extraction and generation of updates)
	var mapMu sync.Mutex
	var failedOps []WikiPendingOp
	slugUpdates := make(map[string][]SlugUpdate)
	var docResults []*docIngestResult
	var retractFolderIDs []string
	// rateLimited flips true when any map/reduce LLM failure looks like an
	// upstream 429/quota trip. It steers the follow-up scheduler onto the
	// longer wikiRateLimitBackoff so retries don't keep hammering an already
	// saturated rpm budget. Guarded by mapMu (map phase) / reduceMu (reduce
	// phase) — both are held when written.
	rateLimited := false

	eg, mapCtx := errgroup.WithContext(ctx)
	eg.SetLimit(mapParallel) // Map phase limit (configurable via WikiConfig)

	for _, op := range pendingOps {
		op := op
		eg.Go(func() error {
			if op.Op == WikiOpRetract {
				// Resolve the authoritative page set at run-time. The caller
				// (knowledgeService.cleanupWikiOnKnowledgeDelete) captures
				// PageSlugs from a DB snapshot taken *before* this task fires,
				// but there is a window where:
				//   - cleanup ran before ingest → snapshot is empty, but a
				//     concurrent ingest may have already created pages by now
				//   - a previous ingest batch created new pages after cleanup
				//     captured its snapshot
				// Re-querying ListPagesBySourceRef here unions the caller's
				// slugs with whatever currently references the knowledge, so
				// no page is left un-retracted. It also lets us support
				// callers that deliberately enqueue retract with empty
				// PageSlugs as "figure it out yourself" — see
				// cleanupWikiOnKnowledgeDelete's comment (3).
				slugSet := make(map[string]struct{}, len(op.PageSlugs))
				folderSet := make(map[string]struct{}, len(op.FolderIDs))
				for _, slug := range op.PageSlugs {
					if slug == "" {
						continue
					}
					slugSet[slug] = struct{}{}
				}
				for _, folderID := range op.FolderIDs {
					if folderID != "" {
						folderSet[folderID] = struct{}{}
					}
				}
				if op.KnowledgeID != "" {
					livePages, err := s.wikiService.ListPagesBySourceRef(mapCtx, payload.KnowledgeBaseID,
						op.KnowledgeID)
					if err != nil {
						logger.Warnf(mapCtx, "wiki ingest: retract lookup failed for %s: %v", op.KnowledgeID, err)
					} else {
						for _, p := range livePages {
							if p == nil || p.Slug == "" {
								continue
							}
							// Index pages never carry real source_refs;
							// if they somehow surface here, skip — the
							// reduce stage would be a no-op anyway.
							if p.PageType == types.WikiPageTypeIndex {
								continue
							}
							slugSet[p.Slug] = struct{}{}
							if p.FolderID != "" {
								folderSet[p.FolderID] = struct{}{}
							}
						}
					}
				}

				mapMu.Lock()
				retractOps++
				retractHandled++
				docPreview = append(docPreview, fmt.Sprintf("retract[%s]: %s (%d slugs)", previewText(op.KnowledgeID,
					24), previewText(op.DocTitle, 48), len(slugSet)))

				for slug := range slugSet {
					slugUpdates[slug] = append(slugUpdates[slug], SlugUpdate{
						Slug:              slug,
						Type:              "retract",
						RetractDocContent: op.DocSummary,
						DocTitle:          op.DocTitle,
						KnowledgeID:       op.KnowledgeID,
						Language:          types.ResolveLanguageName(ctx, op.Language),
					})
				}
				for folderID := range folderSet {
					retractFolderIDs = append(retractFolderIDs, folderID)
				}
				mapMu.Unlock()
				return nil
			}

			// Ingest
			mapMu.Lock()
			ingestOps++
			mapMu.Unlock()

			logger.Infof(mapCtx, "wiki ingest: processing document '%s' (%s)", op.DocTitle, op.KnowledgeID)
			result, updates, err := s.mapOneDocumentRecovered(mapCtx, chatModel, payload, op, batchCtx)
			if err != nil {
				mapMu.Lock()
				ingestFailed++
				failedOps = append(failedOps, op)
				if isLikelyRateLimitError(err) {
					rateLimited = true
				}
				mapMu.Unlock()
				logger.Warnf(mapCtx, "wiki ingest: failed to map knowledge %s: %v", op.KnowledgeID, err)
				return nil // Don't fail the whole batch
			}

			if result != nil {
				mapMu.Lock()
				ingestSucceeded++
				docResults = append(docResults, result)
				docPreview = append(docPreview, fmt.Sprintf("ingest[%s]: title=%s summary=%s",
					previewText(result.KnowledgeID, 24), previewText(result.DocTitle, 40), previewText(result.Summary,
						64)))
				for _, u := range updates {
					slugUpdates[u.Slug] = append(slugUpdates[u.Slug], u)
				}
				mapMu.Unlock()

				// No fail-count reset needed: a successful op is added
				// to peekedIDs and gets DELETEd from task_pending_ops at
				// trim time, so there is no stale fail_count column to
				// scrub. Compare with the legacy Redis path, which kept
				// a separate wiki:failcount:<...> key alive for 24h
				// regardless of whether the original op had drained.
				//
				// The finalizing slot is drained later (after reduce +
				// publish) in the docResults loop, so "completed" only
				// arrives once wiki is fully written.
			} else {
				// err == nil && result == nil: mapOneDocument skipped this
				// doc at a terminal, non-retryable state (knowledge
				// deleted / no chunks / insufficient text). It produces no
				// docResult and is not a failedOp, so neither the success
				// nor the dead-letter drain path will fire. Release the
				// finalizing slot here so the row doesn't hang in
				// "finalizing" until the housekeeping sweep marks it
				// failed. The matching +1 was seeded by
				// KnowledgePostProcess.SetFinalizing.
				s.finalizeWikiSubtask(mapCtx, op.KnowledgeID, op.Attempt)
			}
			return nil
		})
	}
	_ = eg.Wait()

	// Re-read identity claims after every map worker has chosen a slug so
	// concurrent romanizations of the same title collapse before taxonomy
	// planning and Reduce lock by slug.
	slugUpdates = s.remapSlugUpdatesByIdentity(ctx, payload.KnowledgeBaseID, slugUpdates, batchCtx)

	// Plan the directory once for the whole batch BEFORE reduce. Reduce writes
	// pages in parallel, so it can't converge on shared folders on its own; this
	// single pass assigns every new entity/concept slug a coherent category_path
	// that reuses existing folders. Reduce then only applies the plan to pages
	// that don't already have a category (user-curated pages are never churned).
	batchCtx.PlannedFolderID = s.resolvePlannedFolders(ctx, kb,
		s.planBatchTaxonomy(ctx, chatModel, kb, slugUpdates, lang))

	// 2. REDUCE PHASE (Parallel upserting grouped by Slug)
	egReduce, reduceCtx := errgroup.WithContext(ctx)
	egReduce.SetLimit(reduceParallel) // Reduce phase limit (LLM + DB concurrent connections, configurable)

	var reduceMu sync.Mutex
	var allPagesAffected []string
	var ingestPagesAffected []string
	var retractPagesAffected []string
	// failedAdditionSlugs collects entity/concept slugs whose page
	// generation LLM call failed (so the page was never written). The
	// post-reduce cleanup step uses this set to strip dead [[slug]]
	// references from the same batch's summary pages and exclude failed
	// pages from finalize processing.
	failedAdditionSlugs := make(map[string]struct{})
	// unappliedSlugKIDs collects the knowledge_ids that contributed to a
	// slug whose update never landed — either because we could NOT acquire
	// the per-slug lock within wikiSlugLockWait, or because reduce returned
	// an error. In both cases the page keeps its prior content, so the
	// owning document(s) must be re-queued rather than trimmed — otherwise
	// the row is deleted and the contribution is silently lost forever
	// (finalize only rebuilds the index / cross-links, it does not re-run
	// reduce). requeueFailedOps' fail_count budget bounds the retries and
	// dead-letters a document whose slug fails/stays hot permanently.
	unappliedSlugKIDs := make(map[string]struct{})
	// collectUnapplied records every knowledge_id backing a slug we failed
	// to apply. Caller holds no lock; it takes reduceMu itself.
	collectUnapplied := func(updates []SlugUpdate) {
		reduceMu.Lock()
		for _, u := range updates {
			if u.KnowledgeID != "" {
				unappliedSlugKIDs[u.KnowledgeID] = struct{}{}
			}
		}
		reduceMu.Unlock()
	}

	// Build the kid → wikiSpan lookup before kicking off reduce. Each
	// per-slug reduce attaches a postprocess.wiki.page[slug] subspan
	// under the FIRST contributing doc's wiki span — see comment in
	// reduceSlugUpdates for the multi-contributor attribution rule.
	kidToWikiSpan := make(map[string]*Span, len(docResults))
	for _, r := range docResults {
		if r != nil && r.WikiSpan != nil {
			kidToWikiSpan[r.KnowledgeID] = r.WikiSpan
		}
	}

	for slug, updates := range slugUpdates {
		slug := slug
		updates := updates
		egReduce.Go(func() error {
			var (
				changed        bool
				affectedType   string
				additionFailed bool
				updateDeferred bool
				reduceErr      error
			)
			// Serialize same-slug read-modify-write across concurrent batches
			// (standard mode). runs fn directly in Lite mode.
			acquired, lockErr := s.withSlugLock(reduceCtx, payload.KnowledgeBaseID, slug, func() error {
				// errgroup does not recover panics; see mapOneDocumentRecovered.
				defer func() {
					if r := recover(); r != nil {
						logger.Errorf(reduceCtx, "wiki ingest: reduce panicked for slug %s: %v\n%s",
							slug, r, debug.Stack())
						reduceErr = fmt.Errorf("wiki reduce panicked for slug %s: %v", slug, r)
					}
				}()
				changed, affectedType, additionFailed, updateDeferred, reduceErr = s.reduceSlugUpdates(
					reduceCtx, chatModel, payload.KnowledgeBaseID, slug, updates, payload.TenantID, batchCtx,
					kidToWikiSpan)
				// 归并失败交给下方统一分类；这里仅返回获取锁的错误。
				return nil
			})
			if lockErr != nil {
				collectUnapplied(updates)
				// ctx cancelled (batch timeout / shutdown) — stop quietly.
				return nil
			}
			if !acquired {
				// Contended slug we couldn't get in time. The page keeps its
				// prior content, so the documents that fed this slug are NOT
				// done: record their knowledge_ids so the trim phase re-queues
				// them (via the failed-op retry budget) for a later,
				// hopefully-uncontended batch instead of deleting their rows.
				logger.Warnf(reduceCtx, "wiki ingest: slug %s busy > %s, deferring update", slug, wikiSlugLockWait)
				collectUnapplied(updates)
				return nil
			}
			if updateDeferred {
				// The page kept its previous body because the rewrite dropped
				// table rows that are still on it. Not an error and the page is
				// not damaged, but this slug's contribution never landed, so the
				// documents behind it must survive the trim phase and come back
				// in a later batch — trimming them here would delete their
				// pending row and lose the addition for good.
				logger.Warnf(reduceCtx,
					"wiki ingest: slug %s kept its previous content (rewrite dropped rows), deferring update",
					slug)
				collectUnapplied(updates)
			}
			if reduceErr != nil {
				// The page's read-modify-write failed, so this slug's update
				// never landed. Re-queue the contributing documents (same
				// fail_count budget as a map failure) so a later batch retries
				// instead of silently dropping the row at trim time.
				logger.Warnf(reduceCtx, "wiki ingest: reduce failed for slug %s: %v", slug, reduceErr)
				collectUnapplied(updates)
				if isLikelyRateLimitError(reduceErr) {
					reduceMu.Lock()
					rateLimited = true
					reduceMu.Unlock()
				}
			}
			if changed {
				reduceMu.Lock()
				allPagesAffected = append(allPagesAffected, slug)
				switch affectedType {
				case "ingest":
					ingestPagesAffected = append(ingestPagesAffected, slug)
				case "retract":
					retractPagesAffected = append(retractPagesAffected, slug)
				}
				reduceMu.Unlock()
			}
			if additionFailed {
				reduceMu.Lock()
				failedAdditionSlugs[slug] = struct{}{}
				reduceMu.Unlock()
			}
			return nil
		})
	}
	_ = egReduce.Wait()

	tailCtx, tailCancel := wikiIngestCleanupContext(ctx)
	defer tailCancel()

	// Sanitize the doc summary pages produced by this batch BEFORE we
	// rebuild the index. The summary LLM (run during
	// map) was free to inject [[entity/foo|name]] links to every slug it
	// saw extracted, but reduce may have failed to materialize some of
	// those slugs into actual pages. Rewrite those dead links to plain
	// text so the summary doesn't contain unresolvable references.
	if len(failedAdditionSlugs) > 0 && len(docResults) > 0 {
		s.sanitizeDeadSummaryLinks(tailCtx, payload.KnowledgeBaseID, docResults, failedAdditionSlugs, batchCtx)
	}

	totalPagesAffected = len(allPagesAffected)

	// Project one bounded summary into the knowledge-base activity feed.
	// Detailed per-document Wiki log rows duplicated that feed and were never
	// consumed by retrieval, so the activity record is now written directly.
	wikiActivityActions := make(map[string]int, 2)
	for _, op := range pendingOps {
		if op.Op == WikiOpRetract {
			wikiActivityActions["retract"]++
		}
	}
	for _, r := range docResults {
		if r != nil {
			wikiActivityActions["ingest"]++
		}
	}
	RecordWikiContentActivity(tailCtx, s.audit, payload.TenantID, payload.KnowledgeBaseID, wikiActivityActions)

	// Publish freshly-generated pages immediately (NOT deferred to finalize):
	// users should see a document's wiki pages as soon as their content is
	// written, not after the debounce window. This is a cheap status flip.
	if len(allPagesAffected) > 0 {
		logger.Infof(ctx, "wiki ingest: publishing draft pages")
		s.publishDraftPages(tailCtx, payload.KnowledgeBaseID, allPagesAffected)
	}

	// Defer KB-global convergence (index-intro rebuild + dead-link cleanup +
	// cross-link injection) to a debounced per-KB wiki:finalize task instead of
	// running it at the tail of every 5-doc batch. We record what changed into
	// the finalize lane and schedule a coalesced trigger; a burst of N documents
	// then rebuilds the index ONCE. See ProcessWikiFinalize.
	//
	// freshTitleBySlug carries the (slug → title) pairs this batch successfully
	// wrote (minus reduce-phase failures) so finalize's cross-link pass can
	// linkify mentions of the new pages.
	freshTitleBySlug := make(map[string]string, len(docResults)*4)
	for _, dr := range docResults {
		if dr == nil {
			continue
		}
		for _, p := range dr.Pages {
			if p.Slug == "" || p.Title == "" {
				continue
			}
			if _, bad := failedAdditionSlugs[p.Slug]; bad {
				continue
			}
			freshTitleBySlug[p.Slug] = p.Title
		}
	}
	if len(allPagesAffected) > 0 || len(docResults) > 0 || retractHandled > 0 || len(retractFolderIDs) > 0 {
		changes := make([]wikiFinalizeChange, 0, len(docResults)+len(pendingOps))
		for _, r := range docResults {
			changes = append(changes, wikiFinalizeChange{
				Action: wikiFinalizeAdded, DocTitle: r.DocTitle, DocSummary: r.Summary,
			})
		}
		for _, op := range pendingOps {
			if op.Op == WikiOpRetract {
				changes = append(changes, wikiFinalizeChange{
					Action: wikiFinalizeRemoved, DocTitle: op.DocTitle, DocSummary: op.DocSummary,
				})
			}
		}
		s.enqueueFinalize(tailCtx, payload, allPagesAffected, freshTitleBySlug, changes, retractFolderIDs)
	}

	// Close postprocess.wiki spans for every successfully-mapped doc.
	// Span duration now spans map + reduce + index rebuild + cleanup +
	// cross-link injection + publish, matching the wall-clock window
	// the user thinks of as "wiki processing for this knowledge".
	// Per-doc page write outcomes are summarised in the output so the
	// trace viewer can show how many of the doc's extracted pages
	// actually landed (vs. dropped because reduce-phase generation
	// failed).
	failedAdditionSlugCount := len(failedAdditionSlugs)
	spanCtx, spanCancel := wikiIngestCleanupContext(ctx)
	for _, r := range docResults {
		if r == nil {
			continue
		}
		// A successfully-mapped doc is terminal for its wiki op, so
		// release the knowledge's slot in pending_subtasks_count (the row
		// promotes to completed once the counter hits zero). Done before
		// the WikiSpan nil-check below so a doc that had no attempt to
		// attach a span to still drains its counter slot. The matching +1
		// is seeded by KnowledgePostProcess.SetFinalizing.
		//
		// EXCEPT docs with an unapplied slug (contended lock or reduce
		// error): those are re-queued below, so keep their finalizing slot
		// held — the retry (or the dead-letter drain in requeueFailedOps)
		// releases it once the op reaches a real terminal state.
		if _, unapplied := unappliedSlugKIDs[r.KnowledgeID]; !unapplied {
			s.finalizeWikiSubtask(ctx, r.KnowledgeID, opAttempts[r.KnowledgeID])
		}
		if r.WikiSpan == nil {
			continue
		}
		writtenPages := make([]map[string]string, 0, len(r.Pages))
		droppedPages := make([]map[string]string, 0)
		for _, p := range r.Pages {
			entry := map[string]string{
				"slug":  p.Slug,
				"title": previewText(p.Title, 80),
			}
			if _, bad := failedAdditionSlugs[p.Slug]; bad {
				droppedPages = append(droppedPages, entry)
				continue
			}
			writtenPages = append(writtenPages, entry)
		}
		output := types.JSONMap{
			"pages_written":         len(writtenPages),
			"pages_dropped":         len(droppedPages),
			"pages_total":           len(r.Pages),
			"failed_slug_writes":    failedAdditionSlugCount,
			"pages_written_preview": writtenPages,
		}
		if len(droppedPages) > 0 {
			output["pages_dropped_preview"] = droppedPages
		}
		for k, v := range r.MapStats {
			output[k] = v
		}
		s.tracker().EndSpan(spanCtx, r.WikiSpan, output)
	}
	spanCancel()
	// Failed-map docs already had FailSpan called inside
	// mapOneDocument (the failedOps path returns before reaching
	// docResults). Nothing extra to do here for them.

	// Fold documents with an unapplied slug (contended lock or reduce error)
	// into failedOps so they are neither trimmed nor promoted to completed:
	// requeueFailedOps then runs them through the same fail_count budget
	// (retry now, dead-letter once the slug stays permanently hot/broken) as
	// a map-phase failure. A doc already counted as a map failure is skipped
	// to avoid a double fail_count bump.
	if len(unappliedSlugKIDs) > 0 {
		failedKIDs := make(map[string]struct{}, len(failedOps))
		for _, op := range failedOps {
			failedKIDs[op.KnowledgeID] = struct{}{}
		}
		for _, op := range pendingOps {
			if _, unapplied := unappliedSlugKIDs[op.KnowledgeID]; !unapplied {
				continue
			}
			if _, already := failedKIDs[op.KnowledgeID]; already {
				continue
			}
			failedOps = append(failedOps, op)
			failedKIDs[op.KnowledgeID] = struct{}{}
		}
	}

	// Build the trim set: rows that should be removed from
	// task_pending_ops. We start from the full peekedIDs (every row we
	// pulled, even ones de-duplicated by knowledge_id) and subtract
	// any failed op's dbID — those need to stay in place so the
	// requeueFailedOps path can decide between retry and dead-letter.
	failedIDSet := make(map[int64]struct{}, len(failedOps))
	for _, op := range failedOps {
		if op.dbID != 0 {
			failedIDSet[op.dbID] = struct{}{}
		}
	}
	trimIDs := make([]int64, 0, len(peekedIDs))
	for _, id := range peekedIDs {
		if _, fail := failedIDSet[id]; fail {
			continue
		}
		trimIDs = append(trimIDs, id)
	}
	settleCtx, settleCancel := wikiIngestCleanupContext(ctx)
	trimErr := s.trimPendingList(settleCtx, trimIDs)

	// Process failed ops: increment fail_count and dead-letter once
	// the cap is hit. Must come AFTER trim so successful siblings are
	// already gone from the queue — otherwise a follow-up batch could
	// re-pick them up.
	var requeueErr error
	if len(failedOps) > 0 {
		requeueErr = s.requeueFailedOps(settleCtx, payload, failedOps)
	}
	settleCancel()
	if err := errors.Join(trimErr, requeueErr); err != nil {
		exitStatus = "settle_failed"
		return fmt.Errorf("wiki ingest: settle claimed rows: %w", err)
	}
	// All claimed rows have now reached a terminal state (deleted on success,
	// released for retry, or dead-lettered), so disarm the abnormal-exit
	// release net.
	claimsSettled = true

	logger.Infof(ctx, "wiki ingest: batch completed for KB %s, %d ops, %d pages affected", payload.KnowledgeBaseID,
		len(pendingOps), len(allPagesAffected))

	// Pace the follow-up: on a rate-limit trip, back off so the per-minute
	// window can reset instead of retrying the failed docs immediately.
	followUpDelay := wikiFollowUpDelay
	if rateLimited {
		followUpDelay = wikiRateLimitBackoff
		logger.Warnf(ctx, "wiki ingest: KB %s hit upstream rate limiting, backing off follow-up to %s",
			payload.KnowledgeBaseID, followUpDelay)
	}
	followCtx, followCancel := wikiIngestCleanupContext(ctx)
	// Follow-up fan-out returns to the ordinary backlog after this selected
	// document has settled; it must not keep re-arming the consumed key.
	payload.KnowledgeID = ""
	followUpScheduled = s.scheduleFollowUp(followCtx, payload, followUpDelay, batchSize, maxInflight, rateLimited)
	followCancel()
	return nil
}
