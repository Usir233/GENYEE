package dingtalk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func jsonHTTPResponse(req *http.Request, status int, payload any) *http.Response {
	raw, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(raw))),
		Request:    req,
	}
}

func TestW5DingTalkAudit_ConcurrentAccessTokenRefreshAndInvalidationRace(t *testing.T) {
	var tokenSeq atomic.Int64
	var failFirstAuth atomic.Bool
	failFirstAuth.Store(true)

	cli := &client{
		baseURL:   "https://api.dingtalk.test",
		operator:  "union-op-secret",
		appKey:    "app-key-secret",
		appSecret: "app-secret-value",
		sleep:     func(context.Context, time.Duration) error { return nil },
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/v1.0/oauth2/accessToken":
					n := tokenSeq.Add(1)
					return jsonHTTPResponse(req, http.StatusOK, map[string]any{
						"accessToken": "tok-" + string(rune('a'+n%20)),
						"expireIn":    7200,
					}), nil
				case "/v2.0/wiki/workspaces":
					if failFirstAuth.CompareAndSwap(true, false) {
						return jsonHTTPResponse(req, http.StatusUnauthorized, map[string]any{
							"code":    "InvalidAuthentication",
							"message": "expired token " + req.Header.Get("x-acs-dingtalk-access-token"),
						}), nil
					}
					return jsonHTTPResponse(req, http.StatusOK, map[string]any{
						"workspaces": []map[string]any{{"workspaceId": "ws-1", "name": "Ops"}},
					}), nil
				default:
					return jsonHTTPResponse(req, http.StatusBadRequest, map[string]any{
						"code":    "BadRequest",
						"message": "echo app-secret-value",
					}), nil
				}
			}),
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%3 == 0 {
				_ = cli.doJSON(context.Background(), http.MethodGet, "/v2.0/unknown", nil, true, nil)
				return
			}
			ws, err := cli.listWorkspaces(context.Background())
			if err != nil {
				t.Errorf("listWorkspaces failed: %v", err)
				return
			}
			if len(ws) != 1 {
				t.Errorf("unexpected workspace count: %d", len(ws))
			}
		}()
	}
	wg.Wait()
}

func TestW5DingTalkAudit_RejectsDotSegmentIDsAndRedactsSignedDownloadErrors(t *testing.T) {
	for _, raw := range []string{
		".",
		"..",
		"dingtalk:v1?workspace=..&node=n1",
		"dingtalk:v1?workspace=ws1&node=..",
		"dingtalk:v1?workspace=ws1&node=n1&ancestor=..",
		"dingtalk:v1?workspace=.&node=n1",
	} {
		if _, err := decodeResourceReference(raw); err == nil {
			t.Fatalf("expected decodeResourceReference(%q) to fail closed", raw)
		}
	}

	cli := &client{
		baseURL:   "https://api.dingtalk.test",
		operator:  "op-1",
		appKey:    "ak-1",
		appSecret: "sk-1",
		token:     "valid-token",
		tokenExpiry: time.Now().Add(time.Hour),
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected HTTP call with dot-segment ID: %s", req.URL.String())
				return nil, nil
			}),
		},
	}
	if _, err := cli.documentBlocks(context.Background(), ".."); err == nil {
		t.Fatal("expected documentBlocks(\"..\") to fail closed")
	}
	if _, err := cli.resolveDentry(context.Background(), ".."); err == nil {
		t.Fatal("expected resolveDentry(\"..\") to fail closed")
	}
	if _, err := cli.requestDownloadURL(context.Background(), dentryRef{SpaceID: "..", DentryID: "123"}); err == nil {
		t.Fatal("expected requestDownloadURL with SpaceID=\"..\" to fail closed")
	}

	// Control characters in pre-signed header values must not echo raw signature secrets via net/http.
	cli.fileHTTP = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Request:    req,
			}, nil
		}),
	}
	_, _, err := cli.fetchSigned(context.Background(), downloadTarget{
		URL:     "https://oss.dingtalk.test/object",
		Headers: map[string]string{"Authorization": "OSS sig-top-secret\r\nInjected: 1"},
	})
	if err == nil {
		t.Fatal("expected fetchSigned to reject control characters in signed header value")
	}
	if strings.Contains(err.Error(), "sig-top-secret") {
		t.Fatalf("fetchSigned leaked signed header secret in error: %v", err)
	}
}
