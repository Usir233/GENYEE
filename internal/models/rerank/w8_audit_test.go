package rerank

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

type staticInnerReranker struct {
	results []api.RerankResult
}

func (s *staticInnerReranker) Rerank(_ context.Context, _ string, _ []string) ([]api.RerankResult, error) {
	out := make([]api.RerankResult, len(s.results))
	copy(out, s.results)
	return out, nil
}

func (s *staticInnerReranker) AcceptsImages() bool { return true }

func (s *staticInnerReranker) RerankImages(_ context.Context, _ string, _ []api.EmbedImage) ([]api.RerankResult, error) {
	out := make([]api.RerankResult, len(s.results))
	copy(out, s.results)
	return out, nil
}

func TestProtocolRerankerNilContextAndRejectsNonFiniteScores(t *testing.T) {
	for name, badScore := range map[string]float64{
		"NaN":  math.NaN(),
		"+Inf": math.Inf(1),
		"-Inf": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			inner := &staticInnerReranker{
				results: []api.RerankResult{
					{Index: 0, Score: 0.5},
					{Index: 1, Score: badScore},
				},
			}
			r := &protocolReranker{
				inner:     inner,
				images:    inner,
				settings:  api.RerankSettings{ImageInput: api.ImageInput{ImageField: "image", MaxImageBatchSize: 4}},
				modelName: "rerank-v1",
			}
			_, err := r.Rerank(nil, "q", []string{"a", "b"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "non-finite")

			_, err = r.RerankImages(nil, "q", []Image{
				{Data: []byte{1}, MIMEType: "image/png"},
				{Data: []byte{2}, MIMEType: "image/png"},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "non-finite")
		})
	}

	// Valid scores with nil ctx must succeed cleanly without panicking.
	okInner := &staticInnerReranker{results: []api.RerankResult{{Index: 0, Score: 0.7}}}
	r := &protocolReranker{
		inner:     okInner,
		images:    okInner,
		settings:  api.RerankSettings{ImageInput: api.ImageInput{ImageField: "image"}},
		modelName: "rerank-v1",
	}
	got, err := r.Rerank(nil, "q", []string{"a"})
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestConfigFromModelMapIsolationAndDebugRedaction(t *testing.T) {
	row := &types.Model{
		ID:   "r1",
		Name: "bge-reranker",
		Parameters: types.ModelParameters{
			ExtraConfig:   map[string]string{"secret_key": "keep"},
			CustomHeaders: map[string]string{"X-Header": "orig"},
		},
	}
	cfg := ConfigFromModel(row, "sec")
	cfg.ExtraConfig["secret_key"] = "mutated"
	cfg.CustomHeaders["X-Header"] = "mutated"
	assert.Equal(t, "keep", row.Parameters.ExtraConfig["secret_key"])
	assert.Equal(t, "orig", row.Parameters.CustomHeaders["X-Header"])

	line := buildRerankRequestDebug(
		"bge-reranker",
		"https://admin:supersecretpw@rerank.example.com/v1?api_key=sk-rerank-secret-999",
		"query",
		[]string{"doc"},
	)
	assert.NotContains(t, line, "supersecretpw")
	assert.NotContains(t, line, "sk-rerank-secret-999")

	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	err := validateRerankBaseURL("https://admin:supersecretpw@127.0.0.1:8080\x7f?token=sk-rerank-secret-999")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-rerank-secret-999")

	_, err = newVolcengineClient(nil, nil)
	require.Error(t, err)
}
