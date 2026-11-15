package sqlite

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BatchUpdateChunkEnabledStatus / BatchUpdateChunkTagID only touch the index
// copy (lite_embeddings) that retrieval reads, while the authoritative chunks
// row is written by the service layer beforehand. Swallowing an UPDATE error
// therefore makes the call look successful while retrieval keeps filtering on
// the stale is_enabled / tag_id value, so the error has to reach the caller.

func TestBatchUpdateChunkEnabledStatusReturnsUpdateError(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	saveSQLiteTestVector(t, repository,
		sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true),
		[]float32{1, 0},
	)
	require.NoError(t, repository.db.Exec("DROP TABLE lite_embeddings").Error)

	err := repository.BatchUpdateChunkEnabledStatus(context.Background(), map[string]bool{"chunk-1": false})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk-1")
	assert.Contains(t, err.Error(), "is_enabled")
}

func TestBatchUpdateChunkTagIDReturnsUpdateError(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	saveSQLiteTestVector(t, repository,
		sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true),
		[]float32{1, 0},
	)
	require.NoError(t, repository.db.Exec("DROP TABLE lite_embeddings").Error)

	err := repository.BatchUpdateChunkTagID(context.Background(), map[string]string{"chunk-1": "tag-2"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk-1")
	assert.Contains(t, err.Error(), "tag_id")
}

// A statement that fails inside a batch must not be masked by the remaining
// statements of the same batch.
func TestBatchUpdateChunkTagIDReportsFailingStatementInBatch(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	for _, chunkID := range []string{"chunk-ok", "chunk-fail"} {
		saveSQLiteTestVector(t, repository,
			sqliteTestIndex(chunkID, "kb-1", "knowledge-1", "tag-1", true),
			[]float32{1, 0},
		)
	}
	require.NoError(t, repository.db.Exec(`CREATE TRIGGER fail_chunk_tag_update
		BEFORE UPDATE ON lite_embeddings
		WHEN OLD.chunk_id = 'chunk-fail'
		BEGIN SELECT RAISE(ABORT, 'simulated index write failure'); END`).Error)

	err := repository.BatchUpdateChunkTagID(context.Background(), map[string]string{
		"chunk-ok":   "tag-2",
		"chunk-fail": "tag-2",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk-fail")
	assert.Contains(t, err.Error(), "simulated index write failure")
}

// Every failing statement of a batch has to be reported, not just the one that
// happened to be visited first: stopping at the first failure leaves an
// arbitrary (map-iteration-order-dependent) prefix of the index copy updated
// while blaming a single chunk. Each collected error can only exist if its own
// UPDATE was attempted, so the count proves the whole batch was run.
func TestBatchUpdateChunkTagIDReportsEveryFailingStatementInBatch(t *testing.T) {
	const batchSize = 5
	repository := newSQLiteRetrieverTestRepository(t)
	chunkTagMap := make(map[string]string, batchSize)
	for i := 1; i <= batchSize; i++ {
		chunkID := fmt.Sprintf("chunk-%d", i)
		chunkTagMap[chunkID] = "tag-2"
		saveSQLiteTestVector(t, repository,
			sqliteTestIndex(chunkID, "kb-1", "knowledge-1", "tag-1", true),
			[]float32{1, 0},
		)
	}
	require.NoError(t, repository.db.Exec(`CREATE TRIGGER fail_chunk_tag_update
		BEFORE UPDATE ON lite_embeddings
		BEGIN SELECT RAISE(ABORT, 'simulated index write failure'); END`).Error)

	err := repository.BatchUpdateChunkTagID(context.Background(), chunkTagMap)

	require.Error(t, err)
	for i := 1; i <= batchSize; i++ {
		assert.Contains(t, err.Error(), fmt.Sprintf("chunk-%d", i))
	}
	assert.Equal(t, batchSize, strings.Count(err.Error(), "simulated index write failure"))
}

// A failing statement must not cut the batch short: the other engines (Milvus,
// Qdrant) accumulate the error and finish the batch, so the rows the batch was
// asked to update are still written. The assertions are on the resulting rows
// rather than on a position in the batch because map iteration order is random.
func TestBatchUpdateChunkTagIDKeepsUpdatingAfterStatementFailure(t *testing.T) {
	const batchSize = 5
	repository := newSQLiteRetrieverTestRepository(t)
	for i := 1; i <= batchSize; i++ {
		chunkID := fmt.Sprintf("chunk-%d", i)
		saveSQLiteTestVector(t, repository,
			sqliteTestIndex(chunkID, "kb-1", "knowledge-1", "tag-1", true),
			[]float32{1, 0},
		)
	}
	require.NoError(t, repository.db.Exec(`CREATE TRIGGER fail_chunk_tag_update
		BEFORE UPDATE ON lite_embeddings
		WHEN OLD.chunk_id = 'chunk-3'
		BEGIN SELECT RAISE(ABORT, 'simulated index write failure'); END`).Error)

	err := repository.BatchUpdateChunkTagID(context.Background(), map[string]string{
		"chunk-1": "tag-2",
		"chunk-2": "tag-2",
		"chunk-3": "tag-2",
		"chunk-4": "tag-2",
		"chunk-5": "tag-2",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk-3")
	assert.Contains(t, err.Error(), "simulated index write failure")

	var rows []sqliteEmbedding
	require.NoError(t, repository.db.Order("chunk_id").Find(&rows).Error)
	require.Len(t, rows, batchSize)
	for _, row := range rows {
		if row.ChunkID == "chunk-3" {
			assert.Equal(t, "tag-1", row.TagID, "the failing statement must not be applied")
			continue
		}
		assert.Equal(t, "tag-2", row.TagID, "chunk %s was not updated after the failure", row.ChunkID)
	}
}

// Matching no row is not an error -- the index copy may legitimately lag behind
// the authoritative chunks table -- but it used to be completely invisible, so
// both batch updates warn with the table and the chunk they were meant for.
func TestBatchUpdateChunkWarnsWhenStatementMatchesNoRow(t *testing.T) {
	testCases := []struct {
		name   string
		column string
		update func(repository *sqliteRepository, ctx context.Context) error
	}{
		{
			name:   "is_enabled",
			column: "is_enabled",
			update: func(repository *sqliteRepository, ctx context.Context) error {
				return repository.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{
					"chunk-1":       false,
					"chunk-missing": false,
				})
			},
		},
		{
			name:   "tag_id",
			column: "tag_id",
			update: func(repository *sqliteRepository, ctx context.Context) error {
				return repository.BatchUpdateChunkTagID(ctx, map[string]string{
					"chunk-1":       "tag-2",
					"chunk-missing": "tag-2",
				})
			},
		},
		{
			name:   "content",
			column: "content",
			update: func(repository *sqliteRepository, ctx context.Context) error {
				row := sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true)
				row.SourceID = "chunk-1"
				if err := repository.Save(ctx, row, map[string]any{
					"embedding": map[string][]float32{"chunk-1": {1, 0}},
				}); err != nil {
					return err
				}
				return repository.BatchUpdateChunkContent(ctx, map[string]string{
					"chunk-1":       "updated content",
					"chunk-missing": "updated content",
				})
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newSQLiteRetrieverTestRepository(t)
			saveSQLiteTestVector(t, repository,
				sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true),
				[]float32{1, 0},
			)

			var logs bytes.Buffer
			logger.SetOutput(&logs)
			t.Cleanup(func() { logger.SetOutput(os.Stdout) })

			require.NoError(t, testCase.update(repository, context.Background()))

			output := logs.String()
			t.Logf("captured log output:\n%s", output)
			assert.Contains(t, output, "lite_embeddings."+testCase.column)
			assert.Contains(t, output, "matched 0 rows for chunk chunk-missing")
			assert.NotContains(t, output, "matched 0 rows for chunk chunk-1")
		})
	}
}

// Happy path: the updates still apply and report success.
func TestBatchUpdateChunkStatusAndTagApply(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	saveSQLiteTestVector(t, repository,
		sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true),
		[]float32{1, 0},
	)

	require.NoError(t, repository.BatchUpdateChunkEnabledStatus(
		context.Background(), map[string]bool{"chunk-1": false}))
	require.NoError(t, repository.BatchUpdateChunkTagID(
		context.Background(), map[string]string{"chunk-1": "tag-2"}))

	var row sqliteEmbedding
	require.NoError(t, repository.db.Where("chunk_id = ?", "chunk-1").First(&row).Error)
	require.NotNil(t, row.IsEnabled)
	assert.False(t, *row.IsEnabled)
	assert.Equal(t, "tag-2", row.TagID)
}

func TestBatchUpdateChunkContentUpdatesPrimaryChunkAndFTSWithoutTouchingQuestionsOrVectors(t *testing.T) {
	ctx := context.Background()
	repository := newSQLiteRetrieverTestRepository(t)

	primary := sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true)
	primary.SourceID = "chunk-1"
	primary.Content = "Doc Title\nSee ![diagram](resource://oldhandleabc)"
	saveSQLiteTestVector(t, repository, primary, []float32{1, 0})

	question := sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true)
	question.SourceID = "chunk-1-q1"
	question.Content = "Doc Title\nHow does architecture diagram route traffic?"
	saveSQLiteTestVector(t, repository, question, []float32{0.6, 0.8})

	imageVec := sqliteTestIndex("imgvec-1", "kb-1", "knowledge-1", "tag-1", true)
	imageVec.SourceID = "imgvec-1"
	imageVec.Content = "architecture diagram caption"
	saveSQLiteTestVector(t, repository, imageVec, []float32{0, 1})

	updatedContent := "Doc Title\nSee ![diagram](resource://newhandlexyz)"
	require.NoError(t, repository.BatchUpdateChunkContent(ctx, map[string]string{
		"chunk-1": updatedContent,
	}))

	// Vector retrieval on [1, 0] returns updated primary content with its original vector intact.
	vecRes, err := repository.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0},
		TopK:             3,
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, vecRes, 1)
	require.NotEmpty(t, vecRes[0].Results)
	assert.Equal(t, "chunk-1", vecRes[0].Results[0].SourceID)
	assert.Equal(t, updatedContent, vecRes[0].Results[0].Content)
	assert.InDelta(t, 1.0, vecRes[0].Results[0].Score, 1e-5)

	// Old resource handle is removed from FTS5; new resource handle is searchable when FTS5 is enabled.
	if repository.db.Migrator().HasTable("lite_embeddings_fts") {
		oldKW, err := repository.Retrieve(ctx, types.RetrieveParams{
			Query:            "oldhandleabc",
			TopK:             5,
			KnowledgeBaseIDs: []string{"kb-1"},
			RetrieverType:    types.KeywordsRetrieverType,
		})
		require.NoError(t, err)
		require.Len(t, oldKW, 1)
		assert.Empty(t, oldKW[0].Results)

		newKW, err := repository.Retrieve(ctx, types.RetrieveParams{
			Query:            "newhandlexyz",
			TopK:             5,
			KnowledgeBaseIDs: []string{"kb-1"},
			RetrieverType:    types.KeywordsRetrieverType,
		})
		require.NoError(t, err)
		require.Len(t, newKW, 1)
		require.Len(t, newKW[0].Results, 1)
		assert.Equal(t, "chunk-1", newKW[0].Results[0].SourceID)
		assert.Equal(t, updatedContent, newKW[0].Results[0].Content)
	}

	// Generated question row sharing chunk_id="chunk-1" keeps its question content.
	var qRow sqliteEmbedding
	require.NoError(t, repository.db.Where("source_id = ?", "chunk-1-q1").First(&qRow).Error)
	assert.Equal(t, question.Content, qRow.Content)

	// Image vector row keeps its vector [0, 1] and caption untouched.
	imgRes, err := repository.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{0, 1},
		TopK:             1,
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, imgRes, 1)
	require.Len(t, imgRes[0].Results, 1)
	assert.Equal(t, "imgvec-1", imgRes[0].Results[0].SourceID)
	assert.Equal(t, imageVec.Content, imgRes[0].Results[0].Content)
	assert.InDelta(t, 1.0, imgRes[0].Results[0].Score, 1e-5)
}

func TestBatchUpdateChunkContentKeepsUpdatingAfterStatementFailure(t *testing.T) {
	ctx := context.Background()
	repository := newSQLiteRetrieverTestRepository(t)
	for _, id := range []string{"chunk-ok", "chunk-fail"} {
		row := sqliteTestIndex(id, "kb-1", "knowledge-1", "tag-1", true)
		row.SourceID = id
		row.Content = "initial-" + id
		saveSQLiteTestVector(t, repository, row, []float32{1, 0})
	}
	require.NoError(t, repository.db.Exec(`CREATE TRIGGER fail_chunk_content_update
		BEFORE UPDATE OF content ON lite_embeddings
		WHEN OLD.chunk_id = 'chunk-fail'
		BEGIN SELECT RAISE(ABORT, 'simulated content write failure'); END`).Error)

	err := repository.BatchUpdateChunkContent(ctx, map[string]string{
		"chunk-ok":   "rewritten-ok",
		"chunk-fail": "rewritten-fail",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk-fail")
	assert.Contains(t, err.Error(), "simulated content write failure")

	var okRow, failRow sqliteEmbedding
	require.NoError(t, repository.db.Where("source_id = ?", "chunk-ok").First(&okRow).Error)
	require.NoError(t, repository.db.Where("source_id = ?", "chunk-fail").First(&failRow).Error)
	assert.Equal(t, "rewritten-ok", okRow.Content)
	assert.Equal(t, "initial-chunk-fail", failRow.Content)
}
