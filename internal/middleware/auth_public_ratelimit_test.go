package middleware

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPublicAuthCleanupPreservesWaitingRequestBudget(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	l := &ipRateLimiter{window: time.Minute, max: 1}
	old := &ipBucket{timestamps: []time.Time{time.Now().Add(-2 * time.Minute)}}
	old.mu.Lock()
	l.buckets.Store("client", old)
	cleanupStarted, cleanupDone := make(chan struct{}), make(chan struct{})
	go func() {
		close(cleanupStarted)
		l.cleanupExpired(time.Now().Add(-l.window))
		close(cleanupDone)
	}()
	<-cleanupStarted
	runtime.Gosched() // The cleanup waits for the held old bucket mutex first.
	requestStarted, requestDone := make(chan struct{}), make(chan bool, 1)
	go func() {
		close(requestStarted)
		requestDone <- l.allow("client")
	}()
	<-requestStarted
	runtime.Gosched() // The request loads the same bucket before waiting.
	old.mu.Unlock()
	<-cleanupDone
	require.True(t, <-requestDone)
	require.False(t, l.allow("client"), "cleanup must not forget the request that waited on its retired bucket")
}

func TestPublicAuthConcurrentCleanupKeepsOneBudget(t *testing.T) {
	l := &ipRateLimiter{window: time.Minute, max: 10}
	l.buckets.Store("client", &ipBucket{timestamps: []time.Time{time.Now().Add(-2 * time.Minute)}})
	var allowed atomic.Int32
	var workers sync.WaitGroup
	for range 100 {
		workers.Go(func() {
			l.cleanupExpired(time.Now().Add(-l.window))
			if l.allow("client") {
				allowed.Add(1)
			}
		})
	}
	workers.Wait()
	require.EqualValues(t, 10, allowed.Load())
}

func TestPublicAuthMiddlewareSharesBudgetAcrossInviteRoutes(t *testing.T) {
	previous := publicAuthLimiter
	publicAuthLimiter = &ipRateLimiter{window: time.Minute, max: publicAuthRateLimitMax}
	t.Cleanup(func() { publicAuthLimiter = previous })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ErrorHandler(), PublicAuthRateLimit())
	paths := []string{"/api/v1/auth/invitations/lookup", "/api/v1/auth/register-by-invite"}
	for _, path := range paths {
		r.POST(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}
	for i := 0; i <= publicAuthRateLimitMax; i++ {
		req := httptest.NewRequest(http.MethodPost, paths[i%len(paths)], nil)
		req.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		want := http.StatusNoContent
		if i == publicAuthRateLimitMax {
			want = http.StatusTooManyRequests
		}
		require.Equal(t, want, w.Code)
	}
}

func TestPublicAuthCleanupRetainsLiveBudgets(t *testing.T) {
	l := &ipRateLimiter{window: time.Minute, max: 2}
	l.buckets.Store("expired", &ipBucket{timestamps: []time.Time{time.Now().Add(-2 * time.Minute)}})
	require.True(t, l.allow("client"))
	require.True(t, l.allow("client"))
	l.cleanupExpired(time.Now().Add(-l.window))
	_, exists := l.buckets.Load("expired")
	require.False(t, exists)
	require.False(t, l.allow("client"))
	require.True(t, l.allow("independent"))
	require.True(t, l.allow(""))
	require.True(t, l.allow(""))
	require.False(t, l.allow(""), "empty IPs share the unknown-client bucket")
}

func TestIPRateLimiterCloseStopsCleanupGoroutine(t *testing.T) {
	l := newIPRateLimiter(time.Minute, 5)
	require.True(t, l.allow("198.51.100.1"))
	l.Close()
	l.Close() // idempotent
}
