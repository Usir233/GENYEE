// Package doris implements the Apache Doris retrieve engine repository.
package doris

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDorisIdentifierValidation_RejectsSQLInjectionInTableAndDatabase(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()
	primeCompatMode(repo, dorisCompatModeInnerProductDuplicate, nil)

	repo.tableBaseName = "genyee`; DROP TABLE secrets; --"
	err := repo.DeleteByChunkIDList(context.Background(), []string{"c1"}, 768, "")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())

	repo.tableBaseName = "genyee_embeddings"
	repo.database = "genyee`--"
	_, err = repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{
		Query:            "hello",
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb1"},
		RetrieverType:    types.KeywordsRetrieverType,
	})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDorisListEmbeddingTables_IgnoresHostileRemoteTableNames(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema.tables`).
		WithArgs("genyee", "genyee_embeddings\\_%").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).
			AddRow("genyee_embeddings_768`; DROP TABLE x; --").
			AddRow("genyeeXembeddings_768").
			AddRow("genyee_embeddings_0").
			AddRow("genyee_embeddings_768"))

	tables, err := repo.listEmbeddingTables(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"genyee_embeddings_768"}, tables)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDorisRetrieveAndMutations_FailClosedOnMissingKBOrBlankTargets(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()
	primeCompatMode(repo, dorisCompatModeLegacy, nil)

	// Empty / whitespace KnowledgeBaseIDs must never execute cross-tenant SQL.
	_, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding:        []float32{1, 0},
		TopK:             5,
		KnowledgeBaseIDs: []string{"", "  "},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.Error(t, err)

	_, err = repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{
		Query:            "hello",
		TopK:             5,
		KnowledgeBaseIDs: nil,
		RetrieverType:    types.KeywordsRetrieverType,
	})
	require.Error(t, err)

	// Empty query vector or NaN threshold must fail closed.
	_, err = repo.VectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding:        nil,
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb1"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.Error(t, err)

	_, err = repo.VectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding:        []float32{1, 0},
		TopK:             5,
		Threshold:        math.NaN(),
		KnowledgeBaseIDs: []string{"kb1"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.Error(t, err)

	// Blank target IDs, non-positive dimensions, and blank KB IDs on Delete/Move/Copy must fail closed.
	require.Error(t, repo.DeleteByChunkIDList(context.Background(), []string{" "}, 768, ""))
	require.Error(t, repo.DeleteByChunkIDList(context.Background(), []string{"c1"}, 0, ""))
	require.Error(t, repo.MoveKnowledgeIndices(context.Background(), "", "kb2", "k1", nil, 768, ""))
	require.Error(
		t,
		repo.CopyIndices(
			context.Background(),
			"kb1",
			map[string]string{"k1": "k2"},
			map[string]string{"c1": "c2"},
			"",
			768,
			"",
		),
	)
	require.Error(
		t,
		repo.CopyIndices(
			context.Background(),
			"kb1",
			map[string]string{"k1": "k2"},
			map[string]string{"c1": "c2"},
			"kb2",
			0,
			"",
		),
	)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDorisKeywordsRetrieve_NegativeTopKDoesNotPanicAndPropagatesTableErrors(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	// 1. Negative TopK must clamp to safe positive default instead of panicking on slice bounds.
	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema.tables`).
		WithArgs("genyee", "genyee_embeddings\\_%").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("genyee_embeddings_768"))
	mock.ExpectQuery(`MATCH_ANY \? LIMIT 10`).
		WithArgs(true, "kb1", "你好").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "content", "source_id", "source_type",
			"chunk_id", "knowledge_id", "knowledge_base_id", "tag_id", "is_enabled",
		}).AddRow("id1", "你好", "c1", 0, "c1", "k1", "kb1", "", true))

	require.NotPanics(t, func() {
		res, err := repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{
			Query:            "你好",
			TopK:             -5,
			KnowledgeBaseIDs: []string{"kb1"},
			RetrieverType:    types.KeywordsRetrieverType,
		})
		require.NoError(t, err)
		require.Len(t, res, 1)
		require.Len(t, res[0].Results, 1)
	})

	// 2. When every matching table query fails, KeywordsRetrieve must return an error instead of empty success.
	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema.tables`).
		WithArgs("genyee", "genyee_embeddings\\_%").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("genyee_embeddings_768"))
	mock.ExpectQuery(`MATCH_ANY \?`).
		WillReturnError(errors.New("connection reset by peer"))

	_, err := repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{
		Query:            "你好",
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb1"},
		RetrieverType:    types.KeywordsRetrieverType,
	})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDorisResolveCompatMode_RetriesAfterTransientFailure(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()
	repo.compatModeRequested = dorisCompatModeAuto

	// First call experiences a transient DB failure while listing tables.
	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema.tables`).
		WithArgs("genyee", "genyee_embeddings\\_%").
		WillReturnError(errors.New("temporary network outage"))

	_, err := repo.resolveCompatMode(context.Background())
	require.Error(t, err)

	// Second call must retry and succeed instead of being permanently poisoned by sync.Once.
	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema.tables`).
		WithArgs("genyee", "genyee_embeddings\\_%").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}))
	mock.ExpectQuery(`SELECT inner_product_approximate\(\[1.0\],\[1.0\]\)`).
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(1.0))
	mock.ExpectQuery(`SELECT cosine_distance_approximate\(\[1.0\],\[1.0\]\)`).
		WillReturnError(errors.New("unsupported"))

	mode, err := repo.resolveCompatMode(context.Background())
	require.NoError(t, err)
	assert.Equal(t, dorisCompatModeInnerProductDuplicate, mode)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDorisStreamLoadOnce_TruncatesFailureFieldsAndRedactsCredentials(t *testing.T) {
	repo, _, _, cleanup := newTestRepo(t)
	defer cleanup()

	hugeMsg := strings.Repeat("M", 8*maxStreamLoadErrorPreviewBytes)
	secretURL := "http://admin:SuperSecretPass123@127.0.0.1:8040/api/_load_error_log?file=err_1"
	payload, err := json.Marshal(map[string]any{
		"Status":   "Fail",
		"Message":  hugeMsg,
		"ErrorURL": secretURL,
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	repo.feHTTPBase = srv.URL

	err = repo.streamLoadOnce(
		context.Background(),
		"genyee_embeddings_768",
		[]string{"id"},
		[]map[string]any{{"id": "1"}},
	)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SuperSecretPass123", "credentials in ErrorURL must be redacted")
	assert.LessOrEqual(t, len(err.Error()), 2*maxStreamLoadErrorPreviewBytes, "error message must be bounded")
}
