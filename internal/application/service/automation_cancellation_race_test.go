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

func TestAutomationRecordAwaitingApprovalFencesCancelRequestedAndClosesPrompt(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		name := "owner_cancelled"
		if deleted {
			name = "automation_deleted"
		}
		t.Run(name, func(t *testing.T) {
			f := newAutomationFixture(t)
			a := f.create(t, types.AutomationPatch{})
			f.suspendOnApproval(t, "op:knowledge.delete:kb1")
			run, err := f.svc.RunNow(f.ctx, a.ID)
			require.NoError(t, err)
			started, err := f.svc.startRun(f.ctx, run, f.now)
			require.NoError(t, err)
			require.True(t, started)

			out := f.svc.runOnce(f.ctx, run)
			require.Equal(t, types.AutomationRunAwaitingApproval, out.status)
			require.NotEmpty(t, out.promptID)

			// Remote CancelRun / Delete lands after runOnce finishes but before record writes.
			if deleted {
				require.NoError(t, f.svc.Delete(f.ctx, a.ID))
			} else {
				require.NoError(t, f.db.Model(run).Update("cancel_requested", true).Error)
			}

			f.svc.record(f.ctx, run, out)

			prompt, err := f.broker.Store().Get(f.ctx, out.promptID)
			require.NoError(t, err)
			require.Equal(t, types.AgentPromptCanceled, prompt.Status, "suspended prompt must be closed when cancellation fence misses")

			var notices int64
			require.NoError(t, f.db.Model(&types.Notification{}).Count(&notices).Error)
			require.Zero(t, notices, "must not send ghost awaiting_approval notification")

			if deleted {
				var runs, sessions int64
				require.NoError(t, f.db.Model(&types.AutomationRun{}).Where("automation_id = ?", a.ID).Count(&runs).Error)
				require.NoError(t, f.db.Model(&types.Session{}).Where("kind = ?", types.SessionKindAutomation).Count(&sessions).Error)
				require.Zero(t, runs, "converged run of deleted automation must be cleaned up")
				require.Zero(t, sessions, "session of deleted automation must be cleaned up")
			} else {
				var actual types.AutomationRun
				require.NoError(t, f.db.First(&actual, "id = ?", run.ID).Error)
				require.Equal(t, types.AutomationRunCancelled, actual.Status)
				require.Equal(t, "cancelled by the owner", actual.Error)
			}
		})
	}
}

func TestAutomationFinishRunFencesCancelRequestedWithoutGhostNotifyOrPause(t *testing.T) {
	for _, out := range []runOutcome{
		{status: types.AutomationRunSucceeded, answer: "done"},
		{status: types.AutomationRunFailed, reason: "transient failure", pause: "owner unavailable"},
	} {
		t.Run(out.status, func(t *testing.T) {
			f := newAutomationFixture(t)
			notifySuccess := true
			a := f.create(t, types.AutomationPatch{NotifyOnSuccess: &notifySuccess})
			run, err := f.svc.RunNow(f.ctx, a.ID)
			require.NoError(t, err)
			started, err := f.svc.startRun(f.ctx, run, f.now)
			require.NoError(t, err)
			require.True(t, started)

			// Cancellation arrives before record/finishRun commits terminal status.
			require.NoError(t, f.db.Model(run).Update("cancel_requested", true).Error)
			f.svc.record(f.ctx, run, out)

			var actual types.AutomationRun
			require.NoError(t, f.db.First(&actual, "id = ?", run.ID).Error)
			require.Equal(t, types.AutomationRunCancelled, actual.Status)
			require.Equal(t, "cancelled by the owner", actual.Error)

			stored := f.reload(t, a.ID)
			require.True(t, stored.Enabled, "cancelled run must not pause the automation")
			require.Zero(t, stored.ConsecutiveFailures, "cancelled run must not increment consecutive failures")

			var notices int64
			require.NoError(t, f.db.Model(&types.Notification{}).Count(&notices).Error)
			require.Zero(t, notices, "cancelled run must not emit success/failure/pause notifications")
		})
	}
}
