package metric

import (
	"math"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNDCGMetric_Compute(t *testing.T) {
	for _, tc := range []struct {
		name string
		k    int
		gt   [][]int
		ids  []int
		want float64
	}{
		{"one of three relevant documents", 3, [][]int{{1, 2, 3}}, []int{1}, 0.46927872602275644},
		{"two of three relevant documents", 3, [][]int{{1, 2, 3}}, []int{1, 2}, 0.7653606369886217},
		{"ideal ranking respects cutoff", 1, [][]int{{1, 2, 3}}, []int{1}, 1},
		{"complete retrieval below cutoff", 3, [][]int{{1, 2}}, []int{1, 2}, 1},
		{"relevant result beyond cutoff", 2, [][]int{{1}}, []int{9, 8, 1}, 0},
		{"rank discount", 3, [][]int{{1}}, []int{9, 1}, 0.6309297535714574},
		{"distinct relevant documents", 3, [][]int{{1, 1}, {1}}, []int{1, 9, 8}, 1},
		{"no ground truth", 3, nil, []int{1}, 0},
		{"empty ground truth set", 3, [][]int{{}}, []int{1}, 0},
		{"empty retrieval", 3, [][]int{{1, 2}}, nil, 0},
		{"zero cutoff", 0, [][]int{{1}}, []int{1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &types.MetricInput{RetrievalGT: tc.gt, RetrievalIDs: tc.ids}
			require.InDelta(t, tc.want, NewNDCGMetric(tc.k).Compute(input), 1e-9)
		})
	}
}

func TestMetricsGuardNegativeCutoffEmptyReferenceUnknownRougeAndNilInput(t *testing.T) {
	input := &types.MetricInput{RetrievalGT: [][]int{{1, 2}}, RetrievalIDs: []int{1, 2}}
	require.Equal(t, 0.0, NewNDCGMetric(-1).Compute(input))
	require.Equal(t, 0.0, NewNDCGMetric(-100).Compute(input))

	// Nil MetricInput must never panic across any metric calculator.
	require.Equal(t, 0.0, NewNDCGMetric(3).Compute(nil))
	require.Equal(t, 0.0, NewMAPMetric().Compute(nil))
	require.Equal(t, 0.0, NewMRRMetric().Compute(nil))
	require.Equal(t, 0.0, NewPrecisionMetric().Compute(nil))
	require.Equal(t, 0.0, NewRecallMetric().Compute(nil))
	require.Equal(t, 0.0, NewBLEUMetric(true, BLEU4Gram).Compute(nil))
	require.Equal(t, 0.0, NewRougeMetric(true, "rouge-1", "f").Compute(nil))

	// Smoothed BLEU must return 0 when reference ground truth has zero tokens.
	require.Equal(t, 0.0, NewBLEUMetric(true, BLEU1Gram).Compute(&types.MetricInput{
		GeneratedTexts: "candidate answer",
		GeneratedGT:    "",
	}))
	require.Equal(t, 0.0, NewBLEUMetric(true, BLEU4Gram).Compute(&types.MetricInput{
		GeneratedTexts: "candidate answer",
		GeneratedGT:    "。。。",
	}))

	// Non-finite or negative BLEU weights must never emit NaN or Inf.
	badWeightScore := NewBLEUMetric(true, BLEUWeight{math.NaN(), math.Inf(1), -1}).Compute(&types.MetricInput{
		GeneratedTexts: "alpha beta",
		GeneratedGT:    "alpha beta",
	})
	require.False(t, math.IsNaN(badWeightScore) || math.IsInf(badWeightScore, 0))
	require.Equal(t, 0.0, badWeightScore)

	// ROUGE unknown metric returns 0 instead of nil-func panic; uppercase metric/stat normalizes cleanly.
	require.Equal(t, 0.0, NewRougeMetric(true, "rouge-99", "f").Compute(&types.MetricInput{
		GeneratedTexts: "alpha beta",
		GeneratedGT:    "alpha beta",
	}))
	upperRouge := NewRougeMetric(true, "ROUGE-1", "P").Compute(&types.MetricInput{
		GeneratedTexts: "alpha beta",
		GeneratedGT:    "alpha gamma",
	})
	require.InDelta(t, 0.5, upperRouge, 1e-9)
}
