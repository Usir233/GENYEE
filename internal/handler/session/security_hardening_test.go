package session

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/internal/agent"
	"github.com/Usir233/GENYEE/internal/agent/hitl"
	"github.com/Usir233/GENYEE/internal/browserskill"
	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/middleware"
	"github.com/Usir233/GENYEE/internal/models/asr"
	"github.com/Usir233/GENYEE/internal/stream"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

func TestSetHostApprovalModeRejectsAPIKey(t *testing.T) {
	modes := &fakeHostModes{modes: map[string]string{}}
	h := &Handler{
		sessionService: modeSessionService{owned: map[string]bool{"s1": true}},
		hostModes:      modes,
	}

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPut, "/sessions/s1/host-approval-mode", strings.NewReader(`{"mode":"auto"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := types.WithTenantAPIKeyScope(req.Context(), types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
	})
	c.Request = req.WithContext(ctx)
	c.Params = gin.Params{{Key: "id", Value: "s1"}}

	h.SetHostApprovalMode(c)

	appErr := lastAppError(t, c)
	require.Equal(t, http.StatusForbidden, appErr.HTTPCode)
	require.Empty(t, modes.modes)
}

func TestCreateSessionRejectsProjectDirForAPIKey(t *testing.T) {
	h := handlerWithApprovedDirs("/Users/dev/My Project")
	gin.SetMode(gin.TestMode)

	runReq := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := gin.New()
		r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
			c.Set(types.TenantIDContextKey.String(), uint64(1))
			ctx := types.WithTenantAPIKeyScope(c.Request.Context(), types.TenantAPIKeyScope{
				Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
			})
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		})
		r.POST("/sessions", h.CreateSession)
		req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}

	denied := runReq(`{"project_dir":"/Users/dev/My Project"}`)
	require.Equal(t, http.StatusForbidden, denied.Code)
	require.Nil(t, h.created)

	allowed := runReq(`{"title":"api-chat"}`)
	require.Equal(t, http.StatusCreated, allowed.Code)
	require.NotNil(t, h.created)
	require.Empty(t, h.created.HostWorkspaceDir)
}

func TestBrowserSkillConnectionRejectsAPIKeyAndUnauthenticatedUser(t *testing.T) {
	t.Setenv("BROWSERSKILL_BINARY", "/configured/bsk")
	h := &Handler{
		sessionService: modeSessionService{owned: map[string]bool{"s1": true}},
		browserSkill:   browserskill.NewManager(),
	}

	// 1. Machine API key with synthetic owner user ID must be forbidden from controlling personal browser extension.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s1/local-browser", nil)
	ctx := context.WithValue(req.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
	})
	c.Request = req.WithContext(ctx)
	c.Params = gin.Params{{Key: "id", Value: "s1"}}
	h.BrowserSkillConnection(c)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 2. Missing user identity must return 401 before touching browserSkill.
	recNoUser := httptest.NewRecorder()
	cNoUser, _ := gin.CreateTestContext(recNoUser)
	reqNoUser := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s1/local-browser", nil)
	ctxNoUser := context.WithValue(reqNoUser.Context(), types.TenantIDContextKey, uint64(7))
	cNoUser.Request = reqNoUser.WithContext(ctxNoUser)
	cNoUser.Params = gin.Params{{Key: "id", Value: "s1"}}
	h.BrowserSkillConnection(cNoUser)
	require.Equal(t, http.StatusUnauthorized, recNoUser.Code)
}

type stopSessionProbeStub struct {
	interfaces.SessionService
	interfaces.MessageService
	owned        map[string]*types.Session
	msgLookupHit bool
	nilMessage   bool
}

func (s *stopSessionProbeStub) GetOwnedSession(_ context.Context, id string) (*types.Session, error) {
	if sess, ok := s.owned[id]; ok {
		return sess, nil
	}
	return nil, apperrors.ErrSessionNotFound
}

func (s *stopSessionProbeStub) GetMessage(_ context.Context, sessionID, messageID string) (*types.Message, error) {
	s.msgLookupHit = true
	if s.nilMessage {
		return nil, nil
	}
	return &types.Message{ID: messageID, SessionID: sessionID}, nil
}

func TestStopSessionRejectsOversizedBodyAndEnforcesOwnershipBeforeMessageLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. Oversized body on public embed / session stop must be rejected with 413.
	stub := &stopSessionProbeStub{owned: map[string]*types.Session{"s1": {ID: "s1", TenantID: 1}}}
	h := &Handler{sessionService: stub, messageService: stub, streamManager: stream.NewMemoryStreamManager()}

	recBig := httptest.NewRecorder()
	cBig, _ := gin.CreateTestContext(recBig)
	oversized := `{"message_id":"` + strings.Repeat("a", 70*1024) + `"}`
	reqBig := httptest.NewRequest(http.MethodPost, "/sessions/s1/stop", strings.NewReader(oversized))
	reqBig.Header.Set("Content-Type", "application/json")
	cBig.Request = reqBig
	cBig.Params = gin.Params{{Key: "session_id", Value: "s1"}}
	cBig.Set(types.TenantIDContextKey.String(), uint64(1))
	h.StopSession(cBig)
	require.Equal(t, http.StatusRequestEntityTooLarge, recBig.Code)
	require.False(t, stub.msgLookupHit)

	// 2. Non-owner caller must fail GetOwnedSession BEFORE GetMessage can probe message existence.
	unownedStub := &stopSessionProbeStub{owned: map[string]*types.Session{}}
	hUnowned := &Handler{
		sessionService: unownedStub, messageService: unownedStub, streamManager: stream.NewMemoryStreamManager(),
	}
	recUnowned := httptest.NewRecorder()
	cUnowned, _ := gin.CreateTestContext(recUnowned)
	reqUnowned := httptest.NewRequest(
		http.MethodPost, "/sessions/unowned/stop", strings.NewReader(`{"message_id":"msg-secret"}`),
	)
	reqUnowned.Header.Set("Content-Type", "application/json")
	cUnowned.Request = reqUnowned
	cUnowned.Params = gin.Params{{Key: "session_id", Value: "unowned"}}
	cUnowned.Set(types.TenantIDContextKey.String(), uint64(1))
	hUnowned.StopSession(cUnowned)
	require.Equal(t, http.StatusNotFound, recUnowned.Code)
	require.False(t, unownedStub.msgLookupHit, "StopSession must check GetOwnedSession before GetMessage")

	// 3. Wrong-typed tenant_id in Gin context must return 401 without panicking, and nil message must return 404.
	recBadTenant := httptest.NewRecorder()
	cBadTenant, _ := gin.CreateTestContext(recBadTenant)
	reqBadTenant := httptest.NewRequest(http.MethodPost, "/sessions/s1/stop", strings.NewReader(`{"message_id":"m1"}`))
	reqBadTenant.Header.Set("Content-Type", "application/json")
	cBadTenant.Request = reqBadTenant
	cBadTenant.Params = gin.Params{{Key: "session_id", Value: "s1"}}
	cBadTenant.Set(types.TenantIDContextKey.String(), "not-a-uint64")
	require.NotPanics(t, func() { h.StopSession(cBadTenant) })
	require.Equal(t, http.StatusUnauthorized, recBadTenant.Code)

	nilMsgStub := &stopSessionProbeStub{
		owned:      map[string]*types.Session{"s1": {ID: "s1", TenantID: 1}},
		nilMessage: true,
	}
	hNilMsg := &Handler{
		sessionService: nilMsgStub, messageService: nilMsgStub, streamManager: stream.NewMemoryStreamManager(),
	}
	recNilMsg := httptest.NewRecorder()
	cNilMsg, _ := gin.CreateTestContext(recNilMsg)
	reqNilMsg := httptest.NewRequest(http.MethodPost, "/sessions/s1/stop", strings.NewReader(`{"message_id":"m1"}`))
	reqNilMsg.Header.Set("Content-Type", "application/json")
	cNilMsg.Request = reqNilMsg
	cNilMsg.Params = gin.Params{{Key: "session_id", Value: "s1"}}
	cNilMsg.Set(types.TenantIDContextKey.String(), uint64(1))
	require.NotPanics(t, func() { hNilMsg.StopSession(cNilMsg) })
	require.Equal(t, http.StatusNotFound, recNilMsg.Code)
}

func TestSearchKnowledgeRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Exceeds 13 MiB search body envelope.
	oversized := `{"query":"` + strings.Repeat("a", 14*1024*1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-search", strings.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h := &Handler{}
	h.SearchKnowledge(c)

	appErr := lastAppError(t, c)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.Equal(t, "request body is too large", appErr.Message)
}

func TestSessionJSONEndpointsRejectOversizedBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}

	cases := []struct {
		name  string
		limit int
		call  func(c *gin.Context)
	}{
		{
			name:  "SteerMessage",
			limit: 300 * 1024,
			call: func(c *gin.Context) {
				c.Params = gin.Params{{Key: "session_id", Value: "s1"}}
				h.SteerMessage(c)
			},
		},
		{
			name:  "GenerateTitle",
			limit: 300 * 1024,
			call: func(c *gin.Context) {
				c.Params = gin.Params{{Key: "session_id", Value: "s1"}}
				h.GenerateTitle(c)
			},
		},
		{
			name:  "CreateSession",
			limit: 80 * 1024,
			call:  h.CreateSession,
		},
		{
			name:  "UpdateSession",
			limit: 80 * 1024,
			call: func(c *gin.Context) {
				c.Params = gin.Params{{Key: "id", Value: "s1"}}
				c.Set(types.TenantIDContextKey.String(), uint64(1))
				h.UpdateSession(c)
			},
		},
		{
			name:  "BatchDeleteSessions",
			limit: 80 * 1024,
			call:  h.BatchDeleteSessions,
		},
		{
			name:  "ForkSession",
			limit: 80 * 1024,
			call: func(c *gin.Context) {
				c.Params = gin.Params{{Key: "session_id", Value: "s1"}}
				h.ForkSession(c)
			},
		},
		{
			name:  "RewindSession",
			limit: 80 * 1024,
			call: func(c *gin.Context) {
				c.Params = gin.Params{{Key: "session_id", Value: "s1"}}
				h.RewindSession(c)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			body := `{"title":"` + strings.Repeat("x", tc.limit) + `"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			c.Request = req

			tc.call(c)

			appErr := lastAppError(t, c)
			assert.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
			assert.Equal(t, "request body is too large", appErr.Message)
		})
	}
}

func TestBuildResumeRequestEnforcesSessionOwnershipTenantAndRejectsAPIKey(t *testing.T) {
	c, ctx := newResolveAgentTestContext(1)
	messages := &resumeMessages{rows: map[string]*types.Message{
		"m": {ID: "m", SessionID: "sess-1", RequestID: "turn-1", RunState: types.MessageRunStateAwaitingInput},
	}}
	b := hitl.NewBroker(nil, nil)
	request, err := json.Marshal(types.ResumableAgentRequest{AgentID: "a", UserMessageID: "u1"})
	require.NoError(t, err)
	snapshot, err := json.Marshal(agent.RunSnapshot{Version: agent.RunSnapshotVersion, Query: "q", Request: request})
	require.NoError(t, err)
	require.NoError(t, b.Suspensions().Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: "m", PromptID: "p", Snapshot: types.JSON(snapshot),
	}))

	// 1. Read-only / non-owned session must be rejected by buildResumeRequest.
	hUnowned := &Handler{
		promptBroker:       b,
		messageService:     messages,
		streamManager:      stream.NewMemoryStreamManager(),
		sessionService:     &resumeSessionService{ownedErr: apperrors.ErrSessionNotFound},
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{ID: "a"}},
	}
	req, _, err := hUnowned.buildResumeRequest(ctx, c, &types.AgentPrompt{
		ID: "p", TenantID: 1, SessionID: "sess-1", AssistantMessageID: "m",
	})
	require.Error(t, err)
	require.Nil(t, req)
	require.Equal(t, types.MessageRunStateAwaitingInput, messages.rows["m"].RunState)

	// 2. Machine API-key context must be rejected.
	hOwned := &Handler{
		promptBroker:       b,
		messageService:     messages,
		streamManager:      stream.NewMemoryStreamManager(),
		sessionService:     &resumeSessionService{tenantID: 1},
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{ID: "a"}},
	}
	apiKeyCtx := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
	})
	req, _, err = hOwned.buildResumeRequest(apiKeyCtx, c, &types.AgentPrompt{
		ID: "p", TenantID: 1, SessionID: "sess-1", AssistantMessageID: "m",
	})
	require.Error(t, err)
	require.Nil(t, req)

	// 3. Cross-tenant prompt mismatch must be rejected.
	req, _, err = hOwned.buildResumeRequest(ctx, c, &types.AgentPrompt{
		ID: "p", TenantID: 999, SessionID: "sess-1", AssistantMessageID: "m",
	})
	require.Error(t, err)
	require.Nil(t, req)

	// 4. Nil prompt in ResumeSuspendedRun must be a safe no-op.
	require.NotPanics(t, func() {
		hOwned.ResumeSuspendedRun(c, nil, false, nil)
	})
}

type listSessionsErrStub struct {
	interfaces.SessionService
	err error
}

func (s *listSessionsErrStub) ListSessions(_ context.Context, _ *types.SessionListQuery) (*types.PageResult, error) {
	return nil, s.err
}

func TestGetSessionsByTenantPreservesAppErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{
		sessionService: &listSessionsErrStub{err: apperrors.NewForbiddenError("admin required for source filter")},
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/sessions?source=api", nil)

	h.GetSessionsByTenant(c)

	appErr := lastAppError(t, c)
	require.Equal(t, http.StatusForbidden, appErr.HTTPCode)
}

func TestSteerMessageRejectsDelegatedSessionAndOutOfScopeAPIKeyMentions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	streams := stream.NewMemoryStreamManager()
	require.NoError(t, streams.SetLiveRun(t.Context(), "s-del", "assist-1", "req-1"))
	require.NoError(t, streams.SetLiveRun(t.Context(), "s-key", "assist-2", "req-2"))

	// 1. Delegated child sessions must reject manual SteerMessage with 403.
	hDel := &Handler{
		sessionService: &stopSessionProbeStub{owned: map[string]*types.Session{
			"s-del": {ID: "s-del", TenantID: 1, Kind: types.SessionKindAgentDelegated},
		}},
		streamManager: streams,
	}
	wDel := httptest.NewRecorder()
	cDel, _ := gin.CreateTestContext(wDel)
	reqDel := httptest.NewRequest(http.MethodPost, "/sessions/s-del/steer", strings.NewReader(`{"query":"hello"}`))
	reqDel.Header.Set("Content-Type", "application/json")
	cDel.Request = reqDel
	cDel.Params = gin.Params{{Key: "session_id", Value: "s-del"}}
	hDel.SteerMessage(cDel)
	require.Equal(t, http.StatusForbidden, lastAppError(t, cDel).HTTPCode)

	// 2. KB-restricted API key cannot smuggle unauthorized KB @mentions via SteerMessage.
	hKey := &Handler{
		sessionService: &stopSessionProbeStub{owned: map[string]*types.Session{
			"s-key": {ID: "s-key", TenantID: 1},
		}},
		streamManager: streams,
	}
	wKey := httptest.NewRecorder()
	cKey, _ := gin.CreateTestContext(wKey)
	body := `{"query":"check this","mentioned_items":[{"id":"kb-secret","name":"Secret","type":"kb"}]}`
	reqKey := httptest.NewRequest(http.MethodPost, "/sessions/s-key/steer", strings.NewReader(body))
	reqKey.Header.Set("Content-Type", "application/json")
	ctxKey := types.WithTenantAPIKeyScope(reqKey.Context(), types.TenantAPIKeyScope{
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityChat)},
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	cKey.Request = reqKey.WithContext(ctxKey)
	cKey.Params = gin.Params{{Key: "session_id", Value: "s-key"}}
	hKey.SteerMessage(cKey)
	require.Equal(t, http.StatusForbidden, lastAppError(t, cKey).HTTPCode)
}

type failingASRModelStub struct {
	err error
}

func (s *failingASRModelStub) Transcribe(_ context.Context, _ []byte, _ string) (*asr.TranscriptionResult, error) {
	return nil, s.err
}
func (s *failingASRModelStub) GetModelName() string { return "whisper" }
func (s *failingASRModelStub) GetModelID() string   { return "asr-1" }

type failingASRServiceStub struct {
	interfaces.ModelService
	loadErr error
	asr     asr.ASR
}

func (s *failingASRServiceStub) GetASRModel(_ context.Context, _ string) (asr.ASR, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.asr, nil
}

func TestTranscribeVoiceInputRedactsUpstreamErrorDetails(t *testing.T) {
	c, _ := newVoiceInputRequest(t, map[string]string{
		"agent_id":                     "builtin-smart-reasoning",
		types.AgentSourceTenantIDParam: "84",
	}, "voice.wav", []byte("RIFF"))
	h := &Handler{
		agentShareService: &resolveAgentShareStub{agent: &types.CustomAgent{
			ID: "builtin-smart-reasoning", TenantID: 84,
			Config: types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "asr-shared"},
		}},
		customAgentService: &resolveOwnAgentStub{},
		modelService: &failingASRServiceStub{
			asr: &failingASRModelStub{err: stderrors.New(`Post "http://10.0.1.9:8000/v1/audio": connection refused`)},
		},
	}

	h.TranscribeVoiceInput(c)

	appErr := lastAppError(t, c)
	require.Equal(t, http.StatusServiceUnavailable, appErr.HTTPCode)
	require.Nil(t, appErr.Details, "upstream internal ASR endpoint errors must not leak in HTTP error details")
}

type tempDocTenantCaptureStub struct {
	interfaces.TemporaryDocumentService
	gotTenantID uint64
}

func (s *tempDocTenantCaptureStub) List(
	_ context.Context, tenantID uint64, _ string,
) ([]*types.TemporaryDocument, error) {
	s.gotTenantID = tenantID
	return []*types.TemporaryDocument{}, nil
}

type artifactDeleteStub struct {
	interfaces.MessageService
}

func (*artifactDeleteStub) DeleteSessionArtifact(
	_ context.Context, _ *types.ArtifactDeleteRequest,
) (*types.ArtifactDeleteResult, error) {
	return &types.ArtifactDeleteResult{FileName: "out.txt", Deleted: 1}, nil
}

func TestTemporaryDocumentAndArtifactDeleteUseSessionTenantWithoutPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. Temporary document endpoints use session.TenantID even when c.Keys lacks tenant_id.
	tempStub := &tempDocTenantCaptureStub{}
	h := &Handler{
		sessionService:     &resumeSessionService{tenantID: 42},
		temporaryDocuments: tempStub,
		messageService:     &artifactDeleteStub{},
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/sessions/s1/attachments", nil)
	c.Request = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(42)))
	c.Params = gin.Params{{Key: "id", Value: "s1"}}
	h.ListTemporaryDocuments(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, uint64(42), tempStub.gotTenantID)

	// 2. deleteArtifact must not panic if TenantIDContextKey is absent from context.
	wDel := httptest.NewRecorder()
	cDel, _ := gin.CreateTestContext(wDel)
	cDel.Request = httptest.NewRequest(http.MethodDelete, "/artifacts?session_id=s1&message_id=m1&index=0", nil)
	require.NotPanics(t, func() {
		h.DeleteLibraryArtifact(cDel)
	})
	require.Equal(t, http.StatusOK, wDel.Code)
}
