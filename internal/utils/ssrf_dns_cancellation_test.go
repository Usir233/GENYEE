package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func assertDNSCancellation(t *testing.T, want error, run func(context.Context) error) {
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
		case err := <-done:
			_ = err
		case <-time.After(time.Second):
			t.Error("call did not settle after fixture release")
		}
	}
}

func TestSSRFSinkDNSCancellation(t *testing.T) {
	SetSSRFWhitelistFromRaw("")
	t.Cleanup(ResetSSRFWhitelistForTest)
	assertDNSCancellation(t, context.Canceled, func(ctx context.Context) error {
		client := NewSSRFSafeHTTPClientWithTransport(DefaultSSRFSafeHTTPClientConfig(), &recordingRoundTripper{})
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://audit-slow-http.invalid", nil)
		resp, err := client.Do(req)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return err
	})
}

func TestSSRFDialCIDRDNSCancellation(t *testing.T) {
	SetSSRFWhitelistFromRaw("10.0.0.0/8")
	t.Cleanup(ResetSSRFWhitelistForTest)
	assertDNSCancellation(t, context.Canceled, func(ctx context.Context) error {
		_, err := SSRFSafeDialContext(ctx, "tcp", "audit-slow-dial.invalid:443")
		return err
	})
}

func TestSSRFSinkDNSHonorsHTTPClientTimeout(t *testing.T) {
	SetSSRFWhitelistFromRaw("")
	t.Cleanup(ResetSSRFWhitelistForTest)
	assertDNSCancellation(t, context.DeadlineExceeded, func(ctx context.Context) error {
		config := DefaultSSRFSafeHTTPClientConfig()
		config.Timeout = 30 * time.Millisecond
		client := NewSSRFSafeHTTPClientWithTransport(config, &recordingRoundTripper{})
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://audit-slow-timeout.invalid", nil)
		resp, err := client.Do(req)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return err
	})
}
