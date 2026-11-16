package googlegenai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatRedactsQueryKeyAndUserinfoInRequestCreationError(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@generativelanguage.googleapis.com/v1beta\x7f",
			Model:   "gemini-2.5-flash",
			Query:   map[string]string{"key": "AIzaSySecretKey99999"},
		},
	})
	_, err := c.Chat(nil, []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "AIzaSySecretKey99999")
	assert.NotContains(t, err.Error(), "supersecretpw")

	_, err = c.ChatStream(nil, []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "AIzaSySecretKey99999")
	assert.NotContains(t, err.Error(), "supersecretpw")
}

func TestRequestURLEscapesModelNameAndNormalizesPrefix(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	var seenPath, seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: srv.URL + "/v1beta",
			Model:   "models/gemini-2.5-flash?key=injected#frag",
		},
	})
	resp, err := c.Chat(nil, []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	assert.Empty(t, seenQuery, "model name must not inject query parameters")
	assert.NotContains(t, seenPath, "models/models/")
}

func TestImagePartRejectsUnsafeSchemesAndOversizedDataURI(t *testing.T) {
	for _, unsafeRef := range []string{
		"",
		"file:///etc/passwd",
		"resource://missing-chunk",
		"local://../secret.png",
		"minio://bucket/key.png",
		"javascript:alert(1)",
	} {
		assert.Nil(t, imagePart(unsafeRef), "unsafe ref %q must be dropped", unsafeRef)
	}

	// Safe remote/GCS URIs remain allowed.
	gsPart := imagePart("gs://my-bucket/diagram.png")
	require.NotNil(t, gsPart)
	assert.Equal(t, "gs://my-bucket/diagram.png", gsPart["fileData"].(map[string]any)["fileUri"])

	httpsPart := imagePart("https://example.com/diagram.png")
	require.NotNil(t, httpsPart)
	assert.Equal(t, "https://example.com/diagram.png", httpsPart["fileData"].(map[string]any)["fileUri"])

	// Inline data URI exceeding 20 MiB decoded budget must be dropped rather than decoded/sent.
	hugePayload := strings.Repeat("A", ((20<<20)/3+4)*4)
	assert.Nil(t, imagePart("data:image/png;base64,"+hugePayload))
}
