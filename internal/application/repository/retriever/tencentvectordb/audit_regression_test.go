package tencentvectordb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseFilterDoubleQuotedLiterals extracts every double-quoted string token from
// cond while respecting backslash escapes, returning the unescaped contents and
// whatever remains outside string literals.
func stripEscapedFilterLiterals(t *testing.T, cond string) (outside string, literals []string) {
	t.Helper()
	var out strings.Builder
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(cond); i++ {
		ch := cond[i]
		if inQuote {
			if ch == '\\' {
				require.Less(t, i+1, len(cond), "dangling backslash in filter condition: %s", cond)
				cur.WriteByte(cond[i+1])
				i++
				continue
			}
			if ch == '"' {
				inQuote = false
				literals = append(literals, cur.String())
				cur.Reset()
				out.WriteString(`""`)
				continue
			}
			cur.WriteByte(ch)
		} else {
			if ch == '"' {
				inQuote = true
				continue
			}
			out.WriteByte(ch)
		}
	}
	require.False(t, inQuote, "unclosed string literal in filter condition: %s", cond)
	return out.String(), literals
}

func TestTencentVectorDBFilterEscapingAndEmptyScopeFailClosed(t *testing.T) {
	ctx := context.Background()
	repo := NewTencentVectorDBRetrieveEngineRepository(nil, "", nil).(*repository)

	t.Run("quotes and backslashes cannot break out of filter in/not-in clauses", func(t *testing.T) {
		injKB := `kb-1") or is_enabled=1 or knowledge_base_id in ("kb-2`
		injDoc := `doc-1\") or knowledge_id != ("doc-2`
		injChunk := `chk-1")` + "\n" + ` or 1=1`

		cond := repo.baseFilter(types.RetrieveParams{
			KnowledgeBaseIDs: []string{injKB},
			KnowledgeIDs:     []string{injDoc},
			ExcludeChunkIDs:  []string{injChunk},
		}).Cond()

		outside, literals := stripEscapedFilterLiterals(t, cond)
		assert.NotContains(t, strings.ToLower(outside), " or ",
			"injected 'or' clause must remain inside quoted string literal, got outside=%q full=%q", outside, cond)
		require.Len(t, literals, 3)
		assert.Equal(t, injKB, literals[0])
		assert.Equal(t, injDoc, literals[1])
		assert.NotContains(t, cond, "\n")

		copyParams, err := copySourceQueryParams(injKB, []string{injChunk}, 0)
		require.NoError(t, err)
		copyOutside, copyLiterals := stripEscapedFilterLiterals(t, copyParams.Filter.Cond())
		assert.NotContains(t, strings.ToLower(copyOutside), " or ")
		require.Len(t, copyLiterals, 2)
		assert.Equal(t, injKB, copyLiterals[0])
	})

	t.Run("Retrieve and baseFilter fail closed on empty or blank KB and Knowledge scope", func(t *testing.T) {
		for _, params := range []types.RetrieveParams{
			{RetrieverType: types.VectorRetrieverType, Embedding: []float32{0.1, 0.2}, TopK: 5},
			{
				RetrieverType:    types.VectorRetrieverType,
				Embedding:        []float32{0.1, 0.2},
				KnowledgeBaseIDs: []string{"", "  "},
				TopK:             5,
			},
			{
				RetrieverType:    types.VectorRetrieverType,
				Embedding:        []float32{0.1, 0.2},
				KnowledgeBaseIDs: []string{"kb-1"},
				TagIDs:           []string{"   "},
				TopK:             5,
			},
			{RetrieverType: types.KeywordsRetrieverType, Query: "离线交付", TopK: 5},
			{RetrieverType: types.KeywordsRetrieverType, Query: "离线交付", KnowledgeIDs: []string{"   "}, TopK: 5},
		} {
			res, err := repo.Retrieve(ctx, params)
			require.NoError(t, err)
			require.Len(t, res, 1)
			assert.Empty(t, res[0].Results, "empty/blank scope must fail closed without touching backend client")
		}

		emptyCond := repo.baseFilter(types.RetrieveParams{}).Cond()
		assert.Contains(t, emptyCond, "is_enabled=0", "direct baseFilter on empty scope must emit unsatisfiable guard")
	})

	t.Run("Delete CopyIndices and MoveKnowledgeIndices guard empty targets and invalid dimensions", func(t *testing.T) {
		// Blank-only delete slices are safe no-ops rather than `in ("")`
		require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{"", "   "}, 1024, ""))
		require.NoError(t, repo.DeleteBySourceIDList(ctx, []string{"  "}, 1024, ""))
		require.NoError(t, repo.DeleteByKnowledgeIDList(ctx, []string{""}, 1024, ""))

		// Non-empty delete with invalid dimension or nil client fails closed cleanly (never panics)
		require.Error(t, repo.DeleteByChunkIDList(ctx, []string{"c1"}, 0, ""))
		require.Error(t, repo.DeleteByChunkIDList(ctx, []string{"c1"}, 1024, ""))

		// CopyIndices and MoveKnowledgeIndices fail closed on empty KB / knowledge IDs
		_, err := copySourceQueryParams("", []string{"c1"}, 0)
		require.Error(t, err, "copySourceQueryParams must not omit knowledge_base_id filter")
		require.Error(
			t,
			repo.CopyIndices(ctx, "", map[string]string{"d1": "d2"}, map[string]string{"c1": "c2"}, "kb-dst", 1024, ""),
		)
		require.Error(
			t,
			repo.CopyIndices(ctx, "kb-src", map[string]string{"d1": "d2"}, map[string]string{"c1": "c2"}, "", 1024, ""),
		)
		require.Error(t, repo.MoveKnowledgeIndices(ctx, "", "kb-dst", "doc-1", nil, 1024, ""))
		require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "", "doc-1", nil, 1024, ""))
		require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "kb-dst", "", nil, 1024, ""))
		require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "kb-dst", "doc-1", nil, 0, ""))
	})
}

func TestTencentVectorDBRedactsCredentialsIsolatesCollectionsAndFiltersQuestions(t *testing.T) {
	repo := NewTencentVectorDBRetrieveEngineRepository(nil, "", nil).(*repository)

	t.Run("API keys and Bearer headers are redacted from wrapped SDK errors", func(t *testing.T) {
		rawErr := errors.New(
			`post http://10.0.0.8:80/document/search: Authorization: Bearer account=root&api_key=sk-live-secret-` +
				`998877 failed (code: 15202)`,
		)
		safeErr := redactTencentErr(rawErr)
		require.Error(t, safeErr)
		assert.NotContains(t, safeErr.Error(), "sk-live-secret-998877")
		assert.Contains(t, safeErr.Error(), "[REDACTED]")
		assert.True(
			t,
			isCollectionAlreadyExistsErr(safeErr),
			"error code detection must continue working after redaction",
		)
	})

	t.Run("matchesCollection isolates sibling collection prefixes", func(t *testing.T) {
		assert.True(t, repo.matchesCollection("genyee_embeddings_1024"))
		assert.False(t, repo.matchesCollection("genyee_embeddings"))
		assert.False(t, repo.matchesCollection("genyee_embeddings_0"))
		assert.False(t, repo.matchesCollection("genyee_embeddings_other_1024"),
			"must not match another store's collection sharing the prefix")
		assert.False(t, repo.matchesCollection("genyee_embeddings_notadim"))
	})

	t.Run("ExcludeGeneratedQuestionKBIDs filtering and VectorScore preservation", func(t *testing.T) {
		vecHit := toIndexWithScore(&vectorEmbedding{
			ID:              "c1",
			ChunkID:         "c1",
			SourceID:        "c1",
			SourceType:      int(types.ChunkSourceType),
			KnowledgeBaseID: "kb-off",
			Score:           0.87,
			IsEnabled:       true,
		}, types.MatchTypeEmbedding)
		assert.InDelta(t, 0.87, vecHit.Score, 1e-6)
		assert.InDelta(t, 0.87, vecHit.VectorScore, 1e-6)

		qHit := toIndexWithScore(&vectorEmbedding{
			ID:              "c1-q1",
			ChunkID:         "c1",
			SourceID:        "c1-q1",
			SourceType:      int(types.ChunkSourceType),
			KnowledgeBaseID: "kb-off",
			Score:           0.95,
			IsEnabled:       true,
		}, types.MatchTypeKeywords)
		assert.Zero(t, qHit.VectorScore)

		filtered := filterGeneratedQuestionHits([]*types.IndexWithScore{qHit, vecHit}, []string{"kb-off"})
		require.Len(t, filtered, 1)
		assert.Equal(t, "c1", filtered[0].SourceID)
	})
}
