package milvus

import (
	"github.com/milvus-io/milvus/client/v2/column"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCollectionDimensionForBase(t *testing.T) {
	dimension, ok := collectionDimensionForBase("genyee_embeddings_1536", "genyee_embeddings")
	require.True(t, ok)
	require.Equal(t, 1536, dimension)

	_, ok = collectionDimensionForBase("genyee_embeddings_multilingual_1536", "genyee_embeddings")
	require.False(t, ok)
	_, ok = collectionDimensionForBase("genyee_embeddings", "genyee_embeddings")
	require.False(t, ok)
	_, ok = collectionDimensionForBase("genyee_embeddings_1536_backup", "genyee_embeddings")
	require.False(t, ok)

	require.True(t, matchesDimensionCollection(
		"genyee_embeddings_multilingual_1536",
		"genyee_embeddings_multilingual",
	))
	require.False(t, matchesDimensionCollection("genyee_embeddings_1536", "genyee_embeddings_multilingual"))
}

func TestConvertResultSetFloatVector(t *testing.T) {
	embedding := []float32{0.1, 0.2, 0.3}
	docs, _, err := convertResultSet([]client.ResultSet{{
		ResultCount: 1,
		Fields: client.DataSet{
			column.NewColumnVarChar(fieldID, []string{"pk-1"}),
			column.NewColumnVarChar(fieldContent, []string{"hello"}),
			column.NewColumnVarChar(fieldLanguage, []string{"english"}),
			column.NewColumnFloatVector(fieldEmbedding, 3, [][]float32{embedding}),
		},
	}})
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "pk-1", docs[0].ID)
	require.Equal(t, "hello", docs[0].Content)
	require.Equal(t, "english", docs[0].Language)
	require.Equal(t, embedding, docs[0].Embedding)
}

func TestConvertResultSetDoubleArray(t *testing.T) {
	docs, _, err := convertResultSet([]client.ResultSet{{
		ResultCount: 1,
		Fields: client.DataSet{
			column.NewColumnVarChar(fieldID, []string{"pk-1"}),
			column.NewColumnDoubleArray(fieldEmbedding, [][]float64{{1, 2, 3}}),
		},
	}})
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, []float32{1, 2, 3}, docs[0].Embedding)
}
