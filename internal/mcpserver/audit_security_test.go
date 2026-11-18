package mcpserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/event"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type countingStreamReader struct {
	data []byte
	pos  int
	read atomic.Int64
}

func (r *countingStreamReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	r.read.Add(int64(n))
	return n, nil
}

func TestAuditMCPServerRejectsOversizedRequestBody(t *testing.T) {
	prev := maxMCPServerRequestBytes
	maxMCPServerRequestBytes = 4096
	t.Cleanup(func() { maxMCPServerRequestBytes = prev })

	ep := &types.MCPEndpoint{
		ID:                 "ep-size",
		TenantID:           1,
		Enabled:            true,
		RateLimitPerMinute: 100,
		Tools:              types.StringArray{types.MCPEndpointToolListKnowledgeBases},
	}
	r := newTestEngine(t, ep)

	t.Run("rejects declared Content-Length above limit with 413", func(t *testing.T) {
		big := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"pad":"` + strings.Repeat("x", 8192) + `"}}`
		req := httptest.NewRequest(http.MethodPost, "/mcp/ep-size", strings.NewReader(big))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("bounds streaming request body without buffering unbounded payload", func(t *testing.T) {
		big := []byte(
			`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"pad":"` +
				strings.Repeat("y", 32768) + `"}}`,
		)
		stream := &countingStreamReader{data: big}
		req := httptest.NewRequest(http.MethodPost, "/mcp/ep-size", io.NopCloser(stream))
		req.ContentLength = -1
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
		require.LessOrEqual(
			t, stream.read.Load(), maxMCPServerRequestBytes+1, "must stop reading immediately past limit",
		)
	})

	t.Run("accepts normal JSON-RPC request within limit", func(t *testing.T) {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		req := httptest.NewRequest(http.MethodPost, "/mcp/ep-size", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})
}

type forbiddingKnowledgeService struct {
	*stubKnowledgeService
	updateCalled atomic.Bool
	deleteCalled atomic.Bool
}

func (s *forbiddingKnowledgeService) UpdateManualKnowledge(
	_ context.Context, id string, payload *types.ManualKnowledgePayload,
) (*types.Knowledge, error) {
	s.updateCalled.Store(true)
	return &types.Knowledge{ID: id, Title: payload.Title}, nil
}

func (s *forbiddingKnowledgeService) DeleteKnowledge(_ context.Context, _ string) error {
	s.deleteCalled.Store(true)
	return nil
}

func TestAuditUpdateAndDeleteDocumentRequireKBWriteCapability(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-own", TenantID: 1, Name: "Own"}
	ks := &forbiddingKnowledgeService{
		stubKnowledgeService: &stubKnowledgeService{
			docs: map[string]*types.Knowledge{
				"doc-1": {ID: "doc-1", TenantID: 1, KnowledgeBaseID: "kb-own", Title: "Doc 1"},
			},
		},
	}
	srv := newScopeTestServer(kb)
	srv.knowledgeService = ks

	// Endpoint lacks ingest tools -> MCPEndpointScope lacks APIKeyCapabilityIngest.
	readOnlyEp := &types.MCPEndpoint{
		ID:               "ep-ro",
		TenantID:         1,
		KnowledgeBaseIDs: types.StringArray{"kb-own"},
		Tools:            types.StringArray{types.MCPEndpointToolSearchKnowledge},
	}
	roCtx := mcpCallContext(1, readOnlyEp)

	updRes, err := srv.handleUpdateDocument(roCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: types.MCPEndpointToolUpdateDocument,
			Arguments: map[string]any{
				"knowledge_id": "doc-1",
				"content":      "tampered markdown",
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, updRes)
	require.True(t, updRes.IsError, "update_document must reject callers without KB write capability")
	require.False(t, ks.updateCalled.Load(), "UpdateManualKnowledge must not be called without write grant")

	delRes, err := srv.handleDeleteDocument(roCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: types.MCPEndpointToolDeleteDocument,
			Arguments: map[string]any{
				"knowledge_id": "doc-1",
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, delRes)
	require.True(t, delRes.IsError, "delete_document must reject callers without KB write capability")
	require.False(t, ks.deleteCalled.Load(), "DeleteKnowledge must not be called without write grant")
}

type panickingAskSessionService struct {
	interfaces.SessionService
}

func (s *panickingAskSessionService) CreateSession(_ context.Context, session *types.Session) (*types.Session, error) {
	session.ID = "session-panic"
	return session, nil
}

func (s *panickingAskSessionService) SetSessionOwnerID(context.Context, uint64, string, string) error {
	return nil
}

func (s *panickingAskSessionService) KnowledgeQA(context.Context, *types.QARequest, *event.EventBus) error {
	panic("simulated KnowledgeQA panic")
}

func (s *panickingAskSessionService) AgentQA(context.Context, *types.QARequest, *event.EventBus) error {
	panic("simulated AgentQA panic")
}

type panickingEndpointRepo struct {
	interfaces.MCPEndpointRepository
	done chan struct{}
}

func (r *panickingEndpointRepo) TouchLastUsed(context.Context, string) error {
	defer close(r.done)
	panic("simulated TouchLastUsed panic")
}

func TestAuditAskAndTouchLastUsedRecoverSpawnedGoroutinePanics(t *testing.T) {
	t.Run("KnowledgeQA panic is recovered and returned as tool error", func(t *testing.T) {
		session := &askProgressTestSession{notifications: make(chan mcp.JSONRPCNotification, 4)}
		f, ctx := newAskProgressTestFixture(t, session)
		f.srv.sessionService = &panickingAskSessionService{}

		result, err := f.srv.handleAsk(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{"question": "Will this crash the process?"},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, result.IsError)
	})

	t.Run("AgentQA panic is recovered and returned as tool error", func(t *testing.T) {
		session := &askProgressTestSession{notifications: make(chan mcp.JSONRPCNotification, 4)}
		f, ctx := newAskProgressTestFixture(t, session)
		f.srv.sessionService = &panickingAskSessionService{}
		f.srv.agentService = &stubAgentService{agents: map[string]*types.CustomAgent{
			"agent-1": {
				ID:       "agent-1",
				TenantID: 1,
				Config:   types.CustomAgentConfig{AgentMode: types.AgentModeSmartReasoning},
			},
		}}

		result, err := f.srv.handleAsk(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"question": "Will AgentQA panic crash the process?",
					"agent_id": "agent-1",
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, result.IsError)
	})

	t.Run("touchLastUsed recovers background repository panic", func(t *testing.T) {
		repo := &panickingEndpointRepo{done: make(chan struct{})}
		srv := &Server{endpointRepo: repo}
		srv.touchLastUsed(context.Background(), "ep-panic")
		select {
		case <-repo.done:
		case <-time.After(time.Second):
			t.Fatal("touchLastUsed did not invoke repository")
		}
		time.Sleep(20 * time.Millisecond)
	})
}
