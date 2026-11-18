package event

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsyncEmitLogsHandlerErrorAndDrainsOnWait(t *testing.T) {
	var logBuf bytes.Buffer
	logger.SetOutput(&logBuf)
	logger.SetLogLevel(logger.LevelDebug)
	t.Cleanup(logger.ConfigureFromEnv)

	bus := NewAsyncEventBus()
	bus.On(EventQueryReceived, func(_ context.Context, _ Event) error {
		return errors.New("async-subscriber-failure-xyz")
	})

	require.NoError(t, bus.Emit(context.Background(), Event{Type: EventQueryReceived}))
	bus.Wait()

	assert.Contains(t, logBuf.String(), "async-subscriber-failure-xyz",
		"async EventBus must log handler errors instead of silently discarding them")
}

func TestEmitAndWaitSkipsNilSlotWithoutPanic(t *testing.T) {
	bus := NewEventBus()
	called := false
	bus.mu.Lock()
	bus.handlers[EventQueryReceived] = []EventHandler{
		nil,
		func(_ context.Context, _ Event) error {
			called = true
			return nil
		},
	}
	bus.mu.Unlock()

	require.NotPanics(t, func() {
		require.NoError(t, bus.EmitAndWait(context.Background(), Event{Type: EventQueryReceived}))
	})
	assert.True(t, called)
}
