// Package event implements the in-process synchronous and asynchronous event bus.
package event

import (
	"context"

	"github.com/Usir233/GENYEE/internal/types"
)

// EventBusAdapter adapts *EventBus to types.EventBusInterface.
// This allows EventBus to be used through the interface without circular dependencies.
//
//nolint:revive // EventBusAdapter is the historical name every caller uses.
type EventBusAdapter struct {
	bus *EventBus
}

// NewEventBusAdapter creates a new adapter for EventBus
func NewEventBusAdapter(bus *EventBus) types.EventBusInterface {
	return &EventBusAdapter{bus: bus}
}

// On registers an event handler for a specific event type
func (a *EventBusAdapter) On(eventType types.EventType, handler types.EventHandler) {
	if a == nil || a.bus == nil || handler == nil {
		return
	}
	evtType := EventType(eventType)
	evtHandler := func(ctx context.Context, evt Event) error {
		typesEvt := types.Event{
			ID:        evt.ID,
			Type:      types.EventType(evt.Type),
			SessionID: evt.SessionID,
			Data:      evt.Data,
			Metadata:  evt.Metadata,
			RequestID: evt.RequestID,
		}
		return handler(ctx, typesEvt)
	}

	a.bus.On(evtType, evtHandler)
}

// Emit publishes an event to all registered handlers
func (a *EventBusAdapter) Emit(ctx context.Context, evt types.Event) error {
	if a == nil || a.bus == nil {
		return nil
	}
	eventEvt := Event{
		ID:        evt.ID,
		Type:      EventType(evt.Type),
		SessionID: evt.SessionID,
		Data:      evt.Data,
		Metadata:  evt.Metadata,
		RequestID: evt.RequestID,
	}
	return a.bus.Emit(ctx, eventEvt)
}

// AsEventBusInterface converts *EventBus to types.EventBusInterface
func (eb *EventBus) AsEventBusInterface() types.EventBusInterface {
	return NewEventBusAdapter(eb)
}
