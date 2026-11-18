package openaiembeddings

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
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.5,0.5]}]}`))
	}))
	defer srv.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: srv.URL, Model: "text-embedding-3-small"},
		Settings: api.EmbeddingsSettings{ImageInput: api.ImageInput{ImageField: "image"}},
	})
	//nolint:staticcheck // verifying defensive nil-context handling
	got, err := c.Embed(nil, nil, api.EmbedDocument)
	require.NoError(t, err)
	assert.Empty(t, got)
	//nolint:staticcheck // verifying defensive nil-context handling
	gotImg, err := c.EmbedImages(nil, nil, api.EmbedDocument)
	require.NoError(t, err)
	assert.Empty(t, gotImg)
	assert.Zero(t, hits.Load(), "empty batch must not send HTTP POST")

	//nolint:staticcheck // verifying defensive nil-context handling
	got, err = c.Embed(nil, []string{"one"}, api.EmbedDocument)
	require.NoError(t, err)
	require.Len(t, got, 1)

	bad := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@api.openai.com/v1\x7f",
			Model:   "text-embedding-3-small",
			Query:   map[string]string{"api_key": "sk-openai-embed-secret"},
		},
	})
	//nolint:staticcheck // verifying defensive nil-context handling
	_, err = bad.Embed(nil, []string{"one"}, api.EmbedDocument)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-openai-embed-secret")
}
