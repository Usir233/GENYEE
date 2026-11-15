package chatpipeline

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/event"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterHistoryResults_EnforcesCurrentTurnScopeAndDoesNotMutateHistory(t *testing.T) {
	ctx := context.Background()
	matchingBody := "refund policy: customers may request a refund within seven days of delivery. " +
		strings.Repeat("operational detail about warehouses and shipping partners. ", 20)

	allowedOrig := &types.SearchResult{
		ID:              "allowed-kb-ref",
		KnowledgeBaseID: "kb-allowed",
		KnowledgeID:     "doc-1",
		Content:         matchingBody,
		Score:           0.8,
		MatchType:       types.MatchTypeEmbedding,
	}

	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			WebSearchEnabled: false,
			SearchTargets: types.SearchTargets{
				{
					Type:            types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID: "kb-allowed",
				},
				{
					Type:            types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID: "kb-tagged",
					TagIDs:          []string{"tag-restricted"},
				},
			},
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "refund within seven days delivery",
			History: []*types.History{{
				KnowledgeReferences: []*types.SearchResult{
					{
						ID:              "revoked-kb-ref",
						KnowledgeBaseID: "kb-revoked",
						KnowledgeID:     "doc-revoked",
						Content:         matchingBody,
						Score:           0.95,
						MatchType:       types.MatchTypeEmbedding,
					},
					{
						ID:              "tagged-kb-ref",
						KnowledgeBaseID: "kb-tagged",
						KnowledgeID:     "doc-untagged",
						Content:         matchingBody,
						Score:           0.92,
						MatchType:       types.MatchTypeEmbedding,
					},
					{
						ID:              "web-ref",
						KnowledgeSource: "web_search",
						Content:         matchingBody,
						Score:           0.90,
						MatchType:       types.MatchTypeWebSearch,
					},
					allowedOrig,
				},
			}},
		},
	}

	got := filterHistoryResults(ctx, cm, nil)
	require.Len(t, got, 1, "only in-scope KB reference should survive current-turn scope enforcement")
	assert.Equal(t, "allowed-kb-ref", got[0].ID)
	assert.Equal(t, types.MatchTypeHistory, got[0].MatchType)

	// Stored History entries must NOT be mutated in place.
	assert.Equal(t, types.MatchTypeEmbedding, allowedOrig.MatchType, "history entry MatchType must not be mutated")
	assert.InDelta(t, 0.8, allowedOrig.Score, 1e-9, "history entry Score must not be discounted in place")
	assert.Nil(t, allowedOrig.Metadata, "history entry Metadata must not be mutated in place")
}

func TestAttachCitationSources_DoesNotCrossLinkTopLevelChunksWithEmptyParentID(t *testing.T) {
	c1 := &types.Chunk{
		ID:              "c1",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Shared phrase.",
		ParentChunkID:   "",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		ChunkIndex:      0,
	}
	c2 := &types.Chunk{
		ID:              "c2",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Shared phrase.",
		ParentChunkID:   "",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		ChunkIndex:      1,
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"c1": c1,
			"c2": c2,
		},
	}
	p := &PluginMerge{chunkRepo: repo}
	r1 := &types.SearchResult{
		ID:               "c1",
		KnowledgeID:      "doc",
		KnowledgeBaseID:  "kb",
		Content:          "Shared phrase. Extra context around c1.",
		ContentRewritten: true,
	}
	r2 := &types.SearchResult{
		ID:               "c2",
		KnowledgeID:      "doc",
		KnowledgeBaseID:  "kb",
		Content:          "Unrelated section around c2.",
		ContentRewritten: true,
	}

	p.attachCitationSources(context.Background(), []*types.SearchResult{r1, r2})
	require.Len(t, r1.CitationSources, 1, "top-level chunks with empty ParentChunkID must not be linked as siblings")
	assert.Equal(t, "c1", r1.CitationSources[0].ID)
}

type panicOnAnswerEventBus struct {
	mu     sync.Mutex
	errors []event.ErrorData
}

func (b *panicOnAnswerEventBus) On(types.EventType, types.EventHandler) {}

func (b *panicOnAnswerEventBus) Emit(_ context.Context, evt types.Event) error {
	if evt.Type == types.EventType(event.EventAgentFinalAnswer) {
		panic("boom in final answer consumer")
	}
	if evt.Type == types.EventType(event.EventError) {
		b.mu.Lock()
		if data, ok := evt.Data.(event.ErrorData); ok {
			b.errors = append(b.errors, data)
		}
		b.mu.Unlock()
	}
	return nil
}

func (b *panicOnAnswerEventBus) errorCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.errors)
}

type panicWebClient struct{}

func (panicWebClient) Search(context.Context, types.WebSearchRequest) ([]*types.WebSearchResult, error) {
	return nil, nil
}

func (panicWebClient) Fetch(context.Context, string) (*types.WebPage, error) {
	panic("fetcher boom")
}

func (panicWebClient) Capabilities() types.WebSearchCapabilities {
	return types.WebSearchCapabilities{}
}

type panicWebSearchService struct {
	returnNilClient bool
}

func (s panicWebSearchService) Resolve(context.Context, string) (interfaces.WebClient, error) {
	if s.returnNilClient {
		return nil, nil
	}
	return panicWebClient{}, nil
}

func TestPipelineGoroutinePanicIsolation_RunParallelStreamAndWebFetch(t *testing.T) {
	t.Run("RunParallel recovers panicking task into PluginError", func(t *testing.T) {
		siblingRan := false
		var errs map[string]*PluginError
		require.NotPanics(t, func() {
			errs = RunParallel(
				ParallelTask{
					Name: "panicky",
					Run: func() *PluginError {
						panic("parallel task panic")
					},
				},
				ParallelTask{
					Name: "healthy",
					Run: func() *PluginError {
						siblingRan = true
						return nil
					},
				},
			)
		})
		assert.True(t, siblingRan)
		require.Contains(t, errs, "panicky")
		assert.NotNil(t, errs["panicky"])
	})

	t.Run("PluginChatCompletionStream recovers consumer goroutine panic", func(t *testing.T) {
		bus := &panicOnAnswerEventBus{}
		model := &openStreamChat{
			chunks: []types.StreamResponse{
				{ResponseType: types.ResponseTypeAnswer, Content: "hello", Done: true, FinishReason: "stop"},
			},
			closeStream: true,
		}
		cm := &types.ChatManage{}
		cm.SessionID = "sess-panic"
		cm.EventBus = bus

		plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
		require.Nil(t, plugin.OnEvent(context.Background(), types.CHAT_COMPLETION_STREAM, cm, func() *PluginError {
			return nil
		}))

		require.Eventually(t, func() bool {
			return bus.errorCount() == 1
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("PluginWebFetch recovers panicking page fetch goroutine", func(t *testing.T) {
		plugin := &PluginWebFetch{webSearch: panicWebSearchService{}}
		cm := &types.ChatManage{}
		cm.WebSearchEnabled = true
		cm.WebFetchEnabled = true
		cm.WebSearchProviderID = "prov-1"
		cm.RerankResult = []*types.SearchResult{
			{ID: "https://example.com/a", KnowledgeSource: "web_search", Content: "original snippet"},
		}
		nextCalled := false
		require.NotPanics(t, func() {
			err := plugin.OnEvent(context.Background(), types.WEB_FETCH, cm, func() *PluginError {
				nextCalled = true
				return nil
			})
			assert.Nil(t, err)
		})
		assert.True(t, nextCalled)
		assert.Equal(t, "original snippet", cm.RerankResult[0].Content)
	})
}

type wikiBoostKBStub struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s wikiBoostKBStub) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return s.kbs[id], nil
}

func TestPluginWikiBoost_NilServiceSafetyAndScopedToWikiEnabledKB(t *testing.T) {
	ctx := context.Background()

	t.Run("nil kbService does not panic", func(t *testing.T) {
		p := &PluginWikiBoost{kbService: nil}
		cm := &types.ChatManage{
			PipelineRequest: types.PipelineRequest{
				SearchTargets: types.SearchTargets{
					{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-wiki"},
				},
			},
			PipelineState: types.PipelineState{
				RerankResult: []*types.SearchResult{
					{ID: "w1", KnowledgeBaseID: "kb-wiki", ChunkType: types.ChunkTypeWikiPage, Score: 0.5},
				},
			},
		}
		require.NotPanics(t, func() {
			assert.Nil(t, p.OnEvent(ctx, types.CHUNK_RERANK, cm, func() *PluginError { return nil }))
		})
	})

	t.Run("only wiki-enabled KB chunks receive boost", func(t *testing.T) {
		p := &PluginWikiBoost{
			kbService: wikiBoostKBStub{
				kbs: map[string]*types.KnowledgeBase{
					"kb-wiki": {
						ID:               "kb-wiki",
						Type:             types.KnowledgeBaseTypeWiki,
						IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
					},
					"kb-doc": {ID: "kb-doc", Type: types.KnowledgeBaseTypeDocument},
				},
			},
		}
		wikiChunk := &types.SearchResult{
			ID:              "w-real",
			KnowledgeBaseID: "kb-wiki",
			ChunkType:       types.ChunkTypeWikiPage,
			Score:           0.5,
		}
		nonWikiChunk := &types.SearchResult{
			ID:              "w-spoof",
			KnowledgeBaseID: "kb-doc",
			ChunkType:       types.ChunkTypeWikiPage,
			Score:           0.5,
		}
		cm := &types.ChatManage{
			PipelineRequest: types.PipelineRequest{
				SearchTargets: types.SearchTargets{
					{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-wiki"},
					{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-doc"},
				},
			},
			PipelineState: types.PipelineState{
				RerankResult: []*types.SearchResult{nonWikiChunk, wikiChunk},
			},
		}

		require.Nil(t, p.OnEvent(ctx, types.CHUNK_RERANK, cm, func() *PluginError { return nil }))
		assert.InDelta(t, 0.65, wikiChunk.Score, 1e-9, "wiki-enabled KB chunk should be boosted 1.3x")
		assert.InDelta(t, 0.50, nonWikiChunk.Score, 1e-9, "non-wiki KB chunk must not be boosted")
	})
}
