package service

import (
	"testing"

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
	require.NoError(t, f.db.Callback().Query().After("gorm:after_query").Register("audit:delete_automation", func(tx *gorm.DB) {
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
