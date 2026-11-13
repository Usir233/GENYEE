package event

import (
	"context"
	"sync/atomic"
)

var globalEventBus atomic.Pointer[EventBus]

// GetGlobalEventBus returns the global event bus instance
// It uses singleton pattern to ensure only one instance exists
func GetGlobalEventBus() *EventBus {
	for {
		if bus := globalEventBus.Load(); bus != nil {
			return bus
		}
		// A configured bus (or another initializer) always wins over this lazy
		// default. The pointer is published atomically to concurrent emitters.
		bus := NewEventBus()
		if globalEventBus.CompareAndSwap(nil, bus) {
			return bus
		}
	}
}

// SetGlobalEventBus sets the global event bus instance
// This is useful for testing or custom configurations
func SetGlobalEventBus(bus *EventBus) {
	globalEventBus.Store(bus)
}

// On registers an event handler on the global event bus
func On(eventType EventType, handler EventHandler) {
	GetGlobalEventBus().On(eventType, handler)
}

// Off removes all handlers for a specific event type from the global event bus
func Off(eventType EventType) {
	GetGlobalEventBus().Off(eventType)
}

// Emit publishes an event to the global event bus
func Emit(ctx context.Context, event Event) error {
	return GetGlobalEventBus().Emit(ctx, event)
}

// EmitAndWait publishes an event to the global event bus and waits for all handlers
func EmitAndWait(ctx context.Context, event Event) error {
	return GetGlobalEventBus().EmitAndWait(ctx, event)
}

// HasHandlers checks if there are any handlers registered for an event type
func HasHandlers(eventType EventType) bool {
	return GetGlobalEventBus().HasHandlers(eventType)
}

// Clear removes all event handlers from the global event bus
func Clear() {
	GetGlobalEventBus().Clear()
}
