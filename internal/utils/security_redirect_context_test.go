package utils

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSSRFRedirectDNSHonorsHTTPClientTimeout(t *testing.T) {
	SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(ResetSSRFWhitelistForTest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://audit-redirect-timeout.invalid/", http.StatusFound)
	}))
	defer server.Close()

	assertDNSCancellation(t, context.DeadlineExceeded, func(ctx context.Context) error {
		config := DefaultSSRFSafeHTTPClientConfig()
		config.Timeout = 30 * time.Millisecond
		client := NewSSRFSafeHTTPClient(config)
		defer client.CloseIdleConnections()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		return err
	})
}

func TestSSRFRedirectValidationKeepsEarlierDeadline(t *testing.T) {
	for _, earlierResponse := range []bool{false, true} {
		name := "request"
		if earlierResponse {
			name = "previous response"
		}
		t.Run(name, func(t *testing.T) {
			SetSSRFWhitelistFromRaw("")
			t.Cleanup(ResetSSRFWhitelistForTest)
			assertDNSCancellation(t, context.DeadlineExceeded, func(ctx context.Context) error {
				requestBudget, responseBudget := 30*time.Millisecond, time.Hour
				if earlierResponse {
					requestBudget, responseBudget = responseBudget, requestBudget
				}
				requestCtx, cancelRequest := context.WithTimeout(ctx, requestBudget)
				defer cancelRequest()
				responseCtx, cancelResponse := context.WithTimeout(context.Background(), responseBudget)
				defer cancelResponse()
				req, _ := http.NewRequestWithContext(
					requestCtx, http.MethodGet, "https://audit-redirect-earlier.invalid/", nil,
				)
				previous, _ := http.NewRequestWithContext(responseCtx, http.MethodGet, "https://previous.example/", nil)
				req.Response = &http.Response{Request: previous}
				return newSSRFCheckRedirect(10)(req, nil)
			})
		})
	}
}

func TestSSRFRedirectValidationKeepsRequestCancellation(t *testing.T) {
	SetSSRFWhitelistFromRaw("")
	t.Cleanup(ResetSSRFWhitelistForTest)
	assertDNSCancellation(t, context.Canceled, func(ctx context.Context) error {
		previousCtx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://audit-redirect-cancel.invalid/", nil)
		previous, _ := http.NewRequestWithContext(previousCtx, http.MethodGet, "https://previous.example/", nil)
		req.Response = &http.Response{Request: previous}
		return newSSRFCheckRedirect(10)(req, nil)
	})
}

func TestSSRFRedirectValidationIgnoresPreviousResponseCancellation(t *testing.T) {
	SetSSRFWhitelistFromRaw("redirect-approved.example")
	t.Cleanup(ResetSSRFWhitelistForTest)
	previousCtx, cancel := context.WithTimeout(context.Background(), time.Hour)
	previous, _ := http.NewRequestWithContext(previousCtx, http.MethodGet, "https://previous.example/", nil)
	// Closing the previous response body can cancel its request context even
	// while the redirect's original request and overall deadline remain valid.
	cancel()
	req, _ := http.NewRequest(http.MethodGet, "https://redirect-approved.example/", nil)
	req.Response = &http.Response{Request: previous}
	if err := newSSRFCheckRedirect(10)(req, nil); err != nil {
		t.Fatalf("previous response cancellation leaked into redirect: %v", err)
	}
}
