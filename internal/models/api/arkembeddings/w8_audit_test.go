package arkembeddings

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbedNilContextAndRedactsSecretsInRequestError(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@ark.cn-beijing.volces.com\x7f",
			Model:   "doubao-embedding-vision",
			Query:   map[string]string{"api_key": "ark-secret-key-999"},
		},
	})
	//nolint:staticcheck // verifying defensive nil-context handling
	_, err := c.Embed(nil, []string{"hello"}, api.EmbedDocument)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "ark-secret-key-999")
}

func TestPostValidatesEmbeddingVectorFinity(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	// Overflow float64 -> +Inf when decoded into float32!
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"embedding":[0.1, 1e300],"object":"embedding"}}`))
	}))
	defer srv.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: srv.URL, Model: "doubao-embedding-vision"},
	})
	//nolint:staticcheck // verifying defensive nil-context handling
	_, err := c.Embed(nil, []string{"hello"}, api.EmbedDocument)
	require.Error(t, err)

	_, err = validateArkVector([]float32{0.1, float32(math.NaN())})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-finite")

	_, err = validateArkVector([]float32{float32(math.Inf(1)), 0.2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-finite")
}
