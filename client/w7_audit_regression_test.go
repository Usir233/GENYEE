package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestW7RedirectBoomerangRequestIDAndBodyReplayBlocked(t *testing.T) {
	t.Run("boomerang redirect OriginA -> EvilB -> OriginA strips all credentials and X-Request-ID", func(t *testing.T) {
		var finalHeaders http.Header
		var originURL string

		evilSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, originURL+"/api/v1/tenants/1", http.StatusFound)
		}))
		defer evilSrv.Close()

		originSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/knowledge-bases" {
				http.Redirect(w, r, evilSrv.URL+"/bounce", http.StatusFound)
				return
			}
			finalHeaders = r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true}`))
		}))
		defer originSrv.Close()
		originURL = originSrv.URL

		c := NewClient(
			originSrv.URL,
			WithBearerToken("jwt-secret-token"),
			WithAPIKey("gy_secret_key"),
			WithTenantID(42),
		)
		//nolint:staticcheck // Exercises string RequestID context key supported by applyAuthHeaders.
		ctx := context.WithValue(context.Background(), "RequestID", "req-sensitive-123")
		resp, err := c.Raw(ctx, http.MethodGet, "/api/v1/knowledge-bases", nil)
		if err != nil {
			t.Fatalf("unexpected GET redirect error: %v", err)
		}
		_ = resp.Body.Close()

		for _, h := range []string{"Authorization", "X-Api-Key", "X-Tenant-Id", "X-Request-Id"} {
			if got := finalHeaders.Get(h); got != "" {
				t.Fatalf("boomerang redirect re-attached %s=%q after cross-origin hop", h, got)
			}
		}
	})

	t.Run("cross-origin 307 POST refuses to replay sensitive request body", func(t *testing.T) {
		var leakedBody string
		evilSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			leakedBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"access_token":"a","refresh_token":"r"}`))
		}))
		defer evilSrv.Close()

		originSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, evilSrv.URL+"/steal", http.StatusTemporaryRedirect)
		}))
		defer originSrv.Close()

		_, err := NewClient(originSrv.URL).RefreshToken(context.Background(), "rt-super-secret")
		if err == nil {
			t.Fatal("expected cross-origin 307 POST redirect to be refused")
		}
		if leakedBody != "" {
			t.Fatalf("sensitive POST body replayed to cross-origin server: %q", leakedBody)
		}
	})
}

func TestW7ExportFAQEntriesCapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.Copy(w, &fillerReader{remaining: maxResponseBodyBytes + 1024})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).ExportFAQEntries(context.Background(), "kb-1")
	if !errors.Is(err, errResponseBodyTooLarge) {
		t.Fatalf("ExportFAQEntries() = %v, want %v", err, errResponseBodyTooLarge)
	}
}

func TestW7SSEMultiLineFrameCapAndNamedEventFiltering(t *testing.T) {
	t.Run("multi-line SSE frame exceeding cap is rejected without unbounded buffering", func(t *testing.T) {
		chunk := "data: " + strings.Repeat("a", 64*1024) + "\n"
		repeats := (4<<20)/(64*1024) + 4
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for i := 0; i < repeats; i++ {
				if _, err := io.WriteString(w, chunk); err != nil {
					return
				}
			}
			_, _ = io.WriteString(w, "\n")
		}))
		defer srv.Close()

		err := NewClient(srv.URL).KnowledgeQAStream(
			context.Background(),
			"sess-1",
			&KnowledgeQARequest{Query: "q"},
			func(*StreamResponse) error { return nil },
		)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("expected SSE frame size cap error, got %v", err)
		}
	})

	t.Run("named non-message SSE events are ignored in KnowledgeQAStream and AgentQAStream", func(t *testing.T) {
		ssePayload := "event: progress\ndata: non-json-heartbeat\n\n" +
			"data: {\"response_type\":\"answer\",\"content\":\"ok\",\"done\":true}\n\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, ssePayload)
		}))
		defer srv.Close()

		var gotQA string
		if err := NewClient(srv.URL).KnowledgeQAStream(
			context.Background(),
			"sess-1",
			&KnowledgeQARequest{Query: "q"},
			func(sr *StreamResponse) error {
				gotQA += sr.Content
				return nil
			},
		); err != nil {
			t.Fatalf("KnowledgeQAStream failed on named SSE event: %v", err)
		}
		if gotQA != "ok" {
			t.Fatalf("got %q, want ok", gotQA)
		}

		var gotAgent string
		if err := NewClient(srv.URL).AgentQAStreamWithRequest(
			context.Background(),
			"sess-1",
			&AgentQARequest{Query: "q", AgentEnabled: true},
			func(sr *AgentStreamResponse) error {
				gotAgent += sr.Content
				return nil
			},
		); err != nil {
			t.Fatalf("AgentQAStreamWithRequest failed on named SSE event: %v", err)
		}
		if gotAgent != "ok" {
			t.Fatalf("got %q, want ok", gotAgent)
		}
	})
}

func TestW7ValidateAPIPathRawAndNilGuards(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected HTTP request to %s", r.URL.String())
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	ctx := context.Background()

	for _, bad := range []string{
		"/api/v1/knowledge-bases?page=1#frag",
		"//evil.example.com/api/v1/knowledge-bases",
		`/api/v1/knowledge\..\..\tenants`,
		"/api/v1/knowledge/%2e%2e/tenants/1/api-keys",
		"/api/v1/knowledge/%2E%2E/tenants/1/api-keys",
		"/api/v1/knowledge/foo%2fbar",
	} {
		if resp, err := c.Raw(ctx, http.MethodGet, bad, nil); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("expected Raw(%q) to be rejected", bad)
		}
	}

	if _, err := NewClient("").ListKnowledgeBases(ctx); err == nil {
		t.Fatal("expected empty baseURL to fail cleanly")
	}

	// Nil request / callback guards must return errors rather than panic.
	if err := c.UpdateKnowledge(ctx, nil); err == nil {
		t.Fatal("expected UpdateKnowledge(nil) error")
	}
	if _, err := c.UpdateTenant(ctx, nil); err == nil {
		t.Fatal("expected UpdateTenant(nil) error")
	}
	if err := c.KnowledgeQAStream(ctx, "s1", nil, func(*StreamResponse) error { return nil }); err == nil {
		t.Fatal("expected KnowledgeQAStream(nil request) error")
	}
	if err := c.KnowledgeQAStream(ctx, "s1", &KnowledgeQARequest{Query: "q"}, nil); err == nil {
		t.Fatal("expected KnowledgeQAStream(nil callback) error")
	}
	if err := c.ContinueStream(ctx, "s1", "m1", nil); err == nil {
		t.Fatal("expected ContinueStream(nil callback) error")
	}
	if _, err := c.SearchKnowledgeDetailed(ctx, nil); err == nil {
		t.Fatal("expected SearchKnowledgeDetailed(nil) error")
	}
	if err := c.AgentQAStreamWithRequest(ctx, "s1", &AgentQARequest{Query: "q"}, nil); err == nil {
		t.Fatal("expected AgentQAStreamWithRequest(nil callback) error")
	}
}
