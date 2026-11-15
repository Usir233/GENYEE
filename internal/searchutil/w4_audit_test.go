package searchutil

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeKeywordScoresAndClampFloatRejectNonFinite(t *testing.T) {
	type item struct {
		kw    bool
		score float64
	}

	// Case 1: +Inf mixed with finite scores must not yield Inf/Inf = NaN or collapse finite scores.
	items := []*item{
		{kw: true, score: 2.0},
		{kw: true, score: 6.0},
		{kw: true, score: math.Inf(1)},
		{kw: true, score: math.NaN()},
		{kw: true, score: math.Inf(-1)},
	}
	NormalizeKeywordScores(
		items,
		func(it *item) bool { return it.kw },
		func(it *item) float64 { return it.score },
		func(it *item, s float64) { it.score = s },
		KeywordScoreCallbacks{},
	)
	for i, it := range items {
		assert.False(t, math.IsNaN(it.score) || math.IsInf(it.score, 0),
			"item %d has non-finite normalized score %v", i, it.score)
		assert.GreaterOrEqual(t, it.score, 0.0)
		assert.LessOrEqual(t, it.score, 1.0)
	}
	assert.Greater(t, items[1].score, items[0].score, "finite relative ordering must be preserved")
	assert.Equal(t, 1.0, items[2].score, "+Inf should clamp to 1.0")
	assert.Equal(t, 0.0, items[3].score, "NaN should sanitize to 0.0")
	assert.Equal(t, 0.0, items[4].score, "-Inf should clamp to 0.0")

	// Case 2: Large batch (len >= 20) containing NaN must never emit NaN.
	large := make([]*item, 25)
	for i := range large {
		large[i] = &item{kw: true, score: float64(i + 1)}
	}
	large[0].score = math.NaN()
	large[12].score = math.NaN()
	NormalizeKeywordScores(
		large,
		func(it *item) bool { return it.kw },
		func(it *item) float64 { return it.score },
		func(it *item, s float64) { it.score = s },
		KeywordScoreCallbacks{},
	)
	for i, it := range large {
		assert.False(t, math.IsNaN(it.score) || math.IsInf(it.score, 0),
			"large[%d] has non-finite normalized score %v", i, it.score)
	}

	// ClampFloat must sanitize NaN and handle inverted bounds cleanly.
	assert.Equal(t, 0.0, ClampFloat(math.NaN(), 0.0, 1.0))
	assert.Equal(t, 0.5, ClampFloat(0.5, 1.0, 0.0))
}

func TestMergeTextChunksAndImageInfoNilSafety(t *testing.T) {
	chunks := []*types.Chunk{
		nil,
		{ID: "c2", Content: "world", StartAt: 6, EndAt: 11, ChunkIndex: 1},
		nil,
		{ID: "c1", Content: "hello", StartAt: 0, EndAt: 5, ChunkIndex: 0},
	}
	merged := MergeTextChunks(chunks, " ")
	assert.Equal(t, "hello world", merged)

	// Nil *types.ImageInfo and nil repo / slice entries must never panic.
	assert.Equal(t, "", BuildImageInfoXML(nil))
	assert.Equal(t, "", BuildImageInfoXMLWithURL("https://example.com/a.png", nil))

	results := []*types.SearchResult{
		nil,
		{ID: "r1", Content: "text"},
	}
	require.NotPanics(t, func() {
		EnrichSearchResultsImageInfo(context.Background(), nil, 1, results)
		EnrichSearchResultsImageInfoOnly(context.Background(), nil, results)
		_ = CollectImageInfoByChunkIDs(context.Background(), nil, 1, []string{"r1"})
		_ = CollectImageInfoByChunkIDsOnly(context.Background(), nil, []string{"r1"})
	})

	// Nil child/grandchild chunks returned by listChildren must not panic.
	infoMap, _ := collectChildEvidence([]string{"p1"}, func(_ []string) ([]*types.Chunk, error) {
		return []*types.Chunk{
			nil,
			{
				ID:            "child1",
				ParentChunkID: "p1",
				IsEnabled:     true,
				ChunkType:     types.ChunkTypeImageCaption,
				ImageInfo:     `[{"url":"https://example.com/x.png","caption":"diagram"}]`,
			},
		}, nil
	})
	assert.Contains(t, infoMap["p1"], "diagram")
}

func TestExtractSnippetBoundedWindowAndUTF8Safety(t *testing.T) {
	prefix := strings.Repeat("前缀中文内容", 500) // 3000 runes
	match := "目标关键词" + strings.Repeat("匹配扩展", 40) // > 100 runes
	suffix := strings.Repeat("后缀中文内容", 500)
	doc := prefix + match + suffix

	got := ExtractSnippet(doc, "目标关键词(?:匹配扩展)+")
	require.NotEmpty(t, got)
	assert.True(t, utf8.ValidString(got))
	assert.Contains(t, got, "目标关键词")
	// Context window: "... " + <=60 before + 100+"..." match + <=60 after + " ..."
	assert.LessOrEqual(t, utf8.RuneCountInString(got), 240)
}

func TestTokenizeSimpleConcurrentRaceSafety(t *testing.T) {
	inputs := []string{
		"企业级知识库检索系统支持混合搜索与语义重排",
		"Hybrid search combines BM25 keyword and dense vector recall",
		"多模态图文检索 OCR 与标题摘要融合测试 2026",
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				toks := TokenizeSimple(inputs[(id+i)%len(inputs)])
				assert.NotEmpty(t, toks)
			}
		}(g)
	}
	wg.Wait()
}
