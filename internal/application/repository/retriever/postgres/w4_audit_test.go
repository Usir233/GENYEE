package postgres

import (
	"context"
	"errors"
	"math"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestW4PostgresAudit_RetrieveBoundsFailClosedNaNAndExclusions(t *testing.T) {
	ctx := context.Background()

	t.Run("negative_and_zero_topk_no_panic_or_sql", func(t *testing.T) {
		repo, _ := newKeywordTestRepository(t)
		for _, topK := range []int{-5, -1, 0} {
			res, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
				Embedding:        []float32{0.1, 0.2},
				TopK:             topK,
				KnowledgeBaseIDs: []string{"kb-1"},
			})
			require.NoError(t, err)
			require.Len(t, res, 1)
			assert.Empty(t, res[0].Results)

			kwRes, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
				Query:            "hello",
				TopK:             topK,
				KnowledgeBaseIDs: []string{"kb-1"},
			})
			require.NoError(t, err)
			require.Len(t, kwRes, 1)
			assert.Empty(t, kwRes[0].Results)
		}
	})

	t.Run("empty_kb_slice_fails_closed_and_blank_query_or_empty_embedding_skips_sql", func(t *testing.T) {
		repo, _ := newKeywordTestRepository(t)

		vecRes, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
			Embedding:        []float32{0.1, 0.2},
			TopK:             5,
			KnowledgeBaseIDs: []string{},
		})
		require.NoError(t, err)
		require.Len(t, vecRes, 1)
		assert.Empty(t, vecRes[0].Results)

		kwRes, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
			Query:            "secret",
			TopK:             5,
			KnowledgeBaseIDs: []string{""},
		})
		require.NoError(t, err)
		require.Len(t, kwRes, 1)
		assert.Empty(t, kwRes[0].Results)

		emptyEmb, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
			Embedding:        nil,
			TopK:             5,
			KnowledgeBaseIDs: []string{"kb-1"},
		})
		require.NoError(t, err)
		require.Len(t, emptyEmb, 1)
		assert.Empty(t, emptyEmb[0].Results)

		blankKW, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
			Query:            "   ",
			TopK:             5,
			KnowledgeBaseIDs: []string{"kb-1"},
		})
		require.NoError(t, err)
		require.Len(t, blankKW, 1)
		assert.Empty(t, blankKW[0].Results)
	})

	t.Run("nan_and_inf_vectors_rejected", func(t *testing.T) {
		repo, _ := newKeywordTestRepository(t)
		_, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
			Embedding:        []float32{float32(math.NaN()), 1.0},
			TopK:             5,
			KnowledgeBaseIDs: []string{"kb-1"},
		})
		require.Error(t, err)

		_, err = repo.VectorRetrieve(ctx, types.RetrieveParams{
			Embedding:        []float32{float32(math.Inf(-1)), 1.0},
			TopK:             5,
			KnowledgeBaseIDs: []string{"kb-1"},
		})
		require.Error(t, err)

		err = repo.Save(ctx, &types.IndexInfo{ChunkID: "c1", SourceID: "c1"}, map[string]any{
			"embedding": map[string][]float32{"c1": {float32(math.NaN()), 0.5}},
		})
		require.Error(t, err)
	})

	t.Run("exclude_knowledge_and_chunk_ids_enforced_in_keywords_and_vector_sql", func(t *testing.T) {
		repo, mock := newKeywordTestRepository(t)
		mock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL)).
			WillReturnRows(sqlmock.NewRows([]string{"score_function"}).AddRow("paradedb.score"))

		mock.ExpectQuery(`SELECT paradedb\.score\(id\) as score,.*"knowledge_id" <> \$2 AND "chunk_id" <> \$3.*`).
			WithArgs("kb-1", "doc-skip", "chunk-skip", "term", true, 5).
			WillReturnRows(sqlmock.NewRows([]string{"id", "chunk_id", "score"}))

		kwRes, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
			Query:               "term",
			TopK:                5,
			KnowledgeBaseIDs:    []string{"kb-1"},
			ExcludeKnowledgeIDs: []string{"doc-skip"},
			ExcludeChunkIDs:     []string{"chunk-skip"},
		})
		require.NoError(t, err)
		require.Len(t, kwRes, 1)

		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL hnsw.ef_search = 100").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("SET LOCAL hnsw.iterative_scan = strict_order").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(`SELECT .*FROM embeddings WHERE .*knowledge_id NOT IN \(\$4\) AND chunk_id NOT IN \(\$5\).*`).
			WithArgs(sqlmock.AnyArg(), 2, "kb-1", "doc-skip", "chunk-skip", true, 100, 1.0, 3).
			WillReturnRows(sqlmock.NewRows([]string{"id", "chunk_id", "score"}))
		mock.ExpectCommit()

		vecRes, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
			Embedding:           []float32{0.6, 0.8},
			TopK:                3,
			KnowledgeBaseIDs:    []string{"kb-1"},
			ExcludeKnowledgeIDs: []string{"doc-skip"},
			ExcludeChunkIDs:     []string{"chunk-skip"},
		})
		require.NoError(t, err)
		require.Len(t, vecRes, 1)
	})
}

func TestW4PostgresAudit_WriteDeleteMoveAndBatchUpdateGuards(t *testing.T) {
	ctx := context.Background()
	repo, mock := newKeywordTestRepository(t)

	// Empty BatchSave and empty DeleteBy* lists must be clean no-ops without issuing SQL.
	require.NoError(t, repo.BatchSave(ctx, nil, nil))
	require.NoError(t, repo.BatchSave(ctx, []*types.IndexInfo{}, nil))
	require.NoError(t, repo.DeleteByChunkIDList(ctx, nil, 0, ""))
	require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{}, 0, ""))
	require.NoError(t, repo.DeleteByKnowledgeIDList(ctx, nil, 0, ""))
	require.NoError(t, repo.DeleteByKnowledgeIDList(ctx, []string{}, 0, ""))

	// Nil IndexInfo entries must not panic.
	require.Error(t, repo.Save(ctx, nil, nil))
	require.Error(t, repo.BatchSave(ctx, []*types.IndexInfo{nil}, nil))
	assert.Equal(t, int64(0), repo.EstimateStorageSize(ctx, []*types.IndexInfo{nil}, nil))

	// MoveKnowledgeIndices and CopyIndices must guard empty KB/document scopes.
	require.Error(t, repo.MoveKnowledgeIndices(ctx, "", "kb-dst", "doc-1", nil, 4, ""))
	require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "", "doc-1", nil, 4, ""))
	require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "kb-dst", "", nil, 4, ""))
	require.Error(
		t,
		repo.CopyIndices(ctx, "", map[string]string{"k1": "k2"}, map[string]string{"c1": "c2"}, "kb-dst", 4, ""),
	)

	// BatchUpdateChunkEnabledStatus must execute inside a transaction and roll back on failure.
	boom := errors.New("db failure on second update")
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "embeddings" SET "is_enabled"=\$1,"updated_at"=\$2 WHERE chunk_id IN \(\$3\)`).
		WithArgs(true, sqlmock.AnyArg(), "c-on").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "embeddings" SET "is_enabled"=\$1,"updated_at"=\$2 WHERE chunk_id IN \(\$3\)`).
		WithArgs(false, sqlmock.AnyArg(), "c-off").
		WillReturnError(boom)
	mock.ExpectRollback()

	err := repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{
		"c-on":  true,
		"c-off": false,
	})
	require.ErrorIs(t, err, boom)
}
