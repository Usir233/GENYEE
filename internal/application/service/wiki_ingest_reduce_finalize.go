package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/agent"
	apprepo "github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/common/redislock"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// ProcessWikiFinalize runs the debounced, per-KB KB-global convergence pass:
// index-intro rebuild, dead-link cleanup, and cross-link injection. It drains
// the finalize lane of task_pending_ops (written by ProcessWikiIngest via
// enqueueFinalize) so a burst of documents rebuilds the index ONCE instead of
// once per 5-doc batch.
// wikiFinalizeLockRenewInterval is a var so tests can shorten the renewal cadence.
var wikiFinalizeLockRenewInterval = wikiFinalizeLockRenew

func (s *wikiIngestService) ProcessWikiFinalize(ctx context.Context, t *asynq.Task) error {
	startedAt := time.Now()
	var payload WikiIngestPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("wiki finalize: unmarshal payload: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}
	if s.pendingRepo == nil {
		return nil
	}

	// Same tenant-liveness guard as the ingest batch: finalize calls the
	// synthesis model to rebuild the index page, and a deleted tenant must
	// never accrue new model requests (#3593). Drop the KB's queue.
	if s.tenantIsDeleted(ctx, payload.TenantID) {
		if err := s.clearDeletedKnowledgeBasePendingOps(ctx, payload.KnowledgeBaseID); err != nil {
			return fmt.Errorf("wiki finalize: clear deleted tenant queue: %w", err)
		}
		return nil
	}

	// Per-KB finalize lock, separate from the ingest active lock so finalize
	// and ingest batches never block each other. Coalescing via asynq.TaskID
	// already keeps at most one finalize pending per KB; this guards the
	// reschedule-overlap window against a concurrent index-page write.
	if s.redisClient != nil {
		key := "wiki:finalize:active:" + payload.KnowledgeBaseID
		token := uuid.NewString()
		acquired, err := s.redisClient.SetNX(ctx, key, token, wikiFinalizeLockTTL).Result()
		if err != nil {
			// Fail CLOSED: proceeding unlocked would let two finalize runs
			// drain the same PeekBatch rows and double-rebuild the index page
			// (GetPage→modify→UpdatePage lost update). Return an error so
			// asynq retries the (coalesced) finalize once Redis recovers,
			// instead of racing a concurrent finalize.
			logger.Warnf(ctx, "wiki finalize: SetNX failed for KB %s: %v (retrying)", payload.KnowledgeBaseID, err)
			return fmt.Errorf("wiki finalize: acquire lock: %w", err)
		} else if !acquired {
			// Another finalize is running; it will drain the lane and
			// reschedule if rows remain. Safe to no-op.
			return nil
		}
		const wikiFinalizeRedisOpTimeout = 5 * time.Second
		lockCtx, cancelLock := context.WithCancel(context.Background())
		renewDone := make(chan struct{})
		go func() {
			defer close(renewDone)
			ticker := time.NewTicker(wikiFinalizeLockRenewInterval)
			defer ticker.Stop()
			for {
				select {
				case <-lockCtx.Done():
					return
				case <-ticker.C:
					rpcCtx, rpcCancel := context.WithTimeout(lockCtx, wikiFinalizeRedisOpTimeout)
					ok, rerr := redislock.Renew(rpcCtx, s.redisClient, key, token, wikiFinalizeLockTTL)
					rpcCancel()
					if rerr == nil && !ok {
						return
					}
				}
			}
		}()
		defer func() {
			cancelLock()
			<-renewDone
			relCtx, relCancel := context.WithTimeout(context.Background(), wikiFinalizeRedisOpTimeout)
			defer relCancel()
			if _, relErr := redislock.Release(relCtx, s.redisClient, key, token); relErr != nil {
				logger.Warnf(ctx, "wiki finalize: release lock failed for KB %s: %v",
					payload.KnowledgeBaseID, relErr)
			}
		}()
	} else {
		if _, loaded := s.liteFinalizeLocks.LoadOrStore(payload.KnowledgeBaseID, struct{}{}); loaded {
			return nil
		}
		defer s.liteFinalizeLocks.Delete(payload.KnowledgeBaseID)
	}

	rows, err := s.pendingRepo.PeekBatch(ctx, wikiFinalizeTaskType, wikiTaskScope, payload.KnowledgeBaseID,
		wikiFinalizeMaxRows)
	if err != nil {
		return fmt.Errorf("wiki finalize: peek: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if errors.Is(err, apprepo.ErrKnowledgeBaseNotFound) || (err == nil && kb == nil) {
		if cleanupErr := s.clearDeletedKnowledgeBasePendingOps(ctx, payload.KnowledgeBaseID); cleanupErr != nil {
			return fmt.Errorf("wiki finalize: clear deleted KB queue: %w", cleanupErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("wiki finalize: get KB: %w", err)
	}
	if kb.TenantID != 0 {
		if payload.TenantID != 0 && payload.TenantID != kb.TenantID {
			return fmt.Errorf("wiki finalize: payload tenant %d does not match KB tenant %d", payload.TenantID,
				kb.TenantID)
		}
		if payload.TenantID == 0 {
			payload.TenantID = kb.TenantID
			ctx = context.WithValue(ctx, types.TenantIDContextKey, kb.TenantID)
			if s.tenantIsDeleted(ctx, kb.TenantID) {
				if cleanupErr := s.clearDeletedKnowledgeBasePendingOps(ctx,
					payload.KnowledgeBaseID); cleanupErr != nil {
					return fmt.Errorf("wiki finalize: clear deleted tenant queue: %w", cleanupErr)
				}
				return nil
			}
		}
	}

	// Manual-only Wiki keeps the page APIs and retrieval available, but it must
	// not apply queued automatic changes after the setting is disabled. Drain
	// the finalize rows without running index rebuild, link cleanup, or folder
	// pruning. A queued finalize task can outlive the configuration update, so
	// checking only at enqueue time would leave those changes in flight.
	if !kb.IsWikiAutoGenerateEnabled() {
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		drainCtx, drainCancel := wikiIngestCleanupContext(ctx)
		err := s.trimPendingList(drainCtx, ids)
		drainCancel()
		if err != nil {
			return fmt.Errorf("wiki finalize: trim automatic lane: %w", err)
		}
		return nil
	}

	// Aggregate the drained rows into: affected slugs (dedup), fresh cross-link
	// refs, and the index-intro change description.
	ids := make([]int64, 0, len(rows))
	pruneRowIDs := make([]int64, 0)
	affectedSet := make(map[string]struct{}, len(rows))
	var affectedSlugs []string
	var freshRefs []linkRef
	var folderPruneIDs []string
	var changeDesc strings.Builder
	for _, r := range rows {
		ids = append(ids, r.ID)
		if r.Op == wikiFinalizeOpFolderPrune {
			pruneRowIDs = append(pruneRowIDs, r.ID)
		}
		if len(r.Payload) == 0 {
			continue
		}
		var row wikiFinalizeRow
		if err := json.Unmarshal(r.Payload, &row); err != nil {
			logger.Warnf(ctx, "wiki finalize: unmarshal row id=%d failed: %v", r.ID, err)
			continue
		}
		if r.Op == wikiFinalizeOpFolderPrune {
			folderPruneIDs = append(folderPruneIDs, row.FolderIDs...)
			continue
		}
		if row.Change != nil {
			if row.Change.Action == wikiFinalizeRemoved {
				fmt.Fprintf(&changeDesc,
					"<document_removed>\n<title>%s</title>\n<summary>%s</summary>\n</document_removed>\n\n",
					row.Change.DocTitle, row.Change.DocSummary)
			} else {
				fmt.Fprintf(&changeDesc,
					"<document_added>\n<title>%s</title>\n<summary>%s</summary>\n</document_added>\n\n",
					row.Change.DocTitle, row.Change.DocSummary)
			}
			continue
		}
		if row.Slug != "" {
			if _, ok := affectedSet[row.Slug]; !ok {
				affectedSet[row.Slug] = struct{}{}
				affectedSlugs = append(affectedSlugs, row.Slug)
			}
			if row.Title != "" {
				freshRefs = append(freshRefs, linkRef{slug: row.Slug, matchText: row.Title})
			}
		}
	}

	synthesisModelID := ""
	if kb.WikiConfig != nil {
		synthesisModelID = kb.WikiConfig.SynthesisModelID
	}
	if synthesisModelID == "" {
		synthesisModelID = kb.SummaryModelID
	}
	if synthesisModelID == "" {
		// No model to rebuild the index with; still run the pure-text passes,
		// then drain. Missing model is a config gap, not a transient error.
		logger.Warnf(ctx, "wiki finalize: no synthesis model for KB %s, skipping index rebuild",
			payload.KnowledgeBaseID)
	}

	batchCtx := s.newWikiBatchContext(payload.KnowledgeBaseID, kb.WikiConfig)
	lang := types.LanguageNameFromContext(ctx)

	indexRebuilt := false
	if kb.IsWikiAutoGenerateEnabled() && changeDesc.Len() > 0 && synthesisModelID != "" {
		chatModel, mErr := s.modelService.GetChatModel(ctx, synthesisModelID)
		if mErr != nil {
			logger.Warnf(ctx, "wiki finalize: get chat model failed: %v", mErr)
		} else if err := s.rebuildIndexPage(ctx, chatModel, payload, changeDesc.String(), lang,
			batchCtx.ContentInstructions); err != nil {
			logger.Warnf(ctx, "wiki finalize: rebuild index failed: %v", err)
		} else {
			indexRebuilt = true
		}
	}

	if len(affectedSlugs) > 0 {
		s.cleanDeadLinks(ctx, payload.KnowledgeBaseID, affectedSlugs, batchCtx)
		s.injectCrossLinks(ctx, payload.KnowledgeBaseID, affectedSlugs, freshRefs, batchCtx)
	}

	// A retract may leave one or more generated folders empty. Do not prune
	// while any ingest row for this KB is queued or claimed: taxonomy planning
	// creates folders before reduce writes pages, so an apparently-empty folder
	// can still be owned by an in-flight batch. The durable prune rows stay in
	// the finalize lane and are retried after the ingest lane drains.
	pruneDeferred := false
	deletedFolders := 0
	if len(folderPruneIDs) > 0 {
		pending, pErr := s.pendingRepo.PendingCount(ctx, wikiTaskType, wikiTaskScope, payload.KnowledgeBaseID)
		switch {
		case pErr != nil:
			logger.Warnf(ctx, "wiki finalize: cannot verify ingest drain before folder prune: %v", pErr)
			pruneDeferred = true
		case pending > 0:
			pruneDeferred = true
		default:
			deleted, pruneErr := s.wikiService.PruneEmptyFolderChains(
				ctx, payload.KnowledgeBaseID, uniqueWikiFolderIDs(folderPruneIDs))
			if pruneErr != nil {
				logger.Warnf(ctx, "wiki finalize: prune empty folders failed: %v", pruneErr)
				pruneDeferred = true
			} else {
				deletedFolders = len(deleted)
			}
		}
	}

	// Drain the processed rows. Best-effort convergence mirrors the legacy
	// in-batch behaviour: a failed index rebuild is logged (not retried),
	// so we delete regardless to avoid re-doing the whole pass forever.
	idsToTrim := ids
	if pruneDeferred && len(pruneRowIDs) > 0 {
		deferred := make(map[int64]struct{}, len(pruneRowIDs))
		for _, id := range pruneRowIDs {
			deferred[id] = struct{}{}
		}
		idsToTrim = idsToTrim[:0:0]
		for _, id := range ids {
			if _, keep := deferred[id]; !keep {
				idsToTrim = append(idsToTrim, id)
			}
		}
	}
	drainCtx, drainCancel := wikiIngestCleanupContext(ctx)
	err = s.trimPendingList(drainCtx, idsToTrim)
	drainCancel()
	if err != nil {
		return fmt.Errorf("wiki finalize: trim pending rows: %w", err)
	}

	// If more finalize rows landed while we were working, reschedule so they
	// get their own convergence pass.
	rescheduled := false
	if pruneDeferred {
		s.scheduleFinalizeRetry(ctx, payload)
		rescheduled = true
	}
	if n, cErr := s.pendingRepo.PendingCount(ctx, wikiFinalizeTaskType, wikiTaskScope,
		payload.KnowledgeBaseID); cErr == nil && n > 0 {
		if !pruneDeferred {
			s.scheduleFinalize(ctx, payload)
		}
		rescheduled = true
	}

	logger.Infof(ctx,
		"wiki finalize: kb=%s rows=%d affected_slugs=%d deleted_folders=%d folder_prune_deferred=%v "+
			"index_rebuilt=%v rescheduled=%v elapsed=%s",
		payload.KnowledgeBaseID, len(rows), len(affectedSlugs), deletedFolders, pruneDeferred, indexRebuilt,
		rescheduled,
		time.Since(startedAt).Round(time.Millisecond),
	)
	return nil
}

// resolveSlugUpdateLanguage picks the language the editor prompt should write
// the page in.
//
// A single page aggregates updates from every document that cites it, and an
// update queued before the language field existed — or enqueued from a
// background path that never saw the HTTP language middleware — carries none.
// Scanning for the first update that resolved a language, rather than indexing
// into one bucket, keeps the page localised as long as ANY contributor knew the
// language; the deployment default covers the case where none did. Without the
// fallback the prompt renders "Write in ." and the model picks a language at
// random, which is how single pages ended up in the wrong language.
func resolveSlugUpdateLanguage(ctx context.Context, updates []SlugUpdate) string {
	for _, u := range updates {
		if u.Language != "" {
			return u.Language
		}
	}
	return types.LanguageNameFromContext(ctx)
}

// reduceSlugUpdates returns:
//   - changed:          whether the wiki page was created or updated
//   - affectedType:     "ingest" or "retract" — drives downstream bookkeeping
//   - additionFailed:   true iff the slug had entity/concept additions queued
//     AND the WikiPageModifyUserPrompt LLM call failed, so no page exists/was
//     refreshed for it. Callers use this to sanitize dead [[slug]] links
//     elsewhere (e.g. in the doc's summary page) and to drop the slug from
//     the wiki log feed so users don't see a clickable entry that 404s.
//   - updateDeferred:   true iff the page write was refused by the row guard
//     (ErrWikiWriteDroppedTableRows): the page kept its previous body, so this
//     slug's contribution never landed and its documents must be re-queued
//     rather than trimmed. Distinct from err: a successful write that changes
//     nothing user-visible also leaves `version` alone, and that one IS applied.
//   - err:              transport / repo error from the persisted upsert.
func (s *wikiIngestService) reduceSlugUpdates(
	ctx context.Context,
	chatModel chat.Chat,
	kbID string,
	slug string,
	updates []SlugUpdate,
	tenantID uint64,
	batchCtx *WikiBatchContext,
	kidToWikiSpan map[string]*Span,
) (changed bool, affectedType string, additionFailed bool, updateDeferred bool, err error) {
	// Final safety net for the ingest/delete race: between Map (which already
	// checks isKnowledgeGone) and Reduce there is a long LLM call where the
	// source document may be deleted. Drop any addition/summary updates whose
	// knowledge no longer exists so we don't resurrect a ghost source_ref.
	// Retract updates are kept — they actively remove refs, which is what we
	// want when the doc is gone.
	if _, ok := types.TenantIDFromContext(ctx); !ok && tenantID != 0 {
		ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	}
	updates, err = s.filterLiveUpdates(ctx, kbID, updates)
	if err != nil {
		return false, "", false, false, err
	}
	if len(updates) == 0 {
		return false, "", false, false, nil
	}

	// Per-slug page span attribution: a single slug can receive
	// contributions from multiple docs in the same batch (entity /
	// concept pages aggregate across sources). We attach the
	// postprocess.wiki.page[slug] subspan under whichever
	// contributing doc's wikiSpan is encountered first in the updates
	// list — span tree topology only allows one parent. Every
	// contributing knowledge id is recorded in the span's `contributors`
	// output so users can still see the full attribution. Pages whose
	// only contributors had no wikiSpan (e.g. their parse attempt
	// already closed and was archived) simply get a nil pageSpan,
	// which the tracker helpers no-op on.
	var (
		pageSpan     *Span
		contributors []string
	)
	{
		seen := make(map[string]bool, len(updates))
		for _, u := range updates {
			kid := u.KnowledgeID
			if kid == "" || seen[kid] {
				continue
			}
			seen[kid] = true
			contributors = append(contributors, kid)
			if pageSpan == nil {
				if sp, ok := kidToWikiSpan[kid]; ok && sp != nil {
					pageSpan = s.tracker().BeginSubSpan(ctx, sp, fmt.Sprintf("postprocess.wiki.page[%s]", slug),
						types.SpanKindSubSpan, types.JSONMap{
							"slug":         slug,
							"updates":      len(updates),
							"contributors": contributors,
						})
				}
			}
		}
	}
	var page *types.WikiPage
	// Deferred output captures `&page` so it observes the post-merge
	// state (title, page type, content snippet) at function return —
	// that's what's actually useful in the trace viewer, not the
	// stale pre-reduce shell that exists when the defer is registered.
	defer func() {
		if pageSpan == nil {
			return
		}
		// 取消后仍以有界清理上下文记录终态，避免页面 span 留在 running。
		spanCtx, spanCancel := wikiIngestCleanupContext(ctx)
		defer spanCancel()
		if err != nil {
			s.tracker().FailSpan(spanCtx, pageSpan, "REDUCE_FAILED", err.Error(), err)
			return
		}
		if !changed {
			s.tracker().SkipSpan(spanCtx, pageSpan, "no_change")
			return
		}
		out := types.JSONMap{
			"affected_type":   affectedType,
			"addition_failed": additionFailed,
			"contributors":    contributors,
		}
		if page != nil {
			out["page_title"] = previewText(page.Title, 160)
			out["page_type"] = string(page.PageType)
			out["page_summary"] = previewText(page.Summary, 200)
			out["content_preview"] = previewText(page.Content, 320)
			out["source_refs"] = len(page.SourceRefs)
			out["chunk_refs"] = len(page.ChunkRefs)
			out["aliases"] = []string(page.Aliases)
		}
		s.tracker().EndSpan(spanCtx, pageSpan, out)
	}()

	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return false, "", false, false, ctx.Err()
		}
		if attempt > 1 {
			// 重试重新读取来源存活状态；不重新执行整篇 Map。
			updates, err = s.filterLiveUpdates(ctx, kbID, updates)
			if err != nil || len(updates) == 0 {
				return false, "", false, false, err
			}
			batchCtx = s.wikiPageRetryContext(kbID, batchCtx)
		}
		changed, affectedType, additionFailed, updateDeferred, err = s.reduceSlugUpdatesOnce(
			ctx, chatModel, kbID, slug, updates, tenantID, batchCtx, &page)
		if !errors.Is(err, apprepo.ErrWikiPageConflict) || attempt >= wikiPageConflictMaxAttempts {
			// 未落库的生成结果不能记入成功页面集合。
			if err != nil {
				changed = false
			}
			return changed, affectedType, additionFailed, updateDeferred, err
		}
		logger.Debugf(ctx, "wiki ingest: retrying conflicted page %s (attempt %d/%d)",
			slug, attempt+1, wikiPageConflictMaxAttempts)
		if waitErr := waitWikiPageConflictRetry(ctx, attempt); waitErr != nil {
			return false, "", false, false, waitErr
		}
	}
}

// reduceSlugUpdatesOnce 只计算当前页；保存必须仍对应本次读取的快照。
// pageResult 供外层唯一的页面 span 记录最终结果，不承载跨尝试的可变状态。
func (s *wikiIngestService) reduceSlugUpdatesOnce(
	ctx context.Context, chatModel chat.Chat, kbID, slug string, updates []SlugUpdate,
	tenantID uint64, batchCtx *WikiBatchContext, pageResult **types.WikiPage,
) (changed bool, affectedType string, additionFailed bool, updateDeferred bool, err error) {
	readCtx := context.WithValue(ctx, wikiPageRawReadKey{}, true)
	page, err := s.wikiService.GetPageBySlug(readCtx, kbID, slug)
	if ctx.Err() != nil {
		return false, "", false, false, ctx.Err()
	}
	exists := (err == nil && page != nil)

	if !exists {
		hasAdditions := false
		for _, u := range updates {
			if u.Type == types.WikiPageTypeEntity || u.Type == types.WikiPageTypeConcept || u.Type == "summary" {
				hasAdditions = true
				break
			}
		}
		if !hasAdditions {
			return false, "", false, false, nil
		}

		page = &types.WikiPage{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			KnowledgeBaseID: kbID,
			Slug:            slug,
			Status:          types.WikiPageStatusDraft,
			SourceRefs:      types.StringArray{},
			Aliases:         types.StringArray{},
		}
		// Reset err: GetPageBySlug returned "not found" which we just
		// handled by synthesizing the page. Don't leak that error to
		// the named return — subsequent assignments would mask it
		// anyway, but be explicit.
		err = nil
	}

	*pageResult = page
	if exists {
		// 模型生成期间不持锁，真正写入时由仓储再次原子核对。
		ctx = types.WithWikiPageWriteSnapshot(ctx, page)
	}

	affectedType = "ingest"

	var summaryUpdate *SlugUpdate
	var retracts []SlugUpdate
	var additions []SlugUpdate

	for i, u := range updates {
		switch u.Type {
		case "summary":
			summaryUpdate = &updates[i]
		case "retract", "retractStale":
			retracts = append(retracts, u)
			affectedType = "retract"
		case types.WikiPageTypeEntity, types.WikiPageTypeConcept:
			additions = append(additions, u)
			affectedType = "ingest" // Additions override retracts type
		}
	}

	if summaryUpdate != nil {
		page.Title = summaryUpdate.DocTitle + " - Summary"
		page.Content = summaryUpdate.SummaryBody
		page.Summary = summaryUpdate.SummaryLine
		page.PageType = types.WikiPageTypeSummary
		page.SourceRefs = appendUnique(page.SourceRefs, summaryUpdate.SourceRef)
		// Summary pages don't carry chunk-level citations (they are document-
		// level synopses generated from the whole content). Clear any stale
		// chunk refs that may remain if this slug was once an entity page
		// and got converted to a summary page.
		page.ChunkRefs = types.StringArray{}
		changed = true

		if err = s.checkWikiPageRetrySources(ctx, kbID, updates); err != nil {
			return false, "", false, false, err
		}
		if exists {
			// A summary rewrite can drop table rows too, and the refusal has
			// the same meaning here: the page kept its previous body, so this
			// slug's contribution never landed and its documents must survive
			// the trim phase. Classify the explicit signal; do not read it off
			// the page version.
			_, writeErr := s.wikiService.UpdatePage(ctx, page)
			updateDeferred, err = classifyWikiPageWrite(writeErr)
		} else {
			_, err = s.wikiService.CreatePage(ctx, page)
		}
		return changed, affectedType, false, updateDeferred, err
	}

	var remainingSourcesContent strings.Builder
	var deletedContent strings.Builder
	var relatedSlugs strings.Builder
	var newContentBuilder strings.Builder
	var sharedSourceContexts strings.Builder
	var docTitles []string

	language := resolveSlugUpdateLanguage(ctx, updates)

	if len(retracts) > 0 {
		for _, r := range retracts {
			fmt.Fprintf(&deletedContent, "<document>\n<title>%s</title>\n<content>\n%s\n</content>\n</document>\n\n",
				r.DocTitle, r.RetractDocContent)
		}

		retractKIDs := make(map[string]bool)
		for _, r := range retracts {
			retractKIDs[r.KnowledgeID] = true
		}

		for _, ref := range page.SourceRefs {
			refKnowledgeID, refTitle := types.ParseWikiSourceRef(ref)
			if refKnowledgeID == "" {
				continue
			}
			if refTitle == "" {
				// Legacy bare refs carry no title; the ID is the only label
				// available for the retract prompt.
				refTitle = refKnowledgeID
			}

			if retractKIDs[refKnowledgeID] {
				continue
			}

			if content := batchCtx.SummaryContentByKnowledgeID(ctx, refKnowledgeID); content != "" {
				fmt.Fprintf(&remainingSourcesContent,
					"<document>\n<title>%s</title>\n<content>\n%s\n</content>\n</document>\n\n", refTitle, content)
			} else {
				fmt.Fprintf(&remainingSourcesContent,
					"<document>\n<title>%s</title>\n<content>\n(summary not available)\n</content>\n</document>\n\n",
					refTitle)
			}
		}
		if remainingSourcesContent.Len() == 0 {
			remainingSourcesContent.WriteString("(no remaining sources)")
		}

		newRefs := types.StringArray{}
		for _, ref := range page.SourceRefs {
			if !retractKIDs[types.WikiSourceKnowledgeID(ref)] {
				newRefs = append(newRefs, ref)
			}
		}
		page.SourceRefs = newRefs
	}

	if len(additions) > 0 {
		// Resolve SourceChunks → chunk contents in a single batched query per
		// knowledge ID, so the <new_information> block can quote the chunks
		// verbatim instead of relying on the short Details paraphrase.
		chunkContentByID := s.resolveCitedChunks(ctx, tenantID, additions)
		// A document summary is shared by every page derived from that document.
		// Render a deterministic, de-duplicated block before any page-specific
		// metadata so provider prefix caches can reuse it across parallel reduce
		// calls.
		sourceContextByRef := make(map[string]string)

		for _, add := range additions {
			cited := collectCitedChunkContent(add.SourceChunks, chunkContentByID)
			// Frame the chunks with the document-level summary body so the
			// editor model knows BOTH what the document is about AND what
			// kind of document it is (resume vs announcement vs product
			// page vs schedule). The one-sentence headline alone was too
			// terse to keep the editor grounded on longer or multi-topic
			// source documents, and calibrating tone (self-reported vs
			// third-party authoritative) benefits from the richer context.
			sourceCtx := strings.TrimSpace(add.DocSummary)
			if sourceCtx != "" {
				contextKey := add.SourceRef
				if contextKey == "" {
					contextKey = add.KnowledgeID + "\x00" + add.DocTitle
				}
				sourceContextByRef[contextKey] = fmt.Sprintf(
					"<document>\n<title>%s</title>\n<context>\n%s\n</context>\n</document>\n",
					add.DocTitle, sourceCtx,
				)
			}
			if cited != "" {
				fmt.Fprintf(&newContentBuilder,
					"<document>\n<title>%s</title>\n<content>\n**%s**: %s\n\n%s\n</content>\n</document>\n\n",
					add.DocTitle, add.Item.Name, add.Item.Description, cited)
			} else {
				// Fallback: no citations available (legacy path, citation pass
				// failed, or bad chunk IDs were filtered out) — stick with
				// the short Details summary so the page still gets real text.
				fmt.Fprintf(&newContentBuilder,
					"<document>\n<title>%s</title>\n<content>\n**%s**: %s\n\n%s\n</content>\n</document>\n\n",
					add.DocTitle, add.Item.Name, add.Item.Description, add.Item.Details)
			}
			docTitles = appendUnique(docTitles, add.DocTitle)

			for _, alias := range add.Item.Aliases {
				page.Aliases = appendUnique(page.Aliases, alias)
			}
			page.SourceRefs = appendUnique(page.SourceRefs, add.SourceRef)

			if page.Title == "" {
				page.Title = add.Item.Name
			}
			if page.PageType == "" {
				page.PageType = add.Type
			}
		}

		contextKeys := make([]string, 0, len(sourceContextByRef))
		for key := range sourceContextByRef {
			contextKeys = append(contextKeys, key)
		}
		sort.Strings(contextKeys)
		for _, key := range contextKeys {
			sharedSourceContexts.WriteString(sourceContextByRef[key])
		}
	}

	if len(additions) > 0 || len(retracts) > 0 {
		titles := batchCtx.SlugTitleMany(ctx, []string(page.OutLinks))

		// slugHandles escape high-entropy slugs behind short reference handles
		// (ref-1, ref-2, …) for the editor LLM, then translates them back to
		// real slugs after generation (see decodeContent below).
		slugHandles := newWikiSlugHandles()

		// Escape every out-link slug behind a request-local handle so the editor
		// never has to retype a real slug — this is where UUID-based summary
		// slugs (summary/<uuid>) got mangled into 404-ing links. Both the
		// <valid_wiki_links> listing AND the [[...]] refs already present in
		// <existing_page_content> use the SAME handle table, so the
		// model sees one consistent, copy-safe identifier space; we translate
		// the handles back to real slugs after generation.
		known := make(map[string]struct{}, len(page.OutLinks))
		for _, outSlug := range page.OutLinks {
			known[outSlug] = struct{}{}
			slugHandles.handle(outSlug) // pre-assign for stable ordering
		}
		for _, outSlug := range page.OutLinks {
			if title := titles[outSlug]; title != "" {
				fmt.Fprintf(&relatedSlugs, "- %s (%s)\n", slugHandles.handle(outSlug), title)
			}
		}

		// Older generated pages may still contain short chunk aliases such as
		// [c003]. They are internal ingest metadata; keep the editor context
		// clean so a subsequent update cannot copy them into rewritten prose.
		existingContent := stripWikiInlineChunkCitations(page.Content)
		existingContent = slugHandles.encodeContent(existingContent, known)
		if !exists || existingContent == "" {
			existingContent = "(New page)"
		}

		hasAdditionsStr := ""
		if len(additions) > 0 {
			hasAdditionsStr = "1"
		}
		hasRetractionsStr := ""
		if len(retracts) > 0 {
			hasRetractionsStr = "1"
		}

		// Fall back gracefully if title/type are still unset (shouldn't happen
		// for well-formed updates — both get populated from `additions` above,
		// and retract-only paths require an existing page — but stay defensive
		// so we never feed the LLM an empty identity block).
		pageTitle := page.Title
		if pageTitle == "" {
			pageTitle = slug
		}
		pageType := string(page.PageType)
		if pageType == "" {
			pageType = "wiki page"
		}
		pageAliases := strings.Join(page.Aliases, ", ")

		var updatedContent string
		updatedContent, err = s.generateWithTemplate(ctx, chatModel, agent.WikiPageModifyUserPrompt, map[string]string{
			"HasAdditions":            hasAdditionsStr,
			"HasRetractions":          hasRetractionsStr,
			"PageSlug":                slug,
			"PageTitle":               pageTitle,
			"PageType":                pageType,
			"PageAliases":             pageAliases,
			"ExistingContent":         existingContent,
			"SharedSourceContexts":    sharedSourceContexts.String(),
			"NewContent":              newContentBuilder.String(),
			"DeletedContent":          deletedContent.String(),
			"RemainingSourcesContent": remainingSourcesContent.String(),
			"AvailableSlugs":          relatedSlugs.String(),
			"Language":                language,
			"CustomInstructions":      batchCtx.ContentInstructions,
			"InstructionScope":        "wiki_content",
		})

		if ctx.Err() != nil {
			return false, "", false, false, ctx.Err()
		}

		if err == nil && updatedContent != "" {
			// Translate request-local handles (ref-N) the model copied from the
			// encoded context back to their real slugs BEFORE the content is
			// parsed/stored, so out_links reflect real pages again.
			updatedContent = slugHandles.decodeContent(updatedContent)
			updatedSummary, updatedBody := splitSummaryLine(updatedContent)
			if updatedBody != "" {
				page.Content = updatedBody
			} else {
				page.Content = updatedContent
			}
			if updatedSummary != "" {
				page.Summary = updatedSummary
			}
			changed = true
		} else if err != nil {
			logger.Warnf(ctx, "wiki ingest: update/retract failed for slug %s: %v", slug, err)
			// Flag addition failures so the batch can sanitize stale
			// [[slug]] references in the doc's summary page and keep the
			// missing page out of finalize processing.
			// Retract-only failures don't poison anything (they leave
			// the existing page unchanged), so don't flag those.
			if len(additions) > 0 {
				additionFailed = true
			}
			// Preserve the error so the owning documents are retried rather than
			// marked complete while their page contribution is missing.
		}
	}

	// Apply the batch taxonomy plan, but only to pages that aren't already
	// filed — so brand-new pages get a coherent folder while previously-filed
	// or user-moved pages keep their placement (manual edits are authoritative).
	// The page's category_path cache is derived from folder_id downstream by
	// CreatePage/UpdatePage, so assigning the folder id is sufficient here.
	if page.FolderID == "" && batchCtx != nil {
		if fid := batchCtx.PlannedFolderID[slug]; fid != "" {
			page.FolderID = fid
		}
	}

	if changed {
		if err = s.checkWikiPageRetrySources(ctx, kbID, updates); err != nil {
			return false, "", false, false, err
		}
		// Refresh chunk refs in-place on the page so they persist alongside
		// the rest of the row. Retract-only updates (no additions) preserve
		// the existing refs; addition rounds append the newly-cited chunks
		// on top of what was already there, deduplicated.
		page.ChunkRefs = mergeChunkRefs(page.ChunkRefs, additions)
		if exists {
			// A retraction removes a deleted document's contribution on
			// purpose, so this write is allowed to drop the table rows that
			// contribution owned. Saying so explicitly is what lets the choke
			// point in UpdatePage refuse an accidental truncation everywhere
			// else; without it a retract round would be refused and the stale
			// content would stay on the page for good.
			writeCtx := ctx
			if len(retracts) > 0 {
				writeCtx = types.WithWikiShrinkAllowed(ctx)
			}
			// UpdatePage refuses a machine rewrite that drops too many table
			// rows still on the page and says so with
			// ErrWikiWriteDroppedTableRows. That refusal is not a failure — the
			// page is intact and this caller did nothing wrong — but it does
			// mean this slug's contribution never landed, and its documents
			// must be kept for a later batch. The version cannot stand in for
			// that signal: a successful write that changes nothing
			// user-visible leaves `version` alone too (see the version-bump
			// policy in wiki_page.go), so only the explicit refusal is
			// classified as deferred.
			_, updateErr := s.wikiService.UpdatePage(writeCtx, page)
			updateDeferred, err = classifyWikiPageWrite(updateErr)
		} else {
			_, err = s.wikiService.CreatePage(ctx, page)
		}
		return true, affectedType, additionFailed, updateDeferred, err
	}

	return false, "", additionFailed, false, err
}

// classifyWikiPageWrite turns the result of one guarded UpdatePage call into
// the reduce phase's (updateDeferred, err) pair.
//
// ErrWikiWriteDroppedTableRows means the row guard refused the write: the page
// was not modified, but nothing failed, so the caller must keep this slug's
// contributing documents for a later batch (updateDeferred) instead of
// reporting an error. Every other outcome — including a successful write that
// only refreshed bookkeeping and therefore did not bump `version` — was
// applied and is not deferred.
func classifyWikiPageWrite(updateErr error) (deferred bool, err error) {
	if errors.Is(updateErr, ErrWikiWriteDroppedTableRows) {
		return true, nil
	}
	return false, updateErr
}

// mergeChunkRefs unions the chunk IDs currently on the page with the ones
// cited by this batch's additions, preserving insertion order and dropping
// duplicates. Empty strings are filtered out so a malformed source_chunks
// array can't leave junk in the column.
//
// A retract round with no additions leaves the current refs untouched —
// retract-only paths don't carry chunk IDs (only knowledge IDs), and we
// can't surgically filter without that info. The next time the slug is
// re-materialized via additions the fresh chunks will overlay on top.
func mergeChunkRefs(current types.StringArray, additions []SlugUpdate) types.StringArray {
	seen := make(map[string]bool, len(current))
	out := make(types.StringArray, 0, len(current))
	for _, id := range current {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, add := range additions {
		for _, chunkID := range add.SourceChunks {
			if chunkID == "" || seen[chunkID] {
				continue
			}
			seen[chunkID] = true
			out = append(out, chunkID)
		}
	}
	return out
}
