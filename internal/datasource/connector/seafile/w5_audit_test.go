package seafile

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/datasource"
	"github.com/Usir233/GENYEE/internal/types"
)

func TestW5SeafileAudit_BoundsRunawayDirectoryDepth(t *testing.T) {
	const repoUUID = "11111111-1111-1111-1111-111111111111"
	var dirCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api2/repos/":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": repoUUID, "name": "LoopLib", "type": "repo", "encrypted": false},
			})
			return
		case "/api2/repos/" + repoUUID + "/dir/":
			if dirCalls.Add(1) > 400 {
				t.Errorf("unbounded directory recursion in walkDir: dir listed %d times", dirCalls.Load())
				http.Error(w, "runaway depth", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "d1", "type": "dir", "name": "loop", "mtime": 1700000000},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cfg := &types.DataSourceConfig{
		Type: types.ConnectorTypeSeafile,
		Credentials: map[string]interface{}{
			"base_url":  srv.URL,
			"api_token": "tok-test",
		},
		ResourceIDs: []string{repoUUID + ":/"},
	}

	rec := &recorder{}
	_, err := NewConnector().FetchStream(context.Background(), cfg, nil, rec)
	if !errors.Is(err, datasource.ErrFetchFailed) {
		t.Fatalf("expected ErrFetchFailed on runaway directory depth, got %v", err)
	}
	if calls := dirCalls.Load(); calls < 200 || calls > 300 {
		t.Fatalf("expected walkDir to abort at maxTraversalDepth (200..300 dir calls), got %d", calls)
	}
}
