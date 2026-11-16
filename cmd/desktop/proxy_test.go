//go:build !bindings

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDesktopAPIProxyRewritesWailsHostToLoopback(t *testing.T) {
	var got string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	target, err := url.Parse(backend.URL)
	require.NoError(t, err)
	front := httptest.NewServer(desktopAPIProxy(target))
	defer front.Close()

	req, err := http.NewRequest(http.MethodPost, front.URL+"/api/v1/me/browser", nil)
	require.NoError(t, err)
	req.Host = "wails.localhost"
	resp, err := front.Client().Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, target.Host, got)
}

func TestDesktopAPIProxyKeepsHostForOtherRoutes(t *testing.T) {
	var got string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	target, err := url.Parse(backend.URL)
	require.NoError(t, err)
	front := httptest.NewServer(desktopAPIProxy(target))
	defer front.Close()

	req, err := http.NewRequest(http.MethodGet, front.URL+"/api/v1/auth/oidc/start", nil)
	require.NoError(t, err)
	req.Host = "wails.localhost"
	resp, err := front.Client().Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, "wails.localhost", got)
}

func TestNewDesktopHTTPServer_HasSlowlorisAndHeaderTimeouts(t *testing.T) {
	srv := newDesktopHTTPServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	require.Greater(t, srv.ReadHeaderTimeout, int64(0))
	require.Greater(t, srv.IdleTimeout, int64(0))
	require.Greater(t, srv.MaxHeaderBytes, 0)
}

func TestApp_ConcurrentEndpointsAndStartupRaceFree(t *testing.T) {
	app := NewApp()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			app.startup(context.Background())
			app.setBackendEndpoints("http://127.0.0.1:18080", "http://192.168.1.2:18080/api/v1", idx%2 == 0)
		}(i)
		go func() {
			defer wg.Done()
			_ = app.GetAPIBaseURL()
			_ = app.GetAPILanBaseURL()
			_ = app.GetDesktopListenPublicActive()
			_ = app.getContext()
		}()
	}
	wg.Wait()
	require.Equal(t, "http://127.0.0.1:18080/api/v1", app.GetAPIBaseURL())
}
