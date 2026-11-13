package retriever

import (
	"context"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/repository/retriever/postgres"
	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

type estimateEmbedder struct {
	embedding.Embedder
	dimensions int
}

func (e *estimateEmbedder) GetDimensions() int { return e.dimensions }

func TestEstimateStorageSizeCountsEverySourceVector(t *testing.T) {
	// The production PostgreSQL estimator is pure: it needs no database or
	// embedding API. Halfvec storage plus its index estimate is 6 bytes per
	// dimension, in addition to text bytes and 200 bytes of row metadata.
	engine := NewKVHybridRetrieveEngine(postgres.NewPostgresRetrieveEngineRepository(nil),
		types.PostgresRetrieverEngineType)
	for _, tc := range []struct {
		name    string
		sources []string
	}{
		{name: "chunk", sources: []string{"chunk"}},
		{name: "generated question", sources: []string{"chunk-question"}},
		{name: "FAQ similar questions", sources: []string{"chunk", "chunk-q1", "chunk-q2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]*types.IndexInfo, len(tc.sources))
			var textAndMetadata int64
			for i, source := range tc.sources {
				rows[i] = &types.IndexInfo{SourceID: source, ChunkID: "chunk", Content: "问题"}
				textAndMetadata += int64(len(rows[i].Content)) + 200
			}
			for _, dimensions := range []int{2, 768} {
				model := &estimateEmbedder{dimensions: dimensions}
				want := textAndMetadata + int64(len(rows)*dimensions*6)
				require.Equal(t, want, engine.EstimateStorageSize(context.Background(), model, rows,
					[]types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}),
					"every source must contribute a %d-dimensional vector", dimensions)
			}
			// Keyword-only indexing has no model or vector storage.
			require.Equal(t, textAndMetadata, engine.EstimateStorageSize(context.Background(), nil, rows,
				[]types.RetrieverType{types.KeywordsRetrieverType}))
		})
	}
}
