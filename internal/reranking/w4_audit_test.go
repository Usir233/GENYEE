package reranking

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/imageprep"
	"github.com/Usir233/GENYEE/internal/models/rerank"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubUntrustedReranker struct {
	textResults  []rerank.RankResult
	imageResults []rerank.RankResult
	acceptsImg   bool
}

func (s *stubUntrustedReranker) GetModelName() string { return "stub-untrusted" }
func (s *stubUntrustedReranker) GetModelID() string   { return "model-untrusted" }
func (s *stubUntrustedReranker) Rerank(_ context.Context, _ string, _ []string) ([]rerank.RankResult, error) {
	return s.textResults, nil
}
func (s *stubUntrustedReranker) AcceptsImages() bool { return s.acceptsImg }
func (s *stubUntrustedReranker) ImageLimits() imageprep.Limits {
	return imageprep.Limits{}
}
func (s *stubUntrustedReranker) RerankImages(_ context.Context, _ string, _ []rerank.Image) ([]rerank.RankResult, error) {
	return s.imageResults, nil
}

func tinyPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestRerankHandlesDuplicateIndicesNonFiniteScoresAndAllImageSorting(t *testing.T) {
	ctx := context.Background()

	// 1. Duplicate provider indices must never duplicate candidates in Results/Indices.
	candidates := []*types.SearchResult{
		{ID: "c0", Content: "alpha passage", Score: 0.5},
		{ID: "c1", Content: "beta passage", Score: 0.4},
	}
	dupModel := &stubUntrustedReranker{
		textResults: []rerank.RankResult{
			{Index: 1, RelevanceScore: 0.9},
			{Index: 1, RelevanceScore: 0.8},
			{Index: 0, RelevanceScore: 0.7},
		},
	}
	dupRes := Rerank(ctx, dupModel, "query", candidates, Options{Threshold: 0.3})
	require.Len(t, dupRes.Results, 2, "duplicate provider index must not duplicate output rows")
	assert.Equal(t, []int{1, 0}, dupRes.Indices)

	// 2. Non-finite RelevanceScore (NaN / Inf) from custom Reranker must trigger graceful
	// model-error fallback rather than writing NaN into Metadata or CompositeScore.
	for _, badScore := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		nanModel := &stubUntrustedReranker{
			textResults: []rerank.RankResult{{Index: 0, RelevanceScore: badScore}},
		}
		nanRes := Rerank(ctx, nanModel, "query", candidates, Options{Threshold: 0.3})
		assert.Equal(t, types.RerankOutcomeModelError, nanRes.Diagnostics.Outcome)
		for _, r := range nanRes.Results {
			assert.False(t, math.IsNaN(r.Score) || math.IsInf(r.Score, 0))
		}
	}

	// 3. All-image batch (len(textPassages) == 0) with unsorted image scores must still
	// produce sorted ModelScores and deduplicate image indices.
	pngData := tinyPNGBytes(t)
	imgInputs := []*types.SearchResult{
		imageHit("img0", "first diagram", 0.4, 0),
		imageHit("img1", "second diagram", 0.5, 0),
	}
	imgModel := &stubUntrustedReranker{
		acceptsImg: true,
		imageResults: []rerank.RankResult{
			{Index: 0, RelevanceScore: 0.42},
			{Index: 1, RelevanceScore: 0.91},
			{Index: 1, RelevanceScore: 0.50}, // duplicate image index
		},
	}
	imgRes := Rerank(ctx, imgModel, "query", imgInputs, Options{
		Threshold: 0.3,
		LoadImage: func(_ context.Context, _ *types.SearchResult) ([]byte, error) {
			return pngData, nil
		},
	})
	require.Len(t, imgRes.ModelScores, 2)
	assert.Equal(t, 1, imgRes.ModelScores[0].Index)
	assert.InDelta(t, 0.91, imgRes.ModelScores[0].RelevanceScore, 1e-6)
	require.Len(t, imgRes.Results, 2)

	// 4. CompositeScore must guard nil SearchResult and non-finite inputs without returning NaN.
	comp := CompositeScore(nil, math.NaN(), math.Inf(1))
	assert.False(t, math.IsNaN(comp) || math.IsInf(comp, 0))
	assert.GreaterOrEqual(t, comp, 0.0)
	assert.LessOrEqual(t, comp, 1.0)
}

func TestRerankNilGuardsMMRNumericsAndObservabilityEmptyIDs(t *testing.T) {
	ctx := context.Background()

	// 1. Nil model in Rerank must degrade cleanly via RerankOutcomeModelError instead of panicking.
	in := []*types.SearchResult{{ID: "c1", Content: "valid content", Score: 0.6}}
	resNilModel := Rerank(ctx, nil, "query", in, Options{Threshold: 0.3})
	assert.Equal(t, types.RerankOutcomeModelError, resNilModel.Diagnostics.Outcome)
	require.Len(t, resNilModel.Results, 1)

	// 2. EnrichedPassage and ModelPassage must return "" on nil *types.SearchResult without panicking.
	assert.Equal(t, "", EnrichedPassage(ctx, nil))
	assert.Equal(t, "", ModelPassage(ctx, nil))

	// 3. SelectMMR must not panic on nil elements and must not produce NaN comparisons when
	// Score is -Inf and lambda == 0 (0 * -Inf = NaN in IEEE-754).
	mmrInput := []*types.SearchResult{
		nil,
		{ID: "a", Content: "first distinct document alpha beta", Score: math.Inf(-1)},
		{ID: "b", Content: "second distinct document gamma delta", Score: 0.8},
	}
	picksZeroLambda := SelectMMR(ctx, mmrInput, 2, 0.0)
	require.Equal(t, []int{1, 2}, picksZeroLambda, "nil entry must be skipped and 0*-Inf must not yield NaN")
	picksDefault := SelectMMR(ctx, mmrInput, 2, DefaultMMRLambda)
	require.Equal(t, []int{2, 1}, picksDefault, "finite positive score must beat -Inf while skipping nil")

	// 4. summarizeSelection must not collide on empty/duplicate IDs when attributing top_k_mmr_cut
	// vs rerank_rejected, and must tolerate nil entries in Result.Results.
	emptyIDInputs := []*types.SearchResult{
		imageHit("", "high relevance image", 0.4, 0),
		imageHit("", "low relevance image", 0.2, 0),
	}
	opts := Options{Threshold: 0.5, TopK: 1}
	scorer := &textScorer{scores: map[string]float64{
		"high relevance image": 0.9,
		"low relevance image":  0.1,
	}}
	emptyIDRes := Rerank(ctx, scorer, "q", emptyIDInputs, opts)
	diag := summarizeSelection(emptyIDInputs, emptyIDRes, opts, nil)
	assert.Equal(t, map[string]int{
		"kept":            1,
		"rerank_rejected": 1,
	}, diag["image_decision_counts"])
}
