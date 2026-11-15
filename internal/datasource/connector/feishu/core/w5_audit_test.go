package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestW5FeishuCoreAudit_EscapesPathAndQueryAndRejectsDotSegments(t *testing.T) {
	cfg := &Config{
		AppID:     "cli_test",
		AppSecret: "sec_test",
		BaseURL:   "https://open.feishu.cn///",
	}
	if got := cfg.GetBaseURL(); got != "https://open.feishu.cn" {
		t.Fatalf("GetBaseURL() = %q, want https://open.feishu.cn", got)
	}

	var unexpectedHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(TokenResponse{
				ApiResponse:       ApiResponse{Code: 0},
				TenantAccessToken: "tok-test",
				Expire:            7200,
			})
			return
		}
		// Check query escaping on ListWikiSpaces page_token with '&'
		if r.URL.Path == "/open-apis/wiki/v2/spaces" {
			if r.URL.Query().Get("injected") != "" {
				t.Errorf("page_token injected query param: rawQuery=%q", r.URL.RawQuery)
			}
			if r.URL.Query().Get("page_token") == "" {
				_ = json.NewEncoder(w).Encode(WikiSpaceListResponse{
					ApiResponse: ApiResponse{Code: 0},
					Data: WikiSpaceListData{
						HasMore:   true,
						PageToken: "tok&injected=1",
						Items:     []WikiSpace{{SpaceID: "sp1", Name: "Space"}},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(WikiSpaceListResponse{
				ApiResponse: ApiResponse{Code: 0},
				Data:        WikiSpaceListData{HasMore: false},
			})
			return
		}
		unexpectedHits.Add(1)
		http.Error(w, "unexpected path: "+r.URL.Path, http.StatusBadRequest)
	}))
	defer srv.Close()

	cli := NewClient(&Config{
		AppID:     "cli_test",
		AppSecret: "sec_test",
		BaseURL:   srv.URL + "/",
	})
	cli.httpClient = srv.Client()

	spaces, err := cli.ListWikiSpaces(context.Background())
	if err != nil {
		t.Fatalf("ListWikiSpaces error: %v", err)
	}
	if len(spaces) != 1 {
		t.Fatalf("len(spaces) = %d, want 1", len(spaces))
	}

	// Dot-segment and slash traversal IDs must fail closed before issuing API requests.
	for _, badID := range []string{"..", ".", "a/b", "a\\b", "id?x=1"} {
		if _, err := cli.ListWikiNodes(context.Background(), badID, ""); err == nil {
			t.Fatalf("expected ListWikiNodes(%q) to fail closed", badID)
		}
		if _, err := cli.DownloadDriveFile(context.Background(), badID); err == nil {
			t.Fatalf("expected DownloadDriveFile(%q) to fail closed", badID)
		}
		if _, err := cli.GetDriveFolderMeta(context.Background(), badID); err == nil {
			t.Fatalf("expected GetDriveFolderMeta(%q) to fail closed", badID)
		}
		if _, err := cli.listDocumentBlocks(context.Background(), badID); err == nil {
			t.Fatalf("expected listDocumentBlocks(%q) to fail closed", badID)
		}
	}
	if hits := unexpectedHits.Load(); hits != 0 {
		t.Fatalf("expected 0 unexpected HTTP hits for unsafe IDs, got %d", hits)
	}
}

func TestW5FeishuCoreAudit_BreaksWikiNodeCyclesAndRepeatedBlockRecordTokens(t *testing.T) {
	var n1Calls atomic.Int64
	var blockCalls atomic.Int64
	var recCalls atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(TokenResponse{
				ApiResponse:       ApiResponse{Code: 0},
				TenantAccessToken: "tok-test",
				Expire:            7200,
			})
		case "/open-apis/wiki/v2/spaces/sp1/nodes":
			parent := r.URL.Query().Get("parent_node_token")
			switch parent {
			case "":
				_ = json.NewEncoder(w).Encode(WikiNodeListResponse{
					ApiResponse: ApiResponse{Code: 0},
					Data: WikiNodeListData{
						Items: []WikiNode{{SpaceID: "sp1", NodeToken: "n1", Title: "N1", HasChild: true}},
					},
				})
			case "n1":
				if n1Calls.Add(1) > 4 {
					http.Error(w, "cycle loop", http.StatusInternalServerError)
					return
				}
				_ = json.NewEncoder(w).Encode(WikiNodeListResponse{
					ApiResponse: ApiResponse{Code: 0},
					Data: WikiNodeListData{
						Items: []WikiNode{{SpaceID: "sp1", NodeToken: "n2", Title: "N2", HasChild: true}},
					},
				})
			case "n2":
				_ = json.NewEncoder(w).Encode(WikiNodeListResponse{
					ApiResponse: ApiResponse{Code: 0},
					Data: WikiNodeListData{
						Items: []WikiNode{{SpaceID: "sp1", NodeToken: "n1", Title: "N1-cycle", HasChild: true}},
					},
				})
			}
		case "/open-apis/docx/v1/documents/doc1/blocks":
			if blockCalls.Add(1) > 4 {
				http.Error(w, "repeated block page_token not detected", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(DocxBlocksResponse{
				ApiResponse: ApiResponse{Code: 0},
				Data: DocxBlocksData{
					HasMore:   true,
					PageToken: "same-block-tok",
					Items:     []DocxBlock{{BlockID: "b1", BlockType: 2}},
				},
			})
		case "/open-apis/bitable/v1/apps/app1/tables/tbl1/fields":
			_ = json.NewEncoder(w).Encode(bitableFieldsResponse{
				ApiResponse: ApiResponse{Code: 0},
				Data: bitableFieldsData{
					Items: []bitableField{{FieldName: "Name", Type: 1}},
				},
			})
		case "/open-apis/bitable/v1/apps/app1/tables/tbl1/records/search":
			if recCalls.Add(1) > 4 {
				http.Error(w, "repeated record page_token not detected", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(bitableRecordsResponse{
				ApiResponse: ApiResponse{Code: 0},
				Data: bitableRecordsData{
					HasMore:   true,
					PageToken: "same-rec-tok",
					Items:     []bitableRecord{{Fields: map[string]any{"Name": "Row"}}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cli := NewClient(&Config{AppID: "cli_test", AppSecret: "sec_test", BaseURL: srv.URL})
	cli.httpClient = srv.Client()

	nodes, err := cli.listAllWikiNodesRecursive(context.Background(), "sp1")
	if err != nil {
		t.Fatalf("listAllWikiNodesRecursive failed on cycle: %v", err)
	}
	if n1Calls.Load() != 1 {
		t.Fatalf("expected cycle node n1 children listed once, got %d", n1Calls.Load())
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 unique wiki nodes in cyclic tree, got %d", len(nodes))
	}

	if _, err := cli.listDocumentBlocks(context.Background(), "doc1"); err == nil || blockCalls.Load() > 2 {
		t.Fatalf("expected listDocumentBlocks repeated page_token error in <=2 calls, calls=%d err=%v", blockCalls.Load(), err)
	}
	if _, _, err := cli.readBitableRecords(context.Background(), "app1_tbl1"); err == nil || recCalls.Load() > 2 {
		t.Fatalf("expected readBitableRecords repeated page_token error in <=2 calls, calls=%d err=%v", recCalls.Load(), err)
	}
}
