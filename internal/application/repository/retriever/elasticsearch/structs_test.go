package elasticsearch

import (
	"math"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToDBVectorEmbedding_NilInputDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		got := ToDBVectorEmbedding(nil, nil)
		require.NotNil(t, got)
		assert.Empty(t, got.ChunkID)
	})
}

func TestFromDBVectorEmbeddingWithScore_NilInputDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		got := FromDBVectorEmbeddingWithScore("doc-1", nil, types.MatchTypeKeywords)
		require.NotNil(t, got)
		assert.Equal(t, "doc-1", got.ID)
		assert.Equal(t, types.MatchTypeKeywords, got.MatchType)
	})
}

func TestValidateEmbeddingAndScopingHelpers(t *testing.T) {
	require.NoError(t, ValidateEmbedding([]float32{0.1, -0.2, 0}))
	require.Error(t, ValidateEmbedding([]float32{float32(math.NaN())}))
	require.Error(t, ValidateEmbedding([]float32{float32(math.Inf(-1))}))

	_, err := RequireKnowledgeBaseIDs([]string{"", "  "})
	require.Error(t, err)
	kbIDs, err := RequireKnowledgeBaseIDs([]string{" kb-1 ", ""})
	require.NoError(t, err)
	assert.Equal(t, []string{"kb-1"}, kbIDs)

	emptyIDs, err := SanitizeDeleteTargetIDs(nil)
	require.NoError(t, err)
	assert.Nil(t, emptyIDs)
	_, err = SanitizeDeleteTargetIDs([]string{"c1", " "})
	require.Error(t, err)

	assert.Equal(t, "dial http://[REDACTED]@es:9200", RedactURLCredentials("dial http://elastic:secret@es:9200"))
}
