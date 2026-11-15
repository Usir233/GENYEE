package rerank

import (
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	secutils "github.com/Usir233/GENYEE/internal/utils"
)

func withRerankSSRFWhitelist(t *testing.T, raw string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", raw)
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestRerankerRejectsInternalBaseURL(t *testing.T) {
	withRerankSSRFWhitelist(t, "")

	_, err := NewReranker(&RerankerConfig{
		BaseURL:   "http://169.254.169.254/latest/meta-data/",
		ModelName: "rerank-test",
	})
	if err == nil {
		t.Fatalf("NewReranker returned nil error for blocked internal BaseURL")
	}
}

func TestRerankerBlocksRedirectToInternalURL(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer server.Close()

	reranker, err := NewReranker(&RerankerConfig{
		BaseURL:   server.URL,
		ModelName: "rerank-test",
		APIKey:    "sk-test",
	})
	if err != nil {
		t.Fatalf("NewReranker: %v", err)
	}

	_, err = reranker.Rerank(t.Context(), "query", []string{"doc"})
	if !stderrors.Is(err, secutils.ErrSSRFRedirectBlocked) {
		t.Fatalf("Rerank error = %v, want ErrSSRFRedirectBlocked", err)
	}
}

func TestLKEAPRerankerBlocksInternalBaseURLAndRedirects(t *testing.T) {
	t.Run("rejects internal BaseURL", func(t *testing.T) {
		withRerankSSRFWhitelist(t, "")
		_, err := NewReranker(&RerankerConfig{
			Provider:  "lkeap",
			ModelName: "lke-reranker-base",
			BaseURL:   "http://169.254.169.254/latest/meta-data/",
			APIKey:    "AKID-test",
			AppSecret: "SK-test",
		})
		if err == nil {
			t.Fatalf("NewReranker(lkeap) returned nil error for blocked internal BaseURL")
		}
	})

	t.Run("blocks redirect to internal URL", func(t *testing.T) {
		withRerankSSRFWhitelist(t, "127.0.0.1")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		}))
		defer server.Close()

		reranker, err := NewReranker(&RerankerConfig{
			Provider:  "lkeap",
			ModelName: "lke-reranker-base",
			BaseURL:   server.URL,
			APIKey:    "AKID-test",
			AppSecret: "SK-test",
		})
		if err != nil {
			t.Fatalf("NewReranker(lkeap): %v", err)
		}

		_, err = reranker.Rerank(t.Context(), "query", []string{"doc"})
		if !stderrors.Is(err, secutils.ErrSSRFRedirectBlocked) {
			t.Fatalf("LKEAP Rerank error = %v, want ErrSSRFRedirectBlocked", err)
		}
	})
}
