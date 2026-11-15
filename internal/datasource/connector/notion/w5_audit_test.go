package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
)

func TestW5NotionAudit_RejectsDotSegmentIDsEscapesCursorAndTrimsBaseURL(t *testing.T) {
	allowNotionTestServer(t)

	ds := &types.DataSourceConfig{
		Settings: map[string]interface{}{
			"base_url": "https://api.notion.com///",
		},
	}
	if got := extractBaseURL(ds); got != "https://api.notion.com" {
		t.Fatalf("extractBaseURL = %q, want https://api.notion.com", got)
	}

	secret := "ntn_super_secret_w5"
	if _, err := parseNotionConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"api_key": secret + "\r\nX-Injected: 1",
		},
	}); err == nil {
		t.Fatal("expected parseNotionConfig to reject control chars in api_key")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("parseNotionConfig leaked secret in error: %v", err)
	}

	var unexpectedHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/blocks/blk1/children" {
			if r.URL.Query().Get("injected") != "" {
				t.Errorf("start_cursor injected query param: rawQuery=%q", r.URL.RawQuery)
			}
			if r.URL.Query().Get("start_cursor") == "" {
				_ = json.NewEncoder(w).Encode(paginatedResponse{
					Results:    json.RawMessage(`[{"id":"b_child","type":"paragraph"}]`),
					HasMore:    true,
					NextCursor: "cur&injected=1",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(paginatedResponse{
				Results: json.RawMessage(`[]`),
				HasMore: false,
			})
			return
		}
		unexpectedHits.Add(1)
		http.Error(w, "unexpected path: "+r.URL.Path, http.StatusBadRequest)
	}))
	defer srv.Close()

	cli, err := newClient("ntn_valid", srv.URL+"/")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}

	blocks, err := cli.GetBlockChildrenFlat(context.Background(), "blk1")
	if err != nil {
		t.Fatalf("GetBlockChildrenFlat: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}

	for _, badID := range []string{"", ".", "..", "../users/me", "a/b", "id?x=1", "id#frag"} {
		if _, err := cli.GetPage(context.Background(), badID); err == nil {
			t.Fatalf("expected GetPage(%q) to fail closed", badID)
		}
		if _, err := cli.GetDatabaseInfo(context.Background(), badID); err == nil {
			t.Fatalf("expected GetDatabaseInfo(%q) to fail closed", badID)
		}
		if _, err := cli.GetDataSourceInfo(context.Background(), badID); err == nil {
			t.Fatalf("expected GetDataSourceInfo(%q) to fail closed", badID)
		}
		if _, err := cli.GetBlockChildrenFlat(context.Background(), badID); err == nil {
			t.Fatalf("expected GetBlockChildrenFlat(%q) to fail closed", badID)
		}
		if _, err := cli.GetBlockChildrenAll(context.Background(), badID); err == nil {
			t.Fatalf("expected GetBlockChildrenAll(%q) to fail closed", badID)
		}
		if _, err := cli.ResolveBlock(context.Background(), badID); err == nil {
			t.Fatalf("expected ResolveBlock(%q) to fail closed", badID)
		}
	}

	if hits := unexpectedHits.Load(); hits != 0 {
		t.Fatalf("expected 0 unexpected HTTP hits for unsafe Notion IDs, got %d", hits)
	}
}

func TestW5NotionAudit_ComputeExcludedSetBreaksParentCycleAndDownloadRejectsUserinfo(t *testing.T) {
	allowNotionTestServer(t)

	done := make(chan map[string]bool, 1)
	go func() {
		done <- computeExcludedSet(
			[]string{"p1", "p2"},
			map[string]string{"p1": "p2", "p2": "p1"},
			[]string{"p3"},
		)
	}()

	select {
	case excluded := <-done:
		if !excluded["p1"] || !excluded["p2"] {
			t.Fatalf("expected cyclic unselected nodes to be excluded, got %v", excluded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("computeExcludedSet hung in infinite parent cycle")
	}

	cli, err := newClient("ntn_valid", DefaultBaseURL)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	secret := "s3-secret-pass-w5"
	if _, err := cli.DownloadFile(context.Background(), "http://user:"+secret+"@127.0.0.1/file.pdf"); err == nil {
		t.Fatal("expected DownloadFile to reject embedded userinfo URL")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("DownloadFile leaked secret userinfo in error: %v", err)
	}
}
