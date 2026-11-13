package service

import (
	"context"
	"errors"
	"testing"

	apprepo "github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed settlement must retain both the durable op and its finalizing slot.
// Exercise real SQLite writes and the complete batch retry without contacting
// a model provider.
func TestWikiDeadLetterFailureKeepsPendingAndFinalizingSlot(t *testing.T) {
	for _, failure := range []struct {
		name, trigger string
	}{
		{"archive", "CREATE TRIGGER fail_settlement BEFORE INSERT ON task_dead_letters BEGIN SELECT RAISE(FAIL, 'archive unavailable'); END"},
		{"delete", "CREATE TRIGGER fail_settlement BEFORE DELETE ON task_pending_ops BEGIN SELECT RAISE(FAIL, 'delete unavailable'); END"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			svc, db := newWikiLookupRecoveryService(t, "kb-1", nil)
			require.NoError(t, db.AutoMigrate(&types.TaskDeadLetter{}))
			svc.deadLetterRepo = apprepo.NewTaskDeadLetterRepository(db)
			task := seedWikiLookupTask(t, svc, db, "kb-1", "unreadable")
			require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("dedup_key = ?", "unreadable").Update("fail_count", wikiMaxFailRetries).Error)
			// A different enrichment task still owns the second slot.
			require.NoError(t, db.Exec("UPDATE knowledges SET pending_subtasks_count = 2 WHERE id = ?", "unreadable").Error)
			svc.knowledgeSvc.(*wikiKnowledgeLookupStub).lookup = func(context.Context, string) (*types.Knowledge, error) {
				return nil, errors.New("persistent lookup failure")
			}
			require.NoError(t, db.Exec(failure.trigger).Error)
			require.Error(t, svc.ProcessWikiIngest(context.Background(), task))
			var pending []types.TaskPendingOp
			require.NoError(t, db.Find(&pending).Error)
			assert.Len(t, pending, 1, "failed settlement must not lose the only retryable record")
			kn, err := svc.knowledgeRepo.GetKnowledgeByIDOnly(context.Background(), "unreadable")
			require.NoError(t, err)
			assert.Equal(t, 2, kn.PendingSubtasksCount, "no slot is released before settlement succeeds")
			assert.Equal(t, types.ParseStatusFinalizing, kn.ParseStatus)

			require.NoError(t, db.Exec("DROP TRIGGER fail_settlement").Error)
			require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
			count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, "kb-1")
			require.NoError(t, err)
			assert.Zero(t, count)
			var letters []types.TaskDeadLetter
			require.NoError(t, db.Find(&letters).Error)
			require.NotEmpty(t, letters, "failed work must remain available to operators")
			kn, err = svc.knowledgeRepo.GetKnowledgeByIDOnly(context.Background(), "unreadable")
			require.NoError(t, err)
			assert.Equal(t, 1, kn.PendingSubtasksCount, "retry must not consume another enrichment task's slot")
			assert.Equal(t, types.ParseStatusFinalizing, kn.ParseStatus)
		})
	}
}
