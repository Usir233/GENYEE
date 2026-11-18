package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	werrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/hibiken/asynq"
)

// ReparseKnowledge deletes existing document content and re-parses the knowledge asynchronously.
// This method reuses the logic from UpdateManualKnowledge for resource cleanup and async parsing.
func (s *knowledgeService) ReparseKnowledge(
	ctx context.Context,
	knowledgeID string,
	processOverrides *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	return s.reparseKnowledge(ctx, knowledgeID, processOverrides, false)
}

// reparseKnowledge implements ReparseKnowledge. tasksDequeued tells it the
// caller already dropped the knowledge's queued tasks (ReplaceKnowledgeFile
// does, before pointing the row at the new file), which saves a second scan
// of every queue.
func (s *knowledgeService) reparseKnowledge(
	ctx context.Context,
	knowledgeID string,
	processOverrides *types.KnowledgeProcessOverrides,
	tasksDequeued bool,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start re-parsing knowledge")

	existing, kb, err := loadKnowledgeWrite(ctx, s.repo, s.kbService, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to load knowledge: %v", err)
		return nil, err
	}

	tenantID := existing.TenantID

	// When the caller supplies new overrides (e.g. via the reparse confirm
	// dialog), validate them against this knowledge's file type, then persist
	// to metadata so both this call's enqueue and the worker re-read the same
	// config. nil keeps whatever was stored at upload time.
	if processOverrides != nil {
		if err := ValidateProcessOverrides(ctx, kb, processOverrides, reparseFileTypes(existing)); err != nil {
			return nil, err
		}
		if err := existing.SetProcessOverrides(processOverrides); err != nil {
			logger.Errorf(ctx, "Failed to set process overrides on reparse: %v", err)
			return nil, err
		}
		if err := s.repo.UpdateKnowledgeColumn(ctx, existing.ID, "metadata", existing.Metadata); err != nil {
			logger.Errorf(ctx, "Failed to persist process overrides on reparse: %v", err)
			return nil, err
		}
	}

	processOverrides, _ = existing.ProcessOverrides()
	reparseEff := ResolveProcessConfig(kb, processOverrides)

	var manualContent string
	if existing.IsManual() {
		meta, metaErr := existing.ManualMetadata()
		if metaErr != nil || meta == nil {
			logger.Errorf(ctx, "Failed to get manual metadata for reparse: %v", metaErr)
			return nil, werrors.NewBadRequestError("无法获取手工知识内容")
		}
		manualContent = meta.Content
	} else {
		// Check the parser engine before anything is deleted: parsing runs
		// later in a worker, so a missing engine would otherwise only show
		// up after the old chunks and vectors are already gone.
		if err := s.preflightReparseEngine(ctx, existing, reparseEff); err != nil {
			logger.Warnf(ctx, "Reparse rejected, parser engine unavailable for knowledge %s: %v", knowledgeID, err)
			return nil, err
		}

		// Cleanup synchronously; manual knowledge is cleaned up by its worker.
		logger.Infof(ctx, "Cleaning up existing resources for knowledge: %s", knowledgeID)
		if err := s.cleanupKnowledgeResources(ctx, existing); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"knowledge_id": knowledgeID,
			})
			return nil, err
		}
	}

	// Everything that can reject the reparse has run. From here on the
	// previous run is superseded, so stop it before announcing the new one.
	//
	// A reparse may land while the previous run is still in flight (batch
	// reparse and the API do not gate on status). Its queued tasks would
	// otherwise run against the new attempt: an old ProcessDocument wipes the
	// new chunks and writes "processing" back, an old post-process seeds the
	// counter for subtasks that all drop themselves as superseded.
	if !tasksDequeued && isInFlightParseStatus(existing.ParseStatus) {
		s.dequeueKnowledgeTasks(ctx, existing.ID)
	}

	// Allocate the new span tree attempt only now. Opening it earlier and
	// then rejecting the reparse left the previous run's subtasks seeing a
	// newer attempt: they skipped their drain as superseded and stranded the
	// row in "finalizing". Opening it before the enqueue still means the UI
	// shows the fresh attempt immediately and the worker does not allocate a
	// second one.
	reparseAttempt := 0
	if root, n, err := s.tracker().OpenAttempt(ctx, existing.ID, ""); err == nil && root != nil {
		reparseAttempt = n
	} else if err != nil {
		logger.Warnf(ctx, "[Reparse] OpenAttempt failed for %s: %v (will fall back in worker)", existing.ID, err)
	}

	// Keep wiki's pending queue consistent across both manual and non-manual
	// paths. The destructive work (swapping old wiki contributions for new)
	// happens asynchronously inside mapOneDocument — see its oldPageSlugs
	// handling — once post-process re-enqueues wiki ingest. All we need to
	// do here is stop any stale pending ingest op from firing against the
	// pre-reparse chunk set. The op's finalizing slot is not released: the
	// reset below zeroes the counter. Scrub even when wiki is off now: an op
	// left by an earlier wiki-enabled run would otherwise drain a slot of
	// the new attempt.
	s.prepareWikiForReparse(ctx, existing)
	recordReparseStarted := func() {
		recordKBActivity(ctx, s.audit, tenantID, existing.KnowledgeBaseID, types.AuditActionKnowledgeReparseStarted,
			"knowledge", existing.ID, types.AuditOutcomeAccepted,
			map[string]any{"title": existing.Title, "type": existing.Type, "attempt": reparseAttempt})
	}

	// For manual knowledge, use async manual processing (cleanup + re-indexing in worker)
	if existing.IsManual() {
		resetKnowledgeForReparse(existing, kb)

		if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
			logger.Errorf(ctx, "Failed to update knowledge status before reparse: %v", err)
			return nil, err
		}
		if err := s.repo.UpdateKnowledgeColumn(ctx, existing.ID, "pending_subtasks_count", 0); err != nil {
			logger.Errorf(ctx, "Failed to reset pending_subtasks_count before reparse: %v", err)
			return nil, err
		}

		if _, err := s.enqueueManualProcessing(ctx, existing, manualContent, true); err != nil {
			logger.Errorf(ctx, "Failed to enqueue manual reparse task: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}
		recordReparseStarted()
		return existing, nil
	}

	// Step 2: Update knowledge status and metadata
	resetKnowledgeForReparse(existing, kb)

	if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge status before reparse: %v", err)
		return nil, err
	}
	if err := s.repo.UpdateKnowledgeColumn(ctx, existing.ID, "pending_subtasks_count", 0); err != nil {
		logger.Errorf(ctx, "Failed to reset pending_subtasks_count before reparse: %v", err)
		return nil, err
	}

	// Step 3: Trigger async re-parsing based on knowledge type
	logger.Infof(ctx, "Knowledge status updated, scheduling async reparse, ID: %s, Type: %s", existing.ID,
		existing.Type)

	// For file-based knowledge, enqueue document processing task
	if existing.FilePath != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		enableMultimodel := reparseEff.EnableMultimodel
		enableQuestionGeneration := reparseEff.QuestionGenerationConfig.Enabled
		questionCount := reparseEff.QuestionGenerationConfig.QuestionCount
		if questionCount <= 0 {
			questionCount = 3
		}

		lang := types.LanguageFromContextOrDefault(ctx)
		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              existing.ID,
			KnowledgeBaseID:          existing.KnowledgeBaseID,
			FilePath:                 existing.FilePath,
			FileName:                 existing.FileName,
			FileType:                 getFileType(existing.FileName),
			EnableMultimodel:         enableMultimodel,
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
			Language:                 lang,
			Attempt:                  reparseAttempt,
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal reparse task payload: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}

		task := asynq.NewTask(
			types.TypeDocumentProcess,
			payloadBytes,
			documentProcessTaskOptions(s.config, asynq.MaxRetry(3))...,
		)
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue reparse task: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}
		logger.Infof(ctx, "Enqueued reparse task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, existing.ID)
		recordReparseStarted()

		// For data tables (csv, xlsx, xls), also enqueue summary task
		enqueueDataTableSummaryIfNeeded(ctx, s.task, tenantID, existing.ID, existing.FileName, existing.FileType,
			kb.SummaryModelID, kb.EmbeddingModelID)

		return existing, nil
	}

	// For file-URL-based knowledge, enqueue document processing task with FileURL field
	if existing.Type == "file_url" && existing.Source != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		enableMultimodel := reparseEff.EnableMultimodel
		enableQuestionGeneration := reparseEff.QuestionGenerationConfig.Enabled
		questionCount := reparseEff.QuestionGenerationConfig.QuestionCount
		if questionCount <= 0 {
			questionCount = 3
		}

		lang := types.LanguageFromContextOrDefault(ctx)
		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              existing.ID,
			KnowledgeBaseID:          existing.KnowledgeBaseID,
			FileURL:                  existing.Source,
			FileName:                 existing.FileName,
			FileType:                 existing.FileType,
			EnableMultimodel:         enableMultimodel,
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
			Language:                 lang,
			Attempt:                  reparseAttempt,
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal file URL reparse task payload: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}

		task := asynq.NewTask(
			types.TypeDocumentProcess,
			payloadBytes,
			documentProcessTaskOptions(s.config)...,
		)
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue file URL reparse task: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}
		logger.Infof(ctx, "Enqueued file URL reparse task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue,
			existing.ID)
		recordReparseStarted()

		enqueueDataTableSummaryIfNeeded(ctx, s.task, tenantID, existing.ID, existing.FileName, existing.FileType,
			kb.SummaryModelID, kb.EmbeddingModelID)

		return existing, nil
	}

	// For URL-based knowledge, enqueue URL processing task
	if existing.Type == "url" && existing.Source != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		enableMultimodel := reparseEff.EnableMultimodel
		enableQuestionGeneration := reparseEff.QuestionGenerationConfig.Enabled
		questionCount := reparseEff.QuestionGenerationConfig.QuestionCount
		if questionCount <= 0 {
			questionCount = 3
		}

		lang := types.LanguageFromContextOrDefault(ctx)
		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              existing.ID,
			KnowledgeBaseID:          existing.KnowledgeBaseID,
			URL:                      existing.Source,
			EnableMultimodel:         enableMultimodel,
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
			Language:                 lang,
			Attempt:                  reparseAttempt,
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal URL reparse task payload: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}

		task := asynq.NewTask(
			types.TypeDocumentProcess,
			payloadBytes,
			documentProcessTaskOptions(s.config, asynq.MaxRetry(3))...,
		)
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue URL reparse task: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}
		logger.Infof(ctx, "Enqueued URL reparse task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, existing.ID)
		recordReparseStarted()

		return existing, nil
	}

	logger.Warnf(ctx, "Knowledge %s has no parseable content (no file, URL, or manual content)", knowledgeID)
	existing.ParseStatus = types.ParseStatusFailed
	existing.ErrorMessage = "Knowledge has no parseable content"
	if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
		logger.Errorf(ctx, "Failed to persist unparseable knowledge state for %s: %v",
			secutils.SanitizeForLog(knowledgeID), err)
	}
	return existing, werrors.NewBadRequestError("Knowledge has no parseable content")
}

// resetKnowledgeForReparse makes the top-level knowledge state describe the
// new processing attempt rather than retaining terminal state from the
// previous one.
func resetKnowledgeForReparse(knowledge *types.Knowledge, kb *types.KnowledgeBase) {
	knowledge.ParseStatus = types.ParseStatusPending
	knowledge.EnableStatus = "disabled"
	knowledge.Description = ""
	knowledge.ProcessedAt = nil
	knowledge.ErrorMessage = ""
	knowledge.EmbeddingModelID = kb.EmbeddingModelID
	// UpdateKnowledge deliberately omits pending_subtasks_count, so callers
	// must still persist this reset through an explicit column update.
	knowledge.PendingSubtasksCount = 0
}

// CancelKnowledgeParse marks an in-progress parse as cancelled by the user.
//
// Semantics (kept aligned with the existing deleting path, but partial work
// is preserved instead of cleaned up):
//   - parse_status is set to "cancelled"; partial chunks/index already written
//     to the database remain on disk. The user can re-trigger parsing via the
//     existing ReparseKnowledge API, which overwrites status back to pending.
//   - Any in-flight worker reads the new status at its next checkpoint and
//     bails (see processChunks / ProcessDocument / downstream handlers).
//   - The asynq inspector (if available) dequeues pending / scheduled / retry
//     tasks for this knowledge_id across all queues used by the pipeline
//     and signals active workers to stop. Lite mode (no Redis) skips the
//     dequeue step — the checkpoint-based abort is the only stop signal there.
//   - Idempotent: re-calling on an already-cancelled row is a no-op.
//
// Errors:
//   - ParseStatusCompleted / ParseStatusFailed: the parse has already finished.
//   - ParseStatusDeleting: a delete is in progress; cancel cannot supersede it.
func (s *knowledgeService) CancelKnowledgeParse(
	ctx context.Context, knowledgeID string,
) (*types.Knowledge, error) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	existing, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "CancelKnowledgeParse: failed to load knowledge: %v", err)
		return nil, err
	}
	if existing == nil {
		return nil, werrors.NewNotFoundError("knowledge not found")
	}

	switch existing.ParseStatus {
	case types.ParseStatusCancelled:
		// Idempotent — still attempt the dequeue in case earlier calls
		// raced an enqueue, but skip the row update / span close path.
		s.dequeueKnowledgeTasks(ctx, knowledgeID)
		return existing, nil
	case types.ParseStatusCompleted, types.ParseStatusFailed:
		return nil, werrors.NewBadRequestError("解析已结束，无法取消")
	case types.ParseStatusDeleting:
		return nil, werrors.NewBadRequestError("知识正在删除中，无法取消解析")
	case types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing:
		// Cancellable. `finalizing` is the post-process fan-out window
		// where graph-extract / summary / question subtasks are still
		// running; cancel here stops the LLM cost they would burn.
	default:
		// Unknown status — let it through but log. Should never happen
		// outside test fixtures or hand-edited rows.
		logger.Warnf(ctx, "CancelKnowledgeParse: unexpected status %q for %s, proceeding",
			existing.ParseStatus, knowledgeID)
	}

	// Flip the row to cancelled and zero the enrichment counter in one
	// update so a late subtask FinalizeSubtask call can't race-promote
	// the row back to completed. Persisted partial data is left in
	// place — the user can reuse it on the next reparse attempt.
	now := time.Now()
	if err := s.repo.UpdateKnowledgeColumns(ctx, existing.ID, map[string]interface{}{
		"parse_status":           types.ParseStatusCancelled,
		"error_message":          "用户已取消解析",
		"pending_subtasks_count": 0,
		// The summary task is dequeued (or will bail) with the rest.
		"summary_status": summaryStatusClosedExpr(types.SummaryStatusNone),
		"updated_at":     now,
	}); err != nil {
		logger.Errorf(ctx, "CancelKnowledgeParse: failed to mark knowledge cancelled: %v", err)
		return nil, err
	}
	existing.ParseStatus = types.ParseStatusCancelled
	existing.ErrorMessage = "用户已取消解析"
	existing.PendingSubtasksCount = 0
	if existing.SummaryStatus == types.SummaryStatusPending || existing.SummaryStatus == types.SummaryStatusProcessing {
		existing.SummaryStatus = types.SummaryStatusNone
	}
	existing.UpdatedAt = now
	logger.Infof(ctx, "Knowledge %s marked as cancelled by user", knowledgeID)

	// Close the active attempt span tree so the UI stops showing "进行中"
	// for the cancelled run. AbortAttempt cascade-cancels every still-
	// running descendant (multimodal per-image, postprocess subtasks,
	// graph chunks) BEFORE closing the root, otherwise the trace
	// viewer would leave those striped/running bars hanging forever
	// because workers exit via their abort-guard without ever calling
	// FailSpan on their own subspan. Best-effort: nil tracker / missing
	// attempt no-ops.
	if attempt := s.tracker().LatestAttempt(ctx, knowledgeID); attempt > 0 {
		s.tracker().AbortAttempt(ctx, knowledgeID, attempt,
			"USER_CANCELLED", "用户已取消解析", "用户已取消解析")
	}

	// Best-effort dequeue. Failures here don't block the cancel — the
	// downstream tasks will still self-abort at their entry guards.
	s.dequeueKnowledgeTasks(ctx, knowledgeID)
	// Wiki ingest lives in its own per-KB pending queue (task_pending_ops)
	// rather than asynq, so dequeueKnowledgeTasks above can't see it.
	// Mirror the deletion path's scrub so a cancelled knowledge doesn't
	// get picked up by the next 30s batch and burn a wiki LLM call on a
	// doc the user already abandoned. The in-flight worker would skip it
	// at isWikiKnowledgeAborted anyway, but scrubbing avoids waking the
	// batch in the first place.
	s.scrubWikiPendingIngest(ctx, existing.KnowledgeBaseID, knowledgeID, "cancel")
	recordKBActivity(ctx, s.audit, tenantID, existing.KnowledgeBaseID, types.AuditActionKnowledgeParseCanceled,
		"knowledge", existing.ID, types.AuditOutcomeCanceled,
		map[string]any{"title": existing.Title, "type": existing.Type})
	return existing, nil
}

// dequeueKnowledgeTasks asks the task inspector to remove any queued
// tasks for this knowledge and signal active workers to stop. Safe to
// call when the inspector is a no-op (Lite mode).
func (s *knowledgeService) dequeueKnowledgeTasks(ctx context.Context, knowledgeID string) {
	if s.taskInspector == nil {
		return
	}
	if _, _, err := s.taskInspector.CancelTasksForKnowledge(ctx, knowledgeID); err != nil {
		logger.Warnf(ctx, "CancelKnowledgeParse: dequeue best-effort failed for %s: %v", knowledgeID, err)
	}
}

// ProcessKnowledgeListReparse handles Asynq knowledge list reparse tasks.
func (s *knowledgeService) ProcessKnowledgeListReparse(ctx context.Context, t *asynq.Task) error {
	var payload types.KnowledgeListReparsePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal knowledge list reparse payload: %v", err)
		return err
	}
	ctx = payload.Initiator.Apply(ctx)
	taskID, _ := asynq.GetTaskID(ctx)
	ctx = withKBActivityTask(ctx, taskID, kbActivityTrigger(ctx))

	logger.Infof(ctx, "Processing knowledge list reparse task for %d knowledge items", len(payload.KnowledgeIDs))

	ctx, ids, err := s.reparseTaskScope(ctx, payload)
	if err != nil || len(ids) == 0 {
		return err
	}

	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get tenant %d: %v", payload.TenantID, err)
		return err
	}

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	outcome, err := runKnowledgeListReparseSubmissions(ids, func(id string) error {
		_, err := s.ReparseKnowledge(ctx, id, payload.ProcessConfig)
		return err
	})
	logger.Infof(ctx, "Knowledge list reparse task finished: %d submitted, %d failed",
		outcome.Submitted, outcome.Failed)
	return err
}

type knowledgeListReparseOutcome struct {
	Submitted int
	Failed    int
}

// runKnowledgeListReparseSubmissions attempts every item so one bad document
// cannot block the remainder of the batch. A partial failure is non-retryable:
// retrying the wrapper task would destructively reparse items that were already
// submitted successfully. Failed rows remain selectable for an explicit retry.
func runKnowledgeListReparseSubmissions(
	ids []string,
	submit func(string) error,
) (knowledgeListReparseOutcome, error) {
	var outcome knowledgeListReparseOutcome
	failures := make([]error, 0)
	for _, id := range ids {
		if err := submit(id); err != nil {
			failures = append(failures, fmt.Errorf("knowledge %s: %w", secutils.SanitizeForLog(id), err))
			outcome.Failed++
			continue
		}
		outcome.Submitted++
	}
	if len(failures) == 0 {
		return outcome, nil
	}
	return outcome, fmt.Errorf(
		"%w: batch reparse submitted %d item(s) and failed %d: %w",
		asynq.SkipRetry, outcome.Submitted, outcome.Failed, errors.Join(failures...),
	)
}

// cutLogPreview keeps at most limit bytes of s for a log line, backing off to a
// rune start so Chinese document text is never cut inside a character.
func cutLogPreview(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
