package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopyIndicesRollsBackRowAndFTSWhenVectorCopyMissing(t *testing.T) {
	repo := newSQLiteRetrieverTestRepository(t)
	ctx := context.Background()

	// Seed a source row claiming Dimension=4 WITHOUT inserting into the vec0 table,
	// so copyVec fails after Create(&newRow) and syncFTS5Insert(&newRow).
	enabled := true
	srcRow := sqliteEmbedding{
		SourceID:        "chunk-src",
		SourceType:      0,
		ChunkID:         "chunk-src",
		KnowledgeID:     "kg-src",
		KnowledgeBaseID: "kb-src",
		Content:         "orphan rollback test content",
		Dimension:       4,
		IsEnabled:       &enabled,
	}
	require.NoError(t, repo.db.WithContext(ctx).Create(&srcRow).Error)
	require.NoError(t, repo.ensureVecTable(4))

	err := repo.CopyIndices(
		ctx,
		"kb-src",
		map[string]string{"kg-src": "kg-dst"},
		map[string]string{"chunk-src": "chunk-dst"},
		"kb-dst",
		4,
		"document",
	)
	require.Error(t, err)

	var targetRows int64
	require.NoError(t, repo.db.Model(&sqliteEmbedding{}).
		Where("knowledge_base_id = ?", "kb-dst").
		Count(&targetRows).Error)
	require.Zero(t, targetRows, "failed CopyIndices must roll back half-indexed lite_embeddings row")

	if !repo.ftsUnavailable {
		var ftsRows int64
		require.NoError(t, repo.db.Raw(
			"SELECT COUNT(*) FROM lite_embeddings_fts WHERE content MATCH ?", "orphan",
		).Scan(&ftsRows).Error)
		require.Zero(t, ftsRows, "failed CopyIndices must roll back half-indexed FTS5 row")
	}
}
