package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type streamingNotifyRecorder struct {
	header      http.Header
	wroteHeader chan int
	once        sync.Once
	mu          sync.Mutex
	code        int
}

func newStreamingNotifyRecorder() *streamingNotifyRecorder {
	return &streamingNotifyRecorder{
		header:      make(http.Header),
		wroteHeader: make(chan int, 1),
	}
}

func (r *streamingNotifyRecorder) Header() http.Header {
	return r.header
}

func (r *streamingNotifyRecorder) WriteHeader(statusCode int) {
	r.mu.Lock()
	r.code = statusCode
	r.mu.Unlock()
	r.once.Do(func() {
		r.wroteHeader <- statusCode
	})
}

func (r *streamingNotifyRecorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return len(p), nil
}

func (r *streamingNotifyRecorder) Flush() {
	r.WriteHeader(http.StatusOK)
}

func TestServerCloseTerminatesActiveSSEListeningStreamAndIsConcurrentSafe(t *testing.T) {
	ep := &types.MCPEndpoint{
		ID:                 "ep-sse",
		TenantID:           1,
		Enabled:            true,
		RateLimitPerMinute: 100,
		Tools:              types.StringArray{types.MCPEndpointToolListKnowledgeBases},
	}
	srv := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	t.Cleanup(srv.Close)

	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()
	reqCtx = context.WithValue(reqCtx, types.MCPEndpointContextKey, ep)
	reqCtx = context.WithValue(reqCtx, types.TenantIDContextKey, ep.TenantID)

	req := httptest.NewRequest(http.MethodGet, "/mcp/ep-sse", nil).WithContext(reqCtx)
	req.Header.Set("Accept", "text/event-stream")
	rec := newStreamingNotifyRecorder()

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		srv.Handler().ServeHTTP(rec, req)
	}()

	select {
	case code := <-rec.wroteHeader:
		require.Equal(t, http.StatusOK, code)
	case <-time.After(2 * time.Second):
		t.Fatal("GET SSE stream did not start")
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.Close()
		}()
	}
	wg.Wait()

	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		reqCancel()
		t.Fatal("active SSE listening session remained open after Server.Close()")
	}
}

type failingBindSessionService struct {
	interfaces.SessionService
	mu         sync.Mutex
	deletedIDs []string
	bindErr    error
}

func (s *failingBindSessionService) CreateSession(_ context.Context, session *types.Session) (*types.Session, error) {
	out := *session
	out.ID = "orphan-session-1"
	return &out, nil
}

func (s *failingBindSessionService) SetSessionOwnerID(context.Context, uint64, string, string) error {
	return s.bindErr
}

func (s *failingBindSessionService) DeleteSession(_ context.Context, id string) error {
	s.mu.Lock()
	s.deletedIDs = append(s.deletedIDs, id)
	s.mu.Unlock()
	return nil
}

func TestResolveAskSessionDeletesOrphanSessionWhenOwnerBindFails(t *testing.T) {
	sessionSvc := &failingBindSessionService{bindErr: errors.New("db write error")}
	srv := &Server{sessionService: sessionSvc}
	ep := &types.MCPEndpoint{ID: "ep-bind", TenantID: 7, Name: "Docs"}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err := srv.resolveAskSession(ctx, ep, "", "What is GENYEE?")
	require.ErrorContains(t, err, "failed to bind session")

	sessionSvc.mu.Lock()
	deleted := append([]string(nil), sessionSvc.deletedIDs...)
	sessionSvc.mu.Unlock()
	require.Equal(
		t, []string{"orphan-session-1"}, deleted,
		"newly created unbound session must be deleted on bind failure",
	)
}
