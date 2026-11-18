package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Use a keyword/value DSN for a disposable, separately provisioned database.
// The name guard prevents these schema-mutating tests from targeting GENYEE.
func newAutomationPostgresFixture(t *testing.T) (*automationFixture, string) {
	t.Helper()
	dsn := os.Getenv("GENYEE_AUTOMATION_AUDIT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GENYEE_AUTOMATION_AUDIT_POSTGRES_DSN is not configured")
	}
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), cfg)
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminSQL.Close()) })
	var database string
	require.NoError(t, admin.Raw("SELECT current_database()").Scan(&database).Error)
	require.Equal(t, "automation_audit", database, "only the disposable automation_audit database may be used")
	schema := "automation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema+" application_name="+schema), cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Automation{}, &types.AutomationRun{}, &types.Notification{},
		&types.Session{}, &types.AgentGrant{}, &types.AgentPrompt{}))
	// Reuse the local executor doubles, replacing every database-bound dependency.
	f := newAutomationFixture(t)
	f.db, f.svc.db, f.sessions.db = db, db, db
	f.svc.grants = repository.NewAgentGrantRepository(db)
	f.svc.notices = NewNotificationService(db)
	return f, schema
}

type automationLifecycleActor struct{}

type automationLifecycleResult struct {
	err     error
	started bool
	session *types.Session
}

func TestAutomationPostgresDeletionSerializesParentWriters(t *testing.T) {
	for _, operation := range []string{
		"RunNow",
		"startRun",
		"runSession/per_run",
		"runSession/persistent",
		"AddGrant",
	} {
		for _, first := range []string{"delete", "writer"} {
			t.Run(operation+"/"+first+"_first", func(t *testing.T) {
				f, application := newAutomationPostgresFixture(t)
				mode := types.AutomationSessionPerRun
				if operation == "runSession/persistent" {
					mode = types.AutomationSessionPersistent
				}
				a := f.create(t, types.AutomationPatch{SessionMode: &mode})
				var run *types.AutomationRun
				if operation != "RunNow" {
					var err error
					run, err = f.svc.RunNow(f.ctx, a.ID)
					require.NoError(t, err)
				}
				if strings.HasPrefix(operation, "runSession/") {
					started, err := f.svc.startRun(f.ctx, run, f.now)
					require.NoError(t, err)
					require.True(t, started)
				}
				if operation == "AddGrant" {
					require.NoError(t, f.db.Model(run).Updates(map[string]any{
						"status": types.AutomationRunSucceeded, "session_id": "approval-session",
					}).Error)
					require.NoError(t, f.db.Create(&types.AgentPrompt{
						ID: "approval", TenantID: a.TenantID,
						UserID: automationGrantUser(a.OwnerUserID), SessionID: "approval-session",
						Kind: types.AgentPromptKindToolApproval,
						Request: types.JSON(mustJSON(t, types.ToolApprovalPromptRequest{
							Grant: "op:knowledge.delete:kb", GrantRisk: "destructive",
						})),
					}).Error)
				}
				ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
				entered, release := make(chan struct{}), make(chan struct{})
				var gated, released sync.Once
				var workers sync.WaitGroup
				defer func() {
					released.Do(func() { close(release) })
					cancel()
					workers.Wait()
				}()
				gate := func(tx *gorm.DB) {
					if tx.Statement.Table != "automations" || tx.Statement.Context.Value(
						automationLifecycleActor{},
					) != first {
						return
					}
					gated.Do(func() {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
							_ = tx.AddError(ctx.Err())
						}
					})
				}
				require.NoError(
					t,
					f.db.Callback().Update().After("gorm:update").Register("lifecycle:hold_writer", gate),
				)
				require.NoError(
					t,
					f.db.Callback().Delete().After("gorm:delete").Register("lifecycle:hold_delete", gate),
				)
				writerDone, deleteDone := make(chan automationLifecycleResult, 1), make(chan error, 1)
				writer := func() {
					workers.Add(1)
					go func() {
						defer workers.Done()
						workCtx := context.WithValue(ctx, automationLifecycleActor{}, "writer")
						var result automationLifecycleResult
						switch operation {
						case "RunNow":
							_, result.err = f.svc.RunNow(workCtx, a.ID)
						case "startRun":
							result.started, result.err = f.svc.startRun(workCtx, run, f.now)
						case "runSession/per_run", "runSession/persistent":
							result.session, result.err = f.svc.runSession(workCtx, a, run)
						case "AddGrant":
							_, result.err = f.svc.AddGrant(workCtx, a.ID, "approval")
						}
						writerDone <- result
					}()
				}
				deletion := func() {
					workers.Add(1)
					go func() {
						defer workers.Done()
						deleteDone <- f.svc.Delete(context.WithValue(ctx, automationLifecycleActor{}, "delete"), a.ID)
					}()
				}
				if first == "delete" {
					deletion()
				} else {
					writer()
				}
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("first transaction did not acquire the parent row: ", ctx.Err())
				}
				if first == "delete" {
					writer()
				} else {
					deletion()
				}
				// Observe PostgreSQL actually waiting on the other transaction's
				// parent UPDATE; a scheduling delay alone is not evidence of locking.
				require.Eventually(t, func() bool {
					var waiting int64
					err := f.db.WithContext(ctx).Raw(`SELECT count(*) FROM pg_stat_activity
						WHERE datname = current_database() AND application_name = ?
						AND wait_event_type = 'Lock' AND query LIKE '%UPDATE "automations"%'`, application).
						Scan(&waiting).Error
					return err == nil && waiting > 0
				}, 3*time.Second, 10*time.Millisecond, "the second transaction must wait on the parent row lock")
				t.Logf("%s/%s_first: PostgreSQL reported parent UPDATE waiting on Lock", operation, first)
				released.Do(func() { close(release) })
				result := <-writerDone
				require.NoError(t, <-deleteDone)
				switch {
				case first == "delete":
					if operation == "startRun" {
						require.NoError(t, result.err)
						require.False(t, result.started)
					} else {
						require.Error(t, result.err)
						require.Nil(t, result.session)
					}
				case strings.HasPrefix(operation, "runSession/"):
					// GetSession runs after the creation transaction commits; Delete
					// may have already removed that session before this final read.
					require.True(t, result.err == nil || errors.Is(result.err, gorm.ErrRecordNotFound), result.err)
				default:
					require.NoError(t, result.err)
					if operation == "startRun" {
						require.True(t, result.started)
					}
				}
				require.True(t, f.reload(t, a.ID).DeletedAt.Valid)
				for _, row := range f.runs(t, a.ID) {
					require.True(t, types.AutomationRunFinished(row.Status) || row.CancelRequested)
				}
				for _, model := range []any{&types.AgentGrant{}, &types.Session{}} {
					var count int64
					require.NoError(t, f.db.Model(model).Count(&count).Error)
					require.Zero(t, count, "deletion must leave no live grant or conversation")
				}
			})
		}
	}
}

func TestAutomationPostgresRunSessionRejectsFinishedOrDeletedRun(t *testing.T) {
	for _, mode := range []string{types.AutomationSessionPerRun, types.AutomationSessionPersistent} {
		for _, state := range []string{"failed", "cancel_requested", "deleted"} {
			t.Run(mode+"/"+state, func(t *testing.T) {
				f, _ := newAutomationPostgresFixture(t)
				a := f.create(t, types.AutomationPatch{SessionMode: &mode})
				run, err := f.svc.RunNow(f.ctx, a.ID)
				require.NoError(t, err)
				started, err := f.svc.startRun(f.ctx, run, f.now)
				require.NoError(t, err)
				require.True(t, started)
				switch state {
				case "failed":
					require.NoError(t, f.db.Model(run).Update("status", types.AutomationRunFailed).Error)
				case "cancel_requested":
					require.NoError(t, f.db.Model(run).Update("cancel_requested", true).Error)
				case "deleted":
					require.NoError(t, f.svc.Delete(f.ctx, a.ID))
				}
				session, err := f.svc.runSession(f.ctx, a, run)
				require.Error(t, err)
				require.Nil(t, session)
				var count int64
				require.NoError(t, f.db.Model(&types.Session{}).Count(&count).Error)
				require.Zero(t, count)
				require.Empty(t, f.reload(t, a.ID).PersistentSessionID)
				t.Logf("%s/%s: no conversation persisted", mode, state)
			})
		}
	}
}

func TestAutomationPostgresPruneRecoversChangedParentSnapshotAfterCleanupFailure(t *testing.T) {
	f, _ := newAutomationPostgresFixture(t)
	a := f.create(t, types.AutomationPatch{SessionMode: ptr(types.AutomationSessionPersistent)})
	run, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	started, err := f.svc.startRun(f.ctx, run, f.now)
	require.NoError(t, err)
	require.True(t, started)
	deleteCtx := context.WithValue(f.ctx, automationLifecycleActor{}, "delete")
	var session *types.Session
	var createErr error
	var changed sync.Once
	require.NoError(t, f.db.Callback().Query().After(
		"gorm:after_query",
	).Register("lifecycle:change_persistent_snapshot", func(
		tx *gorm.DB,
	) {
		if tx.Statement.Table != "automations" || tx.Statement.Context.Value(automationLifecycleActor{}) != "delete" {
			return
		}
		changed.Do(func() { session, createErr = f.svc.runSession(f.ctx, a, run) })
	}))
	cleanupErr := errors.New("injected conversation cleanup failure")
	require.NoError(t, f.db.Callback().Delete().Before("gorm:delete").Register("lifecycle:fail_cleanup", func(
		tx *gorm.DB,
	) {
		if tx.Statement.Table == "sessions" && tx.Statement.Context.Value(automationLifecycleActor{}) == "delete" {
			_ = tx.AddError(cleanupErr)
		}
	}))
	require.ErrorIs(t, f.svc.Delete(deleteCtx, a.ID), cleanupErr)
	require.NoError(t, createErr)
	require.NotNil(t, session)
	parent := f.reload(t, a.ID)
	require.True(t, parent.DeletedAt.Valid)
	require.Empty(t, a.PersistentSessionID, "Delete's original authorization snapshot predates session creation")
	require.Equal(t, session.ID, parent.PersistentSessionID)
	var row types.AutomationRun
	require.NoError(t, f.db.First(&row, "id = ?", run.ID).Error)
	require.Equal(t, session.ID, row.SessionID)
	require.True(t, row.CancelRequested)
	_, err = f.sessions.GetSession(f.ctx, session.ID)
	require.NoError(t, err, "injected cleanup failure left work for prune")
	// The executor observes durable cancellation even though Delete returned
	// a cleanup error. Prune later uses current database references, not a.
	f.svc.record(f.ctx, &row, runOutcome{status: types.AutomationRunCancelled})
	f.svc.prune(f.ctx)
	var sessions, runs int64
	require.NoError(t, f.db.Model(&types.Session{}).Count(&sessions).Error)
	require.NoError(t, f.db.Model(&types.AutomationRun{}).Count(&runs).Error)
	require.Zero(t, sessions)
	require.Zero(t, runs)
	t.Log("changed persistent snapshot + cleanup failure recovered: no conversations or runs remain")
}
