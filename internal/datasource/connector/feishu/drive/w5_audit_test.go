package drive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/datasource/connector/feishu/core"
)

func TestW5FeishuDriveAudit_ResolveAncestorsBreaksFolderCycleAndValidatesParentID(t *testing.T) {
	var subACalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(w, core.TokenResponse{
				ApiResponse:       core.ApiResponse{Code: 0},
				TenantAccessToken: "fake-token",
				Expire:            7200,
			})
		case "/open-apis/drive/v1/files":
			folder := r.URL.Query().Get("folder_token")
			switch folder {
			case "rootFld":
				writeJSON(w, core.DriveFileListResponse{
					ApiResponse: core.ApiResponse{Code: 0},
					Data: core.DriveFileListData{
						Files: []core.DriveFile{{Token: "subA", Name: "SubA", Type: "folder", ParentToken: "rootFld"}},
					},
				})
			case "subA":
				if subACalls.Add(1) > 4 {
					t.Errorf("infinite folder cycle in ResolveResourceAncestors: subA listed %d times", subACalls.Load())
					http.Error(w, "cycle loop", http.StatusInternalServerError)
					return
				}
				writeJSON(w, core.DriveFileListResponse{
					ApiResponse: core.ApiResponse{Code: 0},
					Data: core.DriveFileListData{
						Files: []core.DriveFile{{Token: "subB", Name: "SubB", Type: "folder", ParentToken: "subA"}},
					},
				})
			case "subB":
				writeJSON(w, core.DriveFileListResponse{
					ApiResponse: core.ApiResponse{Code: 0},
					Data: core.DriveFileListData{
						Files: []core.DriveFile{{Token: "subA", Name: "SubA-loop", Type: "folder", ParentToken: "subB"}},
					},
				})
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	conn := NewDriveConnector(core.RegionFeishuDrive)
	cfg := makeConfig(&core.Config{
		AppID:     "cli_test",
		AppSecret: "sec_test",
		BaseURL:   srv.URL,
	}, []string{"rootFld:missingFile"})

	ancestors, err := conn.ResolveResourceAncestors(context.Background(), cfg, []string{"rootFld:missingFile"})
	if err != nil {
		t.Fatalf("ResolveResourceAncestors failed: %v", err)
	}
	if subACalls.Load() != 1 {
		t.Fatalf("expected subA listed once despite cycle, got %d", subACalls.Load())
	}
	if len(ancestors) != 1 || ancestors[0] != "rootFld" {
		t.Fatalf("unexpected ancestors: %v", ancestors)
	}

	// Malformed ":subA" parentID (empty rootFolderToken) must fail closed.
	if _, err := conn.ListResources(context.Background(), cfg, ":subA"); err == nil {
		t.Fatal("expected ListResources with empty root in ':subA' to fail closed")
	}
}
