package asr

import (
	"context"
	"math"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTranscriber struct {
	duration float64
}

func (s stubTranscriber) Transcribe(_ context.Context, _ api.TranscriptionRequest) (*api.Transcription, error) {
	return &api.Transcription{Text: "ok", Duration: s.duration}, nil
}

func TestWithLanguageNilContextAndConfigFromModelMapIsolation(t *testing.T) {
	ctx := WithLanguage(nil, "zh")
	require.NotNil(t, ctx)
	assert.Equal(t, "zh", languageFrom(ctx))
	assert.Equal(t, "", languageFrom(nil))

	row := &types.Model{
		ID:   "m1",
		Name: "whisper-1",
		Parameters: types.ModelParameters{
			ExtraConfig:   map[string]string{"region": "us"},
			CustomHeaders: map[string]string{"X-Test": "1"},
		},
	}
	cfg := ConfigFromModel(row)
	cfg.ExtraConfig["region"] = "mutated"
	cfg.CustomHeaders["X-Test"] = "mutated"
	assert.Equal(t, "us", row.Parameters.ExtraConfig["region"])
	assert.Equal(t, "1", row.Parameters.CustomHeaders["X-Test"])
}

func TestValidateASRBaseURLRedactsSecretsAndSanitizesDuration(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	err := validateASRBaseURL("https://user:supersecretpw@127.0.0.1:8080/v1\x7f?api_key=sk-asr-secret-999")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-asr-secret-999")

	p := &protocolASR{
		inner:     stubTranscriber{duration: math.Inf(1)},
		vendor:    "Generic",
		modelName: "whisper-1",
	}
	res, err := p.Transcribe(nil, []byte("RIFF"), "sample.wav")
	require.NoError(t, err)
	assert.Equal(t, 0.0, res.Duration, "non-finite vendor duration must be sanitized to 0")
}
