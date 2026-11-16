package teirank

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRerankEmptyShortCircuitsNilContextAndRedactsSecrets(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`[{"index":0,"score":0.8}]`))
	}))
	defer srv.Close()

	c := New(Config{Endpoint: api.Endpoint{BaseURL: srv.URL}})
	got, err := c.Rerank(nil, "q", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, hits.Load(), "empty documents slice must not send HTTP POST")

	got, err = c.Rerank(nil, "q", []string{"doc"})
	require.NoError(t, err)
	require.Len(t, got, 1)

	bad := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@tei.example.com\x7f",
			Query:   map[string]string{"token": "tei-secret-token-999"},
		},
	})
	_, err = bad.Rerank(nil, "q", []string{"doc"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "tei-secret-token-999")
}
