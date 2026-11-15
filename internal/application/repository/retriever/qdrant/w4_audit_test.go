package qdrant

import (
	"context"
	"math"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestW4QdrantAudit_NegativeAndZeroTopKNoUnderflowOrPanic(t *testing.T) {
	ctx := context.Background()
	var rpcCalls int
	qClient := newInterceptedQdrantClient(t, func(
		_ context.Context, method string, _, _ any, _ *grpc.ClientConn,
		_ grpc.UnaryInvoker, _ ...grpc.CallOption,
	) error {
		rpcCalls++
		t.Errorf("unexpected Qdrant RPC %s for non-positive TopK", method)
		return nil
	})
	repo := &qdrantRepository{client: qClient, collectionBaseName: "genyee_embeddings"}
	repo.initializedCollections.Store(2, true)

	for _, topK := range []int{-10, -1, 0} {
		vecRes, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
			Embedding:        []float32{0.5, 0.5},
			TopK:             topK,
			KnowledgeBaseIDs: []string{"kb-1"},
		})
		require.NoError(t, err)
		require.Len(t, vecRes, 1)
		assert.Empty(t, vecRes[0].Results)

		kwRes, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
			Query:            "revenue",
			TopK:             topK,
			KnowledgeBaseIDs: []string{"kb-1"},
		})
		require.NoError(t, err)
		require.Len(t, kwRes, 1)
		assert.Empty(t, kwRes[0].Results)
	}
	assert.Equal(t, 0, rpcCalls)
}

func TestW4QdrantAudit_FailClosedScopeNaNVectorsGeneratedQuestionAndMoveGuards(t *testing.T) {
	ctx := context.Background()

	var queryCount, scrollCount int
	qClient := newInterceptedQdrantClient(t, func(
		_ context.Context, _ string, req, reply any, _ *grpc.ClientConn,
		_ grpc.UnaryInvoker, _ ...grpc.CallOption,
	) error {
		switch r := req.(type) {
		case *qdrant.ListCollectionsRequest:
			resp := reply.(*qdrant.ListCollectionsResponse)
			resp.Collections = []*qdrant.CollectionDescription{{Name: "genyee_embeddings_2"}}
			return nil
		case *qdrant.CollectionExistsRequest:
			resp := reply.(*qdrant.CollectionExistsResponse)
			resp.Result = &qdrant.CollectionExists{Exists: true}
			return nil
		case *qdrant.QueryPoints:
			queryCount++
			resp := reply.(*qdrant.QueryResponse)
			resp.Result = []*qdrant.ScoredPoint{
				{
					Id:    &qdrant.PointId{PointIdOptions: &qdrant.PointId_Num{Num: 42}},
					Score: 0.95,
					Payload: newQdrantValueMap(map[string]any{
						fieldChunkID:         "chunk-1",
						fieldKnowledgeID:     "doc-1",
						fieldKnowledgeBaseID: "kb-off",
						fieldSourceID:        "chunk-1-q1",
						fieldSourceType:      0,
						fieldContent:         "generated question?",
						fieldIsEnabled:       true,
					}),
				},
				{
					Id:    &qdrant.PointId{PointIdOptions: &qdrant.PointId_Num{Num: 43}},
					Score: 0.85,
					Payload: newQdrantValueMap(map[string]any{
						fieldChunkID:         "chunk-1",
						fieldKnowledgeID:     "doc-1",
						fieldKnowledgeBaseID: "kb-off",
						fieldSourceID:        "chunk-1",
						fieldSourceType:      0,
						fieldContent:         "body chunk",
						fieldIsEnabled:       true,
					}),
				},
			}
			return nil
		case *qdrant.ScrollPoints:
			scrollCount++
			resp := reply.(*qdrant.ScrollResponse)
			resp.Result = []*qdrant.RetrievedPoint{
				{
					Id: &qdrant.PointId{PointIdOptions: &qdrant.PointId_Num{Num: 99}},
					Payload: newQdrantValueMap(map[string]any{
						fieldChunkID:         "chunk-1",
						fieldKnowledgeID:     "doc-1",
						fieldKnowledgeBaseID: "kb-off",
						fieldSourceID:        "chunk-1-q1",
						fieldSourceType:      0,
						fieldContent:         "body question?",
						fieldIsEnabled:       true,
					}),
				},
				{
					Id: &qdrant.PointId{PointIdOptions: &qdrant.PointId_Num{Num: 100}},
					Payload: newQdrantValueMap(map[string]any{
						fieldChunkID:         "chunk-1",
						fieldKnowledgeID:     "doc-1",
						fieldKnowledgeBaseID: "kb-off",
						fieldSourceID:        "chunk-1",
						fieldSourceType:      0,
						fieldContent:         "body answer",
						fieldIsEnabled:       true,
					}),
				},
			}
			return nil
		case *qdrant.SetPayloadPoints:
			if r.GetCollectionName() == "genyee_embeddings_99" {
				return status.Error(codes.NotFound, "Collection genyee_embeddings_99 doesn't exist!")
			}
			return nil
		default:
			return nil
		}
	})

	repo := &qdrantRepository{client: qClient, collectionBaseName: "genyee_embeddings"}
	repo.initializedCollections.Store(2, true)

	// Explicit empty KnowledgeBaseIDs ([]string{} or []string{""}) must fail closed with 0 RPCs.
	vecEmptyKB, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{0.5, 0.5},
		TopK:             5,
		KnowledgeBaseIDs: []string{},
	})
	require.NoError(t, err)
	assert.Empty(t, vecEmptyKB[0].Results)
	assert.Equal(t, 0, queryCount)

	kwEmptyKB, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query:            "body",
		TopK:             5,
		KnowledgeBaseIDs: []string{""},
	})
	require.NoError(t, err)
	assert.Empty(t, kwEmptyKB[0].Results)
	assert.Equal(t, 0, scrollCount)

	// Empty embedding and blank keyword query must short-circuit cleanly.
	vecNoEmb, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        nil,
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb-off"},
	})
	require.NoError(t, err)
	assert.Empty(t, vecNoEmb[0].Results)
	assert.Equal(t, 0, queryCount)

	kwBlank, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query:            "   ",
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb-off"},
	})
	require.NoError(t, err)
	assert.Empty(t, kwBlank[0].Results)
	assert.Equal(t, 0, scrollCount)

	// NaN / Inf embeddings and nil IndexInfo must be rejected cleanly without panicking.
	_, err = repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{float32(math.NaN()), 1.0},
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb-off"},
	})
	require.Error(t, err)
	require.Error(t, repo.Save(ctx, nil, nil))
	require.Error(t, repo.Save(ctx, &types.IndexInfo{ChunkID: "c1", SourceID: "c1"}, map[string]any{
		"embedding": map[string][]float32{"c1": {float32(math.Inf(1)), 0}},
	}))
	require.Error(t, repo.BatchSave(ctx, []*types.IndexInfo{nil}, nil))
	assert.Equal(t, int64(0), repo.EstimateStorageSize(ctx, []*types.IndexInfo{nil}, nil))

	// ExcludeGeneratedQuestionKBIDs must drop generated-question points before TopK,
	// and numeric PointId values must be preserved on returned hits.
	vecFiltered, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:                     []float32{0.5, 0.5},
		TopK:                          1,
		KnowledgeBaseIDs:              []string{"kb-off"},
		ExcludeGeneratedQuestionKBIDs: []string{"kb-off"},
	})
	require.NoError(t, err)
	require.Len(t, vecFiltered[0].Results, 1)
	assert.Equal(t, "chunk-1", vecFiltered[0].Results[0].SourceID)
	assert.Equal(t, "43", vecFiltered[0].Results[0].ID)

	kwFiltered, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query:                         "body",
		TopK:                          1,
		KnowledgeBaseIDs:              []string{"kb-off"},
		ExcludeGeneratedQuestionKBIDs: []string{"kb-off"},
	})
	require.NoError(t, err)
	require.Len(t, kwFiltered[0].Results, 1)
	assert.Equal(t, "chunk-1", kwFiltered[0].Results[0].SourceID)
	assert.Equal(t, "100", kwFiltered[0].Results[0].ID)

	// MoveKnowledgeIndices must reject empty scope arguments and treat missing collection as a no-op.
	require.Error(t, repo.MoveKnowledgeIndices(ctx, "", "kb-dst", "doc-1", nil, 2, ""))
	require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "kb-dst", "doc-1", nil, 0, ""))
	repo.initializedCollections.Store(99, true)
	require.NoError(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "kb-dst", "doc-1", nil, 99, ""))
	_, stillCached := repo.initializedCollections.Load(99)
	assert.False(t, stillCached, "missing collection during MoveKnowledgeIndices must clear stale initialized cache")
}
