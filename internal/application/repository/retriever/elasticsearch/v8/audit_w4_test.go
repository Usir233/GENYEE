package v8

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	typesLocal "github.com/Usir233/GENYEE/internal/types"
)

func newAuditTestV8Repo(t *testing.T, handler http.HandlerFunc) (*elasticsearchRepository, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	client, err := elasticsearch.NewTypedClient(elasticsearch.Config{
		Addresses:    []string{srv.URL},
		DisableRetry: true,
	})
	require.NoError(t, err)
	return &elasticsearchRepository{
		client: client,
		index:  "genyee_v8_test",
	}, srv
}

func TestV8BatchUpdateChunkTagID_EscapesSpecialCharsWithoutJSONInjection(t *testing.T) {
	var (
		mu      sync.Mutex
		rawBody []byte
	)
	repo, _ := newAuditTestV8Repo(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		rawBody = b
		mu.Unlock()
		_, _ = w.Write([]byte(`{"updated":1,"version_conflicts":0,"failures":[]}`))
	})

	hostileTag := "tag-1\", \"pwned\": \"yes\"\n\\tail"
	err := repo.BatchUpdateChunkTagID(context.Background(), map[string]string{
		"chunk-1": hostileTag,
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	var parsed struct {
		Script struct {
			Params map[string]any `json:"params"`
		} `json:"script"`
	}
	require.NoError(t, json.Unmarshal(rawBody, &parsed))
	assert.Equal(t, hostileTag, parsed.Script.Params["tag_id"])
	_, injected := parsed.Script.Params["pwned"]
	assert.False(t, injected, "hostile tagID injected sibling key into script.params: %s", string(rawBody))
}

func TestV8Retrieve_RejectsEmptyKnowledgeBaseIDsAndHandlesNilHitScore(t *testing.T) {
	repo, _ := newAuditTestV8Repo(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"took":1,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"genyee_v8_test","_id":"doc-nil-score","_score":null,"_source":{"chunk_id":"c1","source_id":"c1","knowledge_id":"k1","knowledge_base_id":"kb-1","content":"hello","is_enabled":true}}]}}`))
	})

	_, err := repo.Retrieve(context.Background(), typesLocal.RetrieveParams{
		Query:            "hello",
		TopK:             5,
		KnowledgeBaseIDs: []string{" "},
		RetrieverType:    typesLocal.KeywordsRetrieverType,
	})
	require.Error(t, err)

	res, err := repo.KeywordsRetrieve(context.Background(), typesLocal.RetrieveParams{
		Query:            "hello",
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    typesLocal.KeywordsRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Len(t, res[0].Results, 1)
	assert.Equal(t, 0.0, res[0].Results[0].Score)
}

func TestV8SaveKeywordOnlyAndCopyIndicesPreservesTagAndRecommended(t *testing.T) {
	var (
		mu         sync.Mutex
		copiedDocs []map[string]any
	)
	repo, _ := newAuditTestV8Repo(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(r.URL.Path, "/_doc"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"_index":"genyee_v8_test","_id":"id-1","_version":1,"result":"created","_shards":{"total":1,"successful":1,"failed":0},"_seq_no":0,"_primary_term":1}`))
		case strings.HasSuffix(r.URL.Path, "/_search"):
			_, _ = w.Write([]byte(`{"took":1,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"genyee_v8_test","_id":"d1","_score":1.0,"_source":{"content":"tagged","source_id":"c1","source_type":0,"chunk_id":"c1","knowledge_id":"k1","knowledge_base_id":"kb-src","tag_id":"tag-keep","is_enabled":true,"is_recommended":true,"embedding":[0.2,0.4]}}]}}`))
		case strings.HasSuffix(r.URL.Path, "/_bulk"):
			sc := bufio.NewScanner(bytes.NewReader(body))
			for sc.Scan() {
				var m map[string]any
				if json.Unmarshal(sc.Bytes(), &m) == nil {
					if _, hasContent := m["content"]; hasContent {
						mu.Lock()
						copiedDocs = append(copiedDocs, m)
						mu.Unlock()
					}
				}
			}
			_, _ = w.Write([]byte(`{"took":1,"errors":false,"items":[]}`))
		}
	})

	// Keyword-only Save must succeed (matching BatchSave and Support()).
	require.NoError(t, repo.Save(context.Background(), &typesLocal.IndexInfo{
		ChunkID:         "c-kw",
		SourceID:        "c-kw",
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		Content:         "keyword chunk",
		IsEnabled:       true,
	}, nil))

	// NaN embedding must fail fast.
	require.Error(t, repo.Save(context.Background(), &typesLocal.IndexInfo{
		ChunkID:  "c-nan",
		SourceID: "c-nan",
	}, map[string]any{"embedding": map[string][]float32{"c-nan": {float32(math.NaN())}}}))

	// CopyIndices must preserve tag_id and is_recommended.
	require.NoError(t, repo.CopyIndices(context.Background(), "kb-src",
		map[string]string{"k1": "k-dst"},
		map[string]string{"c1": "c-dst"},
		"kb-dst", 2, "manual"))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, copiedDocs, 1)
	assert.Equal(t, "tag-keep", copiedDocs[0]["tag_id"])
	assert.Equal(t, true, copiedDocs[0]["is_recommended"])
}

func TestV8DeleteAndMove_RejectBlankScopeAndSurfaceByQueryFailures(t *testing.T) {
	repo, _ := newAuditTestV8Repo(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"took":1,"timed_out":false,"total":1,"deleted":0,"batches":1,"version_conflicts":0,"noops":0,"retries":{"bulk":0,"search":0},"throttled_millis":0,"requests_per_second":-1.0,"throttled_until_millis":0,"failures":[{"index":"genyee_v8_test","id":"doc-5","status":409,"cause":{"type":"version_conflict_engine_exception","reason":"PRIVATE-DOC-CONTENT"}}]}`))
	})

	require.Error(t, repo.DeleteByChunkIDList(context.Background(), []string{""}, 768, ""))
	require.Error(t, repo.MoveKnowledgeIndices(context.Background(), "kb-src", "", "k-1", nil, 768, ""))
	require.Error(t, repo.CopyIndices(context.Background(), "kb-src", map[string]string{"k1": "k2"}, map[string]string{"c1": "c2"}, "", 768, ""))

	err := repo.DeleteByChunkIDList(context.Background(), []string{"c-1"}, 768, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version_conflict_engine_exception")
	assert.False(t, strings.Contains(err.Error(), "PRIVATE-DOC-CONTENT"), "cluster reason leaked: %v", err)
}
