package wiki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/datasource/connector/feishu/core"
	"github.com/Usir233/GENYEE/internal/types"
)

func TestW5FeishuWikiAudit_ResolveAncestorsBreaksParentCycleAndRejectsEmptySpaceID(t *testing.T) {
	var getNodeCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(w, core.TokenResponse{
				ApiResponse:       core.ApiResponse{Code: 0},
				TenantAccessToken: "fake-token",
				Expire:            7200,
			})
		case "/open-apis/wiki/v2/spaces/get_node":
			if getNodeCalls.Add(1) > 6 {
				t.Errorf(
					"infinite parent cycle in ResolveResourceAncestors: get_node called %d times",
					getNodeCalls.Load(),
				)
				http.Error(w, "parent cycle loop", http.StatusInternalServerError)
				return
			}
			tok := r.URL.Query().Get("token")
			parent := "nodeA"
			if tok == "nodeA" {
				parent = "nodeB"
			}
			writeJSON(w, core.WikiNodeInfoResponse{
				ApiResponse: core.ApiResponse{Code: 0},
				Data: core.WikiNodeInfoData{
					Node: core.WikiNode{
						SpaceID:      "space1",
						NodeToken:    tok,
						ParentNodeID: parent,
					},
				},
			})
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	conn := NewConnector(core.RegionFeishu)
	cfg := &types.DataSourceConfig{
		Type: types.ConnectorTypeFeishu,
		Credentials: map[string]interface{}{
			"app_id":     "cli_test",
			"app_secret": "sec_test",
			"base_url":   srv.URL,
		},
		ResourceIDs: []string{":nodeOnly"},
	}

	_, err := conn.ResolveResourceAncestors(context.Background(), cfg, []string{"space1:nodeA"})
	if err != nil {
		t.Fatalf("ResolveResourceAncestors error: %v", err)
	}
	if getNodeCalls.Load() > 2 {
		t.Fatalf("expected parent cycle to stop after <=2 GetWikiNode calls, got %d", getNodeCalls.Load())
	}

	if _, err := conn.ListResources(context.Background(), cfg, ":nodeOnly"); err == nil {
		t.Fatal("expected ListResources with ':nodeOnly' (empty spaceID) to fail closed")
	}
	if _, err := conn.FetchAll(context.Background(), cfg, []string{":nodeOnly"}); err == nil {
		t.Fatal("expected FetchAll with ':nodeOnly' (empty spaceID) to fail closed")
	}
}
