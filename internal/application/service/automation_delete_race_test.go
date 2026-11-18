package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAutomationRunNowCannotQueueAfterConcurrentDeletion(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	deleted := false
	// Delete after RunNow has loaded its authorization snapshot but before it
	// persists a run. The snapshot must not authorize recreating orphan work.
	require.NoError(t, f.db.Callback().Query().After("gorm:after_query").Register("audit:delete_automation", func(
		tx *gorm.DB,
	) {
		if deleted || tx.Statement.Table != "automations" {
			return
		}
		if _, ok := tx.Statement.Dest.(*types.Automation); !ok {
			return
		}
		deleted = true
		require.NoError(t, f.svc.Delete(f.ctx, a.ID))
	}))
	run, err := f.svc.RunNow(f.ctx, a.ID)
	require.True(t, deleted)
	require.Error(t, err)
	require.Nil(t, run)
	var count int64
	require.NoError(t, f.db.Model(&types.AutomationRun{}).Where("automation_id = ?", a.ID).Count(&count).Error)
	require.Zero(t, count, "deleted automation must not acquire orphan queued work")
}

func TestAutomationDeleteRereadsPersistentSessionInsideLock(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{SessionMode: ptr(types.AutomationSessionPersistent)})

	type deleteKey struct{}
	deleteCtx := context.WithValue(f.ctx, deleteKey{}, true)
	var injected sync.Once
	var lateSessionID string

	// Simulate persistent_session_id being committed right after Delete's initial
	// s.load snapshot, with no retained AutomationRun row carrying session_id.
	require.NoError(t, f.db.Callback().Query().After("gorm:after_query").Register("audit:late_persistent_session", func(
		tx *gorm.DB,
	) {
		if tx.Statement.Table != "automations" || tx.Statement.Context.Value(deleteKey{}) == nil {
			return
		}
		injected.Do(func() {
			sess := &types.Session{
				TenantID: a.TenantID, UserID: a.OwnerUserID,
				Kind: types.SessionKindAutomation, Title: "persistent", CreatedAt: f.now, UpdatedAt: f.now,
			}
			require.NoError(t, f.db.Create(sess).Error)
			lateSessionID = sess.ID
			require.NoError(t, f.db.Model(&types.Automation{}).Where("id = ?", a.ID).
				Update("persistent_session_id", lateSessionID).Error)
		})
	}))

	require.NoError(t, f.svc.Delete(deleteCtx, a.ID))
	require.NotEmpty(t, lateSessionID)
	var sessions int64
	require.NoError(t, f.db.Model(&types.Session{}).Where("id = ?", lateSessionID).Count(&sessions).Error)
	require.Zero(t, sessions, "Delete must re-read persistent_session_id inside the lock and remove it")
}

func TestAutomationDeleteJoinsCancelErrorsWithoutAbortingCleanup(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})

	stopErr := errors.New("delegation bus unreachable")
	f.svc.StopDelegation = func(context.Context, uint64, string, string, string) error {
		return stopErr
	}

	sess1 := &types.Session{
		TenantID: a.TenantID, UserID: a.OwnerUserID, Kind: types.SessionKindAutomation,
	}
	sess2 := &types.Session{
		TenantID: a.TenantID, UserID: a.OwnerUserID, Kind: types.SessionKindAutomation,
	}
	require.NoError(t, f.db.Create(sess1).Error)
	require.NoError(t, f.db.Create(sess2).Error)

	r1 := &types.AutomationRun{
		ID: "run-1", AutomationID: a.ID, TenantID: a.TenantID, ScheduledAt: f.now,
		TriggerKind: types.AutomationRunSchedule, Status: types.AutomationRunRunning,
		Owner: "remote-worker", SessionID: sess1.ID, AssistantMessageID: "msg-1",
	}
	r2 := &types.AutomationRun{
		ID: "run-2", AutomationID: a.ID, TenantID: a.TenantID, ScheduledAt: f.now.Add(time.Minute),
		TriggerKind: types.AutomationRunManual, Status: types.AutomationRunQueued,
		SessionID: sess2.ID,
	}
	require.NoError(t, f.db.Create(r1).Error)
	require.NoError(t, f.db.Create(r2).Error)

	err := f.svc.Delete(f.ctx, a.ID)
	require.ErrorIs(t, err, stopErr, "StopDelegation error must still be surfaced")

	// r2 (the second active run) must still have been cancelled AND removed by cleanup!
	var r2Count int64
	require.NoError(t, f.db.Model(&types.AutomationRun{}).Where("id = ?", r2.ID).Count(&r2Count).Error)
	require.Zero(t, r2Count, "error cancelling first run must not abort cancelling and cleaning subsequent runs")

	var sessions int64
	require.NoError(
		t,
		f.db.Model(&types.Session{}).Where("kind = ?", types.SessionKindAutomation).Count(&sessions).Error,
	)
	require.Zero(t, sessions, "conversations must still be cleaned up even when StopDelegation fails")

	// Now simulate the remote worker settling r1: its converged AutomationRun row must be cleaned up immediately.
	f.svc.StopDelegation = nil
	remoteSvc := &AutomationService{
		db: f.db, instance: "remote-worker", now: func() time.Time { return f.now },
		notices: f.svc.notices, wake: make(chan struct{}, 1),
	}
	remoteSvc.record(f.ctx, r1, runOutcome{status: types.AutomationRunSucceeded, answer: "late finish"})

	var remainingRuns int64
	require.NoError(t, f.db.Model(&types.AutomationRun{}).Where("automation_id = ?", a.ID).Count(&remainingRuns).Error)
	require.Zero(
		t,
		remainingRuns,
		"remote running task of deleted automation must clean up its run record on convergence",
	)
}
