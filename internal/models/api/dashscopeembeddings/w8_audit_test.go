package dashscopeembeddings

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

func TestEmbedEmptyShortCircuitsAndNilContextRedactsSecrets(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"output":{"embeddings":[{"index":0,"embedding":[0.1,0.2]}]}}`))
	}))
	defer srv.Close()

	c := New(Config{Endpoint: api.Endpoint{BaseURL: srv.URL, Model: "multimodal-embedding-v1"}})
	got, err := c.Embed(nil, nil, api.EmbedDocument)
	require.NoError(t, err)
	assert.Empty(t, got)
	gotImg, err := c.EmbedImages(nil, nil, api.EmbedDocument)
	require.NoError(t, err)
	assert.Empty(t, gotImg)
	assert.Zero(t, hits.Load(), "empty batch must not send HTTP POST")

	got, err = c.Embed(nil, []string{"one"}, api.EmbedDocument)
	require.NoError(t, err)
	require.Len(t, got, 1)

	bad := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@dashscope.aliyuncs.com\x7f",
			Model:   "multimodal-embedding-v1",
			Query:   map[string]string{"api_key": "sk-dashscope-secret-999"},
		},
	})
	_, err = bad.Embed(nil, []string{"one"}, api.EmbedDocument)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-dashscope-secret-999")
}
