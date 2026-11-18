package ollama

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/stretchr/testify/require"
)

func TestHeartbeatCancellationDoesNotWaitForAnotherRequest(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%t", deadline), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
				} else {
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			defer unblock()
			base, err := url.Parse(server.URL)
			require.NoError(t, err)
			svc := &OllamaService{client: api.NewClient(base, server.Client())}
			first := make(chan error, 1)
			go func() { first <- svc.StartService(t.Context()) }()
			<-started
			ctx, cancel := context.WithCancel(t.Context())
			wantErr := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
				wantErr = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			second := make(chan error, 1)
			go func() { second <- svc.StartService(ctx) }()
			<-ctx.Done()
			select {
			case err := <-second:
				require.ErrorIs(t, err, wantErr)
			case <-time.After(500 * time.Millisecond):
				t.Error("cancelled heartbeat waited on unrelated network IO under the state mutex")
				unblock()
				require.ErrorIs(t, <-second, wantErr)
			}
			unblock()
			require.NoError(t, <-first)
		})
	}
}

func TestAvailabilityReadDoesNotWaitForHeartbeatNetworkIO(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	svc := &OllamaService{client: api.NewClient(base, server.Client()), isAvailable: true}
	done := make(chan error, 1)
	go func() { done <- svc.StartService(t.Context()) }()
	<-started
	available := make(chan bool, 1)
	go func() { available <- svc.IsAvailable() }()
	select {
	case value := <-available:
		require.True(t, value, "read the last completed health check while another is in flight")
	case <-time.After(500 * time.Millisecond):
		t.Error("availability state read waited on heartbeat network IO")
		unblock()
		require.True(t, <-available)
	}
	unblock()
	require.NoError(t, <-done)
}

func TestHeartbeatPreservesAvailabilityAndOptionalFailures(t *testing.T) {
	for _, optional := range []bool{false, true} {
		t.Run(fmt.Sprintf("optional=%t", optional), func(t *testing.T) {
			var fail atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if fail.Load() {
					http.Error(w, "offline", http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			base, err := url.Parse(server.URL)
			require.NoError(t, err)
			svc := &OllamaService{client: api.NewClient(base, server.Client()), isOptional: optional}
			require.NoError(t, svc.StartService(t.Context()))
			require.True(t, svc.IsAvailable())
			fail.Store(true)
			err = svc.StartService(t.Context())
			if optional {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.False(t, svc.IsAvailable())
			fail.Store(false)
			require.NoError(t, svc.StartService(t.Context()))
			require.True(t, svc.IsAvailable())
		})
	}
}
