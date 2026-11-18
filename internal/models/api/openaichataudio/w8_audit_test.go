package openaichataudio

import (
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestBodyRejectsEmptyAudioAndChecksSizeBeforeEncoding(t *testing.T) {
	c := New(Config{
		Endpoint: api.Endpoint{Model: "qwen3-asr-flash"},
		Settings: api.TranscriptionsSettings{MaxEncodedBytes: 128},
	})

	_, err := c.BuildRequestBody(api.TranscriptionRequest{Audio: nil, FileName: "sample.wav"})
	require.Error(t, err)

	_, err = c.BuildRequestBody(api.TranscriptionRequest{Audio: make([]byte, 256), FileName: "sample.wav"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base64-encoded")
}

func TestTranscribeNilContextAndRedactsSecretsInRequestError(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@dashscope.aliyuncs.com/compatible-mode/v1\x7f",
			Model:   "qwen3-asr-flash",
			Query:   map[string]string{"api_key": "sk-audio-secret-999"},
		},
	})
	//nolint:staticcheck // verifying defensive nil-context handling
	_, err := c.Transcribe(nil, api.TranscriptionRequest{Audio: []byte("RIFF"), FileName: "sample.wav"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-audio-secret-999")
}
