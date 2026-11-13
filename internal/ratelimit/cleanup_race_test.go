package ratelimit

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A waiter may have loaded the old entry before cleanup removes it, then
// acquire that entry's mutex after a later request installed a replacement.
func TestCleanupWaiterCannotRemoveReplacementBudget(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	limiter := newLocalLimiter()
	old := &localEntry{}
	old.mu.Lock()
	limiter.entries.Store("channel", old)
	started := make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(started)
		result <- limiter.allow("channel", time.Minute, 1)
	}()
	<-started
	// With one P, yield to let the waiter reach the held entry mutex.
	runtime.Gosched()
	old.deleted = true
	limiter.entries.Delete("channel")
	current := &localEntry{timestamps: []time.Time{time.Now()}}
	limiter.entries.Store("channel", current)
	old.mu.Unlock()
	require.False(t, <-result, "the live replacement has already spent its budget")
	stored, ok := limiter.entries.Load("channel")
	require.True(t, ok)
	require.Same(t, current, stored, "a stale waiter must not delete another generation")
}
