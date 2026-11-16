package vlm

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func vlmBombHeader(width, height uint32) []byte {
	buf := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d,
		'I', 'H', 'D', 'R',
		byte(width >> 24), byte(width >> 16), byte(width >> 8), byte(width),
		byte(height >> 24), byte(height >> 16), byte(height >> 8), byte(height),
		0x08, 0x06, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	crc := crc32.ChecksumIEEE(buf[12:29])
	binary.BigEndian.PutUint32(buf[29:33], crc)
	return buf
}

func TestNilConfigConstructorsReturnErrorInsteadOfPanicking(t *testing.T) {
	_, err := NewVLM(nil, nil)
	require.Error(t, err)

	_, err = NewRemoteAPIVLM(nil)
	require.Error(t, err)

	_, err = NewOllamaVLM(nil, nil)
	require.Error(t, err)
}

func TestConfigFromModelClonesCustomHeadersWhilePreservingSpecPointer(t *testing.T) {
	spec := &types.ModelSpecOverride{API: "openai-completions"}
	row := &types.Model{
		ID:   "vlm-1",
		Name: "gpt-4o",
		Parameters: types.ModelParameters{
			Spec:          spec,
			CustomHeaders: map[string]string{"X-Custom": "orig"},
		},
	}
	cfg := ConfigFromModel(row)
	assert.Same(t, spec, cfg.Spec)
	cfg.CustomHeaders["X-Custom"] = "mutated"
	assert.Equal(t, "orig", row.Parameters.CustomHeaders["X-Custom"])
}

func TestRemoteAPIVLMGuardsImageBoundsNilContextAndNonFiniteTemperature(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"caption"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	v, err := NewRemoteAPIVLM(&Config{
		Source:    types.ModelSourceRemote,
		Provider:  "openai",
		BaseURL:   srv.URL + "/v1",
		ModelName: "gpt-4o",
		APIKey:    "sk-test",
		Extra:     map[string]any{"temperature": "NaN"},
	})
	require.NoError(t, err)
	assert.InDelta(t, defaultTemp, v.temperature, 1e-9, "NaN temperature must fall back to defaultTemp")

	// Nil ctx with small valid stub succeeds cleanly without panicking.
	out, err := v.Predict(nil, [][]byte{bytes.Repeat([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, 3)}, "describe")
	require.NoError(t, err)
	assert.Equal(t, "caption", out)

	// Pixel bomb header rejected before base64 allocation / network call.
	_, err = v.Predict(nil, [][]byte{vlmBombHeader(10_000, 10_000)}, "describe")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceed maximum")

	// Single image > 20 MiB rejected before base64 encoding.
	_, err = v.Predict(nil, [][]byte{make([]byte, (20<<20)+1)}, "describe")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")

	// validateVLMBaseURL redacts userinfo and query secrets.
	secutils.ResetSSRFWhitelistForTest()
	err = validateVLMBaseURL("https://user:supersecretpw@127.0.0.1:8080\x7f?api_key=sk-vlm-secret-999")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-vlm-secret-999")
}
