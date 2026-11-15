package weaviate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdk "github.com/weaviate/weaviate-go-client/v5/weaviate"
)

func newWeaviateTestClient(t *testing.T, handler http.HandlerFunc) *sdk.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client, err := sdk.NewClient(sdk.Config{
		Scheme: "http",
		Host:   strings.TrimPrefix(srv.URL, "http://"),
	})
	require.NoError(t, err)
	return client
}

func TestWeaviateRetrieveFailsClosedOnEmptyScopeAndValidatesClassAndExclusions(t *testing.T) {
	ctx := context.Background()

	t.Run("VectorRetrieve and KeywordsRetrieve fail closed without hitting GraphQL when KB and Knowledge scope are empty", func(t *testing.T) {
		graphqlCalls := 0
		client := newWeaviateTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/v1/meta":
				_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
			case r.URL.Path == "/v1/graphql":
				graphqlCalls++
				_, _ = w.Write([]byte(`{"data":{"Get":{"Genyee_embeddings_2":[{"chunk_id":"leaked","knowledge_base_id":"tenant-b","_additional":{"id":"p1","certainty":0.95,"score":"2.0"}}]}}}`))
			case strings.HasPrefix(r.URL.Path, "/v1/schema"):
				_, _ = w.Write([]byte(`{"classes":[{"class":"Genyee_embeddings_2"}]}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
		repo := &weaviateRepository{client: client, collectionBaseName: "Genyee_embeddings"}

		for _, params := range []types.RetrieveParams{
			{RetrieverType: types.VectorRetrieverType, Embedding: []float32{1, 0}, TopK: 5},
			{RetrieverType: types.VectorRetrieverType, Embedding: []float32{1, 0}, KnowledgeBaseIDs: []string{"", "   "}, TopK: 5},
			{RetrieverType: types.VectorRetrieverType, Embedding: []float32{1, 0}, KnowledgeBaseIDs: []string{"kb-1"}, TagIDs: []string{"   "}, TopK: 5},
			{RetrieverType: types.KeywordsRetrieverType, Query: "secret", TopK: 5},
			{RetrieverType: types.KeywordsRetrieverType, Query: "secret", KnowledgeIDs: []string{"  "}, TopK: 5},
		} {
			res, err := repo.Retrieve(ctx, params)
			require.NoError(t, err)
			require.Len(t, res, 1)
			assert.Empty(t, res[0].Results, "empty/blank scope must fail closed with zero hits")
		}
		assert.Zero(t, graphqlCalls, "empty retrieval scope must not execute unfiltered GraphQL queries")

		rawWhere := repo.getBaseFilter(types.RetrieveParams{}).String()
		assert.Contains(t, rawWhere, "valueBoolean: false", "direct getBaseFilter on empty scope must emit unsatisfiable guard")
	})

	t.Run("multi-value ExcludeKnowledgeIDs and ExcludeChunkIDs emit scalar NotEqual operands", func(t *testing.T) {
		repo := &weaviateRepository{collectionBaseName: "Genyee_embeddings"}
		whereStr := repo.getBaseFilter(types.RetrieveParams{
			KnowledgeBaseIDs:    []string{"kb-1"},
			ExcludeKnowledgeIDs: []string{"doc-1", "doc-2"},
			ExcludeChunkIDs:     []string{"chk-1", "chk-2"},
		}).String()

		assert.NotContains(t, whereStr, `operator: NotEqual path: ["knowledge_id"] valueText: [`,
			"NotEqual must not serialize an array valueText")
		assert.NotContains(t, whereStr, `operator: NotEqual path: ["chunk_id"] valueText: [`,
			"NotEqual must not serialize an array valueText")
		assert.Contains(t, whereStr, `operator: NotEqual path: ["knowledge_id"] valueText: "doc-1"`)
		assert.Contains(t, whereStr, `operator: NotEqual path: ["knowledge_id"] valueText: "doc-2"`)
		assert.Contains(t, whereStr, `operator: NotEqual path: ["chunk_id"] valueText: "chk-1"`)
		assert.Contains(t, whereStr, `operator: NotEqual path: ["chunk_id"] valueText: "chk-2"`)
	})

	t.Run("collection matching isolates sibling prefixes, rejects invalid GraphQL class names, and enforces ExcludeGeneratedQuestionKBIDs", func(t *testing.T) {
		var queriedQueries []string
		var mu sync.Mutex
		client := newWeaviateTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/v1/meta":
				_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/schema":
				_, _ = w.Write([]byte(`{"classes":[
					{"class":"Genyee_2"},
					{"class":"Genyee_other_2"},
					{"class":"Genyee_bad { id }"}
				]}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/graphql":
				var body struct {
					Query string `json:"query"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				queriedQueries = append(queriedQueries, body.Query)
				mu.Unlock()
				_, _ = w.Write([]byte(`{"data":{"Get":{"Genyee_2":[
					{"content":"question row","source_id":"c1-q1","source_type":0,"chunk_id":"c1","knowledge_id":"d1","knowledge_base_id":"kb-off","_additional":{"id":"id-q1","score":"3.5"}},
					{"content":"body row","source_id":"c1","source_type":0,"chunk_id":"c1","knowledge_id":"d1","knowledge_base_id":"kb-off","_additional":{"id":"id-c1","score":"2.0"}},
					{"content":"allowed question","source_id":"c2-q1","source_type":0,"chunk_id":"c2","knowledge_id":"d2","knowledge_base_id":"kb-on","_additional":{"id":"id-q2","score":"1.5"}}
				]}}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})

		repo := &weaviateRepository{client: client, collectionBaseName: "Genyee"}
		res, err := repo.Retrieve(ctx, types.RetrieveParams{
			RetrieverType:                 types.KeywordsRetrieverType,
			Query:                         "row",
			KnowledgeBaseIDs:              []string{"kb-off", "kb-on"},
			ExcludeGeneratedQuestionKBIDs: []string{"kb-off"},
			TopK:                          5,
		})
		require.NoError(t, err)
		require.Len(t, queriedQueries, 1, "must query only Genyee_2 and skip Genyee_other_2 and invalid class names")
		assert.Contains(t, queriedQueries[0], "Genyee_2")
		assert.NotContains(t, queriedQueries[0], "Genyee_other_2")

		require.Len(t, res, 1)
		require.Len(t, res[0].Results, 2)
		assert.Equal(t, "c1", res[0].Results[0].SourceID)
		assert.Equal(t, "c2-q1", res[0].Results[1].SourceID)

		badRepo := NewWeaviateRetrieveEngineRepository(client, &types.IndexConfig{
			CollectionPrefix: "genyee-bad{inject}",
		}).(*weaviateRepository)
		assert.Regexp(t, `^[A-Z][_0-9A-Za-z]*$`, badRepo.getCollectionName(1024))
	})

	t.Run("Delete CopyIndices and MoveKnowledgeIndices fail closed on empty or invalid targets", func(t *testing.T) {
		calls := 0
		client := newWeaviateTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/meta" {
				_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
				return
			}
			calls++
			w.WriteHeader(http.StatusInternalServerError)
		})
		repo := &weaviateRepository{client: client, collectionBaseName: "Genyee_embeddings"}

		require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{"", "  "}, 1024, ""))
		require.NoError(t, repo.DeleteByKnowledgeIDList(ctx, []string{"   "}, 1024, ""))
		require.NoError(t, repo.DeleteBySourceIDList(ctx, []string{""}, 1024, ""))
		require.Error(t, repo.DeleteByChunkIDList(ctx, []string{"c1"}, 0, ""))
		require.Error(t, repo.CopyIndices(ctx, "", map[string]string{"d1": "d2"}, map[string]string{"c1": "c2"}, "kb-dst", 64, ""))
		require.Error(t, repo.CopyIndices(ctx, "kb-src", map[string]string{"d1": "d2"}, map[string]string{"c1": "c2"}, "", 64, ""))
		require.Error(t, repo.MoveKnowledgeIndices(ctx, "", "kb-dst", "doc-1", nil, 64, ""))
		require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "", "doc-1", nil, 64, ""))
		require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "kb-dst", "", nil, 64, ""))
		require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "kb-dst", "doc-1", nil, 0, ""))
		assert.Zero(t, calls, "invalid/blank delete, copy, or move targets must not hit Weaviate")
	})
}

func TestWeaviateBatchUpdatesUseMergeAndSavePreservesGeneratedQuestionIDs(t *testing.T) {
	ctx := context.Background()

	t.Run("BatchUpdateChunkEnabledStatus and BatchUpdateChunkTagID use PATCH merge across all dimension collections", func(t *testing.T) {
		var mu sync.Mutex
		patched := make(map[string]map[string]any)
		putCalls := 0

		client := newWeaviateTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/v1/meta":
				_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/schema":
				_, _ = w.Write([]byte(`{"classes":[{"class":"Genyee_embeddings_768"},{"class":"Genyee_embeddings_1024"}]}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/graphql":
				var body struct {
					Query string `json:"query"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if strings.Contains(body.Query, "Genyee_embeddings_768") {
					// First collection has no matches for chunk-1 or chunk-2
					_, _ = w.Write([]byte(`{"data":{"Get":{"Genyee_embeddings_768":[]}}}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":{"Get":{"Genyee_embeddings_1024":[
					{"chunk_id":"chunk-1","_additional":{"id":"00000000-0000-0000-0000-000000000101"}},
					{"chunk_id":"chunk-1","_additional":{"id":"00000000-0000-0000-0000-000000000102"}},
					{"chunk_id":"chunk-2","_additional":{"id":"00000000-0000-0000-0000-000000000201"}}
				]}}}`))
			case r.Method == http.MethodPut:
				mu.Lock()
				putCalls++
				mu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
			case r.Method == http.MethodPatch:
				var body struct {
					Properties map[string]any `json:"properties"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				id := path.Base(r.URL.Path)
				mu.Lock()
				if patched[id] == nil {
					patched[id] = make(map[string]any)
				}
				for k, v := range body.Properties {
					patched[id][k] = v
				}
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})

		repo := &weaviateRepository{client: client, collectionBaseName: "Genyee_embeddings"}
		require.NoError(t, repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{
			"chunk-1": false,
			"chunk-2": true,
		}))
		require.NoError(t, repo.BatchUpdateChunkTagID(ctx, map[string]string{
			"chunk-1": "tag-a",
			"chunk-2": "tag-b",
		}))

		mu.Lock()
		defer mu.Unlock()
		assert.Zero(t, putCalls, "updates must use PATCH (WithMerge), never destructive PUT")
		assert.Equal(t, map[string]any{fieldIsEnabled: false, fieldTagID: "tag-a"}, patched["00000000-0000-0000-0000-000000000101"])
		assert.Equal(t, map[string]any{fieldIsEnabled: false, fieldTagID: "tag-a"}, patched["00000000-0000-0000-0000-000000000102"])
		assert.Equal(t, map[string]any{fieldIsEnabled: true, fieldTagID: "tag-b"}, patched["00000000-0000-0000-0000-000000000201"])
	})

	t.Run("BatchSave assigns distinct valid UUIDs per SourceID and surfaces per-object batch errors", func(t *testing.T) {
		var savedIDs []string
		returnObjErr := false
		client := newWeaviateTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/v1/meta":
				_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/schema/"):
				_, _ = w.Write([]byte(`{"class":"Genyee_embeddings_2"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/batch/objects":
				var body struct {
					Objects []struct {
						ID string `json:"id"`
					} `json:"objects"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				for _, o := range body.Objects {
					savedIDs = append(savedIDs, o.ID)
				}
				if returnObjErr {
					_, _ = w.Write([]byte(`[{"result":{"errors":{"error":[{"message":"disk quota exceeded"}]}}}]`))
					return
				}
				_, _ = w.Write([]byte(`[]`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})

		repo := &weaviateRepository{client: client, collectionBaseName: "Genyee_embeddings"}
		items := []*types.IndexInfo{
			{ChunkID: "chunk-plain-id", SourceID: "chunk-plain-id", KnowledgeID: "d1", KnowledgeBaseID: "kb1", Content: "body", IsEnabled: true},
			{ChunkID: "chunk-plain-id", SourceID: "chunk-plain-id-q1", KnowledgeID: "d1", KnowledgeBaseID: "kb1", Content: "q1", IsEnabled: true},
		}
		params := map[string]any{
			"embedding": map[string][]float32{
				"chunk-plain-id":    {0.1, 0.9},
				"chunk-plain-id-q1": {0.2, 0.8},
			},
		}
		require.NoError(t, repo.BatchSave(ctx, items, params))
		require.Len(t, savedIDs, 2)
		assert.NotEqual(t, savedIDs[0], savedIDs[1], "chunk body and generated question must not overwrite the same object ID")
		for _, id := range savedIDs {
			_, err := uuid.Parse(id)
			assert.NoError(t, err, "object ID %q must be a valid UUID", id)
		}

		returnObjErr = true
		err := repo.BatchSave(ctx, items[:1], params)
		require.ErrorContains(t, err, "disk quota exceeded")
	})
}

func TestWeaviateRetrieveHandlesMalformedGraphQLPayloadsAndRanksKeywordScores(t *testing.T) {
	ctx := context.Background()

	// Malformed items must never panic
	got := parseGraphQLResponse([]any{
		"not-an-object",
		map[string]any{"chunk_id": "missing-additional"},
		map[string]any{"_additional": "bad-additional"},
		map[string]any{"_additional": map[string]any{"id": 123}},
		map[string]any{"_additional": map[string]any{"id": "ok-vec", "certainty": 0.75}, "chunk_id": "c-ok", "knowledge_base_id": "kb1"},
	}, "Genyee_embeddings_2", types.MatchTypeEmbedding)
	require.Len(t, got, 1)
	assert.InDelta(t, 0.5, got[0].Score, 1e-6)
	assert.InDelta(t, 0.5, got[0].VectorScore, 1e-6)
	assert.True(t, got[0].IsEnabled)

	// KeywordsRetrieve across two dimension collections must parse BM25 string score and rank globally
	client := newWeaviateTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/meta":
			_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/schema":
			_, _ = w.Write([]byte(`{"classes":[{"class":"Genyee_embeddings_768"},{"class":"Genyee_embeddings_1024"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/graphql":
			var body struct {
				Query string `json:"query"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if strings.Contains(body.Query, "Genyee_embeddings_768") {
				_, _ = w.Write([]byte(`{"data":{"Get":{"Genyee_embeddings_768":[
					{"content":"low","source_id":"low","chunk_id":"low","knowledge_base_id":"kb1","_additional":{"id":"id-low","score":"0.4"}}
				]}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"Get":{"Genyee_embeddings_1024":[
				{"content":"high","source_id":"high","chunk_id":"high","knowledge_base_id":"kb1","_additional":{"id":"id-high","score":"4.2"}}
			]}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	repo := &weaviateRepository{client: client, collectionBaseName: "Genyee_embeddings"}
	res, err := repo.Retrieve(ctx, types.RetrieveParams{
		RetrieverType:    types.KeywordsRetrieverType,
		Query:            "test",
		KnowledgeBaseIDs: []string{"kb1"},
		TopK:             1,
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Len(t, res[0].Results, 1)
	assert.Equal(t, "high", res[0].Results[0].ChunkID, "higher BM25 score from second collection must rank first")
	assert.InDelta(t, 4.2, res[0].Results[0].Score, 1e-6)
}
