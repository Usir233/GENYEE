package sandboxalloc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type failingCompleteRepo struct {
	interfaces.SandboxAllocationRepository
	completeErr error
	retried     []string
}

func (r *failingCompleteRepo) CompleteWorkspaceCleanup(ctx context.Context, sessionID string) error {
	if r.completeErr != nil {
		return r.completeErr
	}
	return r.SandboxAllocationRepository.CompleteWorkspaceCleanup(ctx, sessionID)
}

func (r *failingCompleteRepo) RetryWorkspaceCleanup(
	ctx context.Context, sessionID string, nextDueAt time.Time,
) error {
	r.retried = append(r.retried, sessionID)
	return r.SandboxAllocationRepository.RetryWorkspaceCleanup(ctx, sessionID, nextDueAt)
}

func TestWorkspaceCleanerRetriesWhenCompleteWorkspaceCleanupFails(t *testing.T) {
	enableUserMode(t)

	t.Run("deleted directory completion failure schedules retry", func(t *testing.T) {
		id := uuid.NewString()
		e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
		e.place(t, id, "cfg-a")

		ops := &fakeMachineOps{}
		c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})
		_, err := c.ReleaseSessions(context.Background(), []string{id})
		require.NoError(t, err)

		wrapper := &failingCompleteRepo{
			SandboxAllocationRepository: e.svc.repo,
			completeErr:                 errors.New("database write failed"),
		}
		e.svc.repo = wrapper

		done, failed, err := c.RetryDue(context.Background(), 10)
		require.NoError(t, err)
		require.Equal(t, 0, done, "failed DB completion must not be counted as done")
		require.Equal(t, 1, failed, "failed DB completion must be counted as failed")
		require.Equal(t, []string{id}, wrapper.retried)
	})

	t.Run("reclaimed machine completion failure schedules retry", func(t *testing.T) {
		id := uuid.NewString()
		e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
		e.place(t, id, "cfg-a")

		ops := &fakeMachineOps{}
		c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})
		_, err := c.ReleaseSessions(context.Background(), []string{id})
		require.NoError(t, err)

		row, err := e.svc.repo.GetAssignment(context.Background(), id)
		require.NoError(t, err)
		require.NotNil(t, row)
		lock, err := e.svc.repo.LockEmptyMachine(context.Background(), row.MachineDigest)
		require.NoError(t, err)
		require.NotNil(t, lock)
		deleted, err := lock.CommitDelete()
		require.NoError(t, err)
		require.True(t, deleted)

		wrapper := &failingCompleteRepo{
			SandboxAllocationRepository: e.svc.repo,
			completeErr:                 errors.New("database write failed"),
		}
		e.svc.repo = wrapper

		done, failed, err := c.RetryDue(context.Background(), 10)
		require.NoError(t, err)
		require.Equal(t, 0, done)
		require.Equal(t, 1, failed)
		require.Equal(t, []string{id}, wrapper.retried)
	})
}
