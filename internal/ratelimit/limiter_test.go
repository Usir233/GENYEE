package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestLocalLimiterAllowsWithinBudget(t *testing.T) {
	l := New(nil, "test:", time.Minute, "inst")
	ctx := context.Background()
	const budget = 3
	for i := 0; i < budget; i++ {
		if !l.Allow(ctx, "k1", budget) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if l.Allow(ctx, "k1", budget) {
		t.Fatal("request over budget should be denied")
	}
}

func TestLocalLimiterIndependentKeys(t *testing.T) {
	l := New(nil, "test:", time.Minute, "inst")
	ctx := context.Background()
	if !l.Allow(ctx, "a", 1) {
		t.Fatal("first key should be allowed")
	}
	if !l.Allow(ctx, "b", 1) {
		t.Fatal("second key should be allowed")
	}
}

func TestAllowSkipsWhenMaxZero(t *testing.T) {
	l := New(nil, "test:", time.Minute, "inst")
	for i := 0; i < 100; i++ {
		if !l.Allow(context.Background(), "k", 0) {
			t.Fatal("max<=0 should always allow")
		}
	}
}

func TestStartCleanupNilStopChannelReturnsWithoutLeakingGoroutine(t *testing.T) {
	l := New(nil, "test:", time.Minute, "inst")
	done := make(chan struct{})
	go func() {
		l.StartCleanup(nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("StartCleanup(nil) blocked forever instead of returning immediately")
	}
}

func TestAllowHandlesNilContextWithoutPanic(t *testing.T) {
	l := New(nil, "test:", time.Minute, "inst")
	var nilCtx context.Context //nolint:staticcheck // intentionally testing nil context defense
	if !l.Allow(nilCtx, "nil-ctx-key", 1) {
		t.Fatal("expected first call with nil context to succeed")
	}
}
