package collaboration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestZeroConfigRuntimeWaitAndRestartDoNotPanic(t *testing.T) {
	store, scope := testStore(t)
	rt := &Runtime{Store: store}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	require.NotPanics(t, func() {
		updated, err := rt.Wait(ctx, scope, 0, 5*time.Millisecond)
		require.NoError(t, err)
		require.False(t, updated)
	})

	require.NotPanics(t, func() {
		rt.Start(context.Background())
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		require.NoError(t, rt.Stop(stopCtx))

		// Runtime must be cleanly restartable after Stop completes.
		rt.Start(context.Background())
		stopCtx2, stopCancel2 := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel2()
		require.NoError(t, rt.Stop(stopCtx2))
	})
}

func TestBindingEndAndWakeParentReportNonFenceStoreErrors(t *testing.T) {
	store, scope := testStore(t)

	var mu sync.Mutex
	var recordedOps []string
	rt := New(store, nil)
	rt.OnStoreError = func(_ context.Context, _ Scope, op string, _ error) {
		mu.Lock()
		recordedOps = append(recordedOps, op)
		mu.Unlock()
	}

	b, err := rt.AcquireParent(context.Background(), scope)
	require.NoError(t, err)

	sqlDB, err := store.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	b.End(true)
	b.End(false)

	mu.Lock()
	ops := append([]string(nil), recordedOps...)
	mu.Unlock()
	require.Contains(t, ops, "end_suspend_release")
	require.Contains(t, ops, "end_cancel")
}
