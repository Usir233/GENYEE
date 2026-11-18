package utils

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type observedSSRFWaitContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *observedSSRFWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestOutboundSSRFWaiterCancellationIsIndependent(t *testing.T) {
	for _, cancelLeader := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel waiter", true: "cancel leader"}[cancelLeader], func(t *testing.T) {
			SetSSRFWhitelistFromRaw("cache-context.example")
			t.Cleanup(ResetSSRFWhitelistForTest)
			const target = "https://cache-context.example/page"
			key, ok := outboundSSRFCacheKey(target)
			require.True(t, ok)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(release) }) }
			defer finish()
			// Hold an existing flight at its validation boundary. The real
			// waiter must join it, then handle cancellation independently.
			leader := currentOutboundSSRFState().group.DoChan(key, func() (any, error) {
				close(entered)
				<-release
				if cancelLeader {
					return nil, context.Canceled
				}
				return nil, nil
			})
			<-entered
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waiterCtx := &observedSSRFWaitContext{Context: ctx, waiting: make(chan struct{})}
			result := make(chan error, 1)
			go func() { result <- validateURLForSSRFForOutboundContext(waiterCtx, target) }()
			select {
			case <-waiterCtx.waiting:
			case <-time.After(time.Second):
				t.Fatal("waiter did not join shared validation")
			}
			if cancelLeader {
				finish()
			} else {
				cancel()
			}
			select {
			case err := <-result:
				if cancelLeader {
					require.NoError(t, err, "active caller retries a canceled flight")
				} else {
					require.ErrorIs(t, err, context.Canceled, "waiter must not wait for leader release")
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not settle the caller")
			}
			finish()
			<-leader
			require.NoError(
				t, validateURLForSSRFForOutboundContext(context.Background(), target), "cancellation is not cached",
			)
		})
	}
}
