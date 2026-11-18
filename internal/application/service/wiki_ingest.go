package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// ErrWikiIngestConcurrent is returned by the wiki ingest handler in Lite mode
// when another batch is already running for the same KB (the in-process
// liteLocks guard is held). The asynq/sync executor's RetryDelayFunc uses
// errors.Is on this sentinel to apply a short, fixed retry delay instead of
// exponential backoff, so the deferred batch retries promptly once the active
// one releases. Standard (Redis) mode no longer takes an exclusive per-KB
// lock (Phase 3) and never returns this.
var ErrWikiIngestConcurrent = errors.New("concurrent wiki task active")

const (
	// maxContentForWiki limits the document content sent to LLM for wiki generation
	maxContentForWiki = 32768

	// --- Phase 3: concurrent per-KB batches (standard/Redis mode) ---------
	//
	// Phase 3 removed the exclusive per-KB Redis "batch in progress" lock
	// (wiki:active:<kbID>). Standard mode now allows concurrent batches for
	// one KB, kept safe by row claiming (claimPendingList) plus per-slug
	// reduce locks (withSlugLock). Lite mode still serializes per KB via the
	// in-process liteLocks map.
	//
	// wikiClaimStaleAfter is how long a claimed-but-undrained ingest row
	// waits before another worker may re-claim it. It MUST exceed the asynq
	// task Timeout for wiki:ingest (60m) so a still-running batch's rows are
	// never stolen mid-flight — only genuinely crashed/abandoned claims are
	// recovered. This preserves the pre-claim crash behaviour where a dead
	// batch's undeleted rows were simply re-peeked by the next trigger.
	wikiClaimStaleAfter = 90 * time.Minute

	// wikiSlugLockPrefix guards read-modify-write on a single shared wiki
	// page (entity/concept/summary/index) so two concurrent batches for the
	// same KB can't lost-update the same slug. Key: wiki:slug:{kbID}:{slug}.
	wikiSlugLockPrefix = "wiki:slug:"

	// wikiIdentityClaimPrefix reserves the slug chosen for one normalized
	// (page type, display title) identity while concurrent map phases are
	// still running. The existing per-slug lock cannot help when two models
	// emit different slugs for the same title, because those reducers lock
	// different keys. A short-lived identity claim makes both batches use
	// the same slug before summaries and page updates are materialized.
	wikiIdentityClaimPrefix = "wiki:identity:"
	wikiIdentityClaimTTL    = 2 * time.Hour
	// wikiIdentityClaimScript atomically GET-or-SET (or overwrite when
	// ARGV[3] is "1"). A valid existing slug wins and has its TTL refreshed.
	// Missing or corrupt values are replaced so callers cannot diverge on a
	// dirty key. KEYS[1]=claim key; ARGV = proposed slug, TTL seconds,
	// authoritative ("0"/"1"), required slug prefix.
	wikiIdentityClaimScript = `
local proposed = ARGV[1]
local ttl = tonumber(ARGV[2])
local prefix = ARGV[4]
if ARGV[3] == '1' then
  redis.call('SET', KEYS[1], proposed, 'EX', ttl)
  return proposed
end
local existing = redis.call('GET', KEYS[1])
if type(existing) == 'string' and string.sub(existing, 1, #prefix) == prefix then
  redis.call('EXPIRE', KEYS[1], ttl)
  return existing
end
redis.call('SET', KEYS[1], proposed, 'EX', ttl)
return proposed
`
	// wikiSlugLockTTL bounds the per-slug lock so a crashed reducer can't
	// wedge a hot page forever. Comfortably longer than a single reduce
	// (one LLM modify call).
	wikiSlugLockTTL = 5 * time.Minute
	// wikiSlugLockWait / Poll bound how long a reduce goroutine blocks
	// waiting for a contended slug before falling back to a best-effort
	// skip (matching the pre-existing reduce-failure semantics).
	wikiSlugLockWait = 2 * time.Minute
	wikiSlugLockPoll = 50 * time.Millisecond

	// --- Phase 4: per-KB in-flight cap (standard/Redis mode) --------------
	//
	// Since Phase 3 lets many batches for one KB run at once, a single KB's
	// bulk import could otherwise occupy the entire wiki pool and starve
	// other KBs. Each running batch reserves a slot in a per-KB Redis sorted
	// set (score = expiry). ZREMRANGEBYSCORE purges slots left behind by a
	// crashed worker, so the cap is self-healing without an explicit lock.
	//
	// wikiInflightPrefix keys the per-KB slot set: wiki:inflight:{kbID}.
	wikiInflightPrefix = "wiki:inflight:"
	// wikiInflightDefault is the fallback cap when WikiConfig.IngestMaxInflight
	// is unset. 4 leaves half of the default 8-worker wiki pool for other
	// KBs while still giving one KB solid parallelism.
	wikiInflightDefault = 4
	// wikiInflightTTL is how long a reserved slot survives without renewal.
	// MUST exceed wikiInflightRenew by a comfortable margin so a single
	// missed renew (GC pause, Redis blip) doesn't drop a live batch's slot.
	wikiInflightTTL = 90 * time.Second
	// wikiInflightRenew is how often a running batch bumps its slot's expiry.
	wikiInflightRenew = 30 * time.Second
	// wikiInflightBackoff delays the follow-up trigger when a batch is turned
	// away by the cap, so it retries after a slot plausibly frees.
	wikiInflightBackoff = 10 * time.Second

	// wikiIngestDelay is how long to wait after a document is added before
	// the batch task fires. Debounces rapid uploads.
	wikiIngestDelay = 30 * time.Second

	// wikiFollowUpDelay is the normal light debounce before a follow-up
	// batch drains the remaining pending rows. Standard mode already fans a
	// KB's backlog across concurrent claiming batches, so this is just a
	// short breather, not a lock-release wait.
	wikiFollowUpDelay = 5 * time.Second

	// wikiRateLimitBackoff is the (much longer) follow-up delay used when a
	// batch's failures were caused by upstream rate limiting (HTTP 429 /
	// quota). Retrying a rate-limited document at the normal 5s cadence just
	// throws more requests at an already-saturated rpm budget — every retry
	// re-issues extract + classify + summary calls for the doc — which keeps
	// the limiter tripped and drags the whole KB's ingest out. Backing off
	// gives the per-minute window time to reset before we try again.
	wikiRateLimitBackoff = 60 * time.Second

	// wikiMaxDocsPerBatch limits how many documents a single batch processes.
	// Prevents unbounded execution time. Remaining ops stay in
	// task_pending_ops and are picked up by the follow-up task.
	wikiMaxDocsPerBatch = 5

	// wikiMaxFailRetries is the maximum number of times a single document op
	// may be re-attempted via requeueFailedOps before it is permanently
	// archived to task_dead_letters. 5 retries ≈ five full batch cycles
	// (each with a ~30 s delay), giving transient LLM errors a fair chance
	// to recover without letting a persistently-broken doc clog the queue
	// indefinitely.
	wikiMaxFailRetries = 5

	// wikiIngestMaxRetry controls asynq retry budget for wiki:ingest tasks.
	// Keep this moderate: lock conflicts already retry every 15s via
	// asynqRetryDelayFunc, and follow-up/retract paths fire quickly.
	wikiIngestMaxRetry = 10

	// wikiDeletedKeyPrefix is the Redis key prefix for "recently deleted
	// knowledge" tombstones. Key: wiki:deleted:{kbID}:{knowledgeID}. Written
	// by cleanupWikiOnKnowledgeDelete so that any wiki_ingest task still in
	// flight (or queued) for this knowledge can fast-path skip without
	// hitting the DB. TTL > wikiIngestDelay so it's guaranteed to outlast
	// any in-flight ingest.
	wikiDeletedKeyPrefix = "wiki:deleted:"

	// wikiDeletedTTL bounds how long we remember a deletion. Must comfortably
	// exceed the longest plausible ingest run (LLM extraction + reduce).
	wikiDeletedTTL = 1 * time.Hour

	// wikiLLMMaxAttempts is the total attempt count (initial + retries) for
	// every LLM call routed through generateWithTemplate. 3 was chosen to
	// absorb transient 504/timeouts from upstream gateways without
	// materially prolonging task runtime when the remote is genuinely down.
	wikiLLMMaxAttempts = 3

	// wikiLLMMaxTokens is the completion-token budget for every LLM call
	// routed through generateWithTemplate. Combined wiki extraction emits a
	// single large JSON document (entities + concepts + details). When
	// MaxTokens is left at 0, OpenAI-compatible clients omit max_tokens and
	// providers such as DeepSeek apply a default of 8192 — long extracts are
	// truncated mid-JSON with finish_reason=length, and parse fails with
	// "unexpected end of JSON input" (EXTRACT_FAILED). Raising the budget to
	// 32768 matches verified complete outputs for large Chinese policy docs;
	// shorter replies still stop early via finish_reason=stop. See #2604.
	wikiLLMMaxTokens = 32768

	// wikiPageModifyMaxContinuations bounds how many extra LLM rounds a page
	// rewrite may take when the provider stopped at the completion budget
	// (finish_reason=length). A page body is the one artifact whose value IS
	// the whole text, so a fragment is not a usable answer: the editor is asked
	// for the tail instead. Tables are enumerated top-down, so each round makes
	// forward progress; 3 rounds cover a 32768-token budget being clamped to
	// roughly a quarter by a provider or by a nearly-full context window.
	//
	// This is deliberately NOT the general strategy for every wiki call — see
	// the agent loop's reasoning in internal/agent/observe.go: a continuation
	// nudge only helps when the caller can tell the model "you were cut off,
	// keep going", which for a JSON extraction is already covered by the
	// parse-failure path.
	wikiPageModifyMaxContinuations = 3

	// wikiPageModifyContinuationDone is the sentinel a model may reply with
	// when a continuation round finds nothing left to write.
	wikiPageModifyContinuationDone = "(complete)"

	// wikiLLMBackoffBase is the base delay for the exponential backoff
	// between retry attempts. The nth retry waits base << (n-1) — so with
	// a 2s base we wait 2s, 4s, 8s between attempts.
	wikiLLMBackoffBase = 2 * time.Second

	// wikiTaskType is the task_type stamp used in task_pending_ops and
	// task_dead_letters rows for this pipeline. Stable across the lifetime
	// of any pending op so the follow-up consumer can pull it back.
	wikiTaskType = "wiki:ingest"

	// wikiTaskScope is the scope used by both pending ops and dead letters.
	// Wiki ingest is per-KB, so every op is scoped to a knowledge_base.
	wikiTaskScope = types.TaskScopeKnowledgeBase

	// --- Finalize lane (Phase 1: debounced KB-global convergence) ---------
	//
	// The KB-global convergence steps (index-intro rebuild, dead-link
	// cleanup, cross-link injection) used to run at the tail of EVERY
	// 5-doc ingest batch. On a bulk import that meant O(docs/batchSize)
	// index rebuilds — each an LLM call plus an index-page write. They are
	// now deferred to a debounced per-KB wiki:finalize task: each ingest
	// batch records what changed into a separate lane of task_pending_ops
	// (task_type=wiki:finalize) and schedules a coalesced trigger, so a
	// burst of N documents rebuilds the index ONCE at the end.

	// wikiFinalizeTaskType is the task_pending_ops lane for finalize work.
	// Keyed by (task_type, scope, scope_id) like the ingest lane, but with a
	// distinct task_type so PeekBatch never mixes the two.
	wikiFinalizeTaskType = "wiki:finalize"

	// wikiFinalizeOpSlug rows carry one affected page slug (+ its fresh title
	// when this batch wrote it) for the dead-link / cross-link passes.
	wikiFinalizeOpSlug = "slug"
	// wikiFinalizeOpChange rows carry a doc-level add/remove change entry for
	// the index-intro change description.
	wikiFinalizeOpChange = "change"
	// wikiFinalizeOpFolderPrune rows carry folders that may have become empty
	// after a document retract. Keeping this in the durable finalize lane lets
	// us wait until every ingest op for the KB has settled before deleting the
	// directories; taxonomy planning creates folders before reduce writes the
	// corresponding pages, so pruning any earlier can invalidate in-flight
	// folder assignments.
	wikiFinalizeOpFolderPrune = "folder_prune"

	wikiFinalizeAdded   = "added"
	wikiFinalizeRemoved = "removed"

	// wikiFinalizeDelay debounces the finalize trigger so multiple ingest
	// batches within the window coalesce into a single index rebuild.
	wikiFinalizeDelay = 20 * time.Second

	// wikiFinalizeMaxRows caps how many finalize rows one run drains. A run
	// that hits the cap reschedules itself for the remainder.
	wikiFinalizeMaxRows = 5000

	// wikiFinalizeLockTTL / Renew guard against two finalize runs for the
	// same KB writing the index page concurrently (belt-and-suspenders on
	// top of the asynq.TaskID coalescing).
	wikiFinalizeLockTTL   = 60 * time.Second
	wikiFinalizeLockRenew = 20 * time.Second

	// Folder cleanup is maintenance, not user-blocking work. When an ingest is
	// still active, retry slowly so pruning never competes with the primary wiki
	// pipeline for worker capacity.
	wikiFolderPruneRetryDelay = 1 * time.Minute

	// wikiIngestCleanupTimeout bounds detached tail cleanup after the asynq
	// task context has been cancelled or hit its timeout.
	wikiIngestCleanupTimeout = 10 * time.Second
)

// wikiFinalizeChange is a doc-level add/remove entry for the index-intro
// change description, persisted as a finalize-lane row payload.
type wikiFinalizeChange struct {
	Action     string `json:"action"` // wikiFinalizeAdded | wikiFinalizeRemoved
	DocTitle   string `json:"doc_title,omitempty"`
	DocSummary string `json:"doc_summary,omitempty"`
}

// wikiFinalizeRow is the JSON payload of a task_pending_ops row in the
// finalize lane. Exactly one of {Slug, Change, FolderIDs} is set,
// distinguished by the row's Op column.
type wikiFinalizeRow struct {
	Slug      string              `json:"slug,omitempty"`
	Title     string              `json:"title,omitempty"`
	Change    *wikiFinalizeChange `json:"change,omitempty"`
	FolderIDs []string            `json:"folder_ids,omitempty"`
}

// WikiDeletedTombstoneKey returns the Redis key used to mark a knowledge as
// recently deleted, so wiki_ingest tasks in flight can short-circuit. Exposed
// so knowledgeService.cleanupWikiOnKnowledgeDelete can write the same key
// without duplicating the format string.
func WikiDeletedTombstoneKey(kbID, knowledgeID string) string {
	return wikiDeletedKeyPrefix + kbID + ":" + knowledgeID
}

// WikiIngestPayload is the asynq task payload for wiki ingest batch trigger.
// The actual document IDs are stored in the task_pending_ops table; this
// payload only carries the trigger metadata so the worker can resolve
// the queue tuple (task_type, scope, scope_id) and process whatever rows
// are queued under it.
type WikiIngestPayload struct {
	types.TracingContext
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// KnowledgeID restricts an operator trigger to one existing durable key.
	// Automatic triggers leave it empty and keep their normal batch selection.
	KnowledgeID string `json:"knowledge_id,omitempty"`
	Language    string `json:"language,omitempty"`
}

// WikiRetractPayload is the asynq task payload for wiki content retraction
type WikiRetractPayload struct {
	types.TracingContext
	TenantID        uint64   `json:"tenant_id"`
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	KnowledgeID     string   `json:"knowledge_id"`
	DocTitle        string   `json:"doc_title"`
	DocSummary      string   `json:"doc_summary,omitempty"` // one-line summary of the deleted document
	Language        string   `json:"language,omitempty"`
	PageSlugs       []string `json:"page_slugs"`
	FolderIDs       []string `json:"folder_ids,omitempty"`
}

// Wiki pending operation discriminators stored in task_pending_ops.
const (
	WikiOpIngest  = "ingest"
	WikiOpRetract = "retract"
)

// WikiPendingOp represents a single operation queued in task_pending_ops
// under task_type="wiki:ingest". The struct is the JSON payload of the
// task_pending_ops row; the surrounding (task_type, scope, scope_id,
// dedup_key) fields live as separate columns and are not serialized
// here.
//
// dbID is the auto-increment primary key of the task_pending_ops row
// the op was loaded from. PeekBatch fills it; consumers carry it
// through Map/Reduce so DeleteByIDs (after consume) and IncrFailCount
// (after failure) can address the right row. It is intentionally
// unexported and excluded from JSON so the persisted payload does not
// duplicate the column.
type WikiPendingOp struct {
	Op          string `json:"op"`
	KnowledgeID string `json:"knowledge_id"`
	// Ingest fields
	Language string `json:"language,omitempty"`
	// Attempt is the parse attempt whose finalizing counter owns this op's
	// slot. An op that outlived a reparse must not release a slot of the
	// newer attempt. Zero on ops queued before this field existed.
	Attempt int `json:"attempt,omitempty"`
	// Retract fields
	DocTitle   string   `json:"doc_title,omitempty"`
	DocSummary string   `json:"doc_summary,omitempty"`
	PageSlugs  []string `json:"page_slugs,omitempty"`
	FolderIDs  []string `json:"folder_ids,omitempty"`

	// dbID is set by peekPendingList from task_pending_ops.id. Zero in
	// constructions made outside the queue (e.g. legacy tests).
	dbID int64 `json:"-"`
}

// wikiIngestService handles the LLM-powered wiki generation pipeline.
//
// Durable state lives in two places:
//   - task_pending_ops (rows tagged task_type="wiki:ingest", scope=
//     "knowledge_base"): the per-document op queue. Replaces the
//     legacy Redis wiki:pending:<kbID> list, which was vulnerable to
//     24h TTL eviction at 4w-document scale.
//   - task_dead_letters: in-batch failures that exhausted
//     wikiMaxFailRetries land here. The asynq dead-letter middleware
//     also writes asynq-level archived rows here uniformly across
//     every task type.
//
// Redis is still used for the per-KB active-batch lock
// (wiki:active:<kbID>) and the delete tombstone (wiki:deleted:<...>),
// both of which are correctness-critical short-lived flags rather
// than data the system should survive without.
type wikiIngestService struct {
	wikiService    interfaces.WikiPageService
	kbService      interfaces.KnowledgeBaseService
	knowledgeSvc   interfaces.KnowledgeService
	knowledgeRepo  interfaces.KnowledgeRepository
	chunkRepo      interfaces.ChunkRepository
	modelService   interfaces.ModelService
	task           interfaces.TaskEnqueuer
	audit          interfaces.AuditLogService
	pendingRepo    interfaces.TaskPendingOpsRepository
	deadLetterRepo interfaces.TaskDeadLetterRepository
	redisClient    *redis.Client // nil in Lite mode (no Redis)
	// spanTracker lets per-document map work surface as a
	// postprocess.wiki subspan in the knowledge trace tree. Async
	// batch design means we look up the parent attempt by knowledge
	// id at run-time (LatestAttempt) rather than carrying it in the
	// asynq payload, which is per-KB and would otherwise be ambiguous
	// for the 5-docs-per-batch fan-out.
	spanTracker SpanTracker
	// liteLocks provides per-KB mutual exclusion in Lite mode (no Redis).
	// Keys are kbID strings; values are unused (presence = locked).
	liteLocks sync.Map
	// liteFinalizeLocks is the Lite-mode counterpart of the Redis
	// wiki:finalize:active:<kbID> lock, keeping two finalize runs for the
	// same KB from overlapping when there is no Redis.
	liteFinalizeLocks sync.Map
	// llmRequests coalesces byte-identical concurrent prompts within this
	// process. Keys include tenant and model to preserve isolation.
	llmRequests singleflight.Group
	// promptWarmups serializes only the first request for a reusable Wiki page
	// prefix. Other prefixes and already-warmed cohorts stay parallel.
	promptWarmups sync.Map
}

type wikiPromptWarmup struct {
	done chan struct{}
	once sync.Once
}

// NewWikiIngestService creates a new wiki ingest service
func NewWikiIngestService(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeSvc interfaces.KnowledgeService,
	knowledgeRepo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
	modelService interfaces.ModelService,
	task interfaces.TaskEnqueuer,
	audit interfaces.AuditLogService,
	pendingRepo interfaces.TaskPendingOpsRepository,
	deadLetterRepo interfaces.TaskDeadLetterRepository,
	redisClient *redis.Client,
	spanTracker SpanTracker,
) interfaces.TaskHandler {
	svc := &wikiIngestService{
		wikiService:    wikiService,
		kbService:      kbService,
		knowledgeSvc:   knowledgeSvc,
		knowledgeRepo:  knowledgeRepo,
		chunkRepo:      chunkRepo,
		modelService:   modelService,
		task:           task,
		audit:          audit,
		pendingRepo:    pendingRepo,
		deadLetterRepo: deadLetterRepo,
		redisClient:    redisClient,
		spanTracker:    spanTracker,
	}
	return svc
}

// tracker returns a non-nil span tracker so callers don't have to
// nil-check on every Begin/End. Matches the noopSpanTracker pattern
// used elsewhere (see knowledgeService.tracker, KnowledgePostProcessService.tracker).
func (s *wikiIngestService) tracker() SpanTracker {
	if s.spanTracker == nil {
		return noopSpanTracker{}
	}
	return s.spanTracker
}

// beginWikiSubspan opens a postprocess.wiki subspan for this document
// under the knowledge's most recent attempt. Returns nil when there is
// no parse attempt to attach to (e.g. a wiki ingest fired from a manual
// reparse path that never went through the tracker) — callers must
// pair every begin with a tolerant end / fail / skip below.
//
// Lookups are by `LatestAttempt(knowledgeID)` because the asynq task
// payload (WikiIngestPayload) is KB-scoped and carries no per-doc
// attempt — see the type's comment for the batch architecture.
func (s *wikiIngestService) beginWikiSubspan(ctx context.Context, knowledgeID string, input types.JSONMap) *Span {
	if knowledgeID == "" {
		return nil
	}
	attempt := s.tracker().LatestAttempt(ctx, knowledgeID)
	if attempt <= 0 {
		return nil
	}
	parent := s.tracker().LookupStage(ctx, knowledgeID, attempt, types.StagePostProcess)
	if parent == nil {
		return nil
	}
	return s.tracker().BeginSubSpan(ctx, parent, "postprocess.wiki", types.SpanKindSubSpan, input)
}

// EnqueueWikiIngest queues a document for wiki ingestion. The returned bool
// reports whether the durable pending op was persisted. A trigger enqueue
// error may therefore be returned together with true; callers can retry only
// the KB-scoped trigger without appending a duplicate operation.
//
// Architecture: each upload inserts one row into task_pending_ops
// (task_type="wiki:ingest", scope="knowledge_base", scope_id=kbID,
// dedup_key=knowledgeID), then schedules a debounced asynq trigger task.
// When the trigger fires, the worker peeks a batch from
// task_pending_ops, processes it, deletes consumed rows, and (if more
// remain) schedules a follow-up. Multiple debounced triggers within the
// 30s window all coalesce: the first one to acquire the per-KB active
// lock drains the batch; subsequent ones see an empty queue and exit.
//
// Lite mode (no Redis) still works as long as Postgres is reachable —
// the queue lives in PG, only the active-batch lock is Redis-only and
// has a process-local fallback (liteLocks) inside the worker.
func enqueueWikiPendingOp(
	ctx context.Context,
	pendingRepo interfaces.TaskPendingOpsRepository,
	op *types.TaskPendingOp,
) (bool, error) {
	if pendingRepo == nil {
		return true, nil
	}
	if guard, ok := pendingRepo.(interfaces.TaskPendingOpsKnowledgeBaseGuard); ok {
		return guard.EnqueueIfKnowledgeBaseActive(ctx, op)
	}
	if err := pendingRepo.Enqueue(ctx, op); err != nil {
		return false, err
	}
	return true, nil
}

// EnqueueWikiIngest queues a durable wiki ingest operation and debounced trigger.
func EnqueueWikiIngest(
	ctx context.Context,
	task interfaces.TaskEnqueuer,
	pendingRepo interfaces.TaskPendingOpsRepository,
	tenantID uint64,
	kbID, knowledgeID string,
) (bool, error) {
	pendingOp, err := newWikiIngestPendingOp(ctx, tenantID, kbID, knowledgeID)
	// Persist the pending op. A re-ingest of the same knowledge id while
	// a previous op is still queued simply appends another row; the
	// peekPendingList consumer collapses by dedup_key (== knowledge_id),
	// keeping the LATEST op for each knowledge — matching the legacy
	// "RPush + reverse-dedupe" semantics.
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to marshal pending op for %s: %v", knowledgeID, err)
		return false, err
	}
	accepted, err := enqueueWikiPendingOp(ctx, pendingRepo, pendingOp)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to enqueue pending op for %s: %v", knowledgeID, err)
		return false, fmt.Errorf("enqueue wiki ingest pending op: %w", err)
	}
	if !accepted {
		logger.Infof(ctx, "wiki ingest: skip enqueue for deleted KB %s", kbID)
		return false, nil
	}
	if err := enqueueWikiIngestTrigger(ctx, task, tenantID, kbID); err != nil {
		return true, err
	}
	return true, nil
}

// WikiPendingLanguage returns the language recorded on the KB's newest queued
// wiki ingest op, or "" if none. A trigger re-armed without a request context
// must carry it: batch-level taxonomy planning reads the trigger's language,
// and would otherwise fall back to the server default.
func WikiPendingLanguage(ctx context.Context, db *gorm.DB, tenantID uint64, kbID string) string {
	if db == nil || kbID == "" {
		return ""
	}
	// Plucked as text: SQLite returns the column as a string, which does not
	// scan into json.RawMessage.
	var payloads []string
	if err := db.WithContext(ctx).Model(&types.TaskPendingOp{}).
		Where("tenant_id = ? AND task_type = ? AND scope = ? AND scope_id = ? AND op = ?",
			tenantID, wikiTaskType, wikiTaskScope, kbID, WikiOpIngest).
		Order("id DESC").Limit(1).
		Pluck("payload", &payloads).Error; err != nil || len(payloads) == 0 {
		return ""
	}
	var op WikiPendingOp
	if err := json.Unmarshal([]byte(payloads[0]), &op); err != nil {
		return ""
	}
	return op.Language
}

func newWikiIngestPendingOp(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
) (*types.TaskPendingOp, error) {
	lang := types.LanguageFromContextOrDefault(ctx)
	op := WikiPendingOp{
		Op:          WikiOpIngest,
		KnowledgeID: knowledgeID,
		Language:    lang,
		Attempt:     attemptFromCtx(ctx),
	}
	payloadBytes, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("marshal wiki ingest pending op: %w", err)
	}
	return &types.TaskPendingOp{
		TenantID: tenantID,
		TaskType: wikiTaskType,
		Scope:    wikiTaskScope,
		ScopeID:  kbID,
		Op:       WikiOpIngest,
		DedupKey: knowledgeID,
		Payload:  payloadBytes,
	}, nil
}

func enqueueWikiIngestTrigger(
	ctx context.Context,
	task interfaces.TaskEnqueuer,
	tenantID uint64,
	kbID string,
) error {
	lang := types.LanguageFromContextOrDefault(ctx)
	trigger := WikiIngestPayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		Language:        lang,
	}
	langfuse.InjectTracing(ctx, &trigger)
	triggerBytes, err := json.Marshal(trigger)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to marshal trigger task: %v", err)
		return fmt.Errorf("marshal wiki ingest trigger: %w", err)
	}
	if task == nil {
		return errors.New("enqueue wiki ingest trigger: task enqueuer is nil")
	}

	t := asynq.NewTask(types.TypeWikiIngest, triggerBytes,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(wikiIngestDelay),
	)
	if _, err := task.Enqueue(t); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to enqueue trigger task: %v", err)
		return fmt.Errorf("enqueue wiki ingest trigger: %w", err)
	}
	return nil
}

// EnqueueWikiRetract queues a wiki retraction op (a delete cleanup).
// Identical persistence model as EnqueueWikiIngest — the op rides in
// task_pending_ops and an asynq trigger fires shortly after to
// process the batch. Retracts use a slightly shorter ProcessIn delay
// because there is no "user upload arriving in waves" pattern to
// debounce against — a deletion fires once and we want the cleanup
// to land promptly.
func EnqueueWikiRetract(ctx context.Context, task interfaces.TaskEnqueuer,
	pendingRepo interfaces.TaskPendingOpsRepository, payload WikiRetractPayload,
) {
	_ = enqueueWikiRetract(ctx, task, pendingRepo, payload)
}

func enqueueWikiRetract(
	ctx context.Context,
	task interfaces.TaskEnqueuer,
	pendingRepo interfaces.TaskPendingOpsRepository,
	payload WikiRetractPayload,
) error {
	op := WikiPendingOp{
		Op:          WikiOpRetract,
		KnowledgeID: payload.KnowledgeID,
		DocTitle:    payload.DocTitle,
		DocSummary:  payload.DocSummary,
		PageSlugs:   payload.PageSlugs,
		FolderIDs:   payload.FolderIDs,
		Language:    payload.Language,
	}
	payloadBytes, err := json.Marshal(op)
	if err != nil {
		logger.Warnf(ctx, "wiki retract: failed to marshal pending op: %v", err)
		return err
	}
	accepted, err := enqueueWikiPendingOp(ctx, pendingRepo, &types.TaskPendingOp{
		TenantID: payload.TenantID,
		TaskType: wikiTaskType,
		Scope:    wikiTaskScope,
		ScopeID:  payload.KnowledgeBaseID,
		Op:       WikiOpRetract,
		DedupKey: payload.KnowledgeID,
		Payload:  payloadBytes,
	})
	if err != nil {
		logger.Warnf(ctx, "wiki retract: failed to enqueue pending op: %v", err)
		return err
	}
	if !accepted {
		logger.Infof(ctx, "wiki retract: skip enqueue for deleted KB %s", payload.KnowledgeBaseID)
		return nil
	}

	trigger := WikiIngestPayload{
		TenantID:        payload.TenantID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		Language:        payload.Language,
	}
	langfuse.InjectTracing(ctx, &trigger)
	triggerBytes, _ := json.Marshal(trigger)
	t := asynq.NewTask(types.TypeWikiIngest, triggerBytes,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(5*time.Second), // Retract can trigger the batch quickly
	)
	if _, err := task.Enqueue(t); err != nil {
		logger.Warnf(ctx, "wiki retract: failed to enqueue trigger task: %v", err)
		return err
	}
	return nil
}

// Handle implements interfaces.TaskHandler for asynq task processing. The
// wiki service owns two task types, dispatched here by type so a single
// registered handler covers both the ingest batch and the debounced
// KB-global finalize pass.
func (s *wikiIngestService) Handle(ctx context.Context, t *asynq.Task) error {
	switch t.Type() {
	case types.TypeWikiFinalize:
		return s.ProcessWikiFinalize(ctx, t)
	default:
		return s.ProcessWikiIngest(ctx, t)
	}
}

func wikiIngestCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), wikiIngestCleanupTimeout)
}

func (s *wikiIngestService) clearDeletedKnowledgeBasePendingOps(ctx context.Context, kbID string) error {
	cleaner, ok := s.pendingRepo.(interfaces.TaskPendingOpsScopeCleaner)
	if !ok || kbID == "" {
		return nil
	}
	cleanupCtx, cancel := wikiIngestCleanupContext(ctx)
	defer cancel()
	return cleaner.DeleteByScope(cleanupCtx, types.TaskScopeKnowledgeBase, kbID)
}

// tenantIsDeleted reports whether the payload's tenant has been soft-deleted.
// A tenant deletion removes the workspace but leaves its knowledge bases and
// durable pending ops in place, so wiki tasks restored from those ops would
// otherwise keep issuing model requests for a tenant nobody can reach (#3593).
//
// Fail-open on transient lookup errors and when the pending repo does not
// expose tenant liveness (legacy test doubles): the retry machinery still
// covers the task, and the guarded enqueue / startup recovery paths enforce
// the same invariant on their own DB access.
func (s *wikiIngestService) tenantIsDeleted(ctx context.Context, tenantID uint64) bool {
	if tenantID == 0 {
		return false
	}
	checker, ok := s.pendingRepo.(interfaces.TaskPendingOpsTenantLiveness)
	if !ok {
		return false
	}
	active, err := checker.HasActiveTenant(ctx, tenantID)
	if err != nil {
		logger.Warnf(ctx, "wiki: tenant liveness lookup failed for tenant %d: %v (failing open)", tenantID, err)
		return false
	}
	return !active
}

// releaseIngestForUnavailableWiki drops the KB's queued ingest ops when the
// wiki cannot run for a reason retries will not fix, and releases each
// document's wiki slot in the same transaction so it leaves "finalizing".
// Retract ops stay queued for when the wiki is usable again. Documents a live
// batch holds are left to it.
func (s *wikiIngestService) releaseIngestForUnavailableWiki(ctx context.Context, kbID, reason string) error {
	drainer, ok := s.pendingRepo.(interfaces.TaskPendingOpsDrainer)
	if !ok {
		return fmt.Errorf("wiki ingest: KB %s unavailable (%s)", kbID, reason)
	}
	cleanupCtx, cancel := wikiIngestCleanupContext(ctx)
	defer cancel()
	knowledgeIDs, err := drainer.DrainUnclaimedAndRelease(cleanupCtx, wikiTaskType, wikiTaskScope, kbID,
		WikiOpIngest, time.Now().Add(-wikiClaimStaleAfter))
	if err != nil {
		return fmt.Errorf("wiki ingest: KB %s unavailable (%s), drain pending ingest: %w", kbID, reason, err)
	}
	logger.Warnf(ctx, "wiki ingest: KB %s unavailable (%s), dropped pending ingest for %d document(s)",
		kbID, reason, len(knowledgeIDs))
	return nil
}
