package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientEscapesPathSegmentsAndBlocksTraversal(t *testing.T) {
	var hitPaths []string
	var rawPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPaths = append(hitPaths, r.URL.Path)
		rawPaths = append(rawPaths, r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"k1"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("sk-secret"))
	ctx := context.Background()

	// Empty path segment must fail locally before hitting collection routes.
	if _, err := c.GetKnowledgeBase(ctx, ""); err == nil {
		t.Fatal("expected empty knowledgeBaseID to be rejected")
	}
	if len(hitPaths) != 0 {
		t.Fatalf("empty ID should not send HTTP request, hit %v", hitPaths)
	}

	// Raw dot-segment traversal must be rejected locally.
	if _, err := c.Raw(ctx, http.MethodGet, "/api/v1/knowledge/../tenants/1/api-keys", nil); err == nil {
		t.Fatal("expected dot-segment traversal in Raw path to be rejected")
	}

	// Untrusted traversal ID in typed SDK method must stay confined as an escaped segment.
	_, _ = c.GetKnowledge(ctx, "../tenants/1/api-keys")
	if len(rawPaths) != 1 {
		t.Fatalf("expected 1 request, got %d", len(rawPaths))
	}
	if rawPaths[0] == "/api/v1/tenants/1/api-keys" || hitPaths[0] == "/api/v1/tenants/1/api-keys" {
		t.Fatalf("CSPT succeeded! escaped=%q path=%q", rawPaths[0], hitPaths[0])
	}
	if want := "/api/v1/knowledge/..%2Ftenants%2F1%2Fapi-keys"; rawPaths[0] != want {
		t.Fatalf("expected escaped path %q, got %q", want, rawPaths[0])
	}

	// Standalone ".." must also be percent-encoded rather than traversing to /api/v1.
	_, _ = c.GetKnowledge(ctx, "..")
	if got := rawPaths[len(rawPaths)-1]; got != "/api/v1/knowledge/%2E%2E" {
		t.Fatalf("expected standalone '..' to encode as %%2E%%2E, got %q", got)
	}
}

func TestClientStripsCredentialsOnCrossOriginRedirect(t *testing.T) {
	var leakedAuth, leakedAPIKey, leakedTenant string
	evilSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakedAuth = r.Header.Get("Authorization")
		leakedAPIKey = r.Header.Get("X-API-Key")
		leakedTenant = r.Header.Get("X-Tenant-ID")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer evilSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evilSrv.URL+"/captured", http.StatusFound)
	}))
	defer apiSrv.Close()

	c := NewClient(
		apiSrv.URL,
		WithBearerToken("jwt-secret-token"),
		WithAPIKey("sk-secret-key"),
		WithTenantID(42),
	)
	resp, err := c.Raw(context.Background(), http.MethodGet, "/api/v1/knowledge-bases", nil)
	if err != nil {
		t.Fatalf("unexpected error following redirect: %v", err)
	}
	_ = resp.Body.Close()

	if leakedAuth != "" || leakedAPIKey != "" || leakedTenant != "" {
		t.Fatalf("credentials leaked across cross-origin redirect: Authorization=%q X-API-Key=%q X-Tenant-ID=%q",
			leakedAuth, leakedAPIKey, leakedTenant)
	}
}
