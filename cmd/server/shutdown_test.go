package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func startTestServer(t *testing.T) (*http.Server, net.Listener, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()
	waitUntilAccepting(t, ln.Addr().String())
	return srv, ln, errCh
}

func waitUntilAccepting(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s did not accept", addr)
}

func TestShutdownReturnsErrServerClosed(t *testing.T) {
	srv, _, errCh := startTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve err = %v, want ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestManualListenerCloseIsNotErrServerClosed(t *testing.T) {
	_, ln, errCh := startTestServer(t)
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve err = %v, want the raw accept error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestNewHTTPServer_SlowlorisAndHeaderGuards(t *testing.T) {
	srv := newHTTPServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout = %v, want > 0 to mitigate Slowloris", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %v, want > 0 to reclaim idle connections", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Fatalf("MaxHeaderBytes = %d, want > 0 to cap request headers", srv.MaxHeaderBytes)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v, want 0 so long-lived SSE streams are not truncated", srv.WriteTimeout)
	}
}

func TestFormatServerAddr_IPv4IPv6AndEmptyHost(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"", 8080, ":8080"},
		{"   ", 8080, ":8080"},
		{"127.0.0.1", 8080, "127.0.0.1:8080"},
		{"0.0.0.0", 9090, "0.0.0.0:9090"},
		{"::1", 8080, "[::1]:8080"},
		{"[::1]", 8080, "[::1]:8080"},
		{"::", 8080, "[::]:8080"},
	}
	for _, tc := range cases {
		if got := formatServerAddr(tc.host, tc.port); got != tc.want {
			t.Errorf("formatServerAddr(%q, %d) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestRunHTTPServerLifecycle_CancelsBackgroundBeforeCleanupAndOnServeError(t *testing.T) {
	// 1. Signal-driven shutdown must cancel background subscribers BEFORE resourceCleaner.Cleanup runs.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := newHTTPServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	sigCh := make(chan os.Signal, 1)

	var bgCtx context.Context
	var bgCancelledBeforeCleanup atomic.Bool
	var cleanupCompleted atomic.Bool

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- runHTTPServerLifecycle(
			srv,
			ln,
			sigCh,
			2*time.Second,
			func(ctx context.Context) { bgCtx = ctx },
			func(ctx context.Context) []error {
				select {
				case <-bgCtx.Done():
					bgCancelledBeforeCleanup.Store(true)
				default:
				}
				time.Sleep(25 * time.Millisecond)
				cleanupCompleted.Store(true)
				return nil
			},
		)
	}()

	waitUntilAccepting(t, ln.Addr().String())
	sigCh <- syscall.SIGTERM

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("runHTTPServerLifecycle err = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runHTTPServerLifecycle did not finish")
	}

	if !bgCancelledBeforeCleanup.Load() {
		t.Fatal("background context was NOT cancelled before resource cleanup ran")
	}
	if !cleanupCompleted.Load() {
		t.Fatal("runHTTPServerLifecycle returned before resource cleanup finished")
	}

	// 2. Non-ErrServerClosed Serve error must still cancel background context (no goroutine leak).
	lnErr, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = lnErr.Close() // Force immediate Serve error

	var errBgCtx context.Context
	err = runHTTPServerLifecycle(
		newHTTPServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})),
		lnErr,
		make(chan os.Signal, 1),
		time.Second,
		func(ctx context.Context) { errBgCtx = ctx },
		nil,
	)
	if err == nil {
		t.Fatal("expected non-nil error when listener is already closed")
	}
	select {
	case <-errBgCtx.Done():
	default:
		t.Fatal("background context leaked (not cancelled) after Serve failure")
	}
}
