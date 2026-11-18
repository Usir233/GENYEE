package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/models/vlm"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Finding 4: Shared-KB Storage Resolution, Cross-Backend Clone, Lite Progress & Metadata
// ---------------------------------------------------------------------------

type w3aPerBackendResolver struct {
	interfaces.StorageBackendResolver
	byBackend map[string]interfaces.FileService
	tenants   []uint64
}

func (r *w3aPerBackendResolver) ResolveFileService(
	_ context.Context, tenant *types.Tenant, backendID, _, _ string,
) (interfaces.FileService, string, error) {
	if tenant != nil {
		r.tenants = append(r.tenants, tenant.ID)
	}
	if svc, ok := r.byBackend[backendID]; ok {
		return svc, "s3", nil
	}
	return nil, "", errors.New("backend not found")
}

func (r *w3aPerBackendResolver) ResolveBackend(
	_ context.Context, tenant *types.Tenant, backendID, _ string,
) (*types.StorageBackend, error) {
	if tenant != nil {
		r.tenants = append(r.tenants, tenant.ID)
	}
	return &types.StorageBackend{
		ID:       backendID,
		Provider: "s3",
		Config:   types.StorageBackendConfig{BucketName: "bucket-" + backendID},
	}, nil
}

type w3aMultiKBServiceStub struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *w3aMultiKBServiceStub) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, repository.ErrKnowledgeBaseNotFound
}

func (s *w3aMultiKBServiceStub) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return s.GetKnowledgeBaseByID(context.Background(), id)
}

type w3aKnowledgeRepoMem struct {
	interfaces.KnowledgeRepository
	rows          map[string]*types.Knowledge
	transferFails bool
	updatedCols   map[string]map[string]any
}

func (r *w3aKnowledgeRepoMem) GetKnowledgeByID(
	_ context.Context,
	tenantID uint64,
	id string,
) (*types.Knowledge, error) {
	if k, ok := r.rows[id]; ok && k.TenantID == tenantID {
		cp := *k
		return &cp, nil
	}
	return nil, repository.ErrKnowledgeNotFound
}

func (r *w3aKnowledgeRepoMem) UpdateKnowledgeForTransfer(_ context.Context, _, after *types.Knowledge) error {
	if r.transferFails {
		return errors.New("concurrent transfer conflict")
	}
	cp := *after
	r.rows[after.ID] = &cp
	return nil
}

func (r *w3aKnowledgeRepoMem) UpdateKnowledgeColumn(_ context.Context, id string, col string, val interface{}) error {
	if r.updatedCols == nil {
		r.updatedCols = map[string]map[string]any{}
	}
	if r.updatedCols[id] == nil {
		r.updatedCols[id] = map[string]any{}
	}
	r.updatedCols[id][col] = val
	if k, ok := r.rows[id]; ok && col == "summary_status" {
		if s, ok := val.(string); ok {
			k.SummaryStatus = s
		}
	}
	return nil
}

type w3aChunkRepoTransferStub struct {
	interfaces.ChunkRepository
	byKnowledge map[string][]*types.Chunk
	created     []*types.Chunk
	deleted     []string
}

func (r *w3aChunkRepoTransferStub) ListAllChunksByKnowledgeID(
	_ context.Context,
	_ uint64,
	kid string,
) ([]*types.Chunk, error) {
	return r.byKnowledge[kid], nil
}

func (r *w3aChunkRepoTransferStub) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	r.created = append(r.created, chunks...)
	return nil
}

func (r *w3aChunkRepoTransferStub) DeleteChunks(_ context.Context, _ uint64, ids []string) error {
	r.deleted = append(r.deleted, ids...)
	return nil
}

func (r *w3aChunkRepoTransferStub) DeleteChunksByKnowledgeID(_ context.Context, _ uint64, kid string) error {
	delete(r.byKnowledge, kid)
	return nil
}

func (r *w3aChunkRepoTransferStub) ListImageInfoByKnowledgeIDs(
	_ context.Context,
	_ uint64,
	_ []string,
) ([]interfaces.ChunkImageInfo, error) {
	return nil, nil
}

type w3aGraphNoop struct {
	interfaces.RetrieveGraphRepository
}

func (w3aGraphNoop) DelGraph(_ context.Context, _ []types.NameSpace) error { return nil }

func TestAuditW3A_SharedKBStorageResolutionAndCrossBackendClone(t *testing.T) {
	t.Run("shared_KB_resolves_owner_tenant_storage_and_CloneChunk_reads_source_backend", func(t *testing.T) {
		srcBackendID := "src-backend"
		dstBackendID := "dst-backend"
		srcFiles := &w3aMemObjectFileSvc{saved: map[string][]byte{
			"local://7/exports/chart.png": []byte("PNG-BYTES"),
		}}
		dstFiles := &w3aMemObjectFileSvc{saved: map[string][]byte{}}
		resolver := &w3aPerBackendResolver{byBackend: map[string]interfaces.FileService{
			srcBackendID: srcFiles,
			dstBackendID: dstFiles,
		}}

		srcKB := &types.KnowledgeBase{ID: "kb-src", TenantID: 7, StorageBackendID: &srcBackendID}
		dstKB := &types.KnowledgeBase{ID: "kb-dst", TenantID: 7, StorageBackendID: &dstBackendID}
		kbStub := &w3aMultiKBServiceStub{kbs: map[string]*types.KnowledgeBase{
			"kb-src": srcKB,
			"kb-dst": dstKB,
		}}
		tenantRepo := &w3aTenantRepoStub{tenants: map[uint64]*types.Tenant{
			7: {ID: 7, Name: "OwnerWorkspace"},
		}}

		kgRepo := &w3aKnowledgeRepoMem{rows: map[string]*types.Knowledge{
			"k-src": {ID: "k-src", TenantID: 7, KnowledgeBaseID: "kb-src", ParseStatus: types.ParseStatusCompleted},
			"k-dst": {ID: "k-dst", TenantID: 7, KnowledgeBaseID: "kb-dst", ParseStatus: types.ParseStatusProcessing},
		}}
		imgJSON, _ := json.Marshal(
			[]types.ImageInfo{{URL: "local://7/exports/chart.png", OriginalURL: "local://7/exports/chart.png"}},
		)
		chunkRepo := &w3aChunkRepoTransferStub{byKnowledge: map[string][]*types.Chunk{
			"k-src": {{
				ID:              "c-src-1",
				TenantID:        7,
				KnowledgeBaseID: "kb-src",
				KnowledgeID:     "k-src",
				ChunkType:       types.ChunkTypeImageCaption,
				Content:         "![](local://7/exports/chart.png)",
				ImageInfo:       string(imgJSON),
				IsEnabled:       true,
			}},
		}}
		svc := &knowledgeService{
			repo:            kgRepo,
			kbService:       kbStub,
			tenantRepo:      tenantRepo,
			chunkRepo:       chunkRepo,
			chunkService:    &attrsChunkService{repo: &attrsChunkRepo{}},
			storageResolver: resolver,
			fileSvc:         &w3aMemObjectFileSvc{},
		}

		// 1) Shared caller context carries TenantInfo for workspace 1 (not owner workspace 7):
		// resolveFileService & buildStorageConfig must resolve against owner Tenant 7!
		sharedCtx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{ID: 1})
		gotSvc := svc.resolveFileService(sharedCtx, srcKB)
		assert.Same(t, srcFiles, gotSvc, "shared KB must resolve owner workspace's storage backend")
		assert.Contains(t, resolver.tenants, uint64(7))
		cfg := svc.buildStorageConfig(sharedCtx, srcKB)
		assert.Equal(t, "bucket-src-backend", cfg.BucketName)

		// 2) CloneChunk across two KBs with DIFFERENT storage backends must read image bytes from srcFiles and save to
		// dstFiles!
		transferCtx, err := access.WithKBTransferTask(
			context.Background(),
			srcKB,
			dstKB,
			7,
			access.KBTransferClone,
			"task-clone",
			false,
		)
		require.NoError(t, err)
		require.NoError(t, svc.CloneChunk(transferCtx, kgRepo.rows["k-src"], kgRepo.rows["k-dst"]))
		require.Len(t, dstFiles.saved, 1, "image must be copied into destination KB backend")
	})

	t.Run("Lite_mode_nil_redis_clone_move_progress_and_race_free_FAQ_progress", func(t *testing.T) {
		svc := &knowledgeService{} // redisClient == nil (Lite mode)
		ctx := context.Background()

		require.NotPanics(t, func() {
			require.NoError(t, svc.SaveKBCloneProgress(ctx, &types.KBCloneProgress{
				TaskID:   "lite-clone-1",
				SourceID: "s",
				TargetID: "t",
				Status:   types.KBCloneStatusPending,
			}))
		})
		gotClone, err := svc.GetKBCloneProgress(ctx, "lite-clone-1")
		require.NoError(t, err)
		assert.Equal(t, types.KBCloneStatusPending, gotClone.Status)

		require.NoError(t, svc.SaveKnowledgeMoveProgress(ctx, &types.KnowledgeMoveProgress{
			TaskID:     "lite-move-1",
			SourceKBID: "s",
			TargetKBID: "t",
			Status:     types.KBCloneStatusProcessing,
		}))
		gotMove, err := svc.GetKnowledgeMoveProgress(ctx, "lite-move-1")
		require.NoError(t, err)
		assert.Equal(t, types.KBCloneStatusProcessing, gotMove.Status)

		// Concurrent saveFAQImportProgress + GetFAQImportProgress + caller mutation must be race-free under -race.
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(2)
			go func(step int) {
				defer wg.Done()
				_ = svc.saveFAQImportProgress(ctx, &types.FAQImportProgress{
					TaskID:   "lite-faq-race",
					Status:   types.FAQImportStatusProcessing,
					Progress: step,
				})
			}(i)
			go func() {
				defer wg.Done()
				if p, err := svc.GetFAQImportProgress(ctx, "lite-faq-race"); err == nil && p != nil {
					p.Progress = 999 // mutating returned struct must not race with reader/writer
				}
			}()
		}
		wg.Wait()
	})

	t.Run("cleanupKnowledgeResources_handles_missing_TenantInfo_and_mergeKnowledgeMetadata_strips_reserved_keys", func(
		t *testing.T,
	) {
		kb := &types.KnowledgeBase{ID: "kb-clean", TenantID: 7}
		chunkRepo := &w3aChunkRepoTransferStub{byKnowledge: map[string][]*types.Chunk{}}
		sharedTenant := &types.Tenant{ID: 7, StorageUsed: 500}
		svc := &knowledgeService{
			kbService:    &w3aMultiKBServiceStub{kbs: map[string]*types.KnowledgeBase{"kb-clean": kb}},
			chunkRepo:    chunkRepo,
			chunkService: &vectorChunkService{repo: &vectorChunkRepo{chunks: map[string]*types.Chunk{}}},
			graphEngine:  w3aGraphNoop{},
			tenantRepo:   &w3aTenantRepoStub{tenants: map[uint64]*types.Tenant{7: sharedTenant}},
			fileSvc:      &w3aMemObjectFileSvc{},
		}
		// Ensure ListImageInfoByKnowledgeIDs doesn't panic on vectorChunkRepo by wiring chunkRepo as chunkService.
		svc.chunkService = &w3aChunkSvcWithRepo{repo: chunkRepo}

		doc := &types.Knowledge{
			ID:              "k-clean",
			TenantID:        7,
			KnowledgeBaseID: "kb-clean",
			ParseStatus:     types.ParseStatusCompleted,
			StorageSize:     100,
		}
		base := types.WithCaller(
			context.Background(),
			types.Caller{TenantID: 7, UserID: "u", Role: types.TenantRoleAdmin},
		)
		// Deliberately omit TenantInfoContextKey from ctx: must NOT panic!
		var err error
		require.NotPanics(t, func() {
			err = svc.cleanupKnowledgeResources(base, doc)
		})
		require.NoError(t, err)
		assert.Equal(t, int64(500), sharedTenant.StorageUsed, "must not mutate shared Tenant pointer in place")

		// Reserved metadata keys (_knowledge_transfer, process_overrides) must be ignored in caller metadata updates!
		initial := types.JSON(`{"keep":"1","process_overrides":{"chunk_size":512}}`)
		merged, err := mergeKnowledgeMetadata(initial, map[string]string{
			"user_key":                         "ok",
			types.KnowledgeTransferMetadataKey: "corrupted-string",
			"process_overrides":                "not-an-object",
		})
		require.NoError(t, err)
		m, err := merged.Map()
		require.NoError(t, err)
		assert.Equal(t, "ok", m["user_key"])
		assert.NotContains(t, m, types.KnowledgeTransferMetadataKey)
		_, isObj := m["process_overrides"].(map[string]any)
		assert.True(t, isObj, "reserved process_overrides object must not be clobbered by string metadata")
	})
}

type w3aChunkSvcWithRepo struct {
	interfaces.ChunkService
	repo interfaces.ChunkRepository
}

func (s *w3aChunkSvcWithRepo) GetRepository() interfaces.ChunkRepository { return s.repo }

// ---------------------------------------------------------------------------
// Finding 8: Async Retries, Cleanup, Span Leak, Summary Clobber & Bounds
// ---------------------------------------------------------------------------

type w3aDeleteIndexRecorder struct {
	interfaces.RetrieveEngineService
	deleted []string
	failIdx bool
}

func (r *w3aDeleteIndexRecorder) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (r *w3aDeleteIndexRecorder) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (r *w3aDeleteIndexRecorder) DeleteBySourceIDList(_ context.Context, ids []string, _ int, _ string) error {
	r.deleted = append(r.deleted, ids...)
	return nil
}

func (r *w3aDeleteIndexRecorder) BatchIndex(
	_ context.Context,
	_ embedding.Embedder,
	_ []*types.IndexInfo,
	_ []types.RetrieverType,
) error {
	if r.failIdx {
		return errors.New("vector backend timeout")
	}
	return nil
}

type w3aEmbedderStub struct {
	embedding.Embedder
}

func (w3aEmbedderStub) GetDimensions() int { return 3 }

type w3aModelSvcStub struct {
	interfaces.ModelService
	embedder embedding.Embedder
}

func (s *w3aModelSvcStub) GetEmbeddingModel(_ context.Context, _ string) (embedding.Embedder, error) {
	return s.embedder, nil
}

type w3aSimpleVLM struct{}

func (w3aSimpleVLM) Predict(_ context.Context, _ [][]byte, _ string) (string, error) {
	return "contain.text: none\ncontain.data_visual: false\nDESCRIPTION: A clean diagram.", nil
}

func (w3aSimpleVLM) PredictWithOptions(_ context.Context, b [][]byte, p string, _ *vlm.PredictOptions) (string, error) {
	return w3aSimpleVLM{}.Predict(context.Background(), b, p)
}
func (w3aSimpleVLM) GetModelName() string { return "vlm" }
func (w3aSimpleVLM) GetModelID() string   { return "vlm-id" }

func TestAuditW3A_AsyncRetryAndResourceCleanup(t *testing.T) {
	t.Run("ProcessFAQImport_preserves_Completed_status_on_redelivery", func(t *testing.T) {
		f := newFAQWriteFixture(t)
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		f.svc.redisClient = rdb
		f.svc.tenantRepo = faqAdmissionTenant{}

		payload := types.FAQImportPayload{
			TenantID:    7,
			KBID:        "kb",
			KnowledgeID: "faq",
			TaskID:      "task-done",
			InstanceID:  "inst-1",
			Mode:        types.FAQBatchModeAppend,
			Entries: []types.FAQEntryPayload{{
				StandardQuestion: "Already imported?",
				Answers:          []string{"Yes."},
			}},
		}
		ctx := types.WithExecutionTenant(context.Background(), 7)
		require.NoError(t, f.svc.saveFAQImportProgress(ctx, &types.FAQImportProgress{
			TaskID:       "task-done",
			KBID:         "kb",
			KnowledgeID:  "faq",
			Status:       types.FAQImportStatusCompleted,
			Progress:     100,
			Total:        1,
			SuccessCount: 1,
		}))
		require.NoError(t, f.svc.setRunningFAQImportInfo(ctx, "kb", &runningFAQImportInfo{
			TaskID:     "task-done",
			InstanceID: "inst-1",
		}))

		task := asynq.NewTask(types.TypeFAQImport, mustWriteJSON(t, payload))
		require.NoError(t, f.svc.ProcessFAQImport(ctx, task))

		after, err := f.svc.GetFAQImportProgress(ctx, "task-done")
		require.NoError(t, err)
		assert.Equal(
			t,
			types.FAQImportStatusCompleted,
			after.Status,
			"duplicate redelivery must not overwrite Completed status",
		)
		assert.Equal(t, 100, after.Progress)
	})

	t.Run(
		"DataTableSummary_cleanupOnFailure_uses_WithoutCancel_and_respects_retry_budget_while_fencing_transfers",
		func(
			t *testing.T,
		) {
			kgRepo := &w3aKnowledgeRepoMem{
				rows: map[string]*types.Knowledge{
					"k-tbl": {
						ID:              "k-tbl",
						TenantID:        7,
						KnowledgeBaseID: "kb-tbl",
						ParseStatus:     types.ParseStatusProcessing,
					},
				},
			}
			chunkRepo := &w3aChunkRepoTransferStub{}
			idx := &w3aDeleteIndexRecorder{}
			compositeEngine, err := retriever.NewCompositeRetrieveEngine(
				parentChildRetrieveRegistry{engine: idx},
				[]types.RetrieverEngineParams{{
					RetrieverEngineType: types.PostgresRetrieverEngineType,
					RetrieverType:       types.VectorRetrieverType,
				}},
			)
			require.NoError(t, err)
			dtSvc := &DataTableSummaryService{
				knowledgeService: &w3aKGSvcWithRepo{repo: kgRepo},
				chunkService:     &w3aChunkSvcWithRepo{repo: chunkRepo},
			}
			res := &extractionResources{
				knowledge:      kgRepo.rows["k-tbl"],
				retrieveEngine: compositeEngine,
				embeddingModel: w3aEmbedderStub{},
			}
			// Cancelled + retryable context (retried=0 < limit=3): chunks & vectors must still be cleaned up via
			// WithoutCancel,
			// and ParseStatus must remain processing rather than prematurely stamping ParseStatusFailed!
			cancelledBase, cancel := context.WithCancel(context.Background())
			cancel()
			retryCtx := types.WithTaskRetryMetadata(cancelledBase, 0, 3)
			dtSvc.cleanupOnFailure(retryCtx, res, []*types.Chunk{{ID: "tbl-c1"}}, errors.New("embed timeout"))
			assert.Equal(
				t,
				[]string{"tbl-c1"},
				chunkRepo.deleted,
				"chunks must be deleted via WithoutCancel on retryable failure",
			)
			assert.Equal(t, []string{"tbl-c1"}, idx.deleted, "vector index must be deleted on failure")
			assert.Equal(
				t,
				types.ParseStatusProcessing,
				kgRepo.rows["k-tbl"].ParseStatus,
				"non-final retry must not permanently stamp ParseStatusFailed",
			)
		})

	t.Run("ImageMultimodal_bounds_readImageBytes_and_rolls_back_chunks_on_BatchIndex_failure", func(t *testing.T) {
		t.Setenv("MAX_FILE_SIZE_MB", "1")
		oversized := bytes.Repeat([]byte("A"), (1<<20)+64)
		mmSvc := &ImageMultimodalService{
			fileSvc:    &attrsFileService{body: oversized},
			tenantRepo: &attrsTenantRepo{},
		}
		_, err := mmSvc.readImageBytes(context.Background(), types.ImageMultimodalPayload{
			TenantID: 1,
			ImageURL: "local://1/exports/huge.png",
		})
		require.Error(t, err, "oversized stored image must be rejected before unbounded buffering")

		// BatchIndex failure on a vector-enabled KB must delete newly created SQL chunks and return error for retry!
		chunkRepo := &w3aChunkRepoTransferStub{}
		failingEngine := &w3aDeleteIndexRecorder{failIdx: true}
		storeID := "store-mm"
		kb := &types.KnowledgeBase{
			ID:               "kb-mm",
			TenantID:         7,
			EmbeddingModelID: "emb-1",
			VectorStoreID:    &storeID,
			IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		}
		mmRetrySvc := &ImageMultimodalService{
			chunkService: &w3aChunkSvcWithRepo{repo: chunkRepo},
			kbService:    &w3aMultiKBServiceStub{kbs: map[string]*types.KnowledgeBase{"kb-mm": kb}},
			modelService: &w3aModelSvcStub{embedder: w3aEmbedderStub{}},
			tenantRepo:   &w3aTenantRepoStub{tenants: map[uint64]*types.Tenant{7: {ID: 7}}},
			retrieveEngine: &fakeFanoutRegistry{
				byStore: map[string]interfaces.RetrieveEngineService{storeID: failingEngine},
			},
			ownership: &fakeOwnership{owned: map[string]uint64{storeID: 7}},
			fileSvc:   &attrsFileService{body: testPNG(t)},
		}
		err = mmRetrySvc.processImage(context.Background(), &types.ImageMultimodalPayload{
			TenantID:          7,
			KnowledgeBaseID:   "kb-mm",
			KnowledgeID:       "k-mm",
			ChunkID:           "parent-1",
			ImageURL:          "local://7/exports/ok.png",
			ImageAttrsEnabled: true,
			ImageActions:      types.DefaultImageActions(),
		}, w3aSimpleVLM{}, types.VLMConfig{}, noopSpanTracker{}, types.JSONMap{})
		require.Error(t, err, "BatchIndex failure must propagate so Asynq retries")
		require.NotEmpty(t, chunkRepo.deleted, "unindexed SQL chunks must be rolled back on BatchIndex failure")
	})

	t.Run(
		"enqueueSummaryRefresh_preserves_completed_status_when_SummaryModelID_empty_and_RequestKnowledgeSummaryRefre"+
			"sh_checks_write_auth",
		func(
			t *testing.T,
		) {
			kgRepo := &w3aKnowledgeRepoMem{rows: map[string]*types.Knowledge{
				"k-sum": {
					ID:              "k-sum",
					TenantID:        7,
					KnowledgeBaseID: "kb-no-sum",
					SummaryStatus:   types.SummaryStatusCompleted,
				},
			}}
			kbNoSum := &types.KnowledgeBase{ID: "kb-no-sum", TenantID: 7, SummaryModelID: ""}
			kbStub := &w3aMultiKBServiceStub{kbs: map[string]*types.KnowledgeBase{"kb-no-sum": kbNoSum}}

			err := enqueueSummaryRefresh(
				context.Background(),
				kgRepo,
				&metadataUpdateTaskEnqueuer{},
				kbStub,
				noopSpanTracker{},
				kgRepo.rows["k-sum"],
			)
			require.Error(t, err)
			assert.Equal(t, types.SummaryStatusCompleted, kgRepo.rows["k-sum"].SummaryStatus,
				"missing SummaryModelID must not clobber an existing completed summary to failed")

			// Shared read-only viewer must NOT be allowed to trigger RequestKnowledgeSummaryRefresh!
			svc := &knowledgeService{repo: kgRepo, kbService: kbStub, task: &metadataUpdateTaskEnqueuer{}}
			viewerBase := types.WithCaller(
				context.Background(),
				types.Caller{TenantID: 1, UserID: "viewer", Role: types.TenantRoleViewer},
			)
			viewerGrant := &access.KBAccess{
				KnowledgeBase:     kbNoSum,
				Caller:            types.CallerFromContext(viewerBase),
				EffectiveTenantID: 7,
				Permission:        types.OrgRoleViewer,
			}
			require.Error(t, svc.RequestKnowledgeSummaryRefresh(viewerGrant.Context(viewerBase), "k-sum"))
		})

	t.Run("spanTracker_cleans_starts_map_on_SkipSpan_FinalizeAttempt_and_AbortAttempt", func(t *testing.T) {
		trackerIface, _ := setupSpanTrackerTest(t)
		rawTracker, ok := trackerIface.(*spanTracker)
		require.True(t, ok)
		ctx := context.Background()

		_, attempt1, err := rawTracker.OpenAttempt(ctx, "k-span-1", "")
		require.NoError(t, err)
		mm := rawTracker.BeginStage(ctx, "k-span-1", attempt1, types.StageMultimodal, nil)
		rawTracker.SkipSpan(ctx, mm, "no images")
		rawTracker.FinalizeAttempt(ctx, "k-span-1", attempt1, types.SpanStatusDone, nil, "", "")

		_, attempt2, err := rawTracker.OpenAttempt(ctx, "k-span-2", "")
		require.NoError(t, err)
		_ = rawTracker.BeginStage(ctx, "k-span-2", attempt2, types.StageDocReader, nil)
		rawTracker.AbortAttempt(ctx, "k-span-2", attempt2, "USER_CANCELLED", "cancelled", "cancelled")

		rawTracker.startsMu.Lock()
		remaining := len(rawTracker.starts)
		rawTracker.startsMu.Unlock()
		assert.Zero(t, remaining, "starts map must not leak entries across finalized/skipped/aborted spans")
	})

	t.Run("ArtifactCollector_hashStoredArtifact_bounds_reads_and_DispatchEmbedWebhook_protects_envelope", func(
		t *testing.T,
	) {
		collector := NewArtifactCollector(
			nil,
			&attrsFileService{body: bytes.Repeat([]byte("X"), 2048)},
			nil,
			nil,
			ArtifactCollectorConfig{MaxFileBytes: 512},
		)
		gotHash := collector.hashStoredArtifact(context.Background(), types.MessageArtifact{URL: "local://1/art.bin"})
		assert.Empty(t, gotHash, "stored artifact exceeding MaxFileBytes must not be buffered in full")

		withSSRFWhitelist(t, "127.0.0.1")
		received := make(chan map[string]any, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			received <- m
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		DispatchEmbedWebhook(&types.EmbedChannel{
			ID:         "ch-real",
			WebhookURL: srv.URL,
		}, "message.created", "sess-real", map[string]any{
			"type":       "spoofed.event",
			"channel_id": "spoofed-ch",
			"session_id": "spoofed-sess",
			"timestamp":  "1970-01-01T00:00:00Z",
			"query":      "hello",
		})

		select {
		case body := <-received:
			assert.Equal(t, "message.created", body["type"])
			assert.Equal(t, "ch-real", body["channel_id"])
			assert.Equal(t, "sess-real", body["session_id"])
			assert.NotEqual(t, "1970-01-01T00:00:00Z", body["timestamp"])
			assert.Equal(t, "hello", body["query"])
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for embed webhook dispatch")
		}
	})
}

type w3aKGSvcWithRepo struct {
	interfaces.KnowledgeService
	repo interfaces.KnowledgeRepository
}

func (s *w3aKGSvcWithRepo) GetRepository() interfaces.KnowledgeRepository { return s.repo }
