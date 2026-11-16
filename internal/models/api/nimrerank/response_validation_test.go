package nimrerank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/require"
)

// A malformed successful HTTP reply must fail before it can replace retrieval
// candidates with duplicated indexes or invented zero scores.
func TestRejectsMalformedRankingEntries(t *testing.T) {
	for name, payload := range map[string]string{
		"duplicate index": `{"rankings":[{"index":0,"logit":0.9},{"index":0,"logit":0.8}]}`,
		"missing index":   `{"rankings":[{"logit":0.9}]}`,
		"null index":      `{"rankings":[{"index":null,"logit":0.9}]}`,
		"missing score":   `{"rankings":[{"index":0}]}`,
		"null score":      `{"rankings":[{"index":0,"logit":null}]}`,
		"negative index":  `{"rankings":[{"index":-1,"logit":0.9}]}`,
		"large index":     `{"rankings":[{"index":2,"logit":0.9}]}`,
		"null entry":      `{"rankings":[null]}`,
		"null response":   `null`,
		"missing array":   `{}`,
		"null array":      `{"rankings":null}`,
		"error envelope":  `{"error":{"message":"model unavailable"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := validationClient(t, payload)
			got, err := c.Rerank(context.Background(), "query", []string{"first", "second"})
			require.Error(t, err)
			require.Nil(t, got)
			got, err = c.RerankImages(context.Background(), "query", []api.EmbedImage{{Data: []byte{1}, MIMEType: "image/png"}, {Data: []byte{2}, MIMEType: "image/png"}})
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
		"empty":          {`{"rankings":[]}`, []api.RerankResult{}},
		"explicit zeros": {`{"rankings":[{"index":0,"logit":0}]}`, []api.RerankResult{{Index: 0, Score: 0}}},
		"partial":        {`{"rankings":[{"index":1,"logit":0.7}]}`, []api.RerankResult{{Index: 1, Score: 0.7}}},
		"reordered":      {`{"rankings":[{"index":1,"logit":0.7},{"index":0,"logit":0.1}]}`, []api.RerankResult{{Index: 1, Score: 0.7}, {Index: 0, Score: 0.1}}},
		"negative logit": {`{"rankings":[{"index":0,"logit":-3.1}]}`, []api.RerankResult{{Index: 0, Score: -3.1}}},
	} {
		t.Run(name, func(t *testing.T) {
			c := validationClient(t, tc.payload)
			got, err := c.Rerank(context.Background(), "query", []string{"first", "second"})
			require.NoError(t, err)
			for i := range got {
				got[i].Text = ""
			} // NIM attaches the submitted text; compare index/score here.
			require.Equal(t, tc.want, got)
			got, err = c.RerankImages(context.Background(), "query", []api.EmbedImage{{Data: []byte{1}, MIMEType: "image/png"}, {Data: []byte{2}, MIMEType: "image/png"}})
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRejectsMalformedRankings(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{"null response", `null`, "rankings"},
		{"empty object", `{}`, "rankings"},
		{"error envelope", `{"error":{"message":"model is not loaded"}}`, "rankings"},
		{"wrong envelope", `{"data":[]}`, "rankings"},
		{"null rankings", `{"rankings":null}`, "rankings"},
		{"null row", `{"rankings":[null]}`, "index or logit"},
		{"empty row", `{"rankings":[{}]}`, "index or logit"},
		{"missing index", `{"rankings":[{"logit":0.5}]}`, "index or logit"},
		{"null index", `{"rankings":[{"index":null,"logit":0.5}]}`, "index or logit"},
		{"missing logit", `{"rankings":[{"index":0}]}`, "index or logit"},
		{"null logit", `{"rankings":[{"index":0,"logit":null}]}`, "index or logit"},
		{
			"malformed row after valid row",
			`{"rankings":[{"index":1,"logit":1.2},{"index":0}]}`,
			"index or logit",
		},
		{
			"duplicate index",
			`{"rankings":[{"index":0,"logit":1.2},{"index":0,"logit":0.5}]}`,
			"duplicate index",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validationClient(t, tt.payload)

			got, err := c.Rerank(context.Background(), "q", []string{"d0", "d1"})
			require.ErrorContains(t, err, tt.wantErr)
			require.Nil(t, got)

			got, err = c.RerankImages(context.Background(), "q", []api.EmbedImage{
				{Data: []byte{1}, MIMEType: "image/png"},
				{Data: []byte{2}, MIMEType: "image/png"},
			})
			require.ErrorContains(t, err, tt.wantErr)
			require.Nil(t, got)
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

func TestRerankEmptyShortCircuitsNilContextAndRedactsSecrets(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"rankings":[{"index":0,"logit":0.9}]}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, api.RerankSettings{})
	got, err := c.Rerank(nil, "q", nil)
	require.NoError(t, err)
	require.Empty(t, got)
	gotImg, err := c.RerankImages(nil, "q", nil)
	require.NoError(t, err)
	require.Empty(t, gotImg)
	require.Zero(t, hits.Load(), "empty documents/images slice must not send HTTP POST")

	got, err = c.Rerank(nil, "q", []string{"doc"})
	require.NoError(t, err)
	require.Len(t, got, 1)

	gotImg, err = c.RerankImages(nil, "q", []api.EmbedImage{{Data: []byte{1}, MIMEType: "image/png"}})
	require.NoError(t, err)
	require.Len(t, gotImg, 1)

	bad := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@ai.api.nvidia.com\x7f",
			Model:   "nvidia/nv-rerankqa-mistral-4b-v3",
			Query:   map[string]string{"api_key": "nvapi-secret-token-999"},
		},
	})
	_, err = bad.Rerank(nil, "q", []string{"doc"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "supersecretpw")
	require.NotContains(t, err.Error(), "nvapi-secret-token-999")
}
