package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAutomationDeleteFencesRunsArrivingAfterActiveScan(t *testing.T) {
	for _, trigger := range []string{"manual", "schedule"} {
		t.Run(trigger, func(t *testing.T) {
			f := newAutomationFixture(t)
			a := f.create(t, types.AutomationPatch{})
			require.NoError(t, f.db.Model(a).Update("next_run_at", f.now).Error)
			a = f.reload(t, a.ID)
			interleaved := false
			require.NoError(t, f.db.Callback().Query().After(
				"gorm:after_query",
			).Register("lifecycle:arriving_run", func(
				tx *gorm.DB,
			) {
				if interleaved || tx.Statement.Table != "automation_runs" {
					return
				}
				if _, ok := tx.Statement.Dest.(*[]*types.AutomationRun); !ok {
					return
				}
				interleaved = true
				// This request starts after Delete's active-run snapshot. A
				// committed deletion fence may refuse it, otherwise it must
				// appear in the set of runs that deletion cancels.
				if trigger == "manual" {
					_, _ = f.svc.RunNow(f.ctx, a.ID)
				} else {
					require.NoError(t, f.svc.claim(f.ctx, a, f.now))
				}
				for _, row := range f.runs(t, a.ID) {
					if row.Status == types.AutomationRunQueued {
						_, err := f.svc.startRun(f.ctx, &row, f.now)
						require.NoError(t, err)
					}
				}
			}))
			require.NoError(t, f.svc.Delete(f.ctx, a.ID))
			require.True(t, interleaved)
			require.True(t, f.reload(t, a.ID).DeletedAt.Valid)
			for _, run := range f.runs(t, a.ID) {
				t.Logf("remaining run: status=%s cancel_requested=%t", run.Status, run.CancelRequested)
				require.True(t, types.AutomationRunFinished(run.Status) || run.CancelRequested,
					"deleting an automation must fence every run that was allowed to start")
			}
		})
	}
}

func TestAutomationAddGrantCannotRecreateGrantAfterDeletion(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	run := &types.AutomationRun{
		AutomationID: a.ID, TenantID: a.TenantID, ScheduledAt: f.now,
		TriggerKind: types.AutomationRunManual, Status: types.AutomationRunSucceeded, SessionID: "session",
	}
	require.NoError(t, f.db.Create(run).Error)
	prompt := &types.AgentPrompt{
		ID: "approval", TenantID: a.TenantID,
		UserID: automationGrantUser(a.OwnerUserID), SessionID: run.SessionID,
		Kind: types.AgentPromptKindToolApproval, Request: types.JSON(mustJSON(t, types.ToolApprovalPromptRequest{
			Grant: "op:knowledge.delete:kb", GrantRisk: "destructive", GrantLabel: "documents in kb",
		})),
	}
	require.NoError(t, f.db.Create(prompt).Error)
	deleted := false
	require.NoError(t, f.db.Callback().Query().After("gorm:after_query").Register("lifecycle:delete_before_grant", func(
		tx *gorm.DB,
	) {
		if deleted || tx.Statement.Table != "agent_prompts" {
			return
		}
		deleted = true
		require.NoError(t, f.svc.Delete(f.ctx, a.ID))
	}))
	grant, err := f.svc.AddGrant(f.ctx, a.ID, prompt.ID)
	require.True(t, deleted)
	var grants int64
	require.NoError(t, f.db.Model(&types.AgentGrant{}).Where("scope_id = ?", a.ID).Count(&grants).Error)
	t.Logf("AddGrant after deletion: error=%v stored_grants=%d", err, grants)
	require.Zero(t, grants, "a deleted automation must not regain a standing authorization")
	require.Error(t, err)
	require.Nil(t, grant)
}

func TestAutomationPruneRetainsExactlyNewestRunsAtSameTime(t *testing.T) {
	f := newAutomationFixture(t)
	f.svc.Configure(AutomationConfig{Retention: 1})
	a := f.create(t, types.AutomationPatch{})
	old := &types.AutomationRun{
		ID: "old", AutomationID: a.ID, TenantID: a.TenantID,
		ScheduledAt: f.now, CreatedAt: f.now, TriggerKind: types.AutomationRunSchedule,
		Status: types.AutomationRunSucceeded,
	}
	newest := *old
	newest.ID, newest.TriggerKind, newest.CreatedAt = "new", types.AutomationRunManual, f.now.Add(time.Second)
	require.NoError(t, f.db.Create(old).Error)
	require.NoError(t, f.db.Create(&newest).Error)
	f.svc.prune(f.ctx)
	runs := f.runs(t, a.ID)
	t.Logf("retained runs with equal scheduled_at: %v", runs)
	require.Len(t, runs, 1, "a retention boundary must not remove the newest run with a tied schedule time")
	require.Equal(t, newest.ID, runs[0].ID)
}

func TestAutomationPruneConversationLifecycle(t *testing.T) {
	for _, mode := range []string{types.AutomationSessionPerRun, types.AutomationSessionPersistent} {
		for _, deleted := range []bool{false, true} {
			name := mode + "/active"
			if deleted {
				name = mode + "/deleted"
			}
			t.Run(name, func(t *testing.T) {
				f := newAutomationFixture(t)
				f.svc.Configure(AutomationConfig{Retention: 1})
				a := f.create(t, types.AutomationPatch{SessionMode: &mode})
				queued, err := f.svc.RunNow(f.ctx, a.ID)
				require.NoError(t, err)
				run := f.settleRun(t, queued.ID)
				// A later finished row makes the conversation-bearing row old.
				require.NoError(t, f.db.Create(&types.AutomationRun{
					AutomationID: a.ID, TenantID: a.TenantID,
					ScheduledAt: f.now.Add(time.Hour), TriggerKind: types.AutomationRunManual,
					Status: types.AutomationRunSkipped,
				}).Error)
				if deleted {
					// Model a process stopping after the durable parent tombstone,
					// before conversation cleanup. Prune must finish the cleanup.
					require.NoError(t, f.db.Delete(a).Error)
				}
				f.svc.prune(f.ctx)
				var sessions int64
				require.NoError(t, f.db.Model(&types.Session{}).Where("id = ?", run.SessionID).Count(&sessions).Error)
				t.Logf("mode=%s deleted=%t remaining_sessions=%d", mode, deleted, sessions)
				if mode == types.AutomationSessionPersistent && !deleted {
					require.EqualValues(t, 1, sessions, "live persistent conversation must survive old-run pruning")
				} else {
					require.Zero(t, sessions, "an old per-run or deleted automation conversation must be removed")
				}
			})
		}
	}
}

type automationMessageFailure struct{ interfaces.MessageService }

func (*automationMessageFailure) CreateMessage(context.Context, *types.Message) (*types.Message, error) {
	return nil, errors.New("injected message persistence failure")
}

func TestAutomationDeleteCleansConversationAfterMessageFailure(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	run, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	started, err := f.svc.startRun(f.ctx, run, f.now)
	require.NoError(t, err)
	require.True(t, started)
	f.svc.messages = &automationMessageFailure{}
	out := f.svc.runOnce(f.ctx, run)
	require.Equal(t, types.AutomationRunFailed, out.status)
	require.Contains(t, out.reason, "injected message persistence failure")
	f.svc.record(f.ctx, run, out)
	require.NoError(t, f.svc.Delete(f.ctx, a.ID))
	var sessions int64
	require.NoError(
		t,
		f.db.Model(&types.Session{}).Where("kind = ?", types.SessionKindAutomation).Count(&sessions).Error,
	)
	t.Logf("sessions after failed message creation and deletion: %d", sessions)
	require.Zero(t, sessions, "the conversation must be linked before fallible message writes so deletion can find it")
}

func TestAutomationRunSessionRejectsDeletedParent(t *testing.T) {
	for _, mode := range []string{types.AutomationSessionPerRun, types.AutomationSessionPersistent} {
		t.Run(mode, func(t *testing.T) {
			f := newAutomationFixture(t)
			a := f.create(t, types.AutomationPatch{SessionMode: &mode})
			run, err := f.svc.RunNow(f.ctx, a.ID)
			require.NoError(t, err)
			started, err := f.svc.startRun(f.ctx, run, f.now)
			require.NoError(t, err)
			require.True(t, started)
			// The executor already read a; deletion wins before it opens a session.
			require.NoError(t, f.svc.Delete(f.ctx, a.ID))
			session, err := f.svc.runSession(f.ctx, a, run)
			var sessions int64
			require.NoError(
				t,
				f.db.Model(&types.Session{}).Where("kind = ?", types.SessionKindAutomation).Count(&sessions).Error,
			)
			t.Logf("runSession after deletion: error=%v remaining_sessions=%d", err, sessions)
			require.Zero(t, sessions, "a stale executor cannot create a conversation after deletion")
			require.Error(t, err)
			require.Nil(t, session)
		})
	}
}

func TestAutomationPruneDeletedPersistentSessionWithoutRetainedRunReference(t *testing.T) {
	f := newAutomationFixture(t)
	f.svc.Configure(AutomationConfig{Retention: 1})
	a := f.create(t, types.AutomationPatch{SessionMode: ptr(types.AutomationSessionPersistent)})
	queued, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	run := f.settleRun(t, queued.ID)
	require.NoError(t, f.db.Create(&types.AutomationRun{
		AutomationID: a.ID, TenantID: a.TenantID,
		ScheduledAt: f.now.Add(time.Hour), TriggerKind: types.AutomationRunSchedule,
		Status: types.AutomationRunSkipped,
	}).Error)
	f.svc.prune(f.ctx)
	require.Len(t, f.runs(t, a.ID), 1)
	require.Empty(t, f.runs(t, a.ID)[0].SessionID)
	// All conversation-bearing runs were pruned while the parent was live.
	// A later interrupted deletion must still be recoverable from its tombstone.
	require.NoError(t, f.db.Delete(a).Error)
	f.svc.prune(f.ctx)
	var sessions int64
	require.NoError(t, f.db.Model(&types.Session{}).Where("id = ?", run.SessionID).Count(&sessions).Error)
	t.Logf("deleted persistent conversation without retained run reference: remaining_sessions=%d", sessions)
	require.Zero(t, sessions)
}

func TestAutomationRunSessionRollsBackWhenClaimWasLost(t *testing.T) {
	for _, mode := range []string{types.AutomationSessionPerRun, types.AutomationSessionPersistent} {
		t.Run(mode, func(t *testing.T) {
			f := newAutomationFixture(t)
			a := f.create(t, types.AutomationPatch{SessionMode: &mode})
			run, err := f.svc.RunNow(f.ctx, a.ID)
			require.NoError(t, err)
			started, err := f.svc.startRun(f.ctx, run, f.now)
			require.NoError(t, err)
			require.True(t, started)
			require.NoError(t, f.db.Model(run).Update("owner", "another-instance").Error)
			session, err := f.svc.runSession(f.ctx, a, run)
			require.Error(t, err)
			require.Nil(t, session)
			var sessions int64
			require.NoError(
				t,
				f.db.Model(&types.Session{}).Where("kind = ?", types.SessionKindAutomation).Count(&sessions).Error,
			)
			require.Zero(t, sessions)
			require.Empty(t, f.reload(t, a.ID).PersistentSessionID)
		})
	}
}
