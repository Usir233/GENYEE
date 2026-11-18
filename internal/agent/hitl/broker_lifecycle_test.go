package hitl

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type blockingRenewStore struct {
	Store
	entered chan struct{}
	release chan struct{}
	active  atomic.Int32
}

func (s *blockingRenewStore) RenewLease(
	ctx context.Context, id, owner string, until time.Time,
) (bool, error) {
	s.active.Add(1)
	defer s.active.Add(-1)
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return s.Store.RenewLease(ctx, id, owner, until)
}

func TestBrokerCloseStopsRedisSubscriberPromptly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  20 * time.Millisecond,
		ReadTimeout:  20 * time.Millisecond,
		WriteTimeout: 20 * time.Millisecond,
		MaxRetries:   0,
	})
	defer func() { _ = rdb.Close() }()

	broker := NewBroker(NewMemoryStore(), rdb)
	// Allow runSubscriber to hit its reconnect backoff timer.
	time.Sleep(40 * time.Millisecond)

	start := time.Now()
	require.NoError(t, broker.Close())
	require.NoError(t, broker.Close(), "Close must be idempotent")
	require.Less(t, time.Since(start), 400*time.Millisecond,
		"Close must interrupt subscriber backoff timer instead of sleeping through backoff")
}

func TestKeepLeaseStopWaitsForInFlightRenewAndIsIdempotent(t *testing.T) {
	store := &blockingRenewStore{
		Store:   NewMemoryStore(),
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	broker := NewBroker(store, nil, withLease(time.Second, 10*time.Millisecond))
	t.Cleanup(func() { _ = broker.Close() })

	stop := broker.KeepLease(context.Background(), "prompt-1", "owner-1")
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for KeepLease renewal")
	}

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("stop() returned while RenewLease was still in flight")
	case <-time.After(30 * time.Millisecond):
	}

	close(store.release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop() did not finish after RenewLease completed")
	}
	require.Zero(t, store.active.Load())
	// Calling stop a second time must be a safe no-op.
	stop()
}
