package googleembeddings

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

func TestEmbedRedactsQueryKeyAndUserinfoInErrors(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	// Invalid control byte in URL forces http.NewRequestWithContext to return a *url.Error
	// embedding the full target URL (including ?key= and userinfo) BEFORE Endpoint.Do runs.
	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@generativelanguage.googleapis.com/v1beta\x7f",
			Model:   "gemini-embedding-001",
			Query:   map[string]string{"key": "AIzaSySuperSecretKey12345"},
		},
	})
	_, err := c.Embed(nil, []string{"hello"}, api.EmbedDocument)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "AIzaSySuperSecretKey12345")
	assert.NotContains(t, err.Error(), "supersecretpw")
}

func TestEmbedEscapesModelSegmentAndShortCircuitsEmptyInputs(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	var hits atomic.Int32
	var rawPath, rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		rawPath = r.URL.Path
		rawQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"embeddings":[{"values":[0.25,0.75]}]}`))
	}))
	defer srv.Close()

	c := newClient(srv.URL+"/v1beta", "gemini-embedding-001?key=injected#frag", api.EmbeddingsSettings{}, 0)

	got, err := c.Embed(nil, nil, api.EmbedDocument)
	require.NoError(t, err)
	assert.Empty(t, got)
	gotImg, err := c.EmbedImages(nil, nil, api.EmbedDocument)
	require.NoError(t, err)
	assert.Empty(t, gotImg)
	assert.Zero(t, hits.Load(), "empty batch must not send HTTP POST")

	got, err = c.Embed(nil, []string{"a"}, api.EmbedDocument)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, rawQuery, "model name must not inject query parameters")
	assert.NotContains(t, rawPath, "../")
}
