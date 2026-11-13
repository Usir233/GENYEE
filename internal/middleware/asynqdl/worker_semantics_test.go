package asynqdl

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// Exercise actual worker metadata and Redis transitions. Supplying a plain
// context would make every invocation look final and hide early SkipRetry.
func TestMiddlewareMatchesWorkerTerminalSemantics(t *testing.T) {
	ordinary := errors.New("retryable failure")
	for _, tc := range []struct {
		name     string
		maxRetry int
		errors   []error
		state    asynq.TaskState
		deleted  bool
		dead     bool
	}{
		{name: "success", maxRetry: 3, errors: []error{nil}, state: asynq.TaskStateCompleted},
		{name: "ordinary retry then success", maxRetry: 1, errors: []error{ordinary, nil}, state: asynq.TaskStateCompleted},
		{name: "ordinary retry exhausted", maxRetry: 1, errors: []error{ordinary, ordinary}, state: asynq.TaskStateArchived, dead: true},
		{name: "zero retry budget", errors: []error{ordinary}, state: asynq.TaskStateArchived, dead: true},
		{name: "skip retry before budget", maxRetry: 5, errors: []error{asynq.SkipRetry}, state: asynq.TaskStateArchived, dead: true},
		{name: "wrapped skip retry", maxRetry: 5, errors: []error{fmt.Errorf("invalid payload: %w", asynq.SkipRetry)}, state: asynq.TaskStateArchived, dead: true},
		{name: "revoke before budget", maxRetry: 5, errors: []error{asynq.RevokeTask}, deleted: true},
		{name: "revoke on final attempt", errors: []error{asynq.RevokeTask}, deleted: true},
		{name: "revoke after ordinary retry", maxRetry: 1, errors: []error{ordinary, asynq.RevokeTask}, deleted: true},
		{name: "wrapped revoke on final attempt", errors: []error{fmt.Errorf("cancelled: %w", asynq.RevokeTask)}, deleted: true},
		{name: "revoke takes precedence over skip", errors: []error{errors.Join(asynq.SkipRetry, asynq.RevokeTask)}, deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			redis := asynq.RedisClientOpt{Addr: mr.Addr()}
			client := asynq.NewClient(redis)
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			inspector := asynq.NewInspector(redis)
			t.Cleanup(func() { require.NoError(t, inspector.Close()) })
			repo := &fakeRepo{}
			callbacks := make(chan error, len(tc.errors)+1)
			type observation struct {
				id              string
				retried, max    int
				metadataPresent bool
				err             error
			}
			observations := make(chan observation, len(tc.errors)+1)
			wrapped := MiddlewareWithCallback(repo, func(_ context.Context, _ *asynq.Task, err error) {
				callbacks <- err
			})(asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
				retried, _ := asynq.GetRetryCount(ctx)
				return tc.errors[min(retried, len(tc.errors)-1)]
			}))
			handler := asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
				err := wrapped.ProcessTask(ctx, task)
				id, idOK := asynq.GetTaskID(ctx)
				retried, retryOK := asynq.GetRetryCount(ctx)
				max, maxOK := asynq.GetMaxRetry(ctx)
				observations <- observation{id, retried, max, idOK && retryOK && maxOK, err}
				return err
			})
			worker := asynq.NewServer(redis, asynq.Config{
				Concurrency: 1, TaskCheckInterval: 10 * time.Millisecond,
				ShutdownTimeout: time.Second, DelayedTaskCheckInterval: time.Hour,
				// Keep the first failure in Retry until the assertions inspect it.
				RetryDelayFunc: func(int, error, *asynq.Task) time.Duration { return time.Hour },
				LogLevel:       asynq.ErrorLevel,
			})
			require.NoError(t, worker.Start(handler))
			t.Cleanup(worker.Shutdown)
			info, err := client.Enqueue(asynq.NewTask("audit:terminal", []byte(`{"tenant_id":7}`)),
				asynq.MaxRetry(tc.maxRetry), asynq.Retention(time.Hour))
			require.NoError(t, err)
			waitForState := func(want asynq.TaskState, deleted bool) {
				t.Helper()
				require.Eventually(t, func() bool {
					current, err := inspector.GetTaskInfo("default", info.ID)
					if deleted {
						return errors.Is(err, asynq.ErrTaskNotFound)
					}
					return err == nil && current.State == want
				}, 5*time.Second, 10*time.Millisecond, "worker must perform its actual Redis transition")
			}
			if len(tc.errors) > 1 {
				waitForState(asynq.TaskStateRetry, false)
				require.Zero(t, repo.rowCount(), "an ordinary retry is not a dead letter")
				require.Empty(t, callbacks)
				require.NoError(t, inspector.RunTask("default", info.ID))
			}
			waitForState(tc.state, tc.deleted)
			for retried, expected := range tc.errors {
				select {
				case got := <-observations:
					require.True(t, got.metadataPresent, "the fixture must use genuine worker metadata")
					require.Equal(t, info.ID, got.id)
					require.Equal(t, retried, got.retried)
					require.Equal(t, tc.maxRetry, got.max)
					require.True(t, got.err == expected, "middleware must return the original error unchanged")
				default:
					t.Fatal("worker transitioned without reporting its invocation")
				}
			}
			wantCount := 0
			if tc.dead {
				wantCount = 1
			}
			state := "deleted"
			if !tc.deleted {
				state = tc.state.String()
			}
			t.Logf("state=%s attempts=%d deadLetters=%d callbacks=%d",
				state, len(tc.errors), repo.rowCount(), len(callbacks))
			require.Equal(t, wantCount, repo.rowCount())
			require.Len(t, callbacks, wantCount)
			if tc.dead {
				lastErr := tc.errors[len(tc.errors)-1]
				require.True(t, <-callbacks == lastErr)
				row := repo.captureRow(0)
				require.Equal(t, len(tc.errors), row.FailCount)
				require.Equal(t, lastErr.Error(), row.LastError)
				require.EqualValues(t, 7, row.TenantID)
			}
		})
	}
}
