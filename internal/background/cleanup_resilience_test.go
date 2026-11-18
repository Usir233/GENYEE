package background

import (
	"context"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type ctxAwareRegistry struct {
	*MemoryActivityRegistry
}

func (r *ctxAwareRegistry) Remove(ctx context.Context, s interfaces.SessionRef, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.MemoryActivityRegistry.Remove(ctx, s, id)
}

type cancellingEmitter struct {
	cancel context.CancelFunc
}

func (e *cancellingEmitter) EmitWorkFinished(
	_ context.Context, _ interfaces.SessionRef, _ string, _ interfaces.WorkResult,
) error {
	e.cancel()
	return nil
}

func TestFinishRemovesRegistrationEvenWhenContextCancelsDuringEmit(t *testing.T) {
	baseReg := NewMemoryActivityRegistry(nil)
	reg := &ctxAwareRegistry{MemoryActivityRegistry: baseReg}
	inbox := NewMemoryNoticeInbox(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := &Lifecycle{
		Registry: reg,
		Inbox:    inbox,
		Events:   &cancellingEmitter{cancel: cancel},
	}

	require.NoError(t, reg.Add(context.Background(), lcSess, lcActivity("sj_cancel"), time.Minute, 0))
	n := interfaces.BackgroundNotice{
		ID:         "sj_cancel",
		Kind:       "shell_job",
		Text:       "done",
		FinishedAt: time.Now(),
	}
	r := interfaces.WorkResult{ID: "sj_cancel", State: interfaces.WorkStateSucceeded, Terminal: true}

	require.NoError(t, l.Finish(ctx, lcSess, n, r, "msg-1"))
	require.Empty(t, liveIDs(t, baseReg),
		"once notice is in inbox, mid-emit cancellation must not strand activity in registry")
}
