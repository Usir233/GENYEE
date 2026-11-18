package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestApplyResourceURLQuery(t *testing.T) {
	q := url.Values{}
	applyResourceURLQuery(q, nil)
	if len(q) != 0 {
		t.Fatalf("nil opts: got %v, want empty", q)
	}

	q = url.Values{}
	applyResourceURLQuery(q, &ResourceURLOptions{ResourceURLs: ResourceURLModeHandle})
	if got := q.Get("resource_urls"); got != "handle" {
		t.Fatalf("handle mode: got %q, want handle", got)
	}

	q = url.Values{}
	applyResourceURLQuery(q, &ResourceURLOptions{ResourceURLs: ResourceURLModePublic})
	if got := q.Get("resource_urls"); got != "public" {
		t.Fatalf("public mode: got %q, want public", got)
	}
}

func TestExplicitHandleModeOverridesPublicServerDefault(t *testing.T) {
	// Matches internal/storageurl.requestedMode: an omitted query inherits the
	// server default, while an explicit value overrides it.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := "https://example.invalid/public-token"
		if r.URL.Query().Get("resource_urls") == "handle" {
			content = "resource://private-file"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    []map[string]string{{"id": "message", "content": content}},
		})
	}))
	defer s.Close()
	c := NewClient(s.URL)
	for _, tc := range []struct {
		mode ResourceURLMode
		want string
	}{
		{ResourceURLModeHandle, "resource://private-file"},
		{ResourceURLModePublic, "https://example.invalid/public-token"},
		{"", "https://example.invalid/public-token"},
	} {
		rows, err := c.LoadMessages(context.Background(), "session", 10, nil, ResourceURLOptions{ResourceURLs: tc.mode})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Content != tc.want {
			t.Errorf("mode=%q rows=%+v want=%q", tc.mode, rows, tc.want)
		}
	}
}
