package yuque

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/datasource"
	"github.com/Usir233/GENYEE/internal/types"
)

func TestW5YuqueAudit_NilIncrementalConfigLoginTraversalAndTokenControlChars(t *testing.T) {
	conn := NewConnector()
	if _, _, err := conn.FetchIncremental(context.Background(), nil, nil); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig on nil FetchIncremental config, got %v", err)
	}

	secret := "yuque-super-secret-token-w5"
	if _, err := parseYuqueConfig(&types.DataSourceConfig{
		Type: types.ConnectorTypeYuque,
		Credentials: map[string]interface{}{
			"api_token": secret + "\r\nX-Injected: 1",
		},
	}); err == nil {
		t.Fatal("expected parseYuqueConfig to reject control chars in api_token")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("parseYuqueConfig leaked secret token in error: %v", err)
	}

	var unexpectedHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unexpectedHits.Add(1)
		http.Error(w, "unexpected path: "+r.URL.Path, http.StatusBadRequest)
	}))
	defer srv.Close()

	cli := newClient(&Config{APIToken: "valid-token", BaseURL: srv.URL})
	for _, badLogin := range []string{"", ".", "..", "../admin", "grp/sub", "grp\\sub", "grp?x=1", "grp#frag"} {
		if _, err := cli.ListUserRepos(context.Background(), badLogin); err == nil {
			t.Fatalf("expected ListUserRepos(%q) to fail closed", badLogin)
		}
		if _, err := cli.ListGroupRepos(context.Background(), badLogin); err == nil {
			t.Fatalf("expected ListGroupRepos(%q) to fail closed", badLogin)
		}
	}
	if hits := unexpectedHits.Load(); hits != 0 {
		t.Fatalf("expected 0 HTTP hits for invalid Yuque logins, got %d", hits)
	}
}
