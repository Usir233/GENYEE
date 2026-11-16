package openaitranscriptions

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPathMaxFileBytesAndNilContextSecretRedaction(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		_, _ = w.Write([]byte(`{"text":"hello","duration":-3.5}`))
	}))
	defer srv.Close()

	// Empty Settings.Path must default to /audio/transcriptions rather than posting to BaseURL root.
	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: srv.URL + "/v1", Model: "whisper-1"},
		Settings: api.TranscriptionsSettings{MaxFileBytes: 16},
	})

	_, err := c.Transcribe(nil, api.TranscriptionRequest{Audio: nil, FileName: "a.wav"})
	require.Error(t, err)

	_, err = c.Transcribe(nil, api.TranscriptionRequest{Audio: make([]byte, 32), FileName: "a.wav"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")

	out, err := c.Transcribe(nil, api.TranscriptionRequest{Audio: []byte("RIFF"), FileName: "a.wav"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/audio/transcriptions", seenPath)
	assert.Equal(t, 0.0, out.Duration, "negative duration must be sanitized to 0")

	bad := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@api.openai.com/v1\x7f",
			Model:   "whisper-1",
			Query:   map[string]string{"api_key": "sk-transcribe-secret-999"},
		},
	})
	_, err = bad.Transcribe(nil, api.TranscriptionRequest{Audio: []byte("RIFF"), FileName: "a.wav"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-transcribe-secret-999")
}
