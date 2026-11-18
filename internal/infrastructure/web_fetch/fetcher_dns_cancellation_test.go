package web_fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/utils"
)

func assertFetchDNSCancellation(t *testing.T, want error, run func(context.Context) error) {
	t.Helper()
	original := net.DefaultResolver
	started, release := make(chan struct{}), make(chan struct{})
	var signal, releaseOnce sync.Once
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		signal.Do(func() { close(started) })
		select {
		case <-release:
			return nil, fmt.Errorf("audit DNS fixture released")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	defer func() { releaseOnce.Do(func() { close(release) }); net.DefaultResolver = original }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("DNS lookup was never reached")
	}
	if want == context.Canceled {
		cancel()
	}
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Errorf("DNS cancellation error = %v, want %v", err, want)
		}
	case <-time.After(150 * time.Millisecond):
		t.Error("canceled call remains blocked in actual whitelist/SSRF DNS validation")
		releaseOnce.Do(func() { close(release) })
		select {
		case drainErr := <-done:
			_ = drainErr
		case <-time.After(time.Second):
			t.Error("call did not settle after fixture release")
		}
	}
}

func TestWebFetchInitialDNSCancellation(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	assertFetchDNSCancellation(t, context.Canceled, func(ctx context.Context) error {
		_, err := NewPipelineFetcher().Fetch(ctx, "https://audit-slow-fetch.invalid")
		return err
	})
}

func TestWebFetchDNSUsesPageBudget(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	assertFetchDNSCancellation(t, context.DeadlineExceeded, func(ctx context.Context) error {
		fetcher := NewPipelineFetcher()
		fetcher.timeout = 30 * time.Millisecond
		_, err := fetcher.Fetch(ctx, "https://audit-slow-page-budget.invalid")
		code, retry, _ := ErrorDetails(err)
		if code != ErrorTimeout || !retry {
			t.Errorf("page DNS timeout classification = %s, retry=%t", code, retry)
		}
		return err
	})
}

func TestChromiumAndPinnedDialCancelCIDRLookup(t *testing.T) {
	for _, proxy := range []bool{false, true} {
		t.Run(fmt.Sprint(proxy), func(t *testing.T) {
			utils.SetSSRFWhitelistFromRaw("10.0.0.0/8")
			t.Cleanup(utils.ResetSSRFWhitelistForTest)
			assertFetchDNSCancellation(t, context.Canceled, func(ctx context.Context) error {
				if proxy {
					guard := &chromiumSSRFProxy{validateURL: utils.ValidateURLForSSRFContext}
					return guard.validateTargetURL(ctx, "https://audit-proxy-cidr.invalid")
				}
				_, err := NewPipelineFetcher().pinnedDialContext()(ctx, "tcp", "audit-fetch-cidr.invalid:443")
				return err
			})
		})
	}
}

func TestWebFetchBrowserDNSPreservesCancellation(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("status_%d/deadline_%t", status, deadline), func(t *testing.T) {
				utils.SetSSRFWhitelistFromRaw("audit-browser-dns.invalid")
				t.Cleanup(utils.ResetSSRFWhitelistForTest)
				want := context.Canceled
				if deadline {
					want = context.DeadlineExceeded
				}
				assertFetchDNSCancellation(t, want, func(ctx context.Context) error {
					fetcher := NewPipelineFetcher()
					if deadline {
						fetcher.timeout = 30 * time.Millisecond
					}
					fetcher.client = &http.Client{
						Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
							body := `<html><body><div id="app">Loading...</div><script>render()</script></body></html>`
							return &http.Response{
								StatusCode: status,
								Status:     http.StatusText(status),
								Request:    req,
								Header:     http.Header{"Content-Type": []string{"text/html"}},
								Body:       io.NopCloser(strings.NewReader(body)),
							}, nil
						}),
					}
					fetcher.renderBrowser = func(context.Context, pinnedTarget) (string, error) {
						t.Error("renderer should not run after canceled DNS")
						return "", nil
					}
					_, err := fetcher.Fetch(ctx, "https://audit-browser-dns.invalid")
					code, retry, _ := ErrorDetails(err)
					if deadline && (code != ErrorTimeout || !retry) ||
						!deadline && (code != ErrorConnection || retry) {
						t.Errorf("browser DNS cancellation classification = %s, retry=%t", code, retry)
					}
					return err
				})
			})
		}
	}
}
