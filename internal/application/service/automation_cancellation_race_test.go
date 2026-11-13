package service

import (
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

// CancelRun reads the row before it reaches cancelRun. A scheduler or approval
// continuation can change the state between those two operations.
func TestAutomationCancellationSurvivesStateTransition(t *testing.T) {
	for _, states := range [][2]string{
		{types.AutomationRunQueued, types.AutomationRunRunning},
		{types.AutomationRunAwaitingApproval, types.AutomationRunQueued},
		{types.AutomationRunRunning, types.AutomationRunAwaitingApproval},
	} {
		t.Run(states[0]+"_to_"+states[1], func(t *testing.T) {
			f := newAutomationFixture(t)
			a := f.create(t, types.AutomationPatch{})
			run, err := f.svc.RunNow(f.ctx, a.ID)
			require.NoError(t, err)
			stale := *run
			stale.Status = states[0]
			if states[1] == types.AutomationRunRunning {
				started, err := f.svc.startRun(f.ctx, run, f.now)
				require.NoError(t, err)
				require.True(t, started)
			} else {
				require.NoError(t, f.db.Model(run).Update("status", states[1]).Error)
			}
			require.NoError(t, f.svc.cancelRun(f.ctx, &stale, "cancelled by the owner"))
			var actual types.AutomationRun
			require.NoError(t, f.db.First(&actual, "id = ?", run.ID).Error)
			if states[1] == types.AutomationRunRunning {
				require.True(t, actual.CancelRequested, "running worker must observe the cancellation")
			} else {
				require.Equal(t, types.AutomationRunCancelled, actual.Status, "queued/waiting work should stop immediately")
			}
		})
	}
}

func TestAutomationDoesNotStartRunWithCancellationRequested(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	run, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	require.NoError(t, f.db.Model(run).Update("cancel_requested", true).Error)
	started, err := f.svc.startRun(f.ctx, run, f.now)
	require.NoError(t, err)
	require.False(t, started, "a cancellation recorded before claim must prevent execution")
}

func TestAutomationSchedulerSettlesInterruptedQueuedCancellation(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	run, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	// Cancellation can be interrupted after its durable intent is recorded.
	// It must not leave a queued run blocking every later run forever.
	require.NoError(t, f.db.Model(run).Update("cancel_requested", true).Error)
	f.svc.broker = nil
	require.NoError(t, f.svc.reconcileAwaiting(f.ctx, f.now))
	var actual types.AutomationRun
	require.NoError(t, f.db.First(&actual, "id = ?", run.ID).Error)
	require.Equal(t, types.AutomationRunCancelled, actual.Status)
}
