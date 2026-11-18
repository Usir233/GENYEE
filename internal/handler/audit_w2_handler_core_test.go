package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/middleware"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type countingBodyReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingBodyReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

type stubIMAgentService struct {
	interfaces.CustomAgentService
	agents map[string]*types.CustomAgent
}

func (s *stubIMAgentService) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	if a, ok := s.agents[id]; ok {
		return a, nil
	}
	return nil, errors.New("agent not found")
}

func TestAuditW2_IMCreateAndUpdateRedactCredentialsAndRequireNonZeroTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.Exec(`CREATE TABLE im_channels (
		id TEXT PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		agent_id TEXT NOT NULL,
		platform TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		enabled NUMERIC NOT NULL DEFAULT 1,
		mode TEXT NOT NULL DEFAULT 'websocket',
		output_mode TEXT NOT NULL DEFAULT 'stream',
		locale TEXT NOT NULL DEFAULT '',
		knowledge_base_id TEXT DEFAULT '',
		bot_identity TEXT NOT NULL DEFAULT '',
		session_mode TEXT NOT NULL DEFAULT 'user',
		credentials TEXT NOT NULL DEFAULT '{}',
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("CREATE TABLE im_channels: %v", err)
	}

	agentSvc := &stubIMAgentService{
		agents: map[string]*types.CustomAgent{
			"agent-77": {ID: "agent-77", TenantID: 77, Name: "Support Bot"},
		},
	}
	imSvc := im.NewService(db, nil, nil, nil, agentSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	defer imSvc.Stop()

	h := NewIMHandler(imSvc)
	router := gin.New()
	router.POST("/agents/:id/im-channels", h.CreateIMChannel)
	router.PUT("/im-channels/:id", h.UpdateIMChannel)
	router.GET("/im-channels", h.ListAllIMChannels)

	// 1. Zero tenant ID in context must be rejected with 401.
	zeroReq := httptest.NewRequest(
		http.MethodPost,
		"/agents/agent-77/im-channels",
		strings.NewReader(`{"platform":"wecom","mode":"webhook","enabled":false}`),
	)
	zeroReq.Header.Set("Content-Type", "application/json")
	zeroReq = zeroReq.WithContext(context.WithValue(zeroReq.Context(), types.TenantIDContextKey, uint64(0)))
	zeroRec := httptest.NewRecorder()
	router.ServeHTTP(zeroRec, zeroReq)
	if zeroRec.Code != http.StatusUnauthorized {
		t.Fatalf("zero tenant CreateIMChannel status = %d, want 401; body=%s", zeroRec.Code, zeroRec.Body.String())
	}

	// 2. CreateIMChannel must NOT echo plaintext secret credentials in response.
	const secret = "super-secret-wecom-agent-key-999"
	createBody := `{"platform":"wecom","name":"ops","mode":"webhook","enabled":false,` +
		`"credentials":{"corp_id":"ww77","corp_agent_id":"1000002","agent_secret":"` + secret + `"}}`
	createReq := httptest.NewRequest(http.MethodPost, "/agents/agent-77/im-channels", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq = createReq.WithContext(context.WithValue(createReq.Context(), types.TenantIDContextKey, uint64(77)))
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("CreateIMChannel status = %d, want 200; body=%s", createRec.Code, createRec.Body.String())
	}
	if strings.Contains(createRec.Body.String(), secret) || strings.Contains(createRec.Body.String(), `"credentials"`) {
		t.Fatalf("CreateIMChannel leaked credentials in response: %s", createRec.Body.String())
	}

	var created struct {
		Data struct {
			ID                    string `json:"id"`
			CredentialsConfigured bool   `json:"credentials_configured"`
		} `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal CreateIMChannel response: %v", err)
	}
	if created.Data.ID == "" || !created.Data.CredentialsConfigured {
		t.Fatalf("expected non-empty channel ID and credentials_configured=true, got %+v", created.Data)
	}

	// 3. UpdateIMChannel (even when only renaming!) must NOT echo stored decrypted credentials.
	updateReq := httptest.NewRequest(
		http.MethodPut,
		"/im-channels/"+created.Data.ID,
		strings.NewReader(`{"name":"ops-renamed"}`),
	)
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq = updateReq.WithContext(context.WithValue(updateReq.Context(), types.TenantIDContextKey, uint64(77)))
	updateRec := httptest.NewRecorder()
	router.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("UpdateIMChannel status = %d, want 200; body=%s", updateRec.Code, updateRec.Body.String())
	}
	if strings.Contains(updateRec.Body.String(), secret) || strings.Contains(updateRec.Body.String(), `"credentials"`) {
		t.Fatalf("UpdateIMChannel leaked stored credentials in response: %s", updateRec.Body.String())
	}
}

func TestAuditW2_TestMultimodalFunctionCapsMultipartBeforeParse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("MAX_FILE_SIZE_MB", "1")

	router := gin.New()
	router.Use(middleware.ErrorHandler())
	h := &InitializationHandler{}
	router.POST("/initialization/multimodal/test", h.TestMultimodalFunction)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("vlm_model", "gpt-4o-mini")
	_ = mw.WriteField("vlm_base_url", "https://example.com/v1")
	_ = mw.WriteField("storage_type", "minio")
	_ = mw.WriteField("minio_bucket_name", "bucket")
	part, err := mw.CreateFormFile("image", "huge.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	// 4 MiB payload exceeds 1 MiB MAX_FILE_SIZE_MB + 1 MiB uploadEnvelopeSlack.
	if _, err := part.Write(bytes.Repeat([]byte("x"), 4<<20)); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("mw.Close: %v", err)
	}

	counter := &countingBodyReader{r: bytes.NewReader(buf.Bytes())}
	req := httptest.NewRequest(http.MethodPost, "/initialization/multimodal/test", counter)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("TestMultimodalFunction status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	maxExpectedRead := int64((1 << 20) + uploadEnvelopeSlack + (64 << 10))
	if got := counter.n.Load(); got > maxExpectedRead {
		t.Fatalf(
			"expected multipart stream to stop near cap (%d bytes), but handler read %d bytes", maxExpectedRead,
			got,
		)
	}
}

type stubEvaluationService struct {
	interfaces.EvaluationService
	evalCalled   bool
	resultCalled bool
}

func (s *stubEvaluationService) Evaluation(
	ctx context.Context, datasetID, _, _, _ string,
) (*types.EvaluationDetail, error) {
	s.evalCalled = true
	tid := types.MustTenantIDFromContext(ctx)
	return &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        utils.GenerateTaskID("evaluation", tid, datasetID),
			TenantID:  tid,
			DatasetID: datasetID,
		},
	}, nil
}

func (s *stubEvaluationService) EvaluationResult(ctx context.Context, _ string) (*types.EvaluationDetail, error) {
	s.resultCalled = true
	_ = types.MustTenantIDFromContext(ctx)
	return nil, errors.New("tenant ID does not match")
}

func TestAuditW2_EvaluationEnforcesTenantAndAPIKeyKBScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("scoped_api_key_cannot_evaluate_out_of_scope_or_empty_kb", func(t *testing.T) {
		for _, kbID := range []string{"kb-forbidden", ""} {
			evalSvc := &stubEvaluationService{}
			h := &EvaluationHandler{evaluationService: evalSvc}
			r := gin.New()
			r.Use(middleware.ErrorHandler())
			r.POST("/evaluation", func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
				ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
					KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
				})
				c.Request = c.Request.WithContext(ctx)
				c.Set(string(types.TenantIDContextKey), uint64(1))
				h.Evaluation(c)
			})

			payload, _ := json.Marshal(EvaluationRequest{DatasetID: "default", KnowledgeBaseID: kbID})
			req := httptest.NewRequest(http.MethodPost, "/evaluation", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("kb_id=%q: status = %d, want 403; body=%s", kbID, rec.Code, rec.Body.String())
			}
			if evalSvc.evalCalled {
				t.Fatalf("kb_id=%q: evaluationService.Evaluation must not be called", kbID)
			}
		}
	})

	t.Run("zero_tenant_rejected_with_401_without_panic", func(t *testing.T) {
		evalSvc := &stubEvaluationService{}
		h := &EvaluationHandler{evaluationService: evalSvc}
		r := gin.New()
		r.Use(middleware.ErrorHandler())
		r.POST("/evaluation", func(c *gin.Context) {
			c.Set(string(types.TenantIDContextKey), uint64(0))
			h.Evaluation(c)
		})

		req := httptest.NewRequest(http.MethodPost, "/evaluation", strings.NewReader(`{"dataset_id":"default"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("oversized_json_body_rejected_with_413", func(t *testing.T) {
		evalSvc := &stubEvaluationService{}
		h := &EvaluationHandler{evaluationService: evalSvc}
		r := gin.New()
		r.Use(middleware.ErrorHandler())
		r.POST("/evaluation", func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
			c.Request = c.Request.WithContext(ctx)
			c.Set(string(types.TenantIDContextKey), uint64(1))
			h.Evaluation(c)
		})

		bigBody := `{"dataset_id":"` + strings.Repeat("a", 128<<10) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/evaluation", strings.NewReader(bigBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("get_result_hides_cross_tenant_task_as_404_and_rejects_unauthenticated_without_panic", func(t *testing.T) {
		evalSvc := &stubEvaluationService{}
		h := &EvaluationHandler{evaluationService: evalSvc}
		r := gin.New()
		r.Use(middleware.ErrorHandler())
		r.GET("/evaluation", h.GetEvaluationResult)

		foreignTaskID := utils.GenerateTaskID("evaluation", 999, "default")

		// Unauthenticated request must return 401 without panicking in MustTenantIDFromContext.
		unauthReq := httptest.NewRequest(http.MethodGet, "/evaluation?task_id="+foreignTaskID, nil)
		unauthRec := httptest.NewRecorder()
		r.ServeHTTP(unauthRec, unauthReq)
		if unauthRec.Code != http.StatusUnauthorized {
			t.Fatalf(
				"unauthenticated GetEvaluationResult status = %d, want 401; body=%s", unauthRec.Code,
				unauthRec.Body.String(),
			)
		}

		// Cross-tenant task probe must return 404 Not Found without reaching EvaluationResult.
		crossReq := httptest.NewRequest(http.MethodGet, "/evaluation?task_id="+foreignTaskID, nil)
		crossReq = crossReq.WithContext(context.WithValue(crossReq.Context(), types.TenantIDContextKey, uint64(1)))
		crossRec := httptest.NewRecorder()
		r.ServeHTTP(crossRec, crossReq)
		if crossRec.Code != http.StatusNotFound {
			t.Fatalf(
				"cross-tenant GetEvaluationResult status = %d, want 404; body=%s", crossRec.Code,
				crossRec.Body.String(),
			)
		}
		if evalSvc.resultCalled {
			t.Fatal("evaluationService.EvaluationResult must not be called for cross-tenant task_id")
		}
	})

	t.Run("cross_tenant_knowledge_base_id_rejected_with_403", func(t *testing.T) {
		evalSvc := &stubEvaluationService{}
		kbSvc := &stubKBServiceForAudit{
			kbs: map[string]*types.KnowledgeBase{
				"kb-foreign": {ID: "kb-foreign", TenantID: 999, Name: "Other Workspace KB"},
			},
		}
		h := NewEvaluationHandler(evalSvc, kbSvc)
		r := gin.New()
		r.Use(middleware.ErrorHandler())
		r.POST("/evaluation", func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
			c.Request = c.Request.WithContext(ctx)
			c.Set(string(types.TenantIDContextKey), uint64(1))
			h.Evaluation(c)
		})

		req := httptest.NewRequest(
			http.MethodPost,
			"/evaluation",
			strings.NewReader(`{"dataset_id":"default","knowledge_base_id":"kb-foreign"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-tenant Evaluation status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if evalSvc.evalCalled {
			t.Fatal("evaluationService.Evaluation must not be called for foreign KB")
		}
	})
}

type stubDSCredService struct {
	interfaces.DataSourceService
	ds          map[string]*types.DataSource
	putCount    int
	deleteCount int
}

func (s *stubDSCredService) GetDataSource(_ context.Context, id string) (*types.DataSource, error) {
	if d, ok := s.ds[id]; ok {
		return d, nil
	}
	return nil, errors.New("not found")
}

func (s *stubDSCredService) UpdateDataSourceCredentials(
	_ context.Context, id string, _ map[string]interface{},
) (*types.DataSource, error) {
	s.putCount++
	return s.ds[id], nil
}

func (s *stubDSCredService) ClearDataSourceCredentials(_ context.Context, _ string) error {
	s.deleteCount++
	return nil
}

type stubKBServiceForAudit struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *stubKBServiceForAudit) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, repository.ErrKnowledgeBaseNotFound
}

func (s *stubKBServiceForAudit) ListKnowledgeBases(_ context.Context) ([]*types.KnowledgeBase, error) {
	out := make([]*types.KnowledgeBase, 0, len(s.kbs))
	for _, kb := range s.kbs {
		out = append(out, kb)
	}
	return out, nil
}

func TestAuditW2_DataSourceCredentialsEnforcesAPIKeyKBScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dsSvc := &stubDSCredService{
		ds: map[string]*types.DataSource{
			"ds-secret": {ID: "ds-secret", TenantID: 1, KnowledgeBaseID: "kb-secret", Type: "notion"},
		},
	}
	kbSvc := &stubKBServiceForAudit{
		kbs: map[string]*types.KnowledgeBase{
			"kb-secret":  {ID: "kb-secret", TenantID: 1, Name: "Secret KB"},
			"kb-allowed": {ID: "kb-allowed", TenantID: 1, Name: "Allowed KB"},
		},
	}
	h := NewDataSourceCredentialsHandler(dsSvc, kbSvc)

	newScopedRouter := func(allowedKB string) *gin.Engine {
		r := gin.New()
		r.Use(middleware.ErrorHandler())
		r.Use(func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
			ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
				KnowledgeBaseIDs: types.StringArray{allowedKB},
			})
			c.Request = c.Request.WithContext(ctx)
			c.Set(types.TenantIDContextKey.String(), uint64(1))
			c.Next()
		})
		r.PUT("/data-sources/:id/credentials", h.Put)
		r.DELETE("/data-sources/:id/credentials/:field", h.DeleteField)
		return r
	}

	// Out-of-scope API key must be rejected with 403 on both PUT and DELETE.
	deniedRouter := newScopedRouter("kb-allowed")

	putReq := httptest.NewRequest(
		http.MethodPut, "/data-sources/ds-secret/credentials",
		strings.NewReader(`{"credentials":{"api_key":"steal"}}`),
	)
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	deniedRouter.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusForbidden {
		t.Fatalf("PUT out-of-scope status = %d, want 403; body=%s", putRec.Code, putRec.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/data-sources/ds-secret/credentials/credentials", nil)
	delRec := httptest.NewRecorder()
	deniedRouter.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusForbidden {
		t.Fatalf("DELETE out-of-scope status = %d, want 403; body=%s", delRec.Code, delRec.Body.String())
	}
	if dsSvc.putCount != 0 || dsSvc.deleteCount != 0 {
		t.Fatalf("service must not be called for out-of-scope key: put=%d delete=%d", dsSvc.putCount, dsSvc.deleteCount)
	}

	// In-scope API key still succeeds.
	allowedRouter := newScopedRouter("kb-secret")
	okPutReq := httptest.NewRequest(
		http.MethodPut, "/data-sources/ds-secret/credentials",
		strings.NewReader(`{"credentials":{"api_key":"legit"}}`),
	)
	okPutReq.Header.Set("Content-Type", "application/json")
	okPutRec := httptest.NewRecorder()
	allowedRouter.ServeHTTP(okPutRec, okPutReq)
	if okPutRec.Code != http.StatusOK {
		t.Fatalf("PUT in-scope status = %d, want 200; body=%s", okPutRec.Code, okPutRec.Body.String())
	}
}

func TestAuditW2_KnowledgeBaseListMoveTargetsFiltersAPIKeyScopeAndTypeAssertSafety(t *testing.T) {
	gin.SetMode(gin.TestMode)

	kbSvc := &stubKBServiceForAudit{
		kbs: map[string]*types.KnowledgeBase{
			"kb-src": {
				ID: "kb-src", TenantID: 1, Type: types.KnowledgeBaseTypeDocument, EmbeddingModelID: "emb-1",
				Name: "Source",
			},
			"kb-in-scope": {
				ID: "kb-in-scope", TenantID: 1, Type: types.KnowledgeBaseTypeDocument, EmbeddingModelID: "emb-1",
				Name: "In Scope Target",
			},
			"kb-out-of-scope": {
				ID: "kb-out-of-scope", TenantID: 1, Type: types.KnowledgeBaseTypeDocument,
				EmbeddingModelID: "emb-1", Name: "Secret Out Of Scope KB",
			},
		},
	}

	t.Run("list_move_targets_filters_out_of_scope_kbs", func(t *testing.T) {
		h := &KnowledgeBaseHandler{service: kbSvc}
		r := gin.New()
		r.Use(middleware.ErrorHandler())
		r.GET("/knowledge-bases/:id/move-targets", func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
			ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
				KnowledgeBaseIDs: types.StringArray{"kb-src", "kb-in-scope"},
			})
			c.Request = c.Request.WithContext(ctx)
			c.Set(types.TenantIDContextKey.String(), uint64(1))
			h.ListMoveTargets(c)
		})

		req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-src/move-targets", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "kb-out-of-scope") {
			t.Fatalf("ListMoveTargets leaked out-of-scope KB metadata: %s", rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "kb-in-scope") {
			t.Fatalf("ListMoveTargets omitted allowed in-scope KB: %s", rec.Body.String())
		}
	})

	t.Run("duplicate_kb_enforces_api_key_scope_and_non_zero_tenant", func(t *testing.T) {
		dupCalled := false
		svc := &stubKBCopyService{
			byID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
				return &types.KnowledgeBase{ID: id, TenantID: 1, Name: "Source"}, nil
			},
			duplicate: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
				dupCalled = true
				return &types.KnowledgeBase{ID: "dup", TenantID: 1}, nil
			},
		}
		h := &KnowledgeBaseHandler{service: svc}
		r := gin.New()
		r.Use(middleware.ErrorHandler())
		r.POST("/knowledge-bases/:id/duplicate", func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
			ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
				KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
			})
			c.Request = c.Request.WithContext(ctx)
			c.Set(types.TenantIDContextKey.String(), uint64(1))
			h.DuplicateKnowledgeBase(c)
		})

		req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-out-of-scope/duplicate", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("DuplicateKnowledgeBase out-of-scope status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if dupCalled {
			t.Fatal("DuplicateKnowledgeBase service must not be called for out-of-scope API key")
		}
	})

	t.Run("malformed_gin_tenant_value_never_panics", func(t *testing.T) {
		kbH := &KnowledgeBaseHandler{service: kbSvc}
		kgH := &KnowledgeHandler{kbService: kbSvc}

		r := gin.New()
		r.Use(middleware.ErrorHandler())
		r.Use(func(c *gin.Context) {
			// Malformed non-uint64 value in Gin context while request context has valid tenant.
			c.Set(types.TenantIDContextKey.String(), "malformed-tenant")
			ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		})
		r.GET("/knowledge-bases/:id/move-targets", kbH.ListMoveTargets)
		r.DELETE("/knowledge-bases/:id", kbH.DeleteKnowledgeBase)
		r.POST("/knowledge/move", kgH.MoveKnowledge)

		rec1 := httptest.NewRecorder()
		r.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-src/move-targets", nil))
		if rec1.Code != http.StatusUnauthorized {
			t.Fatalf("ListMoveTargets malformed tenant status = %d, want 401", rec1.Code)
		}

		rec2 := httptest.NewRecorder()
		r.ServeHTTP(rec2, httptest.NewRequest(http.MethodDelete, "/knowledge-bases/kb-src", nil))
		if rec2.Code != http.StatusForbidden && rec2.Code != http.StatusUnauthorized {
			t.Fatalf("DeleteKnowledgeBase malformed tenant status = %d, want 401/403", rec2.Code)
		}

		moveReq := httptest.NewRequest(http.MethodPost, "/knowledge/move",
			strings.NewReader(`{"source_kb_id":"kb-src","target_kb_id":`+
				`"kb-in-scope","knowledge_ids":["k1"],"mode":"reuse_vectors"}`))
		moveReq.Header.Set("Content-Type", "application/json")
		rec3 := httptest.NewRecorder()
		r.ServeHTTP(rec3, moveReq)
		if rec3.Code != http.StatusUnauthorized {
			t.Fatalf("MoveKnowledge malformed tenant status = %d, want 401", rec3.Code)
		}
	})
}

func TestAuditW2_PreviewChunkingCapsOversizedJSONStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := newPreviewRouter()

	// 4 MiB JSON body must be stopped by MaxBytesReader near previewMaxBodyBytes (1 MiB),
	// returning 413 with limit=previewMaxChars instead of buffering all 4 MiB.
	hugeJSON := `{"text":"` + strings.Repeat("a", 4<<20) + `"}`
	counter := &countingBodyReader{r: strings.NewReader(hugeJSON)}
	req := httptest.NewRequest(http.MethodPost, "/chunker/preview", counter)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PreviewChunking oversized body status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	var parsed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal 413 body: %v", err)
	}
	if parsed["limit"] != float64(previewMaxChars) {
		t.Fatalf("limit = %v, want %d", parsed["limit"], previewMaxChars)
	}
	if got := counter.n.Load(); got > (1<<20)+64*1024 {
		t.Fatalf("PreviewChunking buffered %d bytes (> 1 MiB cap) before rejecting", got)
	}
}
