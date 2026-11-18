package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/common/redislock"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

func (s *wikiIngestService) enqueueFinalizeRow(ctx context.Context, op *types.TaskPendingOp) bool {
	accepted, err := enqueueWikiPendingOp(ctx, s.pendingRepo, op)
	if err != nil {
		logger.Warnf(ctx, "wiki finalize: enqueue %s row failed: %v", op.Op, err)
		return false
	}
	return accepted
}

// enqueueFinalize persists this batch's KB-global convergence work into the
// finalize lane of task_pending_ops and schedules a debounced trigger. One
// "slug" row per affected page (carrying its fresh title when this batch wrote
// it, for cross-linking) plus one "change" row per doc added/removed (for the
// index-intro change description).
func (s *wikiIngestService) enqueueFinalize(
	ctx context.Context,
	payload WikiIngestPayload,
	affectedSlugs []string,
	freshTitleBySlug map[string]string,
	changes []wikiFinalizeChange,
	folderIDs []string,
) {
	if s.pendingRepo == nil {
		return
	}
	acceptedAny := false
	for _, slug := range affectedSlugs {
		row := wikiFinalizeRow{Slug: slug, Title: freshTitleBySlug[slug]}
		b, err := json.Marshal(row)
		if err != nil {
			continue
		}
		if s.enqueueFinalizeRow(ctx, &types.TaskPendingOp{
			TenantID: payload.TenantID,
			TaskType: wikiFinalizeTaskType,
			Scope:    wikiTaskScope,
			ScopeID:  payload.KnowledgeBaseID,
			Op:       wikiFinalizeOpSlug,
			DedupKey: slug,
			Payload:  b,
		}) {
			acceptedAny = true
		}
	}
	for i := range changes {
		row := wikiFinalizeRow{Change: &changes[i]}
		b, err := json.Marshal(row)
		if err != nil {
			continue
		}
		if s.enqueueFinalizeRow(ctx, &types.TaskPendingOp{
			TenantID: payload.TenantID,
			TaskType: wikiFinalizeTaskType,
			Scope:    wikiTaskScope,
			ScopeID:  payload.KnowledgeBaseID,
			Op:       wikiFinalizeOpChange,
			DedupKey: "",
			Payload:  b,
		}) {
			acceptedAny = true
		}
	}
	if len(folderIDs) > 0 {
		row := wikiFinalizeRow{FolderIDs: uniqueWikiFolderIDs(folderIDs)}
		if b, err := json.Marshal(row); err == nil {
			if s.enqueueFinalizeRow(ctx, &types.TaskPendingOp{
				TenantID: payload.TenantID,
				TaskType: wikiFinalizeTaskType,
				Scope:    wikiTaskScope,
				ScopeID:  payload.KnowledgeBaseID,
				Op:       wikiFinalizeOpFolderPrune,
				DedupKey: "",
				Payload:  b,
			}) {
				acceptedAny = true
			}
		}
	}
	if !acceptedAny {
		return
	}
	s.scheduleFinalize(ctx, payload)
}

func uniqueWikiFolderIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// scheduleFinalize enqueues a debounced, coalesced KB-global finalize trigger.
// asynq.TaskID ("wiki-finalize-<kbID>") makes concurrent schedules within the
// debounce window collapse into one pending task; the conflict error is the
// expected coalescing signal, not a failure. Lite mode reserves stable IDs
// in-process for the lifetime of each delayed or running task.
func (s *wikiIngestService) scheduleFinalize(ctx context.Context, payload WikiIngestPayload) {
	langfuse.InjectTracing(ctx, &payload)
	b, _ := json.Marshal(payload)
	t := asynq.NewTask(types.TypeWikiFinalize, b,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(30*time.Minute),
		asynq.ProcessIn(wikiFinalizeDelay),
		asynq.TaskID("wiki-finalize-"+payload.KnowledgeBaseID),
	)
	if _, err := s.task.Enqueue(t); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			return // a finalize is already scheduled/running for this KB — coalesced
		}
		logger.Warnf(ctx, "wiki finalize: schedule trigger failed: %v", err)
	}
}

// scheduleFinalizeRetry is used when folder pruning is waiting for ingest
// rows to drain. It deliberately has no stable TaskID: the currently-running
// finalize task still owns that ID until it returns, so reusing it here would
// coalesce the only retry away. Duplicate retries are harmless because the
// durable prune row is deleted exactly once and an empty lane is a no-op.
func (s *wikiIngestService) scheduleFinalizeRetry(ctx context.Context, payload WikiIngestPayload) {
	langfuse.InjectTracing(ctx, &payload)
	b, _ := json.Marshal(payload)
	t := asynq.NewTask(types.TypeWikiFinalize, b,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(30*time.Minute),
		asynq.ProcessIn(wikiFolderPruneRetryDelay),
	)
	if _, err := s.task.Enqueue(t); err != nil {
		logger.Warnf(ctx, "wiki finalize: schedule deferred folder prune failed: %v", err)
	}
}

// peekPendingList loads up to `limit` ops from task_pending_ops for
// this KB, least-failed first (then FIFO). Rows are NOT removed;
// callers must DeleteByIDs once they have been consumed (or
// IncrFailCount + leave them in place for the next pass).
//
// peekedIDs returns the DB ids of every row included in the peek
// (NOT just the ones that survived dedup) so trimPendingList can
// delete them all in one statement at the end of the batch — this
// matches the legacy "LTrim peekedCount entries" semantics, where
// duplicates collapsed by the consumer were also drained from the
// list once their canonical sibling had been processed.
func (s *wikiIngestService) peekPendingList(ctx context.Context, kbID string, limit int) (ops []WikiPendingOp,
	peekedIDs []int64, err error,
) {
	if s.pendingRepo == nil {
		return nil, nil, nil
	}
	if limit <= 0 {
		limit = wikiMaxDocsPerBatch
	}
	rows, err := s.pendingRepo.PeekBatch(ctx, wikiTaskType, wikiTaskScope, kbID, limit)
	if err != nil {
		// Surface the error so the caller can distinguish a transient DB
		// failure from a genuinely empty queue: the former must trigger an
		// asynq retry, whereas returning "no rows" here would ack the task
		// as a false success and strand the pending list.
		return nil, nil, err
	}
	ops, peekedIDs = s.decodePendingRows(ctx, rows)
	return ops, peekedIDs, nil
}

// claimPendingList is the standard-mode (Redis) counterpart of
// peekPendingList: it atomically CLAIMS up to `limit` ops (marks
// claimed_at) so concurrent batches for the same KB — allowed since Phase 3
// removed the exclusive per-KB lock — pull DISJOINT documents instead of
// double-processing. Stale claims (older than wikiClaimStaleAfter, i.e. from
// a crashed worker) are recovered. Dedup / peekedIDs semantics match
// peekPendingList; the returned peekedIDs are the claimed rows that the
// caller must DeleteByIDs on success or ReleaseByIDs to retry.
func (s *wikiIngestService) claimPendingList(ctx context.Context, kbID string, limit int) (ops []WikiPendingOp,
	peekedIDs []int64, err error,
) {
	if s.pendingRepo == nil {
		return nil, nil, nil
	}
	if limit <= 0 {
		limit = wikiMaxDocsPerBatch
	}
	rows, err := s.pendingRepo.ClaimBatch(ctx, wikiTaskType, wikiTaskScope, kbID, limit,
		time.Now().Add(-wikiClaimStaleAfter))
	if err != nil {
		// A claim failure is transient (DB blip). Propagate it so the batch
		// returns an error and asynq retries, instead of acking the trigger
		// as a false "no pending ops" success and stranding the queue.
		return nil, nil, err
	}
	ops, peekedIDs = s.decodePendingRows(ctx, rows)
	return ops, peekedIDs, nil
}

// withSlugLock serializes read-modify-write on one shared wiki page across
// concurrent batches. Phase 3 removed the exclusive per-KB batch lock, so
// two batches for the same KB can both produce updates for a shared
// entity/concept slug; without this lock their GetPageBySlug→UpdatePage
// cycles would race and lose one contribution.
//
// Redis mode only: in Lite mode a single process runs one batch per KB at a
// time (liteLocks), so same-slug contention cannot occur and fn runs
// directly. Returns (false, nil) if the lock could not be acquired within
// wikiSlugLockWait — the caller then treats the slug like a best-effort
// reduce miss (matching pre-existing reduce-failure handling). On a Redis
// error we fail OPEN (run fn unlocked): a rare lost-update on a shared page
// is tolerated by the finalize/dead-link passes, whereas silently dropping
// the update is strictly worse.
func (s *wikiIngestService) withSlugLock(ctx context.Context, kbID, slug string, fn func() error) (bool, error) {
	if s.redisClient == nil {
		return true, fn()
	}
	key := wikiSlugLockPrefix + kbID + ":" + slug
	token := uuid.NewString()
	deadline := time.Now().Add(wikiSlugLockWait)
	const wikiRedisOpTimeout = 5 * time.Second
	for {
		ok, rerr := s.redisClient.SetNX(ctx, key, token, wikiSlugLockTTL).Result()
		if rerr != nil {
			logger.Warnf(ctx, "wiki reduce: slug lock SetNX failed for %s: %v (running unlocked)", slug, rerr)
			return true, fn()
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		if err := sleepWithContext(ctx, wikiSlugLockPoll); err != nil {
			return false, err
		}
	}
	defer func() {
		relCtx, relCancel := context.WithTimeout(context.Background(), wikiRedisOpTimeout)
		defer relCancel()
		if _, relErr := redislock.Release(relCtx, s.redisClient, key, token); relErr != nil {
			logger.Warnf(ctx, "wiki reduce: slug lock release failed for %s: %v", slug, relErr)
		}
	}()
	return true, fn()
}

// wikiInflightReserveScript atomically enforces the per-KB in-flight cap.
// It purges expired slots (crash recovery), counts live slots, and adds the
// caller's slot iff under the cap. Returning 1 = reserved, 0 = at cap.
// Keeping purge+count+add in one Lua call closes the check-then-act race two
// concurrent reservers would otherwise have.
const wikiInflightReserveScript = `
local now = tonumber(ARGV[1])
local expiry = tonumber(ARGV[2])
local maxInflight = tonumber(ARGV[3])
local token = ARGV[4]
local ttl = tonumber(ARGV[5])
redis.call('ZREMRANGEBYSCORE', KEYS[1], 0, now)
if redis.call('ZCARD', KEYS[1]) >= maxInflight then
  return 0
end
redis.call('ZADD', KEYS[1], expiry, token)
redis.call('PEXPIRE', KEYS[1], ttl)
return 1
`

// reserveInflightSlot claims one of KB's concurrent-batch slots (Phase 4,
// standard/Redis mode). Returns (release, true) when granted — release() MUST
// run when the batch finishes; (nil, false) when the KB is already at
// maxInflight, so the caller should reschedule and bail. A background renew
// keeps the slot alive for the batch's duration and removes it when the task
// context is canceled, even if the handler itself is still blocked. A crashed
// batch's slot simply expires (wikiInflightTTL) and is purged by the next
// reserver. Lite mode has no shared-pool contention (liteLocks already
// serialize per KB), so it always grants a no-op slot. Fails OPEN on a Redis
// error: a blip must not halt wiki generation, and the pool size still bounds
// total work.
// wikiInflightRenewInterval is a var so tests can shorten the renewal cadence.
var wikiInflightRenewInterval = wikiInflightRenew

func (s *wikiIngestService) reserveInflightSlot(ctx context.Context, kbID string, maxInflight int) (func(), bool) {
	if s.redisClient == nil || maxInflight <= 0 {
		return func() {}, true
	}
	key := wikiInflightPrefix + kbID
	token := uuid.New().String()
	now := time.Now()
	res, err := s.redisClient.Eval(ctx, wikiInflightReserveScript,
		[]string{key},
		now.UnixMilli(),
		now.Add(wikiInflightTTL).UnixMilli(),
		maxInflight,
		token,
		wikiInflightTTL.Milliseconds(),
	).Int()
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: inflight reserve failed for KB %s: %v (running uncapped)", kbID, err)
		return func() {}, true
	}
	if res == 0 {
		return nil, false
	}

	const wikiRedisOpTimeout = 5 * time.Second
	renewCtx, cancel := context.WithCancel(ctx)
	renewDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(wikiInflightRenewInterval)
		defer ticker.Stop()
		defer close(renewDone)
		defer func() {
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), wikiRedisOpTimeout)
			defer cleanCancel()
			_ = s.redisClient.ZRem(cleanCtx, key, token).Err()
		}()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				exp := float64(time.Now().Add(wikiInflightTTL).UnixMilli())
				rpcCtx, rpcCancel := context.WithTimeout(renewCtx, wikiRedisOpTimeout)
				_ = s.redisClient.ZAdd(rpcCtx, key, redis.Z{Score: exp, Member: token}).Err()
				_ = s.redisClient.PExpire(rpcCtx, key, wikiInflightTTL).Err()
				rpcCancel()
			}
		}
	}()
	return func() {
		cancel()
		<-renewDone
	}, true
}

// wikiInflightCountScript purges expired slots exactly like the reserve
// script does, then returns the live count. Read-mostly companion to
// wikiInflightReserveScript so a fan-out decision sees the same view of the
// cap a reserver would.
const wikiInflightCountScript = `
redis.call('ZREMRANGEBYSCORE', KEYS[1], 0, tonumber(ARGV[1]))
return redis.call('ZCARD', KEYS[1])
`

// activeInflightSlots reports how many batches currently hold one of KB's
// in-flight slots (standard/Redis mode). Returns -1 when the count is
// unavailable (no Redis client, or a Redis error); callers must treat that as
// "unknown" and stay conservative instead of scaling on a stale number.
func (s *wikiIngestService) activeInflightSlots(ctx context.Context, kbID string) int {
	if s.redisClient == nil {
		return -1
	}
	res, err := s.redisClient.Eval(ctx, wikiInflightCountScript,
		[]string{wikiInflightPrefix + kbID},
		time.Now().UnixMilli(),
	).Int()
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: inflight count failed for KB %s: %v", kbID, err)
		return -1
	}
	return res
}

// scheduleCappedRetry enqueues a single coalesced follow-up trigger after a
// batch was turned away by the in-flight cap. asynq.TaskID collapses all
// turned-away triggers for one KB into a single pending retry (no thundering
// herd), and the running batches that hold the slots also chain their own
// follow-ups on completion, so the turned-away rows are guaranteed to drain
// once a slot frees.
func (s *wikiIngestService) scheduleCappedRetry(ctx context.Context, payload WikiIngestPayload) {
	langfuse.InjectTracing(ctx, &payload)
	b, _ := json.Marshal(payload)
	t := asynq.NewTask(types.TypeWikiIngest, b,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(wikiInflightBackoff),
		asynq.TaskID("wiki-ingest-capped-"+payload.KnowledgeBaseID),
	)
	if _, err := s.task.Enqueue(t); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			return // a capped retry is already pending for this KB — coalesced
		}
		logger.Warnf(ctx, "wiki ingest: capped-retry enqueue failed: %v", err)
	}
}

// scheduleStaleClaimRecheck arms a single, far-future safety-net trigger for a
// KB that still has pending rows but yielded nothing to claim (every eligible
// row is held by a FRESH claim). Normally a running batch drains those rows and
// chains its own fast follow-up on completion; this net exists only for the
// case where the claim holder CRASHED, leaving claimed_at stamped so no worker
// can re-claim until wikiClaimStaleAfter elapses — and where nothing else would
// ever re-trigger the KB afterwards.
//
// The delay is set past the stale threshold so that when the net fires the
// abandoned claims are guaranteed eligible again. asynq.TaskID coalesces all
// rechecks for one KB into a single pending net (no thundering herd from
// concurrent no-op batches). If PendingCount is already zero the KB has fully
// drained and no net is needed. Returns true if a net is (or already was)
// scheduled.
func (s *wikiIngestService) scheduleStaleClaimRecheck(ctx context.Context, payload WikiIngestPayload) bool {
	if s.pendingRepo == nil {
		return false
	}
	count, err := s.pendingRepo.PendingCount(ctx, wikiTaskType, wikiTaskScope, payload.KnowledgeBaseID)
	if err != nil || count == 0 {
		return false
	}

	logger.Infof(ctx, "wiki ingest: %d rows for KB %s held by fresh claims, arming stale-claim recheck", count,
		payload.KnowledgeBaseID)

	langfuse.InjectTracing(ctx, &payload)
	b, _ := json.Marshal(payload)
	t := asynq.NewTask(types.TypeWikiIngest, b,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(wikiClaimStaleAfter+wikiFollowUpDelay),
		asynq.TaskID("wiki-ingest-recheck-"+payload.KnowledgeBaseID),
	)
	if _, err := s.task.Enqueue(t); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			return true // a recheck is already armed for this KB — coalesced
		}
		logger.Warnf(ctx, "wiki ingest: stale-claim recheck enqueue failed: %v", err)
		return false
	}
	return true
}

// decodePendingRows converts raw task_pending_ops rows into WikiPendingOps,
// applying the last-write-wins dedup by knowledge_id. peekedIDs carries the
// db ids of EVERY row (including dedup-collapsed ones) so the caller can
// drain them all at trim time. Shared by peekPendingList (no claim) and
// claimPendingList (claimed rows).
func (s *wikiIngestService) decodePendingRows(ctx context.Context, rows []*types.TaskPendingOp) (ops []WikiPendingOp,
	peekedIDs []int64,
) {
	if len(rows) == 0 {
		return nil, nil
	}

	all := make([]WikiPendingOp, 0, len(rows))
	peekedIDs = make([]int64, 0, len(rows))
	for _, r := range rows {
		peekedIDs = append(peekedIDs, r.ID)
		var op WikiPendingOp
		if len(r.Payload) > 0 {
			if err := json.Unmarshal(r.Payload, &op); err != nil {
				logger.Warnf(ctx, "wiki ingest: failed to unmarshal pending op id=%d: %v", r.ID, err)
				continue
			}
		} else {
			// Defensive: if payload was lost, fall back to column data
			// so the row is still drainable (otherwise it would loop
			// on every batch as un-deletable).
			op = WikiPendingOp{
				Op:          r.Op,
				KnowledgeID: r.DedupKey,
			}
		}
		op.dbID = r.ID
		all = append(all, op)
	}

	// Deduplicate by KnowledgeID, keeping only the *last* operation for
	// each document. Optimizes out redundant sequences (e.g., upload
	// then immediate delete: [ingest, retract] → [retract]). The
	// non-canonical rows still get drained at trim time — their dbIDs
	// are in peekedIDs.
	seen := make(map[string]bool)
	reversedUnique := make([]WikiPendingOp, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		op := all[i]
		if op.KnowledgeID == "" {
			// No dedup key — keep verbatim (rare; edge case for
			// future ops without a knowledge anchor).
			reversedUnique = append(reversedUnique, op)
			continue
		}
		if seen[op.KnowledgeID] {
			continue
		}
		seen[op.KnowledgeID] = true
		reversedUnique = append(reversedUnique, op)
	}

	ops = make([]WikiPendingOp, 0, len(reversedUnique))
	for i := len(reversedUnique) - 1; i >= 0; i-- {
		ops = append(ops, reversedUnique[i])
	}
	return ops, peekedIDs
}

// trimPendingList deletes consumed rows from task_pending_ops. Empty
// input is a no-op so callers can invoke unconditionally at the end
// of a batch.
func (s *wikiIngestService) trimPendingList(ctx context.Context, ids []int64) error {
	if s.pendingRepo == nil || len(ids) == 0 {
		return nil
	}
	if err := s.pendingRepo.DeleteByIDs(ctx, ids); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to trim %d pending rows: %v", len(ids), err)
		return err
	}
	return nil
}

// finalizeWikiSubtask releases this knowledge's slot in the finalizing
// counter once its wiki op reaches a terminal state (mapped successfully
// or dead-lettered). The matching +1 is seeded by
// KnowledgePostProcess.SetFinalizing when willSpawnWiki is true. Callers
// must only invoke this for ingest ops — retract ops are for deleted
// knowledge that has no counter to drain.
//
// Safe to call on a row that is already completed or whose counter is
// already zero: FinalizeSubtask guards both the decrement (count > 0) and
// the promote (parse_status = finalizing AND count = 0), so an op enqueued
// before this accounting shipped is a harmless no-op.
func (s *wikiIngestService) finalizeWikiSubtask(ctx context.Context, knowledgeID string, attempt int) {
	if err := s.finalizeWikiSubtaskErr(ctx, knowledgeID, attempt); err != nil {
		logger.Errorf(ctx, "finalize subtask decrement failed source=wiki knowledge=%s err=%v; "+
			"row will be left to the housekeeping sweep", knowledgeID, err)
	}
}

func (s *wikiIngestService) finalizeWikiSubtaskErr(ctx context.Context, knowledgeID string, attempt int) error {
	if s.knowledgeRepo == nil || knowledgeID == "" {
		return nil
	}
	if attemptSuperseded(ctx, s.tracker(), knowledgeID, attempt) {
		return nil
	}
	return releaseSubtaskSlot(ctx, s.knowledgeRepo, knowledgeID, attempt)
}

// requeueFailedOps records in-batch failures.
//
// For each failed op:
//
//   - IncrFailCount on the source row. The repo returns the new total,
//     so a single round trip handles both bookkeeping and retry-budget
//     check.
//   - If the count is <= wikiMaxFailRetries: leave the row in place.
//     The next follow-up batch's ClaimBatch / PeekBatch will pick it
//     up after never-attempted work (both order by fail_count ASC,
//     then id ASC). The row is not moved, so the fail_count budget
//     keeps counting down.
//   - If the count exceeds the retry cap: archive the op into
//     task_dead_letters and DeleteByIDs to remove it from the queue.
//     Settlement failures are returned so the caller does not mark claims
//     settled while rows are still claimed or undeleted.
func (s *wikiIngestService) requeueFailedOps(ctx context.Context, payload WikiIngestPayload,
	ops []WikiPendingOp,
) error {
	if s.pendingRepo == nil || len(ops) == 0 {
		return nil
	}
	var settleErrs []error
	for _, op := range ops {
		if op.dbID == 0 {
			// Op was never persisted (synthetic / test) — nothing to
			// retry against.
			continue
		}
		count, err := s.pendingRepo.IncrFailCount(ctx, op.dbID)
		if err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to increment fail count for %s (id=%d): %v", op.KnowledgeID, op.dbID,
				err)
			settleErrs = append(settleErrs, fmt.Errorf("increment fail count id=%d: %w", op.dbID, err))
			// Without a fresh count we can't tell whether to drop. Be
			// conservative: leave the row in place; the next ClaimBatch
			// / PeekBatch will see it again and we'll try once more.
			continue
		}
		if count <= wikiMaxFailRetries {
			// Release the claim so the row is immediately eligible for the
			// next trigger's ClaimBatch instead of waiting out
			// wikiClaimStaleAfter. No-op in Lite mode (row was peeked, never
			// claimed). ReleaseByIDs preserves fail_count, so the retry
			// budget still counts down.
			if err := s.pendingRepo.ReleaseByIDs(ctx, []int64{op.dbID}); err != nil {
				logger.Warnf(ctx, "wiki ingest: failed to release claim for retry id=%d: %v", op.dbID, err)
				settleErrs = append(settleErrs, fmt.Errorf("release retry claim id=%d: %w", op.dbID, err))
			}
			logger.Infof(ctx, "wiki ingest: re-queued failed op %s (%s) for retry (attempt %d/%d)", op.KnowledgeID,
				op.DocTitle, count, wikiMaxFailRetries)
			continue
		}

		// Exhausted in-batch retries — archive and remove. This is the
		// terminal failure point only after settlement succeeds. Keep the
		// source row and its finalizing slot if archiving fails, so the next
		// batch can retry instead of losing the failed work permanently.
		logger.Warnf(ctx, "wiki ingest: dropping op %s (%s) after %d failures (limit %d)",
			op.KnowledgeID, op.DocTitle, count, wikiMaxFailRetries)
		payloadBytes, _ := json.Marshal(op)
		var insertedDL *types.TaskDeadLetter
		if s.deadLetterRepo != nil {
			dl := &types.TaskDeadLetter{
				TenantID:  payload.TenantID,
				TaskType:  wikiTaskType,
				Scope:     wikiTaskScope,
				ScopeID:   payload.KnowledgeBaseID,
				RelatedID: op.KnowledgeID,
				Payload:   payloadBytes,
				LastError: fmt.Sprintf("exceeded wikiMaxFailRetries=%d (in-batch retries)", wikiMaxFailRetries),
				FailCount: count,
			}
			if dlErr := s.deadLetterRepo.Insert(ctx, dl); dlErr != nil {
				logger.Warnf(ctx, "wiki ingest: failed to archive op %s to dead letters: %v", op.KnowledgeID, dlErr)
				settleErrs = append(settleErrs, fmt.Errorf("archive dead letter id=%d: %w", op.dbID, dlErr))
				continue
			}
			insertedDL = dl
		}
		rollbackDeadLetter := func() {
			if s.deadLetterRepo == nil || insertedDL == nil || insertedDL.ID == 0 {
				return
			}
			cleanCtx, cleanCancel := wikiIngestCleanupContext(ctx)
			defer cleanCancel()
			if rbErr := s.deadLetterRepo.DeleteByID(cleanCtx, insertedDL.ID); rbErr != nil {
				logger.Warnf(ctx, "wiki ingest: rollback dead letter id=%d failed: %v", insertedDL.ID, rbErr)
			}
		}
		if err := s.pendingRepo.DeleteByIDs(ctx, []int64{op.dbID}); err != nil {
			rollbackDeadLetter()
			logger.Warnf(ctx, "wiki ingest: failed to drop dead-lettered row id=%d: %v", op.dbID, err)
			settleErrs = append(settleErrs, fmt.Errorf("drop dead-lettered row id=%d: %w", op.dbID, err))
			continue
		}
		// Decrement only once the source row is gone; if the finalizing decrement
		// fails across all retries, restore the pending op and roll back the dead
		// letter so the retry pass can settle both atomically without stranding
		// pending_subtasks_count or duplicating dead-letter entries.
		if op.Op == WikiOpIngest {
			if subErr := s.finalizeWikiSubtaskErr(ctx, op.KnowledgeID, op.Attempt); subErr != nil {
				cleanCtx, cleanCancel := wikiIngestCleanupContext(ctx)
				requeueErr := s.pendingRepo.Enqueue(cleanCtx, &types.TaskPendingOp{
					TenantID:  payload.TenantID,
					TaskType:  wikiTaskType,
					Scope:     wikiTaskScope,
					ScopeID:   payload.KnowledgeBaseID,
					Op:        op.Op,
					DedupKey:  op.KnowledgeID,
					Payload:   payloadBytes,
					FailCount: count,
				})
				cleanCancel()
				if requeueErr != nil {
					logger.Errorf(ctx, "wiki ingest: restore pending op %s after subtask failure failed: %v",
						op.KnowledgeID, requeueErr)
				} else {
					rollbackDeadLetter()
				}
				settleErrs = append(settleErrs, fmt.Errorf("finalize wiki subtask %s: %w", op.KnowledgeID, subErr))
				continue
			}
		}
	}
	return errors.Join(settleErrs...)
}
