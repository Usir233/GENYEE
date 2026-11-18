package service

import (
	"math"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestHookMetricPreservesRetrievalRanks(t *testing.T) {
	relevant := &types.SearchResult{Content: "relevant passage"}
	other := &types.SearchResult{Content: "unrelated result"}
	second := &types.SearchResult{Content: "second evidence"}
	idealTwo := 1 + 1/math.Log2(3)
	for _, tc := range []struct {
		name           string
		pids           []int
		passages       []string
		search, rerank []*types.SearchResult
		want           types.RetrievalMetrics
	}{
		{
			name: "irrelevant hit keeps rank and denominator", pids: []int{7}, passages: []string{relevant.Content},
			search: []*types.SearchResult{other, relevant},
			want: types.RetrievalMetrics{
				Precision: .5,
				Recall:    1,
				MRR:       .5,
				MAP:       .5,
				NDCG3:     1 / math.Log2(3),
				NDCG10:    1 / math.Log2(3),
			},
		},
		{
			name: "duplicate relevant hits keep rank without extra credit", pids: []int{7, 8}, passages: []string{
				relevant.Content,
				second.Content,
			},
			search: []*types.SearchResult{relevant, relevant, other, second},
			want: types.RetrievalMetrics{
				Precision: .5,
				Recall:    1,
				MRR:       1,
				MAP:       .75,
				NDCG3:     1 / idealTwo,
				NDCG10:    (1 + 1/math.Log2(5)) / idealTwo,
			},
		},
		{
			name: "duplicate ground truth IDs are one passage", pids: []int{7, 7}, passages: []string{
				relevant.Content,
				relevant.Content,
			},
			search: []*types.SearchResult{relevant, relevant},
			want:   types.RetrievalMetrics{Precision: .5, Recall: 1, MRR: 1, MAP: 1, NDCG3: 1, NDCG10: 1},
		},
		{
			name: "zero negative and maximum IDs cannot collide with misses", pids: []int{
				0,
				-1,
				int(^uint(0) >> 1),
			}, passages: []string{
				relevant.Content,
				second.Content,
				"third evidence",
			},
			search: []*types.SearchResult{other, relevant, second, {Content: "third evidence"}},
			want: types.RetrievalMetrics{
				Precision: .75,
				Recall:    1,
				MRR:       .5,
				MAP:       (.5 + 2.0/3 + .75) / 3,
				NDCG3:     (1/math.Log2(3) + .5) / (idealTwo + .5),
				NDCG10:    (1/math.Log2(3) + .5 + 1/math.Log2(5)) / (idealTwo + .5),
			},
		},
		{
			name: "nil and empty hits occupy rank slots", pids: []int{7}, passages: []string{relevant.Content},
			search: []*types.SearchResult{nil, {}, relevant},
			want: types.RetrievalMetrics{
				Precision: 1.0 / 3,
				Recall:    1,
				MRR:       1.0 / 3,
				MAP:       1.0 / 3,
				NDCG3:     .5,
				NDCG10:    .5,
			},
		},
		{
			name: "extra passage without ID is not relevant", pids: []int{7}, passages: []string{
				relevant.Content,
				second.Content,
			},
			search: []*types.SearchResult{second, relevant},
			want: types.RetrievalMetrics{
				Precision: .5,
				Recall:    1,
				MRR:       .5,
				MAP:       .5,
				NDCG3:     1 / math.Log2(3),
				NDCG10:    1 / math.Log2(3),
			},
		},
		{
			name:     "missing passage still counts as unretrieved ground truth",
			pids:     []int{7, 8},
			passages: []string{relevant.Content},
			search:   []*types.SearchResult{relevant},
			want: types.RetrievalMetrics{
				Precision: 1,
				Recall:    .5,
				MRR:       1,
				MAP:       .5,
				NDCG3:     1 / idealTwo,
				NDCG10:    1 / idealTwo,
			},
		},
		{
			name: "rerank order takes precedence", pids: []int{7}, passages: []string{relevant.Content},
			search: []*types.SearchResult{relevant, other}, rerank: []*types.SearchResult{other, relevant},
			want: types.RetrievalMetrics{
				Precision: .5,
				Recall:    1,
				MRR:       .5,
				MAP:       .5,
				NDCG3:     1 / math.Log2(3),
				NDCG10:    1 / math.Log2(3),
			},
		},
		{
			name:     "empty rerank rejects all candidates",
			pids:     []int{7},
			passages: []string{relevant.Content},
			search:   []*types.SearchResult{relevant},
			rerank:   []*types.SearchResult{},
		},
		{name: "nil search results", pids: []int{7}, passages: []string{relevant.Content}},
		{
			name:     "empty search results",
			pids:     []int{7},
			passages: []string{relevant.Content},
			search:   []*types.SearchResult{},
		},
		{
			name:     "empty relevant passage cannot match",
			pids:     []int{7},
			passages: []string{""},
			search:   []*types.SearchResult{relevant},
		},
		{name: "no ground truth", search: []*types.SearchResult{other}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := NewHookMetric(1)
			hook.recordInit(0)
			hook.recordQaPair(0, &types.QAPair{QID: 1, PIDs: tc.pids, Passages: tc.passages, Answer: "answer"})
			hook.recordSearchResult(0, tc.search)
			hook.recordRerankResult(0, tc.rerank)
			require.NotPanics(t, func() { hook.recordFinish(0) })
			got := hook.MetricResult().RetrievalMetrics
			t.Logf("retrieval metrics: %+v", got)
			require.InDelta(t, tc.want.Precision, got.Precision, 1e-9, "precision")
			require.InDelta(t, tc.want.Recall, got.Recall, 1e-9, "recall")
			require.InDelta(t, tc.want.MRR, got.MRR, 1e-9, "MRR")
			require.InDelta(t, tc.want.MAP, got.MAP, 1e-9, "MAP")
			require.InDelta(t, tc.want.NDCG3, got.NDCG3, 1e-9, "NDCG@3")
			require.InDelta(t, tc.want.NDCG10, got.NDCG10, 1e-9, "NDCG@10")
		})
	}
}
