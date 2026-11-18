package embedding

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/require"
)

type cancellationEmbedder struct {
	Embedder
	calls atomic.Int32
	run   func(context.Context, []string) ([][]float32, error)
}

func (m *cancellationEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	m.calls.Add(1)
	if m.run != nil {
		return m.run(ctx, texts)
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = []float32{float32(len(text))}
	}
	return out, nil
}

func cancellationPool(t *testing.T, options ...ants.Option) *ants.Pool {
	t.Helper()
	pool, err := ants.NewPool(1, options...)
	require.NoError(t, err)
	t.Cleanup(pool.Release)
	return pool
}

func holdCancellationPool(t *testing.T, pool *ants.Pool) func() {
	t.Helper()
	released := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	t.Cleanup(release)
	require.NoError(t, pool.Submit(func() { <-released }))
	return release
}

func awaitBatchCancellation(t *testing.T, done <-chan error, want error) bool {
	t.Helper()
	select {
	case err := <-done:
		require.ErrorIs(t, err, want)
		return true
	case <-time.After(5 * time.Second):
		t.Error("cancelled embedding request is still waiting for pool work")
		return false
	}
}

// Free counts unallocated workers, not idle workers. Submit a marker after
// queued submissions have been accepted to observe completion instead.
func awaitCancellationPoolIdle(t *testing.T, pool *ants.Pool) {
	t.Helper()
	require.Eventually(t, func() bool { return pool.Waiting() == 0 }, 5*time.Second, time.Millisecond)
	done := make(chan struct{})
	submitted := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		for {
			err := pool.Submit(func() { close(done) })
			if !errors.Is(err, ants.ErrPoolOverload) {
				submitted <- err
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	select {
	case err := <-submitted:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("pool still occupied after cancellation")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("pool completion marker did not run")
	}
}

func TestBatchEmbedWithPoolAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	model := &cancellationEmbedder{}
	got, err := NewBatchEmbedder(cancellationPool(t)).BatchEmbedWithPool(ctx, model, []string{"text"})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, got)
	require.Zero(t, model.calls.Load())
}

func TestBatchEmbedWithPoolCancellationWhileQueued(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			pool := cancellationPool(t)
			release := holdCancellationPool(t, pool)
			ctx, cancel := context.WithCancel(context.Background())
			want := error(context.Canceled)
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
				want = context.DeadlineExceeded
			}
			defer cancel()
			model := &cancellationEmbedder{}
			done := make(chan error, 1)
			go func() {
				_, err := NewBatchEmbedder(pool).BatchEmbedWithPool(ctx, model, []string{"text"})
				done <- err
			}()
			require.Eventually(t, func() bool { return pool.Waiting() == 1 }, 5*time.Second, time.Millisecond)
			if !deadline {
				cancel()
			}
			<-ctx.Done()
			returned := awaitBatchCancellation(t, done, want)
			release()
			if !returned {
				require.ErrorIs(t, <-done, want)
			}
			awaitCancellationPoolIdle(t, pool)
			require.Zero(t, model.calls.Load(), "a cancelled queued task must never call the provider")
		})
	}
}

func TestBatchEmbedWithPoolCancellationWhileRunning(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	pool := cancellationPool(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once, enterOnce sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	model := &cancellationEmbedder{run: func(context.Context, []string) ([][]float32, error) {
		enterOnce.Do(func() { close(entered) })
		<-release // Even an uncooperative provider must not pin the caller.
		return [][]float32{{1}}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewBatchEmbedder(pool).BatchEmbedWithPool(ctx, model, []string{"first", "second"})
		done <- err
	}()
	<-entered
	require.Eventually(t, func() bool { return pool.Waiting() == 1 }, 5*time.Second, time.Millisecond)
	cancel()
	returned := awaitBatchCancellation(t, done, context.Canceled)
	unblock()
	if !returned {
		require.ErrorIs(t, <-done, context.Canceled)
	}
	awaitCancellationPoolIdle(t, pool)
	require.EqualValues(t, 1, model.calls.Load(), "later chunks must not run after cancellation")
}

func TestBatchEmbedWithPoolSubmitFailureCancelsStartedWork(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	pool := cancellationPool(t, ants.WithNonblocking(true))
	providerDone := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &cancellationEmbedder{run: func(ctx context.Context, _ []string) ([][]float32, error) {
		defer close(providerDone)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	_, err := NewBatchEmbedder(pool).BatchEmbedWithPool(ctx, model, []string{"first", "second"})
	require.ErrorIs(t, err, ants.ErrPoolOverload)
	// If the callback had not started, cancellation may skip it altogether.
	// Either way the submitted worker must become available again.
	awaitCancellationPoolIdle(t, pool)
	if model.calls.Load() > 0 {
		<-providerDone
	}
}

func TestBatchEmbedWithPoolCancelledWaitersStayBounded(t *testing.T) {
	pool := cancellationPool(t)
	release := holdCancellationPool(t, pool)
	pooler := NewBatchEmbedder(pool)
	model := &cancellationEmbedder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := pooler.BatchEmbedWithPool(ctx, model, []string{"first"})
		done <- err
	}()
	require.Eventually(t, func() bool { return pool.Waiting() == 1 }, 5*time.Second, time.Millisecond)
	cancel()
	if !awaitBatchCancellation(t, done, context.Canceled) {
		release()
		drainErr := <-done
		_ = drainErr
		return
	}
	for range 8 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, err := pooler.BatchEmbedWithPool(ctx, model, []string{"later"})
		cancel()
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}
	require.LessOrEqual(t, pool.Waiting(), 1, "cancelled calls must not each leave an ants Submit waiter")
	release()
	awaitCancellationPoolIdle(t, pool)
	require.Zero(t, model.calls.Load())
}

func TestBatchEmbedWithPoolSuccessfulOrderAndClosedPool(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	pool := cancellationPool(t)
	pooler := NewBatchEmbedder(pool)
	got, err := pooler.BatchEmbedWithPool(context.Background(), &cancellationEmbedder{}, []string{"a", "bbb", "cc"})
	require.NoError(t, err)
	require.Equal(t, [][]float32{{1}, {3}, {2}}, got)
	pool.Release()
	got, err = pooler.BatchEmbedWithPool(context.Background(), &cancellationEmbedder{}, []string{"text"})
	require.ErrorIs(t, err, ants.ErrPoolClosed)
	require.Nil(t, got)
}

func TestBatchEmbedWithPoolReleaseAndReboot(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "active waiter"
		if cancelled {
			name = "cancelled waiter"
		}
		t.Run(name, func(t *testing.T) {
			pool := cancellationPool(t)
			release := holdCancellationPool(t, pool)
			pooler := NewBatchEmbedder(pool)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := pooler.BatchEmbedWithPool(ctx, &cancellationEmbedder{}, []string{"text"})
				done <- err
			}()
			require.Eventually(t, func() bool { return pool.Waiting() == 1 }, 5*time.Second, time.Millisecond)
			if cancelled {
				cancel()
				if !awaitBatchCancellation(t, done, context.Canceled) {
					release()
					drainErr := <-done
					_ = drainErr
					return
				}
			}
			released := make(chan error, 1)
			go func() { released <- pool.ReleaseTimeout(5 * time.Second) }()
			require.Eventually(
				t,
				func() bool { return pool.IsClosed() && pool.Waiting() == 0 },
				5*time.Second,
				time.Millisecond,
			)
			if !cancelled {
				require.ErrorIs(t, <-done, ants.ErrPoolClosed)
			}
			release()
			require.NoError(t, <-released)
			pool.Reboot() // ants requires all workers stopped before Reboot.
			got, err := pooler.BatchEmbedWithPool(context.Background(), &cancellationEmbedder{}, []string{"new"})
			require.NoError(t, err)
			require.Equal(t, [][]float32{{3}}, got)
		})
	}
}

// Cancellation transfers the input slice back to the caller immediately,
// including when the batching goroutine is still preparing a large request.
// Run with -race to guard against reading the caller's reused backing array.
func TestBatchEmbedWithPoolCancelledInputCanBeReused(t *testing.T) {
	pool := cancellationPool(t)
	release := holdCancellationPool(t, pool)
	pooler := NewBatchEmbedder(pool)
	model := &cancellationEmbedder{}
	texts := make([]string, 100000)
	for i := range texts {
		texts[i] = "original"
	}
	for range 16 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := pooler.BatchEmbedWithPool(ctx, model, texts)
			done <- err
		}()
		timer := time.AfterFunc(100*time.Microsecond, cancel)
		returned := awaitBatchCancellation(t, done, context.Canceled)
		timer.Stop()
		cancel()
		if !returned {
			release()
			drainErr := <-done
			_ = drainErr
			return
		}
		for i := range texts {
			texts[i] = "reused"
		}
	}
	release()
	awaitCancellationPoolIdle(t, pool)
	require.Zero(t, model.calls.Load())
}
