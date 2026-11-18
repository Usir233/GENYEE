package web_search

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
)

func TestGoogleProvider_AttachesAPIKeyAndBlocksRedirects(t *testing.T) {
	utils.ResetSSRFWhitelistForTest()
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	defer func() {
		_ = os.Unsetenv("SSRF_WHITELIST")
		utils.ResetSSRFWhitelistForTest()
	}()

	const secretKey = "AIzaSyTestGoogleSecretKey12345"

	var redirectTargetHit atomic.Bool
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectTargetHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	var shouldRedirect atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != secretKey {
			t.Errorf("expected key query param %q, got %q (raw query: %q)", secretKey, got, r.URL.RawQuery)
			http.Error(w, "missing api key", http.StatusForbidden)
			return
		}
		if got := r.URL.Query().Get("cx"); got != "engine-123" {
			t.Errorf("expected cx=engine-123, got %q", got)
		}
		if got := r.URL.Query().Get("q"); got != "golang" {
			t.Errorf("expected q=golang, got %q", got)
		}
		if shouldRedirect.Load() {
			http.Redirect(w, r, redirectTarget.URL+"/steal", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"title": "Go", "link": "https://go.dev", "snippet": "The Go language"},
			},
		})
	}))
	defer srv.Close()

	searcher, err := NewGoogleProvider(types.WebSearchProviderParameters{
		APIKey:   secretKey,
		EngineID: "engine-123",
	})
	if err != nil {
		t.Fatalf("NewGoogleProvider: %v", err)
	}
	gp := searcher.(*GoogleProvider)
	gp.srv.BasePath = srv.URL + "/"

	results, err := gp.Search(context.Background(), types.WebSearchRequest{
		Query:      "golang",
		MaxResults: 3,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://go.dev" {
		t.Fatalf("unexpected results: %+v", results)
	}

	shouldRedirect.Store(true)
	_, err = gp.Search(context.Background(), types.WebSearchRequest{
		Query:      "golang",
		MaxResults: 3,
	})
	if err == nil {
		t.Fatal("expected redirect response to be rejected instead of followed")
	}
	if redirectTargetHit.Load() {
		t.Fatal("GoogleProvider followed HTTP redirect and leaked request to redirect target")
	}
	if strings.Contains(err.Error(), secretKey) {
		t.Fatalf("GoogleProvider leaked API key in redirect error: %v", err)
	}
}

func TestGoogleProvider_RedactsAPIKeyAndBoundsResponses(t *testing.T) {
	utils.ResetSSRFWhitelistForTest()
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	defer func() {
		_ = os.Unsetenv("SSRF_WHITELIST")
		utils.ResetSSRFWhitelistForTest()
	}()

	const secretKey = "AIzaSyLeakCheck999888777"

	t.Run("redacts api key from upstream error body and transport failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			errBody := "upstream failed for url https://customsearch.googleapis.com/customsearch/v1?key=" +
				secretKey + " detail=" + strings.Repeat("x", maxErrorPreviewBytes*4)
			_, _ = w.Write([]byte(errBody))
		}))

		searcher, err := NewGoogleProvider(types.WebSearchProviderParameters{
			APIKey:   secretKey,
			EngineID: "cx-1",
		})
		if err != nil {
			srv.Close()
			t.Fatalf("NewGoogleProvider: %v", err)
		}
		gp := searcher.(*GoogleProvider)
		gp.srv.BasePath = srv.URL + "/"

		_, err = gp.Search(context.Background(), types.WebSearchRequest{Query: "test", MaxResults: 1})
		srv.Close()
		if err == nil {
			t.Fatal("expected error on 502")
		}
		if strings.Contains(err.Error(), secretKey) {
			t.Fatalf("error leaked secret API key: %v", err)
		}
		if len(err.Error()) > maxErrorPreviewBytes+512 {
			t.Fatalf("error body was not bounded: len=%d", len(err.Error()))
		}

		// Closed server triggers transport error (*url.Error).
		_, err = gp.Search(context.Background(), types.WebSearchRequest{Query: "test", MaxResults: 1})
		if err == nil {
			t.Fatal("expected transport error on closed server")
		}
		if strings.Contains(err.Error(), secretKey) {
			t.Fatalf("transport error leaked secret API key: %v", err)
		}
	})

	t.Run("rejects oversized 200 JSON body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[`))
			_, _ = io.Copy(w, &fillerReader{remaining: maxJSONResponseBytes + (1 << 16)})
		}))
		defer srv.Close()

		searcher, err := NewGoogleProvider(types.WebSearchProviderParameters{
			APIKey:   secretKey,
			EngineID: "cx-1",
		})
		if err != nil {
			t.Fatalf("NewGoogleProvider: %v", err)
		}
		gp := searcher.(*GoogleProvider)
		gp.srv.BasePath = srv.URL + "/"

		_, err = gp.Search(context.Background(), types.WebSearchRequest{Query: "test", MaxResults: 1})
		if err == nil {
			t.Fatal("expected oversized response body to fail")
		}
		if !errors.Is(err, errResponseTooLarge) {
			t.Fatalf("expected errResponseTooLarge, got: %v", err)
		}
	})
}
