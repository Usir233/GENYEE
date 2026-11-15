package sqlite

import (
	"context"
	"math"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestW4SQLiteAudit_CopyIndicesEnforcesSourceKBAndMappedKnowledge(t *testing.T) {
	ctx := context.Background()
	repo := newSQLiteRetrieverTestRepository(t)

	// Seed one chunk in kb-src (mapped), one chunk in kb-src with unmapped knowledge,
	// and one chunk in kb-foreign-tenant with a colliding/targeted chunk ID.
	own := sqliteTestIndex("chunk-own", "kb-src", "know-own", "tag-1", true)
	own.Content = "authorized content"
	unmapped := sqliteTestIndex("chunk-unmapped", "kb-src", "know-unmapped", "", true)
	unmapped.Content = "unmapped document"
	foreign := sqliteTestIndex("chunk-foreign", "kb-foreign", "know-own", "tag-f", true)
	foreign.Content = "foreign tenant secret"

	saveSQLiteTestVector(t, repo, own, []float32{1, 0})
	saveSQLiteTestVector(t, repo, unmapped, []float32{0.8, 0.6})
	saveSQLiteTestVector(t, repo, foreign, []float32{0, 1})

	require.NoError(t, repo.CopyIndices(
		ctx,
		"kb-src",
		map[string]string{"know-own": "know-dst"},
		map[string]string{
			"chunk-own":      "dst-own",
			"chunk-unmapped": "dst-unmapped",
			"chunk-foreign":  "dst-foreign",
		},
		"kb-dst",
		2,
		"",
	))

	res, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0},
		TopK:             10,
		KnowledgeBaseIDs: []string{"kb-dst"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Len(t, res[0].Results, 1, "only chunks in kb-src with mapped knowledge_id may be copied")
	assert.Equal(t, "dst-own", res[0].Results[0].ChunkID)
	assert.Equal(t, "know-dst", res[0].Results[0].KnowledgeID)

	// Empty source/target KB IDs must be rejected on CopyIndices and MoveKnowledgeIndices.
	require.Error(t, repo.CopyIndices(ctx, "", map[string]string{"know-own": "know-dst"}, map[string]string{"chunk-own": "x"}, "kb-dst", 2, ""))
	require.Error(t, repo.MoveKnowledgeIndices(ctx, "", "kb-dst", "know-own", nil, 2, ""))
}

func TestW4SQLiteAudit_FTS5ControlCharNaNVectorsFailClosedAndExclusions(t *testing.T) {
	ctx := context.Background()
	repo := newSQLiteRetrieverTestRepository(t)

	// Control characters including NUL (\x00) must never survive into FTS5 MATCH literals.
	sanitized := sanitizeFTS5Query("alpha\x00beta\x07NEAR(a b) content:secret \" * OR")
	assert.NotContains(t, sanitized, "\x00")
	assert.NotContains(t, sanitized, "\x07")
	assert.Equal(t, `content : ("alpha" OR "beta" OR "NEAR" OR "a" OR "b" OR "content" OR "secret" OR "OR")`, sanitized)
	assert.Equal(t, "", sanitizeFTS5Query("\x00\x01\x1f\x7f"))

	docA := sqliteTestIndex("chunk-a", "kb-1", "doc-a", "tag-1", true)
	docA.Content = "alpha beta"
	docB := sqliteTestIndex("chunk-b", "kb-1", "doc-b", "tag-1", true)
	docB.Content = "alpha gamma"
	otherTenant := sqliteTestIndex("chunk-other", "kb-other-tenant", "doc-other", "", true)
	otherTenant.Content = "alpha secret"

	saveSQLiteTestVector(t, repo, docA, []float32{1, 0})
	saveSQLiteTestVector(t, repo, docB, []float32{0.9, 0.1})
	saveSQLiteTestVector(t, repo, otherTenant, []float32{1, 0})

	// Live FTS5 check when built with -tags sqlite_fts5: NUL bytes and FTS operators must not error.
	if repo.db.Migrator().HasTable("lite_embeddings_fts") {
		kwRes, err := repo.Retrieve(ctx, types.RetrieveParams{
			Query:            "alpha\x00beta content:chunk-other NEAR \" *",
			TopK:             10,
			KnowledgeBaseIDs: []string{"kb-1"},
			RetrieverType:    types.KeywordsRetrieverType,
		})
		require.NoError(t, err)
		require.Len(t, kwRes, 1)
		for _, h := range kwRes[0].Results {
			assert.Equal(t, "kb-1", h.KnowledgeBaseID)
		}
	}

	// Explicit empty KnowledgeBaseIDs ([]string{}) MUST fail closed and never return other tenants' chunks.
	emptyKBVec, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0},
		TopK:             10,
		KnowledgeBaseIDs: []string{},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, emptyKBVec, 1)
	assert.Empty(t, emptyKBVec[0].Results)

	// TopK <= 0 and empty Embedding must return empty without sqlite-vec k<=0 error.
	zeroTopK, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0},
		TopK:             0,
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, zeroTopK, 1)
	assert.Empty(t, zeroTopK[0].Results)

	negTopK, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0},
		TopK:             -3,
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, negTopK, 1)
	assert.Empty(t, negTopK[0].Results)

	// ExcludeKnowledgeIDs and ExcludeChunkIDs must filter before TopK.
	exclKnow, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:           []float32{1, 0},
		TopK:                1,
		KnowledgeBaseIDs:    []string{"kb-1"},
		ExcludeKnowledgeIDs: []string{"doc-a"},
		RetrieverType:       types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, exclKnow, 1)
	require.Len(t, exclKnow[0].Results, 1)
	assert.Equal(t, "chunk-b", exclKnow[0].Results[0].ChunkID)

	exclChunk, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0},
		TopK:             10,
		KnowledgeBaseIDs: []string{"kb-1"},
		ExcludeChunkIDs:  []string{"chunk-a", "chunk-b"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, exclChunk, 1)
	assert.Empty(t, exclChunk[0].Results)

	// NaN / Inf embeddings must be rejected on Save, BatchSave, and Retrieve so NaN never bypasses Threshold.
	nanDoc := sqliteTestIndex("chunk-nan", "kb-1", "doc-nan", "", true)
	err = repo.Save(ctx, nanDoc, map[string]any{
		"embedding": map[string][]float32{nanDoc.SourceID: {float32(math.NaN()), 1.0}},
	})
	require.Error(t, err)

	_, err = repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{float32(math.NaN()), 1.0},
		TopK:             5,
		Threshold:        0.9,
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.Error(t, err)

	// Nil IndexInfo guards.
	require.Error(t, repo.Save(ctx, nil, nil))
	require.Error(t, repo.BatchSave(ctx, []*types.IndexInfo{nil}, nil))
	assert.Equal(t, int64(0), repo.EstimateStorageSize(ctx, []*types.IndexInfo{nil}, nil))
}
