package confluence

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/datasource"
	"github.com/Usir233/GENYEE/internal/types"
)

func TestW5ConfluenceAudit_RejectsDotSegmentTraversalAndHeaderInjection(t *testing.T) {
	for _, badID := range []string{
		".",
		"..",
		"ENG/sub",
		"ENG\\sub",
		"page:..:123",
		"page:ENG:..",
		"page:.:123",
		"page:ENG:.",
		"page:ENG:12/34",
	} {
		if _, err := parseResourceID(badID); err == nil {
			t.Fatalf("expected parseResourceID(%q) to fail closed", badID)
		}
	}

	gotCloud, err := normalizeBaseURL("https://team.atlassian.net:443", true)
	if err != nil {
		t.Fatalf("normalizeBaseURL cloud with port failed: %v", err)
	}
	if gotCloud != "https://team.atlassian.net:443/wiki" {
		t.Fatalf("normalizeBaseURL cloud with port = %q, want https://team.atlassian.net:443/wiki", gotCloud)
	}

	if _, err := normalizeBaseURL("https://admin:secret@team.atlassian.net/wiki", true); err == nil {
		t.Fatal("expected normalizeBaseURL to reject embedded userinfo")
	}

	if _, err := parseConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url": "https://confluence.example.com/wiki",
			"token":    "pat-secret\r\nX-Injected: 1",
		},
	}); err == nil {
		t.Fatal("expected parseConfig to reject CRLF in PAT secret")
	}

	// Upstream returning a dot-segment page ID must not traverse /rest/api/content/.. -> /rest/api.
	c := &client{
		cfg: config{
			edition: editionServer,
			baseURL: "https://confluence.test/wiki",
			usePAT:  true,
			secret:  "pat-1",
		},
		http: &http.Client{
			Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected HTTP request for dot-segment page ID: %s", req.URL.String())
				return nil, nil
			}),
		},
	}
	if _, err := c.pageDetail(context.Background(), ".."); err == nil {
		t.Fatal("expected pageDetail(\"..\") to fail closed before issuing HTTP request")
	}
}

func TestW5ConfluenceAudit_APIRedirectStaysWithinContextPathAndNilFetchAll(t *testing.T) {
	if _, err := (&Connector{}).FetchAll(context.Background(), nil, []string{"ENG"}); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("FetchAll(nil) error = %v, want ErrInvalidConfig", err)
	}

	calls := 0
	c := &client{
		cfg: config{
			edition: editionServer,
			baseURL: "https://confluence.test/wiki",
			usePAT:  true,
			secret:  "super-secret-pat",
		},
		http: &http.Client{
			Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if !strings.HasPrefix(req.URL.Path, "/wiki/") {
					t.Fatalf("API request escaped /wiki context path to %s (auth=%q)", req.URL.String(), req.Header.Get("Authorization"))
				}
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": []string{"https://confluence.test/other-app/collect"}},
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    req,
				}, nil
			}),
		},
	}
	err := c.ping(context.Background())
	if err == nil {
		t.Fatal("expected same-host out-of-context-path API redirect to fail closed")
	}
	if calls != 1 {
		t.Fatalf("expected out-of-scope redirect to fail without retry or follow, calls=%d", calls)
	}
}
