package opensearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
)

func TestInspectBulkResponse_ErrorsTrueWithoutItemDetail_FailsClosed(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"errors": true, "items": []}`,
		`{"errors": true, "items": [{"index": {"_id": "c1", "status": 500}}]}`,
	}
	for _, body := range cases {
		err := inspectBulkResponse(strings.NewReader(body))
		if !errors.Is(err, ErrTransport) {
			t.Fatalf("body=%s: want ErrTransport on errors=true, got %v", body, err)
		}
	}
}

func TestRetrieve_EmptyOrBlankKnowledgeBaseIDs_FailsClosed(t *testing.T) {
	t.Parallel()
	repo, ts := newTestRepo(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "_search") {
			t.Errorf("unexpected cross-tenant _search call on empty KBIDs: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	defer ts.Close()

	for _, rt := range []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType} {
		_, err := repo.Retrieve(context.Background(), types.RetrieveParams{
			Query:            "hello",
			Embedding:        []float32{0.1, 0.2},
			TopK:             5,
			KnowledgeBaseIDs: []string{"", "   "},
			RetrieverType:    rt,
		})
		if err == nil {
			t.Fatalf("RetrieverType=%s: expected error for blank KnowledgeBaseIDs, got nil", rt)
		}
	}

	// NaN threshold must also fail closed instead of marshaling invalid JSON or mis-scoring.
	_, err := repo.Retrieve(context.Background(), types.RetrieveParams{
		Query:            "hello",
		TopK:             5,
		Threshold:        math.NaN(),
		KnowledgeBaseIDs: []string{"kb-1"},
		RetrieverType:    types.KeywordsRetrieverType,
	})
	if err == nil {
		t.Fatal("expected error for NaN threshold, got nil")
	}
}

func TestDeleteByList_InspectsPartialFailuresAndRejectsBlankTargets(t *testing.T) {
	t.Parallel()
	repo, ts := newTestRepo(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "_delete_by_query"):
			_, _ = w.Write([]byte(`{"deleted":0,"version_conflicts":0,"failures":[{"id":"c9","cause":{"type":"version_conflict_engine_exception","reason":"SECRET-DOC-FRAGMENT"}}]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	defer ts.Close()

	if err := repo.DeleteByChunkIDList(context.Background(), []string{"  "}, 768, ""); err == nil {
		t.Fatal("blank chunk ID in DeleteByChunkIDList must fail closed")
	}
	if err := repo.MoveKnowledgeIndices(context.Background(), "", "kb-dst", "k1", nil, 768, ""); err == nil {
		t.Fatal("empty sourceKB in MoveKnowledgeIndices must fail closed")
	}
	if err := repo.CopyIndices(context.Background(), "kb-src", map[string]string{"k1": "k2"}, map[string]string{"c1": "c2"}, "", 768, ""); err == nil {
		t.Fatal("empty targetKB in CopyIndices must fail closed")
	}

	err := repo.DeleteByChunkIDList(context.Background(), []string{"c9"}, 768, "")
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("expected ErrTransport on _delete_by_query failures, got %v", err)
	}
	if strings.Contains(err.Error(), "SECRET-DOC-FRAGMENT") {
		t.Fatalf("error leaked cluster reason: %v", err)
	}
}

func TestBatchSaveAndCopyIndices_PreservesDistinctIDsForGeneratedQuestionsAndLegacyEnabled(t *testing.T) {
	t.Parallel()
	var (
		mu        sync.Mutex
		actionIDs []string
		savedDocs []map[string]any
		searchCnt int
	)
	repo, ts := newTestRepo(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "_search"):
			mu.Lock()
			searchCnt++
			first := searchCnt == 1
			mu.Unlock()
			if first {
				// Primary chunk (missing legacy is_enabled) + generated question sharing chunk_id.
				_, _ = w.Write([]byte(`{"hits":{"hits":[
					{"_source":{"content":"primary","source_id":"c1","source_type":0,"chunk_id":"c1","knowledge_id":"k1","knowledge_base_id":"kbSrc","embedding":[0.1,0.2]}},
					{"_source":{"content":"question?","source_id":"c1-q1","source_type":0,"chunk_id":"c1","knowledge_id":"k1","knowledge_base_id":"kbSrc","is_enabled":true,"embedding":[0.3,0.4]}}
				]}}`))
			} else {
				_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
			}
		case strings.HasSuffix(r.URL.Path, "/_bulk"):
			b, _ := io.ReadAll(r.Body)
			sc := bufio.NewScanner(bytes.NewReader(b))
			mu.Lock()
			for sc.Scan() {
				var m map[string]any
				if json.Unmarshal(sc.Bytes(), &m) != nil {
					continue
				}
				if idx, ok := m["index"].(map[string]any); ok {
					if id, ok := idx["_id"].(string); ok {
						actionIDs = append(actionIDs, id)
					}
					continue
				}
				savedDocs = append(savedDocs, m)
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{"errors":false,"items":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	defer ts.Close()

	err := repo.CopyIndices(context.Background(), "kbSrc",
		map[string]string{"k1": "tk1"},
		map[string]string{"c1": "tc1"},
		"kbDst", 2, "manual")
	if err != nil {
		t.Fatalf("CopyIndices: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(actionIDs) != 2 {
		t.Fatalf("want 2 bulk actions, got %d (%v)", len(actionIDs), actionIDs)
	}
	if actionIDs[0] == actionIDs[1] {
		t.Fatalf("generated question overwrote primary chunk _id: both used %q", actionIDs[0])
	}
	if actionIDs[0] != "tc1" || actionIDs[1] != "tc1-q1" {
		t.Fatalf("want action _ids [tc1, tc1-q1], got %v", actionIDs)
	}
	if len(savedDocs) != 2 || savedDocs[0]["is_enabled"] != true {
		t.Fatalf("legacy doc missing is_enabled should default to true, got %+v", savedDocs)
	}
}
