package event

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstalledGlobalBusReceivesFirstEmission(t *testing.T) {
	installed := NewEventBus()
	called := false
	installed.On(EventStop, func(context.Context, Event) error {
		called = true
		return nil
	})
	SetGlobalEventBus(installed)
	require.NoError(t, Emit(context.Background(), Event{Type: EventStop}))
	require.True(t, called, "lazy initialization must not replace the configured bus")
	require.Same(t, installed, GetGlobalEventBus())
}

func TestGlobalBusReplacementIsConcurrentSafe(_ *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				SetGlobalEventBus(NewEventBus())
				_ = Emit(context.Background(), Event{Type: EventStop})
			}
		}()
	}
	wg.Wait()
}
