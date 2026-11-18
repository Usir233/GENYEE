package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestOAuthAttemptCompletesOnlyAfterCallbackExchange(t *testing.T) {
	ctx := context.Background()
	store := newOAuthStateStore(nil)
	t.Cleanup(store.Close)
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "user-1"}
	state := OAuthState{
		TenantID:  7,
		Principal: principal,
		ServiceID: "service-1",
	}

	require.NoError(t, store.Put(ctx, "attempt-1", state))

	attempt, err := store.Attempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.False(t, attempt.Completed)

	// Consuming the OAuth state means the provider callback started. It must
	// not look successful until the code exchange and token persistence finish.
	_, err = store.Take(ctx, "attempt-1")
	require.NoError(t, err)
	attempt, err = store.Attempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.False(t, attempt.Completed)

	require.NoError(t, store.CompleteAttempt(ctx, "attempt-1"))
	attempt, err = store.Attempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.True(t, attempt.Completed)
	require.Equal(t, uint64(7), attempt.TenantID)
	require.Equal(t, principal.Normalize(), attempt.Principal)
	require.Equal(t, "service-1", attempt.ServiceID)
}

func TestAuthorizationAttemptStatusIsScopedToPrincipalAndService(t *testing.T) {
	ctx := context.Background()
	manager := &OAuthManager{states: newOAuthStateStore(nil)}
	t.Cleanup(manager.Close)
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "user-1"}

	require.NoError(t, manager.states.Put(ctx, "attempt-1", OAuthState{
		TenantID:  7,
		Principal: principal,
		ServiceID: "service-1",
	}))
	require.NoError(t, manager.states.CompleteAttempt(ctx, "attempt-1"))

	completed, err := manager.IsAuthorizationAttemptComplete(
		ctx, 7, principal, "service-1", "attempt-1",
	)
	require.NoError(t, err)
	require.True(t, completed)

	_, err = manager.IsAuthorizationAttemptComplete(
		ctx, 7, types.Principal{Type: types.PrincipalWebUser, ID: "user-2"}, "service-1", "attempt-1",
	)
	require.ErrorContains(t, err, "does not match")

	_, err = manager.IsAuthorizationAttemptComplete(
		ctx, 7, principal, "service-2", "attempt-1",
	)
	require.ErrorContains(t, err, "does not match")
}

func TestOAuthStateStoreSweepsExpiredEntriesAndStopsOnClose(t *testing.T) {
	ctx := context.Background()
	manager := NewOAuthManager(nil, nil, nil)
	store := manager.states
	store.gcInterval = 10 * time.Millisecond
	t.Cleanup(manager.Close)

	require.False(t, store.isGCRunning(), "GC ticker must not run while store is empty")

	principal := types.Principal{Type: types.PrincipalWebUser, ID: "user-gc"}
	require.NoError(t, store.Put(ctx, "expired-1", OAuthState{
		TenantID:  1,
		Principal: principal,
		ServiceID: "svc-gc",
	}))
	require.True(t, store.isGCRunning(), "Put must start in-memory GC loop")

	store.mu.Lock()
	past := time.Now().Add(-time.Second)
	st := store.mem["expired-1"]
	st.expiresAt = past
	store.mem["expired-1"] = st
	att := store.attempts["expired-1"]
	att.expiresAt = past
	store.attempts["expired-1"] = att
	store.mu.Unlock()

	require.Eventually(t, func() bool {
		store.mu.Lock()
		empty := len(store.mem) == 0 && len(store.attempts) == 0
		store.mu.Unlock()
		return empty && !store.isGCRunning()
	}, time.Second, 5*time.Millisecond, "expired entries must be swept and idle GC loop must exit")

	require.NoError(t, store.Put(ctx, "live-1", OAuthState{
		TenantID:  1,
		Principal: principal,
		ServiceID: "svc-gc",
	}))
	require.True(t, store.isGCRunning(), "subsequent Put must restart GC loop")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.Close()
		}()
	}
	wg.Wait()

	require.False(t, store.isGCRunning(), "Close must stop GC loop deterministically")
	require.ErrorContains(t, store.Put(ctx, "after-close", OAuthState{
		TenantID:  1,
		Principal: principal,
		ServiceID: "svc-gc",
	}), "closed")
}
