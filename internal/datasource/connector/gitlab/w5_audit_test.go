package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
)

func TestW5GitLabAudit_RejectsProjectTraversalAndTokenControlChars(t *testing.T) {
	allowLocalGitLabServer(t)

	var unexpectedHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unexpectedHits.Add(1)
		http.Error(w, "unexpected path: "+r.URL.Path, http.StatusBadRequest)
	}))
	defer srv.Close()

	secret := "glpat-super-secret-token-w5"
	if _, err := newClient(srv.URL, secret+"\r\nX-Injected: 1"); err == nil {
		t.Fatal("expected newClient with control chars in token to fail closed")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret token: %v", err)
	}

	conn := NewConnector()
	for _, badProject := range []string{".", "..", "group/../admin", "group%2F..%2Fadmin", "123?sudo=1", "123#frag"} {
		ds := &types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"base_url":     srv.URL,
				"access_token": "valid-token",
			},
			Settings: map[string]interface{}{
				"projects": []interface{}{
					map[string]interface{}{"project_id": badProject},
				},
			},
		}
		if _, err := parseConfig(ds); err == nil {
			t.Fatalf("expected parseConfig to reject project_id %q", badProject)
		}
		validDS := &types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"base_url":     srv.URL,
				"access_token": "valid-token",
			},
			Settings: map[string]interface{}{
				"projects": []interface{}{
					map[string]interface{}{"project_id": "123"},
				},
			},
		}
		if _, err := conn.ListResources(context.Background(), validDS, badProject+":docs"); err == nil {
			t.Fatalf("expected ListResources to reject parent project %q", badProject)
		}
	}

	validDS := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url":     srv.URL,
			"access_token": "valid-token",
		},
		Settings: map[string]interface{}{
			"projects": []interface{}{
				map[string]interface{}{"project_id": "123", "ref": ".."},
			},
		},
	}
	if _, err := parseConfig(validDS); err == nil {
		t.Fatal("expected parseConfig to reject dot-segment ref '..'")
	}

	validDS2 := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url":     srv.URL,
			"access_token": "valid-token",
		},
		Settings: map[string]interface{}{
			"projects": []interface{}{
				map[string]interface{}{"project_id": "123"},
			},
		},
	}
	if _, err := conn.ListResources(context.Background(), validDS2, "123:../etc"); err == nil {
		t.Fatal("expected ListResources to reject traversal directory '../etc'")
	}

	if hits := unexpectedHits.Load(); hits != 0 {
		t.Fatalf("expected 0 HTTP hits for invalid IDs/refs/paths, got %d", hits)
	}
}

func TestW5GitLabAudit_BreaksCyclicTreeWalkAndEnforcesDecodedBase64Cap(t *testing.T) {
	allowLocalGitLabServer(t)

	var subACalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/1":
			_ = json.NewEncoder(w).Encode(project{
				ID:                1,
				PathWithNamespace: "group/repo",
				Name:              "repo",
				DefaultBranch:     "main",
			})
		case "/api/v4/projects/1/repository/tree":
			dir := r.URL.Query().Get("path")
			switch dir {
			case "":
				_ = json.NewEncoder(w).Encode([]treeEntry{{ID: "t1", Name: "subA", Type: "tree", Path: "subA"}})
			case "subA":
				if subACalls.Add(1) > 4 {
					t.Errorf("infinite directory cycle in walkFiles: subA listed %d times", subACalls.Load())
					http.Error(w, "tree cycle", http.StatusInternalServerError)
					return
				}
				_ = json.NewEncoder(w).Encode([]treeEntry{{ID: "t2", Name: "subB", Type: "tree", Path: "subB"}})
			case "subB":
				_ = json.NewEncoder(w).Encode([]treeEntry{
					{ID: "t1", Name: "subA", Type: "tree", Path: "subA"},
					{ID: "b1", Name: "doc.md", Type: "blob", Path: "subB/doc.md"},
				})
			default:
				http.NotFound(w, r)
			}
		case "/api/v4/projects/1/repository/files/subB%2Fdoc%2Emd/raw":
			_, _ = w.Write([]byte("# cyclic ok"))
		case "/api/v4/projects/1/repository/files/big%2Emd/raw":
			http.NotFound(w, r)
		case "/api/v4/projects/1/repository/files/big%2Emd":
			// 9 decoded bytes (> rawLimit=8), while JSON body easily fits inside base64FileBytes(8)=1036.
			_ = json.NewEncoder(w).Encode(map[string]string{
				"encoding": "base64",
				"content":  base64.StdEncoding.EncodeToString([]byte("123456789")),
			})
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ds := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url":     srv.URL,
			"access_token": "valid-token",
		},
		Settings: map[string]interface{}{
			"projects": []interface{}{
				map[string]interface{}{"project_id": "1"},
			},
		},
	}

	items, err := NewConnector().FetchAll(context.Background(), ds, nil)
	if err != nil {
		t.Fatalf("FetchAll on cyclic tree failed: %v", err)
	}
	if subACalls.Load() != 1 {
		t.Fatalf("expected subA listed once, got %d", subACalls.Load())
	}
	if len(items) != 1 || string(items[0].Content) != "# cyclic ok" {
		t.Fatalf("unexpected items from cyclic tree: %+v", items)
	}

	cli, err := newClient(srv.URL, "valid-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	cli.rawLimit = 8
	if _, err := cli.raw(context.Background(), "1", "main", "big.md"); err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("expected base64 fallback exceeding rawLimit to fail with maximum size error, got %v", err)
	}
}
