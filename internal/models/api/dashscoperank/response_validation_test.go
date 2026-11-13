package dashscoperank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/require"
)

// A malformed successful HTTP reply must fail before it can replace retrieval
// candidates with duplicated indexes or invented zero scores.
func TestRejectsMalformedRankingEntries(t *testing.T) {
	for name, payload := range map[string]string{
		"duplicate index": `{"output":{"results":[{"index":0,"relevance_score":0.9},{"index":0,"relevance_score":0.8}]}}`,
		"missing index":   `{"output":{"results":[{"relevance_score":0.9}]}}`,
		"null index":      `{"output":{"results":[{"index":null,"relevance_score":0.9}]}}`,
		"missing score":   `{"output":{"results":[{"index":0}]}}`,
		"null score":      `{"output":{"results":[{"index":0,"relevance_score":null}]}}`,
		"negative index":  `{"output":{"results":[{"index":-1,"relevance_score":0.9}]}}`,
		"large index":     `{"output":{"results":[{"index":2,"relevance_score":0.9}]}}`,
		"null entry":      `{"output":{"results":[null]}}`,
		"null response":   `null`,
		"missing array":   `{}`,
		"null array":      `{"output":{"results":null}}`,
		"error envelope":  `{"error":{"message":"model unavailable"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := validationClient(t, payload)
			got, err := c.Rerank(context.Background(), "query", []string{"first", "second"})
			require.Error(t, err)
			require.Nil(t, got)
		})
	}
}

func TestPreservesValidRankingEntries(t *testing.T) {
	for name, tc := range map[string]struct {
		payload string
		want    []api.RerankResult
	}{
		"empty":          {`{"output":{"results":[]}}`, []api.RerankResult{}},
		"explicit zeros": {`{"output":{"results":[{"index":0,"relevance_score":0}]}}`, []api.RerankResult{{Index: 0, Score: 0}}},
		"partial":        {`{"output":{"results":[{"index":1,"relevance_score":0.7}]}}`, []api.RerankResult{{Index: 1, Score: 0.7}}},
		"reordered":      {`{"output":{"results":[{"index":1,"relevance_score":0.7},{"index":0,"relevance_score":0.1}]}}`, []api.RerankResult{{Index: 1, Score: 0.7}, {Index: 0, Score: 0.1}}},
	} {
		t.Run(name, func(t *testing.T) {
			c := validationClient(t, tc.payload)
			got, err := c.Rerank(context.Background(), "query", []string{"first", "second"})
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func validationClient(t *testing.T, payload string) *Client {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	return newClient(t, server.URL, api.RerankSettings{ImageInput: api.ImageInput{ImageField: "image"}})
}
