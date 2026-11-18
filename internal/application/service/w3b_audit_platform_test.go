package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/config"
	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/models/rerank"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

// ─── 1. KB-Restricted Tenant API Key TagScopes Bypass ─────────────────────────

func TestW3BAudit_ResolveKnowledgeBases_RejectsOutOfScopeTagScopesForRestrictedAPIKey(t *testing.T) {
	svc := &sessionService{}
	baseCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	restrictedCtx := types.WithTenantAPIKeyScope(baseCtx, types.TenantAPIKeyScope{
		KeyID:            101,
		ScopeType:        types.APIKeyScopeTenant,
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})

	t.Run("rejects tag scope targeting out-of-scope KB", func(t *testing.T) {
		req := &types.QARequest{
			Session: &types.Session{ID: "sess-1", TenantID: 7},
			TagScopes: []types.TagScope{
				{KnowledgeBaseID: "kb-secret", TagIDs: []string{"tag-1"}},
			},
		}
		_, _, err := svc.resolveKnowledgeBases(restrictedCtx, req)
		require.Error(t, err)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok)
		assert.Equal(t, apperrors.ErrForbidden, appErr.Code)
	})

	t.Run("rejects unscoped tag IDs without verified KB binding", func(t *testing.T) {
		req := &types.QARequest{
			Session: &types.Session{ID: "sess-1", TenantID: 7},
			TagScopes: []types.TagScope{
				{KnowledgeBaseID: "", TagIDs: []string{"tag-unscoped"}},
			},
		}
		_, _, err := svc.resolveKnowledgeBases(restrictedCtx, req)
		require.Error(t, err)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok)
		assert.Equal(t, apperrors.ErrForbidden, appErr.Code)
	})

	t.Run("allows tag scope targeting allowed KB", func(t *testing.T) {
		req := &types.QARequest{
			Session: &types.Session{ID: "sess-1", TenantID: 7},
			TagScopes: []types.TagScope{
				{KnowledgeBaseID: "kb-allowed", TagIDs: []string{"tag-1"}},
			},
		}
		kbIDs, knowledgeIDs, err := svc.resolveKnowledgeBases(restrictedCtx, req)
		require.NoError(t, err)
		assert.Empty(t, kbIDs)
		assert.Empty(t, knowledgeIDs)
	})
}

// ─── 2. ModelService: Missing Existence Check, IsBuiltin Escalation & Async Race

func TestW3BAudit_UpdateModel_EnforcesExistenceAndPreventsBuiltinEscalation(t *testing.T) {
	t.Run("returns ErrModelNotFound when model is missing or belongs to another tenant", func(t *testing.T) {
		updated := false
		svc := NewModelService(&stubModelRepoForDelete{
			model: nil,
			update: func(*types.Model) error {
				updated = true
				return nil
			},
		}, nil, nil, nil, nil, nil)

		err := svc.UpdateModel(builtinModelContext(false), &types.Model{
			ID:       "foreign-model",
			TenantID: 999,
			Name:     "tampered",
		})
		require.ErrorIs(t, err, ErrModelNotFound)
		assert.False(t, updated)
	})

	t.Run("pins TenantID and prevents non-builtin model from promoting itself to IsBuiltin", func(t *testing.T) {
		stored := &types.Model{
			ID:        "workspace-model",
			TenantID:  7,
			IsBuiltin: false,
			Name:      "original",
		}
		var saved *types.Model
		svc := NewModelService(&stubModelRepoForDelete{
			model: stored,
			update: func(m *types.Model) error {
				cp := *m
				saved = &cp
				return nil
			},
		}, nil, nil, nil, nil, nil)

		err := svc.UpdateModel(builtinModelContext(false), &types.Model{
			ID:        stored.ID,
			TenantID:  999,
			IsBuiltin: true,
			Name:      "updated",
		})
		require.NoError(t, err)
		require.NotNil(t, saved)
		assert.Equal(t, uint64(7), saved.TenantID)
		assert.False(t, saved.IsBuiltin)
	})
}

func TestW3BAudit_DownloadModelAsync_NilOllamaAndNoSharedStructMutationRace(t *testing.T) {
	done := make(chan *types.Model, 1)
	svc := &modelService{
		repo: &stubModelRepoForDelete{
			update: func(m *types.Model) error {
				cp := *m
				done <- &cp
				return nil
			},
		},
		ollamaService: nil,
	}

	callerModel := &types.Model{
		ID:       "local-llama",
		TenantID: 7,
		Name:     "llama3:8b",
		Source:   types.ModelSourceLocal,
		Status:   types.ModelStatusDownloading,
	}
	svc.downloadModelAsync(context.Background(), callerModel)

	// Concurrently read callerModel fields as the HTTP handler does when marshaling response.
	for i := 0; i < 64; i++ {
		_ = callerModel.Status
		_ = callerModel.Name
	}

	select {
	case persisted := <-done:
		assert.Equal(t, types.ModelStatusDownloadFailed, persisted.Status)
	case <-time.After(5 * time.Second):
		t.Fatal("downloadModelAsync did not persist terminal status when ollamaService is nil")
	}
	assert.Equal(
		t,
		types.ModelStatusDownloading,
		callerModel.Status,
		"caller model struct must not be mutated concurrently",
	)
}

// ─── 3. Sandbox Policy Row (__workspace_scripts_policy__) Isolation ──────────

type w3bSandboxConfigRepo struct {
	repository.TenantSandboxConfigRepository
	mu   sync.Mutex
	rows []*types.TenantSandboxConfigEntity
}

func (r *w3bSandboxConfigRepo) Create(_ context.Context, e *types.TenantSandboxConfigEntity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *e
	r.rows = append(r.rows, &cp)
	return nil
}

func (r *w3bSandboxConfigRepo) GetByID(
	_ context.Context,
	tenantID uint64,
	id string,
) (*types.TenantSandboxConfigEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.TenantID == tenantID && row.ID == id {
			cp := *row
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *w3bSandboxConfigRepo) ListByTenant(
	_ context.Context,
	tenantID uint64,
) ([]*types.TenantSandboxConfigEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*types.TenantSandboxConfigEntity
	for _, row := range r.rows {
		if row.TenantID == tenantID {
			cp := *row
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *w3bSandboxConfigRepo) Update(_ context.Context, e *types.TenantSandboxConfigEntity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, row := range r.rows {
		if row.TenantID == e.TenantID && row.ID == e.ID {
			cp := *e
			r.rows[i] = &cp
			return nil
		}
	}
	return nil
}

func (r *w3bSandboxConfigRepo) Delete(_ context.Context, tenantID uint64, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.rows[:0]
	for _, row := range r.rows {
		if row.TenantID != tenantID || row.ID != id {
			kept = append(kept, row)
		}
	}
	r.rows = kept
	return nil
}

func TestW3BAudit_SandboxWorkspacePolicySentinelRowIsHiddenAndReserved(t *testing.T) {
	validCfg := e2bCfg("k", "https://api.e2b.app", "e2b.app", "t1", 300)
	policyRow := &types.TenantSandboxConfigEntity{
		ID:       "policy-row-id",
		TenantID: 7,
		Name:     types.SandboxWorkspacePolicyConfigName,
		Config:   &types.TenantSandboxConfig{},
	}
	realRow := &types.TenantSandboxConfigEntity{
		ID:       "cfg-real",
		TenantID: 7,
		Name:     "Production",
		Config:   validCfg,
	}
	cfgRepo := &w3bSandboxConfigRepo{rows: []*types.TenantSandboxConfigEntity{policyRow, realRow}}
	skillRepo := newInstallSkillRepo()

	t.Run("UserEnvService excludes policy row from ListMine and rejects SetMineSandbox against it", func(t *testing.T) {
		envSvc := NewUserEnvService(skillRepo, cfgRepo, HostSandboxManager{})
		ctx := userEnvCtx(7, "user-1")

		groups, err := envSvc.ListMine(ctx)
		require.NoError(t, err)
		require.Len(t, groups, 1)
		assert.Equal(t, "cfg-real", groups[0].SandboxConfigID)

		err = envSvc.SetMineSandbox(ctx, "policy-row-id", "MY_KEY", "secret")
		require.Error(t, err)
	})

	t.Run("TenantSandboxConfigService rejects reserved policy name on Create/Update and hides Inventory", func(
		t *testing.T,
	) {
		cfgSvc := NewTenantSandboxConfigService(cfgRepo, nil, testGlobalSandboxConfig(), skillRepo, nil)
		ctx := context.WithValue(context.Background(), types.SystemAdminContextKey, true)

		_, err := cfgSvc.Create(ctx, 7, CreateSandboxConfigInput{
			Name:   types.SandboxWorkspacePolicyConfigName,
			Config: validCfg,
		})
		require.Error(t, err)

		_, err = cfgSvc.Update(ctx, 7, "cfg-real", UpdateSandboxConfigInput{
			Name:   types.SandboxWorkspacePolicyConfigName,
			Config: validCfg,
		})
		require.Error(t, err)

		_, err = cfgSvc.Inventory(ctx, 7, "policy-row-id")
		require.Error(t, err)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok)
		assert.Equal(t, http.StatusNotFound, appErr.HTTPCode)
	})
}

// ─── 4. MCPService: MCPUpdateDropSecrets ClientSecret & TestMCPService OAuth ─

type w3bOAuthTokenRepo struct {
	interfaces.MCPOAuthRepository
	mu            sync.Mutex
	seenTenantID  uint64
	seenPrincipal types.Principal
}

func (r *w3bOAuthTokenRepo) GetClient(context.Context, uint64, string) (*types.MCPOAuthClient, error) {
	return nil, nil
}

func (r *w3bOAuthTokenRepo) GetTokenForPrincipal(
	_ context.Context,
	tenantID uint64,
	p types.Principal,
	_ string,
) (*types.MCPOAuthToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seenTenantID = tenantID
	r.seenPrincipal = p
	return nil, nil
}

func TestW3BAudit_MCPService_DropSecretsClearsClientSecretAndTestUsesMCPOAuthPrincipal(t *testing.T) {
	svc, repo := newTestService()
	u := "https://example.com/mcp"
	seed := &types.MCPService{
		ID:            "mcp-1",
		TenantID:      7,
		Name:          "Corp MCP",
		Enabled:       true,
		TransportType: types.MCPTransportSSE,
		URL:           &u,
		Headers:       types.MCPHeaders{"Authorization": "Bearer secret-hdr"},
		AuthConfig: &types.MCPAuthConfig{
			AuthType:      types.MCPAuthOAuth,
			APIKey:        "api-secret",
			Token:         "tok-secret",
			ClientID:      "cid-1",
			ClientSecret:  "csecret-should-be-dropped",
			CustomHeaders: map[string]string{"X-Custom": "val"},
		},
	}
	require.NoError(t, repo.Create(context.Background(), seed))

	err := svc.UpdateMCPService(context.Background(), &types.MCPService{
		ID:       "mcp-1",
		TenantID: 7,
	}, map[string]bool{
		types.MCPUpdateDropSecrets: true,
	})
	require.NoError(t, err)

	got, err := repo.GetByID(context.Background(), 7, "mcp-1")
	require.NoError(t, err)
	require.NotNil(t, got.AuthConfig)
	assert.Empty(t, got.AuthConfig.APIKey)
	assert.Empty(t, got.AuthConfig.Token)
	assert.Empty(t, got.AuthConfig.ClientSecret, "MCPUpdateDropSecrets must clear OAuth ClientSecret")
	assert.Empty(t, got.Headers)

	// Verify TestMCPService wires tenantID fallback and MCPOAuthPrincipalFromContext (embed visitor mapping).
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(
			"WWW-Authenticate",
			`Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`,
		)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	localURL := srv.URL
	oauthRepo := &w3bOAuthTokenRepo{}
	svc.oauthRepo = oauthRepo
	require.NoError(t, repo.Create(context.Background(), &types.MCPService{
		ID:            "mcp-oauth",
		TenantID:      7,
		Name:          "OAuth MCP",
		Enabled:       true,
		TransportType: types.MCPTransportHTTPStreamable,
		URL:           &localURL,
		AuthConfig:    &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth},
	}))

	embedCtx := types.WithEmbedVisitorID(
		types.WithPrincipal(context.Background(), types.EmbedSessionPrincipal(7, "ch-1", "sess-1")),
		"vis-42",
	)
	_, err = svc.TestMCPService(embedCtx, 7, "mcp-oauth")
	require.NoError(t, err)

	oauthRepo.mu.Lock()
	seenTenant := oauthRepo.seenTenantID
	seenPrincipal := oauthRepo.seenPrincipal
	oauthRepo.mu.Unlock()
	assert.Equal(t, uint64(7), seenTenant)
	assert.Equal(t, types.EmbedVisitorPrincipal(7, "ch-1", "vis-42"), seenPrincipal)
}

// ─── 5. MessageService: ClearSessionMessages KB Cleanup Race & Rerank Bounds ──

type w3bMessageRepoForClear struct {
	interfaces.MessageRepository
	mu           sync.Mutex
	knowledgeIDs []string
	deleteErr    error
}

func (r *w3bMessageRepoForClear) GetKnowledgeIDsBySessionID(_ context.Context, _ string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.knowledgeIDs))
	copy(out, r.knowledgeIDs)
	return out, nil
}

func (r *w3bMessageRepoForClear) DeleteMessagesBySessionID(_ context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleteErr != nil {
		return r.deleteErr
	}
	// Once messages are deleted from DB, GetKnowledgeIDsBySessionID returns empty!
	r.knowledgeIDs = nil
	return nil
}

type w3bSessionRepoForClear struct {
	interfaces.SessionRepository
}

func (w3bSessionRepoForClear) Get(
	_ context.Context,
	tenantID uint64,
	_ string,
	sessionID string,
) (*types.Session, error) {
	return &types.Session{ID: sessionID, TenantID: tenantID}, nil
}

type w3bKnowledgeRepoForClear struct {
	interfaces.KnowledgeRepository
}

func (w3bKnowledgeRepoForClear) GetKnowledgeBatch(
	_ context.Context,
	tenantID uint64,
	ids []string,
) ([]*types.Knowledge, error) {
	out := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		out = append(out, &types.Knowledge{ID: id, TenantID: tenantID, KnowledgeBaseID: "kb-chat-hist"})
	}
	return out, nil
}

type w3bKnowServiceForClear struct {
	interfaces.KnowledgeService
	deleted chan []string
}

func (k *w3bKnowServiceForClear) GetRepository() interfaces.KnowledgeRepository {
	return w3bKnowledgeRepoForClear{}
}

func (k *w3bKnowServiceForClear) DeleteKnowledgeList(_ context.Context, ids []string) error {
	cp := make([]string, len(ids))
	copy(cp, ids)
	k.deleted <- cp
	return nil
}

type w3bNegativeReranker struct{}

func (w3bNegativeReranker) Rerank(_ context.Context, _ string, _ []string) ([]rerank.RankResult, error) {
	return []rerank.RankResult{
		{Index: -1, RelevanceScore: 0.99},
		{Index: 99, RelevanceScore: 0.95},
		{Index: 0, RelevanceScore: 0.88},
	}, nil
}

func (w3bNegativeReranker) GetModelName() string { return "neg-rerank" }
func (w3bNegativeReranker) GetModelID() string   { return "neg-rerank-id" }

type w3bRerankModelSvc struct {
	interfaces.ModelService
}

func (w3bRerankModelSvc) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return w3bNegativeReranker{}, nil
}

func TestW3BAudit_ClearSessionMessages_DeletesChatHistoryKBBeforeRowsDisappear(t *testing.T) {
	t.Run("snapshots knowledge IDs before deleting messages", func(t *testing.T) {
		msgRepo := &w3bMessageRepoForClear{knowledgeIDs: []string{"kn-1", "kn-2"}}
		knowSvc := &w3bKnowServiceForClear{deleted: make(chan []string, 1)}
		svc := &messageService{
			messageRepo: msgRepo,
			sessionRepo: w3bSessionRepoForClear{},
			knowService: knowSvc,
		}
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
		require.NoError(t, svc.ClearSessionMessages(ctx, "sess-1"))

		select {
		case ids := <-knowSvc.deleted:
			assert.Equal(t, []string{"kn-1", "kn-2"}, ids)
		case <-time.After(3 * time.Second):
			t.Fatal("expected chat history knowledge IDs to be deleted after ClearSessionMessages")
		}
	})

	t.Run("does not delete chat history KB entries when message deletion fails", func(t *testing.T) {
		msgRepo := &w3bMessageRepoForClear{
			knowledgeIDs: []string{"kn-1"},
			deleteErr:    errors.New("db write error"),
		}
		knowSvc := &w3bKnowServiceForClear{deleted: make(chan []string, 1)}
		svc := &messageService{
			messageRepo: msgRepo,
			sessionRepo: w3bSessionRepoForClear{},
			knowService: knowSvc,
		}
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
		require.Error(t, svc.ClearSessionMessages(ctx, "sess-1"))
		select {
		case ids := <-knowSvc.deleted:
			t.Fatalf("must not delete KB entries when message delete failed, got %v", ids)
		case <-time.After(50 * time.Millisecond):
		}
	})

	t.Run("rerankResults ignores negative index without panicking", func(t *testing.T) {
		svc := &messageService{modelService: w3bRerankModelSvc{}}
		rc := &types.RetrievalConfig{RerankModelID: "rr-1", RerankTopK: 5, RerankThreshold: 0.5}
		in := []*types.SearchResult{{ID: "c1", Content: "hello", Score: 0.4}}
		out := svc.rerankResults(context.Background(), rc, "hello", in)
		require.Len(t, out, 1)
		assert.InDelta(t, 0.88, out[0].Score, 1e-6)
	})
}

// ─── 6. Session: GenerateTitleAsync Race, UpdateSession Nil Guard & Fork Kind ─

type w3bNilSessionRepo struct {
	interfaces.SessionRepository
}

func (w3bNilSessionRepo) Get(context.Context, uint64, string, string) (*types.Session, error) {
	return nil, nil
}

func TestW3BAudit_Session_GenerateTitleAsyncRace_UpdateNilGuard_AndSharedForkKind(t *testing.T) {
	t.Run("GenerateTitleAsync clones session so concurrent caller reads do not race", func(t *testing.T) {
		repo := &titleGenerationRepository{done: make(chan struct{})}
		svc := &sessionService{
			cfg:          &config.Config{Conversation: &config.ConversationConfig{}},
			modelService: &titleGenerationModels{model: &titleGenerationModel{content: "Generated Title"}},
			sessionRepo:  repo,
		}
		callerSession := &types.Session{ID: "sess-race", TenantID: 7, UserID: "u1"}
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
		svc.GenerateTitleAsync(ctx, callerSession, "what is genyee?", "qa-model", nil)

		// Concurrently read callerSession.Title as the QA pipeline does.
		for i := 0; i < 64; i++ {
			_ = callerSession.Title
		}
		select {
		case <-repo.done:
		case <-time.After(5 * time.Second):
			t.Fatal("async title was not persisted")
		}
		assert.Equal(t, "Generated Title", repo.title)
		assert.Empty(t, callerSession.Title, "caller's session struct must not be mutated in background goroutine")
	})

	t.Run("UpdateSession returns ErrSessionNotFound instead of nil-deref when repo returns (nil, nil)", func(
		t *testing.T,
	) {
		svc := &sessionService{sessionRepo: w3bNilSessionRepo{}}
		ctx := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalWebUser, ID: "u1"})
		err := svc.UpdateSession(ctx, &types.Session{ID: "missing", TenantID: 7, Title: "x"})
		require.ErrorIs(t, err, apperrors.ErrSessionNotFound)
	})

	t.Run("forkOnSharedMachine preserves Session.Kind for assistant conversations", func(t *testing.T) {
		turn := checkpointedTurn("u-msg-1", "a-msg-1", "sb-1", "sha1", 0)
		port := &fakeForkSandboxPort{
			boundID: "sb-1", bound: true,
			workspace: &fakeWorkspaceFork{id: "sb-1", reachable: map[string]bool{"sha1": true}},
		}
		svc, sessions, _, _ := newSharedMachineFork(t, port, append(turn, userForkPoint()))
		sessions.source.Kind = types.SessionKindAssistant

		got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, sessions.created)
		assert.Equal(t, types.SessionKindAssistant, sessions.created.Kind)
	})
}

// ─── 7. UserService: Stale Superuser Tenant Gate, AdminResetPassword OIDC & Bounded OIDC Body

type w3bUserRepo struct {
	interfaces.UserRepository
	mu   sync.Mutex
	user *types.User
}

func (r *w3bUserRepo) GetUserByID(_ context.Context, id string) (*types.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user != nil && r.user.ID == id {
		cp := *r.user
		return &cp, nil
	}
	return nil, nil
}

func (r *w3bUserRepo) UpdateUser(_ context.Context, u *types.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *u
	r.user = &cp
	return nil
}

type w3bTokenRepo struct {
	interfaces.AuthTokenRepository
	revokedUser string
}

func (r *w3bTokenRepo) RevokeTokensByUserID(_ context.Context, userID string) error {
	r.revokedUser = userID
	return nil
}

type w3bMemberSvc struct {
	interfaces.TenantMemberService
	memberships map[uint64]*types.TenantMember
	list        []*types.TenantMember
}

func (m *w3bMemberSvc) GetMembership(_ context.Context, _ string, tenantID uint64) (*types.TenantMember, error) {
	if mem, ok := m.memberships[tenantID]; ok {
		return mem, nil
	}
	return nil, nil
}

func (m *w3bMemberSvc) ListByUser(_ context.Context, _ string) ([]*types.TenantMember, error) {
	return m.list, nil
}

func TestW3BAudit_UserService_TenantGate_AdminResetOidcFlag_AndBoundedOIDCRead(t *testing.T) {
	t.Run("homeOrFirstMembershipTenant honors EnableCrossTenantAccess=false even if CanAccessAllTenants=true", func(
		t *testing.T,
	) {
		u := &types.User{
			ID:                  "u-stale-admin",
			TenantID:            10, // removed from tenant 10
			CanAccessAllTenants: true,
		}
		uRepo := &w3bUserRepo{user: u}
		memSvc := &w3bMemberSvc{
			memberships: map[uint64]*types.TenantMember{}, // no membership in tenant 10
			list: []*types.TenantMember{
				{TenantID: 22, Status: types.TenantMemberStatusActive},
			},
		}
		svc := &userService{
			config:        &config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: false}},
			userRepo:      uRepo,
			memberService: memSvc,
		}
		got := svc.homeOrFirstMembershipTenant(context.Background(), u)
		assert.Equal(
			t,
			uint64(22),
			got,
			"must fall back to active membership when cross-tenant access is disabled in config",
		)
	})

	t.Run("AdminResetPassword clears Preferences.OidcOnlyLogin so admin-set password works", func(t *testing.T) {
		oidcOnly := true
		u := &types.User{
			ID:          "u-oidc",
			Preferences: types.UserPreferences{OidcOnlyLogin: &oidcOnly},
		}
		uRepo := &w3bUserRepo{user: u}
		tokRepo := &w3bTokenRepo{}
		svc := &userService{
			userRepo:  uRepo,
			tokenRepo: tokRepo,
		}
		require.NoError(t, svc.AdminResetPassword(context.Background(), "u-oidc", "ValidPass123!"))
		require.NotNil(t, uRepo.user)
		if uRepo.user.Preferences.OidcOnlyLogin != nil {
			assert.False(t, *uRepo.user.Preferences.OidcOnlyLogin, "AdminResetPassword must clear OidcOnlyLogin")
		}
		assert.Equal(t, "u-oidc", tokRepo.revokedUser)
	})

	t.Run("exchangeOIDCCode and fetchOIDCUserInfo bound oversized HTTP body reads via LimitReader", func(t *testing.T) {
		withOIDCSSRFWhitelist(t, "127.0.0.1")
		hugeJSON := `{"access_token":"tok","sub":"u1","pad":"` + strings.Repeat("x", 2<<20) + `"}`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(hugeJSON))
		}))
		defer server.Close()

		svc := &userService{}
		cfg := &config.OIDCAuthConfig{TokenEndpoint: server.URL, ClientID: "cid", ClientSecret: "csecret"}
		_, err := svc.exchangeOIDCCode(context.Background(), cfg, "code", "https://app.example/callback", "")
		require.Error(t, err, "exchangeOIDCCode must reject >1MiB response body")

		_, err = svc.fetchOIDCUserInfo(context.Background(), server.URL, "tok")
		require.Error(t, err, "fetchOIDCUserInfo must reject >1MiB response body")

		_ = bytes.ErrTooLarge
	})
}

// ─── 8. WebSearch: matchesBlacklistRule "/" Panic ─────────────────────────────

func TestW3BAudit_WebSearch_MatchesBlacklistRuleSingleSlashDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		matched := matchesBlacklistRule("https://genyee.local/docs", "/")
		assert.False(t, matched)
	})
	assert.True(t, matchesBlacklistRule("https://bad.example.com/a", `/bad\.example\.com/`))
}

// ─── 9. ShellJobIndex Colon SandboxID & SystemSetting Coercion/NaN Guards ────

func TestW3BAudit_ShellJobIndex_PreservesColonInSandboxIDAndNamespace(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	idx := NewShellJobIndex(client, "genyee:prod")
	ctx := context.Background()
	entry := ShellJobIndexEntry{
		SessionRef: interfaces.SessionRef{TenantID: 7, SessionID: "sess-1"},
		SandboxID:  "arn:aws:ecs:region:sbx-9",
		WorkID:     "sj_deadbeef01",
		YieldUntil: time.Unix(1000, 0),
	}
	require.NoError(t, idx.Add(ctx, entry))

	due, err := idx.Due(ctx, time.Unix(1010, 0))
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "arn:aws:ecs:region:sbx-9", due[0].SandboxID)
	assert.Equal(t, "sj_deadbeef01", due[0].WorkID)
	assert.Equal(t, uint64(7), due[0].SessionRef.TenantID)
	assert.Equal(t, "sess-1", due[0].SessionRef.SessionID)

	// Confirm ClaimWork deletes the exact member too.
	claimed, err := idx.ClaimWork(ctx, due[0].SessionRef, due[0].WorkID)
	require.NoError(t, err)
	assert.True(t, claimed)
	after, err := idx.Due(ctx, time.Unix(1010, 0))
	require.NoError(t, err)
	assert.Empty(t, after)
}

func TestW3BAudit_SystemSetting_RejectsNaNInfAndAcceptsInt32AndNumericStringConcurrency(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e30} {
		_, err := encodeForType("int", bad)
		require.Error(t, err, "encodeForType must reject %v", bad)
		_, err = coerceToPositiveInt64(bad)
		require.Error(t, err, "coerceToPositiveInt64 must reject %v", bad)
	}

	require.NoError(t, validateRegistryEntry("asynq.core_concurrency", int32(4)))
	require.NoError(t, validateRegistryEntry("asynq.core_concurrency", "8"))
	require.Error(t, validateRegistryEntry("asynq.core_concurrency", "0"))
	require.Error(t, validateRegistryEntry("asynq.core_concurrency", "not-a-number"))

	_ = io.EOF
}
