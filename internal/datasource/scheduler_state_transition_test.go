package datasource

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type flakySyncLogRepo struct {
	*fakeSyncLogRepo
	failUpdatesRemaining atomic.Int32
	updateCalls          atomic.Int32
	muCtx                sync.Mutex
	seenUpdateCtxErr     error
}

func (r *flakySyncLogRepo) Update(ctx context.Context, log *types.SyncLog) error {
	r.updateCalls.Add(1)
	r.muCtx.Lock()
	r.seenUpdateCtxErr = ctx.Err()
	r.muCtx.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.failUpdatesRemaining.Add(-1) >= 0 {
		return errors.New("transient database write contention")
	}
	clone := *log
	return r.fakeSyncLogRepo.Update(ctx, &clone)
}

type failingTaskEnqueuer struct {
	err error
	fn  func() error
}

func (e *failingTaskEnqueuer) Enqueue(_ *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	if e.fn != nil {
		return nil, e.fn()
	}
	return nil, e.err
}

func TestScheduler_TriggerSync_RetriesTransientSyncLogUpdateOnEnqueueFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enqueueErr error
		wantStatus string
	}{
		{
			name:       "deduplicated conflict transitions to canceled after transient DB failure",
			enqueueErr: asynq.ErrTaskIDConflict,
			wantStatus: types.SyncLogStatusCanceled,
		},
		{
			name:       "enqueue error transitions to failed after transient DB failure",
			enqueueErr: errors.New("redis connection refused"),
			wantStatus: types.SyncLogStatusFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsRepo := newFakeDataSourceRepo()
			require.NoError(t, dsRepo.Create(context.Background(), &types.DataSource{
				ID:           "ds-retry",
				TenantID:     42,
				Status:       types.DataSourceStatusActive,
				SyncSchedule: "0 0 * * * *",
			}))

			baseLogs := newFakeSyncLogRepo()
			syncLogs := &flakySyncLogRepo{fakeSyncLogRepo: baseLogs}
			syncLogs.failUpdatesRemaining.Store(1)

			scheduler := NewScheduler(dsRepo, syncLogs, &failingTaskEnqueuer{err: tc.enqueueErr})
			scheduler.triggerSync("ds-retry", 42)

			assert.GreaterOrEqual(t, syncLogs.updateCalls.Load(), int32(2),
				"expected Update to be retried rather than silently swallowed on transient DB error")

			running, err := syncLogs.HasRunningSync(context.Background(), "ds-retry")
			require.NoError(t, err)
			assert.False(t, running, "syncLog must not remain stranded in running status")

			baseLogs.mu.Lock()
			defer baseLogs.mu.Unlock()
			require.Len(t, baseLogs.logs, 1)
			for _, entry := range baseLogs.logs {
				assert.Equal(t, tc.wantStatus, entry.Status)
				assert.NotNil(t, entry.FinishedAt)
			}
		})
	}
}

func TestScheduler_TriggerSync_FinalizesSyncLogEvenWhenEnqueueContextExpires(t *testing.T) {
	dsRepo := newFakeDataSourceRepo()
	require.NoError(t, dsRepo.Create(context.Background(), &types.DataSource{
		ID:           "ds-expired",
		TenantID:     7,
		Status:       types.DataSourceStatusActive,
		SyncSchedule: "0 0 * * * *",
	}))

	baseLogs := newFakeSyncLogRepo()
	syncLogs := &flakySyncLogRepo{fakeSyncLogRepo: baseLogs}

	ctx, cancel := context.WithCancel(context.Background())
	enqueuer := &failingTaskEnqueuer{
		fn: func() error {
			cancel()
			return context.DeadlineExceeded
		},
	}

	scheduler := NewScheduler(dsRepo, syncLogs, enqueuer)
	err := scheduler.triggerSyncWithContext(ctx, "ds-expired", 7)
	require.Error(t, err)

	running, hasErr := syncLogs.HasRunningSync(context.Background(), "ds-expired")
	require.NoError(t, hasErr)
	assert.False(t, running, "expired enqueue context must still transition syncLog out of running")
}
