package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	apprepo "github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

type auditBoundedStream struct {
	remaining int64
	readBytes atomic.Int64
}

func (s *auditBoundedStream) Read(p []byte) (int, error) {
	if s.remaining <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > s.remaining {
		n = s.remaining
	}
	for i := int64(0); i < n; i++ {
		p[i] = 'a'
	}
	s.remaining -= n
	s.readBytes.Add(n)
	return int(n), nil
}

func (s *auditBoundedStream) Close() error { return nil }

type auditStreamFileService struct {
	interfaces.FileService
	stream *auditBoundedStream
}

func (f *auditStreamFileService) GetFile(_ context.Context, _ string) (io.ReadCloser, error) {
	return f.stream, nil
}

func (f *auditStreamFileService) SaveBytes(
	_ context.Context, _ []byte, _ uint64, fileName string, _ bool,
) (string, error) {
	return "local://saved/" + fileName, nil
}

type auditNilTenantRepo struct {
	interfaces.TenantRepository
}

func (auditNilTenantRepo) GetTenantByID(_ context.Context, _ uint64) (*types.Tenant, error) {
	return nil, nil
}

type auditNoopDocReader struct {
	interfaces.DocumentReader
}

func (auditNoopDocReader) Read(_ context.Context, _ *types.ReadRequest) (*types.ReadResult, error) {
	return &types.ReadResult{MarkdownContent: "parsed"}, nil
}

func (auditNoopDocReader) ListEngines(
	_ context.Context, _ map[string]string,
) ([]types.ParserEngineInfo, error) {
	return nil, nil
}

func (auditNoopDocReader) Reconnect(_ string) error { return nil }
func (auditNoopDocReader) IsConnected() bool        { return true }

func buildAuditFileHeader(t *testing.T, name string, payload []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(payload)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	mr := multipart.NewReader(&buf, mw.Boundary())
	form, err := mr.ReadForm(int64(len(payload)) + 4096)
	require.NoError(t, err)
	t.Cleanup(func() { _ = form.RemoveAll() })
	headers := form.File["file"]
	require.Len(t, headers, 1)
	return headers[0]
}

func TestAuditFix_UnboundedReadAllLimits(t *testing.T) {
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	const oneMB = int64(1 << 20)

	t.Run("readMultipartFileContent_enforces_max_size", func(t *testing.T) {
		fh := buildAuditFileHeader(t, "oversize.json", bytes.Repeat([]byte("x"), int(oneMB)+2048))
		_, err := readMultipartFileContent(fh)
		require.Error(t, err)
	})

	t.Run("copyOwnedObject_enforces_stream_ceiling", func(t *testing.T) {
		stream := &auditBoundedStream{remaining: (64 << 20) + 4096}
		src := &auditStreamFileService{stream: stream}
		dst := &auditStreamFileService{}
		_, err := copyOwnedObject(context.Background(), src, dst, "local://src.png", 1, "k1", nil, "")
		require.Error(t, err)
		assert.LessOrEqual(t, stream.readBytes.Load(), int64(64<<20)+64*1024)
	})

	t.Run("knowledgeImageReader_readStored_default_cap", func(t *testing.T) {
		stream := &auditBoundedStream{remaining: (64 << 20) + 4096}
		reader := knowledgeImageReader{
			tenantRepo: auditNilTenantRepo{},
			fileSvc:    &auditStreamFileService{stream: stream},
		}
		_, err := reader.readStored(
			context.Background(), 1, func() *types.KnowledgeBase { return nil }, "local://img.png", 0,
		)
		require.Error(t, err)
		assert.LessOrEqual(t, stream.readBytes.Load(), int64(64<<20)+64*1024)
	})

	t.Run("knowledgeProcess_convert_caps_stored_file_read", func(t *testing.T) {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
			Logger: gormlogger.Default.LogMode(gormlogger.Silent),
		})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, db.AutoMigrate(&types.Knowledge{}))

		repo := apprepo.NewKnowledgeRepository(db)
		kn := &types.Knowledge{
			ID: "kn-oversize", TenantID: 1, KnowledgeBaseID: "kb-1",
			Type: "file", FileName: "big.txt", FileType: "txt", ParseStatus: types.ParseStatusProcessing,
		}
		require.NoError(t, repo.CreateKnowledge(context.Background(), kn))

		stream := &auditBoundedStream{remaining: oneMB + 512*1024}
		svc := &knowledgeService{
			repo:           repo,
			tenantRepo:     auditNilTenantRepo{},
			fileSvc:        &auditStreamFileService{stream: stream},
			documentReader: auditNoopDocReader{},
		}
		kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1}
		payload := types.DocumentProcessPayload{
			TenantID: 1, KnowledgeBaseID: "kb-1", KnowledgeID: kn.ID,
			FilePath: "local://big.txt", FileName: "big.txt", FileType: "txt",
		}
		res, convErr := svc.convert(context.Background(), payload, kb, kn, types.EffectiveProcessConfig{}, true)
		assert.Nil(t, res)
		assert.NoError(t, convErr, "last retry permanent oversize failure settles knowledge without asynq error")
		assert.Equal(t, types.ParseStatusFailed, kn.ParseStatus)
		assert.LessOrEqual(t, stream.readBytes.Load(), oneMB+64*1024)
	})

	t.Run("faqImport_caps_entries_object_read", func(t *testing.T) {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
			Logger: gormlogger.Default.LogMode(gormlogger.Silent),
		})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, db.AutoMigrate(&types.Knowledge{}))

		repo := apprepo.NewKnowledgeRepository(db)
		kn := &types.Knowledge{
			ID: "faq-kn-1", TenantID: 7, KnowledgeBaseID: "kb-faq",
			Type: types.KnowledgeTypeFAQ, ParseStatus: types.ParseStatusProcessing,
		}
		require.NoError(t, repo.CreateKnowledge(context.Background(), kn))

		stream := &auditBoundedStream{remaining: (64 << 20) + 4096}
		svc := &knowledgeService{
			repo: repo,
			kbService: &wikiLookupKBService{kb: &types.KnowledgeBase{
				ID: "kb-faq", TenantID: 7, Type: types.KnowledgeBaseTypeFAQ,
			}},
			tenantRepo: auditActiveTenantRepo{},
			fileSvc:    &auditStreamFileService{stream: stream},
		}
		raw, err := json.Marshal(types.FAQImportPayload{
			TenantID:    7,
			TaskID:      "11111111-1111-1111-1111-111111111111",
			KBID:        "kb-faq",
			KnowledgeID: kn.ID,
			EntriesURL:  "local://faq-oversize.json",
		})
		require.NoError(t, err)
		err = svc.ProcessFAQImport(context.Background(), asynq.NewTask(types.TypeFAQImport, raw))
		require.Error(t, err)
		assert.LessOrEqual(t, stream.readBytes.Load(), int64(64<<20)+64*1024)
	})
}

type auditActiveTenantRepo struct {
	interfaces.TenantRepository
}

func (auditActiveTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id, Name: "tenant"}, nil
}

type auditStallZAddHook struct {
	zaddStarted chan struct{}
}

func (h *auditStallZAddHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *auditStallZAddHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), "zadd") {
			select {
			case h.zaddStarted <- struct{}{}:
			default:
			}
			if ctx.Done() == nil {
				time.Sleep(250 * time.Millisecond)
				return context.DeadlineExceeded
			}
			<-ctx.Done()
			return ctx.Err()
		}
		return next(ctx, cmd)
	}
}

func (h *auditStallZAddHook) ProcessPipelineHook(
	next redis.ProcessPipelineHook,
) redis.ProcessPipelineHook {
	return next
}

func TestAuditFix_WikiInflightRenewContextCanceledOnRelease(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	hook := &auditStallZAddHook{zaddStarted: make(chan struct{}, 1)}
	rdb.AddHook(hook)

	prev := wikiInflightRenewInterval
	wikiInflightRenewInterval = 5 * time.Millisecond
	t.Cleanup(func() { wikiInflightRenewInterval = prev })

	svc := &wikiIngestService{redisClient: rdb}
	release, ok := svc.reserveInflightSlot(context.Background(), "kb-lock", 2)
	require.True(t, ok)

	select {
	case <-hook.zaddStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for inflight slot renewal ZAdd")
	}

	done := make(chan struct{})
	go func() {
		release()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("release() blocked because ZAdd used an uncancellable context.Background()")
	}
}

type auditFinalizeLockOrderHook struct {
	renewEntered  chan struct{}
	unblockRenew  chan struct{}
	inFlightRenew atomic.Bool
	racedRelease  atomic.Bool
}

func (h *auditFinalizeLockOrderHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *auditFinalizeLockOrderHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := strings.ToLower(cmd.Name())
		if (name == "eval" || name == "evalsha") && len(cmd.Args()) >= 5 {
			// Renew passes 6 args (cmd, script/sha, numkeys, key, token, ttlMs);
			// Release passes 5 args (cmd, script/sha, numkeys, key, token).
			if len(cmd.Args()) == 6 {
				h.inFlightRenew.Store(true)
				select {
				case h.renewEntered <- struct{}{}:
				default:
				}
				select {
				case <-h.unblockRenew:
				case <-ctx.Done():
					time.Sleep(15 * time.Millisecond)
				}
				err := next(ctx, cmd)
				h.inFlightRenew.Store(false)
				return err
			}
			if len(cmd.Args()) == 5 && h.inFlightRenew.Load() {
				h.racedRelease.Store(true)
			}
		}
		return next(ctx, cmd)
	}
}

func (h *auditFinalizeLockOrderHook) ProcessPipelineHook(
	next redis.ProcessPipelineHook,
) redis.ProcessPipelineHook {
	return next
}

type auditSlowPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	waitCh <-chan struct{}
}

func (r *auditSlowPendingRepo) PeekBatch(
	ctx context.Context, taskType, scope, scopeID string, limit int,
) ([]*types.TaskPendingOp, error) {
	<-r.waitCh
	return r.TaskPendingOpsRepository.PeekBatch(ctx, taskType, scope, scopeID, limit)
}

func TestAuditFix_WikiFinalizeWaitsForRenewBeforeRelease(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	hook := &auditFinalizeLockOrderHook{
		renewEntered: make(chan struct{}, 1),
		unblockRenew: make(chan struct{}),
	}
	rdb.AddHook(hook)

	prev := wikiFinalizeLockRenewInterval
	wikiFinalizeLockRenewInterval = 5 * time.Millisecond
	t.Cleanup(func() { wikiFinalizeLockRenewInterval = prev })

	svc, _ := newWikiLookupRecoveryService(t, "kb-fin", rdb)
	allowPeekReturn := make(chan struct{})
	svc.pendingRepo = &auditSlowPendingRepo{
		TaskPendingOpsRepository: svc.pendingRepo,
		waitCh:                   allowPeekReturn,
	}

	rawPayload, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-fin"})
	require.NoError(t, err)
	task := asynq.NewTask(types.TypeWikiFinalize, rawPayload)

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.ProcessWikiFinalize(context.Background(), task)
	}()

	select {
	case <-hook.renewEntered:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for finalize lock Renew")
	}

	close(allowPeekReturn)
	time.AfterFunc(25*time.Millisecond, func() { close(hook.unblockRenew) })

	require.NoError(t, <-errCh)
	assert.False(t, hook.racedRelease.Load(), "redislock.Release ran while redislock.Renew was still in flight")
}

type auditFailingSnapshotDeleteMgr struct {
	*installSandboxManager
	deleteErr error
}

func (m *auditFailingSnapshotDeleteMgr) DeleteSnapshot(_ context.Context, _ string) error {
	return m.deleteErr
}

type auditFlakySkillUpdateRepo struct {
	*installSkillRepo
	failUpdateLeft atomic.Int32
}

func (r *auditFlakySkillUpdateRepo) UpdateSkill(ctx context.Context, e *types.TenantSkillEntity) error {
	if r.failUpdateLeft.Add(-1) >= 0 {
		return errors.New("transient update failure")
	}
	return r.installSkillRepo.UpdateSkill(ctx, e)
}

func TestAuditFix_SkillAbandonSnapshotAndFailSkillBookkeeping(t *testing.T) {
	t.Run("abandonSnapshot_keeps_failed_delete_reapable", func(t *testing.T) {
		fx := newInstallFixture(t)
		require.NoError(t, fx.skillRepo.CreateSnapshotRow(context.Background(), &types.TenantSkillSnapshotEntity{
			ID: "row-abandon", TenantID: 7, SandboxConfigID: "cfg-1", SkillID: "sk-1",
			Generation: 2, Trigger: types.SkillSnapshotTriggerInstall, State: types.SkillSnapshotStateBuilding,
		}))
		mgr := &auditFailingSnapshotDeleteMgr{
			installSandboxManager: fx.sandboxMgr,
			deleteErr:             &net.OpError{Op: "dial", Err: errors.New("network unreachable")},
		}
		fx.svc.abandonSnapshot(context.Background(), 7, mgr, "row-abandon", "snap-leaked")

		rows, err := fx.skillRepo.ListSnapshotsByConfig(context.Background(), 7, "cfg-1")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, "snap-leaked", rows[0].SnapshotID)
		assert.Equal(t, types.SkillSnapshotStateSuperseded, rows[0].State,
			"failed remote snapshot delete must stay superseded so the reaper can prune it")
	})

	t.Run("failSkill_retries_transient_db_write", func(t *testing.T) {
		fx := newInstallFixture(t)
		flaky := &auditFlakySkillUpdateRepo{installSkillRepo: fx.skillRepo}
		flaky.failUpdateLeft.Store(1)
		fx.svc.skills = flaky

		fx.svc.failSkill(context.Background(), 7, "cfg-1", "sk-1", fx.bundle, errors.New("install failed"))

		skill, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
		require.NoError(t, err)
		assert.Equal(t, types.SkillStatusFailed, skill.Status)
		assert.Equal(t, "install failed", skill.Error)
	})
}

func TestAuditFix_DocumentProgressEmptyJSONOnSQLite(t *testing.T) {
	db := progressTestDB(t)
	seedProgressDocument(t, db, "doc-empty-json", false)
	require.NoError(t, db.Create(&types.TaskPendingOp{
		TenantID: 1, TaskType: wikiTaskType, Scope: wikiTaskScope,
		ScopeID: "kb", Op: WikiOpIngest, DedupKey: "doc-empty-json", Payload: []byte("{}"),
	}).Error)
	require.NoError(t, db.Exec("UPDATE knowledge_bases SET indexing_strategy = '' WHERE id = 'kb'").Error)
	require.NoError(t, db.Exec("UPDATE task_pending_ops SET payload = ''").Error)

	svc := NewDocumentProgressService(db, &progressTestQueue{})
	resp, err := svc.List(context.Background(), DocumentProgressFilter{
		Status: "unfinished", Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
}

type auditSQLiteLockCheckKBRepo struct {
	interfaces.KnowledgeBaseRepository
	probeDB *gorm.DB
	locked  bool
}

func (r *auditSQLiteLockCheckKBRepo) CountByVectorStoreID(
	_ context.Context, _ *gorm.DB, _ uint64, _ string,
) (int64, error) {
	sqlDB, err := r.probeDB.DB()
	if err != nil {
		return 0, err
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()

	_, beginErr := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE")
	if beginErr != nil {
		r.locked = true
		return 0, nil
	}
	_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	return 0, nil
}

func TestAuditFix_VectorStoreDeleteAcquiresSQLiteWriteLockUpfront(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vectorstore_lock.db")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=0"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	probeDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	probeSQLDB, err := probeDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = probeSQLDB.Close() })

	require.NoError(t, db.AutoMigrate(&types.VectorStore{}))
	store := &types.VectorStore{ID: "vs-1", TenantID: 7, Name: "primary", EngineType: "qdrant"}
	require.NoError(t, db.Create(store).Error)

	kbRepo := &auditSQLiteLockCheckKBRepo{probeDB: probeDB}
	svc := NewVectorStoreService(
		&mockVectorStoreRepo{stores: []*types.VectorStore{store}},
		kbRepo,
		newMockStoreRegistry(),
		mockEngineFactory(nil),
		db,
	)
	require.NoError(t, svc.DeleteStore(context.Background(), 7, "vs-1"))
	assert.True(t, kbRepo.locked, "SQLite writer lock must be held before CountByVectorStoreID runs")
}

func TestAuditFix_WikiRequeueFailedOpsNoDuplicateDeadLetters(t *testing.T) {
	t.Run("delete_failure_rolls_back_inserted_dead_letter", func(t *testing.T) {
		svc, db := newWikiLookupRecoveryService(t, "kb-dl", nil)
		require.NoError(t, db.AutoMigrate(&types.TaskDeadLetter{}))
		svc.deadLetterRepo = apprepo.NewTaskDeadLetterRepository(db)
		task := seedWikiLookupTask(t, svc, db, "kb-dl", "doc-dl")
		require.NoError(t, db.Model(&types.TaskPendingOp{}).
			Where("dedup_key = ?", "doc-dl").Update("fail_count", wikiMaxFailRetries).Error)
		require.NoError(t, db.Exec(
			"UPDATE knowledges SET pending_subtasks_count = 2 WHERE id = ?", "doc-dl",
		).Error)
		svc.knowledgeSvc.(*wikiKnowledgeLookupStub).lookup = func(context.Context, string) (*types.Knowledge, error) {
			return nil, errors.New("persistent lookup failure")
		}
		require.NoError(t, db.Exec(
			"CREATE TRIGGER fail_del BEFORE DELETE ON task_pending_ops "+
				"BEGIN SELECT RAISE(FAIL, 'delete unavailable'); END",
		).Error)
		require.Error(t, svc.ProcessWikiIngest(context.Background(), task))

		require.NoError(t, db.Exec("DROP TRIGGER fail_del").Error)
		require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))

		var letters []types.TaskDeadLetter
		require.NoError(t, db.Find(&letters).Error)
		assert.Len(t, letters, 1, "failed DeleteByIDs must not leave duplicate dead-letter rows")
	})

	t.Run("finalize_subtask_failure_restores_pending_op", func(t *testing.T) {
		svc, db := newWikiLookupRecoveryService(t, "kb-fin-err", nil)
		require.NoError(t, db.AutoMigrate(&types.TaskDeadLetter{}))
		svc.deadLetterRepo = apprepo.NewTaskDeadLetterRepository(db)
		task := seedWikiLookupTask(t, svc, db, "kb-fin-err", "doc-fin")
		require.NoError(t, db.Model(&types.TaskPendingOp{}).
			Where("dedup_key = ?", "doc-fin").Update("fail_count", wikiMaxFailRetries).Error)
		require.NoError(t, db.Exec(
			"UPDATE knowledges SET pending_subtasks_count = 1 WHERE id = ?", "doc-fin",
		).Error)
		svc.knowledgeSvc.(*wikiKnowledgeLookupStub).lookup = func(context.Context, string) (*types.Knowledge, error) {
			return nil, errors.New("persistent lookup failure")
		}
		require.NoError(t, db.Exec(
			"CREATE TRIGGER fail_subtask BEFORE UPDATE OF pending_subtasks_count ON knowledges "+
				"BEGIN SELECT RAISE(FAIL, 'subtask counter unavailable'); END",
		).Error)
		require.Error(t, svc.ProcessWikiIngest(context.Background(), task))

		require.NoError(t, db.Exec("DROP TRIGGER fail_subtask").Error)
		require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))

		kn, err := svc.knowledgeRepo.GetKnowledgeByIDOnly(context.Background(), "doc-fin")
		require.NoError(t, err)
		assert.Zero(t, kn.PendingSubtasksCount)
		assert.Equal(t, types.ParseStatusCompleted, kn.ParseStatus)

		var letters []types.TaskDeadLetter
		require.NoError(t, db.Find(&letters).Error)
		assert.Len(t, letters, 1)
	})
}

type auditCountingKBShareRepo struct {
	interfaces.KBShareRepository
	shares []*types.KnowledgeBaseShare
}

func (r *auditCountingKBShareRepo) ListSharedKBsForTenant(
	_ context.Context, _ uint64,
) ([]*types.KnowledgeBaseShare, error) {
	return r.shares, nil
}

type auditCountingOrgRepo struct {
	interfaces.OrganizationRepository
	members     map[string]*types.OrganizationTenantMember
	singleCalls int
	batchCalls  int
}

func (r *auditCountingOrgRepo) GetTenantMember(
	_ context.Context, orgID string, _ uint64,
) (*types.OrganizationTenantMember, error) {
	r.singleCalls++
	if tm, ok := r.members[orgID]; ok {
		return tm, nil
	}
	return nil, apprepo.ErrOrgMemberNotFound
}

func (r *auditCountingOrgRepo) ListTenantMembersByTenantForOrgs(
	_ context.Context, _ uint64, orgIDs []string,
) (map[string]*types.OrganizationTenantMember, error) {
	r.batchCalls++
	out := make(map[string]*types.OrganizationTenantMember, len(orgIDs))
	for _, id := range orgIDs {
		if tm, ok := r.members[id]; ok {
			out[id] = tm
		}
	}
	return out, nil
}

type auditZeroCountKnowledgeRepo struct {
	interfaces.KnowledgeRepository
}

func (auditZeroCountKnowledgeRepo) CountKnowledgeByKnowledgeBaseID(
	_ context.Context, _ uint64, _ string,
) (int64, error) {
	return 3, nil
}

func TestAuditFix_KBShareBatchesOrgMemberLookups(t *testing.T) {
	shares := []*types.KnowledgeBaseShare{
		{
			ID: "s1", OrganizationID: "org-1", SourceTenantID: 10, Permission: types.OrgRoleEditor,
			Organization:  &types.Organization{ID: "org-1", Name: "Org 1"},
			KnowledgeBase: &types.KnowledgeBase{ID: "kb-1", Type: types.KnowledgeBaseTypeDocument},
		},
		{
			ID: "s2", OrganizationID: "org-1", SourceTenantID: 10, Permission: types.OrgRoleViewer,
			Organization:  &types.Organization{ID: "org-1", Name: "Org 1"},
			KnowledgeBase: &types.KnowledgeBase{ID: "kb-2", Type: types.KnowledgeBaseTypeDocument},
		},
		{
			ID: "s3", OrganizationID: "org-2", SourceTenantID: 11, Permission: types.OrgRoleAdmin,
			Organization:  &types.Organization{ID: "org-2", Name: "Org 2"},
			KnowledgeBase: &types.KnowledgeBase{ID: "kb-3", Type: types.KnowledgeBaseTypeDocument},
		},
	}
	orgRepo := &auditCountingOrgRepo{
		members: map[string]*types.OrganizationTenantMember{
			"org-1": {OrganizationID: "org-1", TenantID: 99, Role: types.OrgRoleEditor},
			"org-2": {OrganizationID: "org-2", TenantID: 99, Role: types.OrgRoleViewer},
		},
	}
	svc := NewKBShareService(
		&auditCountingKBShareRepo{shares: shares},
		orgRepo,
		nil,
		auditZeroCountKnowledgeRepo{},
		nil,
		nil,
	)
	got, err := svc.ListSharedKnowledgeBases(context.Background(), 99, types.TenantRoleAdmin)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Zero(t, orgRepo.singleCalls, "ListSharedKnowledgeBases must batch org membership lookups")
	assert.Equal(t, 1, orgRepo.batchCalls)
}
