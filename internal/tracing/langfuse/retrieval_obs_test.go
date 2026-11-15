package langfuse

import (
	"fmt"
	"math"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
)

func TestSummarizeRetrieveOutput_countsAndPreviews(t *testing.T) {
	out := SummarizeRetrieveOutput([]*types.RetrieveResult{
		{
			RetrieverEngineType: types.PostgresRetrieverEngineType,
			RetrieverType:       types.VectorRetrieverType,
			Results: []*types.IndexWithScore{
				{ChunkID: "c1", KnowledgeID: "k1", Score: 0.9, Content: "alpha"},
				{ChunkID: "c2", KnowledgeID: "k1", Score: 0.5, Content: "beta"},
			},
		},
		{
			RetrieverEngineType: types.PostgresRetrieverEngineType,
			RetrieverType:       types.KeywordsRetrieverType,
			Results: []*types.IndexWithScore{
				{ChunkID: "c3", KnowledgeID: "k2", Score: 0.7, Content: "gamma"},
			},
		},
	})

	if out["total_hits"] != 3 {
		t.Fatalf("total_hits = %v, want 3", out["total_hits"])
	}
	if out["vector_hits"] != 2 || out["keyword_hits"] != 1 {
		t.Fatalf("vector/keyword hits = %v/%v", out["vector_hits"], out["keyword_hits"])
	}
	hits := out["top_hits"].([]map[string]interface{})
	if len(hits) != 3 || hits[0]["chunk_id"] != "c1" {
		t.Fatalf("unexpected top_hits: %#v", hits)
	}
	if hits[0]["retriever"] != string(types.VectorRetrieverType) ||
		hits[1]["retriever"] != string(types.KeywordsRetrieverType) {
		t.Fatalf("top_hits should retain both retriever groups: %#v", hits)
	}
	if out["top_hits_strategy"] != "round_robin_by_retriever" {
		t.Fatalf("unexpected top_hits strategy: %v", out["top_hits_strategy"])
	}
}

func TestSummarizeRetrieveOutput_doesNotLetOneScoreScaleHideAnotherRetriever(t *testing.T) {
	vector := make([]*types.IndexWithScore, 0, 5)
	keyword := make([]*types.IndexWithScore, 0, 5)
	for i := 0; i < 5; i++ {
		vector = append(vector, &types.IndexWithScore{
			ChunkID:   fmt.Sprintf("vector-%d", i),
			Score:     0.9 - float64(i)*0.1,
			MatchType: types.MatchTypeEmbedding,
		})
		keyword = append(keyword, &types.IndexWithScore{
			ChunkID:   fmt.Sprintf("keyword-%d", i),
			Score:     1.0,
			MatchType: types.MatchTypeKeywords,
		})
	}

	out := SummarizeRetrieveOutput([]*types.RetrieveResult{
		{RetrieverType: types.VectorRetrieverType, Results: vector},
		{RetrieverType: types.KeywordsRetrieverType, Results: keyword},
	})
	hits := out["top_hits"].([]map[string]interface{})
	seen := map[string]bool{}
	for _, hit := range hits {
		seen[hit["retriever"].(string)] = true
	}
	if !seen[string(types.VectorRetrieverType)] || !seen[string(types.KeywordsRetrieverType)] {
		t.Fatalf("top_hits lost a retriever group: %#v", hits)
	}

	groups := out["top_hits_by_retriever"].([]map[string]interface{})
	if len(groups) != 2 {
		t.Fatalf("top_hits_by_retriever groups = %d, want 2", len(groups))
	}
}

func TestSummarizeSearchResults_sortsByScore(t *testing.T) {
	out := SummarizeSearchResults([]*types.SearchResult{
		{ID: "low", Score: 0.2, Content: "low"},
		{ID: "high", Score: 0.9, Content: "high"},
	}, 10)
	hits := out["top_hits"].([]map[string]interface{})
	if len(hits) != 2 || hits[0]["chunk_id"] != "high" {
		t.Fatalf("unexpected hits: %#v", hits)
	}
}

func TestRetrievalPreviewsPreserveImageSourceAndRawVectorScore(t *testing.T) {
	out := SummarizeRetrieveOutput([]*types.RetrieveResult{{
		RetrieverType: types.VectorRetrieverType,
		Results: []*types.IndexWithScore{{
			ChunkID: "image", SourceType: types.ImageSourceType, Score: 0.7, VectorScore: 0.2688,
		}},
	}})
	hit := out["top_hits"].([]map[string]interface{})[0]
	if hit["source_type"] != types.ImageSourceType || hit["vector_score"] != "0.2688" || hit["score"] != "0.7000" {
		t.Fatalf("image identity/raw score lost: %#v", hit)
	}
	groups := out["top_hits_by_retriever"].([]map[string]interface{})
	groupHit := groups[0]["top_hits"].([]map[string]interface{})[0]
	if groupHit["source_type"] != types.ImageSourceType || groupHit["vector_score"] != "0.2688" {
		t.Fatalf("per-retriever image identity/raw score lost: %#v", groupHit)
	}
}

func TestRetrievalObsHandlesNilElementsAndNaNScores(t *testing.T) {
	retOut := SummarizeRetrieveOutput([]*types.RetrieveResult{
		nil,
		{
			RetrieverEngineType: types.PostgresRetrieverEngineType,
			RetrieverType:       types.VectorRetrieverType,
			Results: []*types.IndexWithScore{
				nil,
				{ChunkID: "nan-hit", Score: math.NaN(), Content: "nan"},
				{ChunkID: "good-hit", Score: 0.85, Content: "good"},
			},
		},
	})
	if retOut["total_hits"] != 2 {
		t.Fatalf("expected total_hits=2 excluding nil hit, got %v", retOut["total_hits"])
	}
	retHits := retOut["top_hits"].([]map[string]interface{})
	if len(retHits) != 2 || retHits[0]["chunk_id"] != "good-hit" {
		t.Fatalf("expected finite score ahead of NaN, got %#v", retHits)
	}

	searchOut := SummarizeSearchResults([]*types.SearchResult{
		nil,
		{ID: "nan-1", Score: math.NaN(), Content: "n1"},
		{ID: "top-1", Score: 0.91, Content: "t1"},
		{ID: "low-1", Score: 0.12, Content: "l1"},
		nil,
	}, 10)
	if searchOut["count"] != 3 {
		t.Fatalf("expected count=3 non-nil search results, got %v", searchOut["count"])
	}
	topHits := searchOut["top_hits"].([]map[string]interface{})
	if len(topHits) != 3 || topHits[0]["chunk_id"] != "top-1" || topHits[1]["chunk_id"] != "low-1" || topHits[2]["chunk_id"] != "nan-1" {
		t.Fatalf("unexpected NaN/nil search ordering: %#v", topHits)
	}

	previews := SummarizePassagePreviews([]*types.SearchResult{
		nil,
		{ID: "c2", Score: 0.5},
	}, []string{"first passage", "second passage"}, 5)
	if len(previews) != 2 || previews[1]["chunk_id"] != "c2" {
		t.Fatalf("unexpected passage previews with nil candidate: %#v", previews)
	}
}
