package v7

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/elastic/go-elasticsearch/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	typesLocal "github.com/Usir233/GENYEE/internal/types"
)

func newAuditTestV7Repo(t *testing.T, handler http.HandlerFunc) (*elasticsearchRepository, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`{"version":{"number":"7.17.0","build_flavor":"default"},"tagline":"You Know, for Search"}`))
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	client, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses:    []string{srv.URL},
		DisableRetry: true,
	})
	require.NoError(t, err)
	return &elasticsearchRepository{
		client: client,
		index:  "genyee_v7_test",
	}, srv
}

func TestV7Retrieve_RejectsEmptyKnowledgeBaseIDsFailClosed(t *testing.T) {
	repo, _ := newAuditTestV7Repo(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected cross-tenant search call: %s %s", r.Method, r.URL.Path)
	})

	_, err := repo.Retrieve(context.Background(), typesLocal.RetrieveParams{
		Query:            "secret",
		TopK:             5,
		KnowledgeBaseIDs: []string{"", "   "},
		RetrieverType:    typesLocal.KeywordsRetrieverType,
	})
	require.Error(t, err)

	_, err = repo.VectorRetrieve(context.Background(), typesLocal.RetrieveParams{
		Embedding:     []float32{0.1, 0.2},
		TopK:          5,
		RetrieverType: typesLocal.VectorRetrieverType,
	})
	require.Error(t, err)
}

func TestV7KeywordSearch_PropagatesTopKAndThresholdViaStructuredJSON(t *testing.T) {
	var (
		mu       sync.Mutex
		captured map[string]any
	)
	repo, _ := newAuditTestV7Repo(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(body, &captured)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
	})

	_, err := repo.KeywordsRetrieve(context.Background(), typesLocal.RetrieveParams{
		Query:            `hello " }}\n{"match_all":{}}`,
		TopK:             42,
		Threshold:        0.35,
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    typesLocal.KeywordsRetrieverType,
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, captured)
	assert.EqualValues(t, 42, captured["size"])
	assert.InDelta(t, 0.35, captured["min_score"], 1e-6)
}

func TestV7VectorRetrieve_SetsEmbeddingMatchTypeAndRejectsInvalidVector(t *testing.T) {
	repo, _ := newAuditTestV7Repo(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hits":{"hits":[{"_id":"doc-1","_score":0.88,"_source":{"chunk_id":"c1","source_id":"c1","knowledge_id":"k1","knowledge_base_id":"kb-1","content":"vec","is_enabled":true}}]}}`))
	})

	res, err := repo.VectorRetrieve(context.Background(), typesLocal.RetrieveParams{
		Embedding:        []float32{0.6, 0.8},
		TopK:             3,
		Threshold:        0.1,
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    typesLocal.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Len(t, res[0].Results, 1)
	assert.Equal(t, typesLocal.MatchTypeEmbedding, res[0].Results[0].MatchType)

	_, err = repo.VectorRetrieve(context.Background(), typesLocal.RetrieveParams{
		Embedding:        []float32{float32(math.NaN())},
		TopK:             3,
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    typesLocal.VectorRetrieverType,
	})
	require.Error(t, err)
}

func TestV7Save_AllowsKeywordOnlyDocAndRejectsNaNVector(t *testing.T) {
	var createdDoc map[string]any
	repo, _ := newAuditTestV7Repo(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &createdDoc)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"_id":"d1","result":"created"}`))
	})

	err := repo.Save(context.Background(), &typesLocal.IndexInfo{
		ChunkID:         "c-kw",
		SourceID:        "c-kw",
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		Content:         "keyword only chunk",
		IsEnabled:       true,
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "c-kw", createdDoc["chunk_id"])

	err = repo.Save(context.Background(), &typesLocal.IndexInfo{
		ChunkID:         "c-nan",
		SourceID:        "c-nan",
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
	}, map[string]any{
		"embedding": map[string][]float32{"c-nan": {float32(math.Inf(1))}},
	})
	require.Error(t, err)
}

func TestV7DeleteAndByQuery_RejectsBlankTargetsAndSurfacesFailuresWithoutLeakingReason(t *testing.T) {
	repo, _ := newAuditTestV7Repo(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"deleted":0,"failures":[{"id":"doc-9","cause":{"type":"version_conflict_engine_exception","reason":"SENSITIVE-CHUNK-TEXT"}}]}`))
	})

	// Blank target IDs must fail closed before issuing _delete_by_query.
	require.Error(t, repo.DeleteByChunkIDList(context.Background(), []string{"  "}, 768, ""))
	require.Error(t, repo.MoveKnowledgeIndices(context.Background(), "", "kb-dst", "k-1", nil, 768, ""))
	require.Error(t, repo.CopyIndices(context.Background(), "", map[string]string{"k1": "k2"}, map[string]string{"c1": "c2"}, "kb-dst", 768, ""))

	// Partial failure in HTTP 200 _delete_by_query body must return an error without leaking reason.
	err := repo.DeleteByChunkIDList(context.Background(), []string{"c-1"}, 768, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version_conflict_engine_exception")
	assert.False(t, strings.Contains(err.Error(), "SENSITIVE-CHUNK-TEXT"), "cluster reason leaked: %v", err)
}
