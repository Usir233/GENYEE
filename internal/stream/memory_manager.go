package stream

import (
	"context"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

const defaultMemoryStreamTTL = time.Hour

// memoryStreamData holds stream events in memory.
type memoryStreamData struct {
	events      []interfaces.StreamEvent
	steerEvents []interfaces.StreamEvent // control-plane events, never surfaced on SSE
	lastUpdated time.Time
	mu          sync.RWMutex
}

// liveRunMarker names the run that is currently generating for a session.
type liveRunMarker struct {
	assistantMessageID string
	requestID          string
	updatedAt          time.Time
}

// MemoryStreamManager implements StreamManager using bounded TTL in-memory storage.
type MemoryStreamManager struct {
	// Map: sessionID -> messageID -> stream data
	streams map[string]map[string]*memoryStreamData
	// Map: sessionID -> the run currently generating. Single-process only,
	// which is why the memory backend is intended for Lite / single-replica mode.
	liveRuns  map[string]liveRunMarker
	ttl       time.Duration
	lastSweep time.Time
	mu        sync.RWMutex
}

// NewMemoryStreamManager creates a new in-memory stream manager with the default 1h TTL.
func NewMemoryStreamManager() *MemoryStreamManager {
	return NewMemoryStreamManagerWithTTL(defaultMemoryStreamTTL)
}

// NewMemoryStreamManagerWithTTL creates an in-memory stream manager with a custom TTL.
func NewMemoryStreamManagerWithTTL(ttl time.Duration) *MemoryStreamManager {
	if ttl <= 0 {
		ttl = defaultMemoryStreamTTL
	}
	return &MemoryStreamManager{
		streams:  make(map[string]map[string]*memoryStreamData),
		liveRuns: make(map[string]liveRunMarker),
		ttl:      ttl,
	}
}

// evictExpiredLocked removes expired streams and live-run markers under m.mu write lock.
func (m *MemoryStreamManager) evictExpiredLocked(now time.Time) {
	cutoff := now.Add(-m.ttl)
	for sessionID, marker := range m.liveRuns {
		if !marker.updatedAt.IsZero() && marker.updatedAt.Before(cutoff) {
			delete(m.liveRuns, sessionID)
		}
	}
	for sessionID, sessionMap := range m.streams {
		for messageID, data := range sessionMap {
			data.mu.RLock()
			expired := !data.lastUpdated.IsZero() && data.lastUpdated.Before(cutoff)
			data.mu.RUnlock()
			if expired {
				delete(sessionMap, messageID)
			}
		}
		if len(sessionMap) == 0 {
			delete(m.streams, sessionID)
		}
	}
	m.lastSweep = now
}

func (m *MemoryStreamManager) touchSessionLocked(sessionID string, now time.Time) {
	if sessionID == "" {
		return
	}
	if marker, ok := m.liveRuns[sessionID]; ok {
		marker.updatedAt = now
		m.liveRuns[sessionID] = marker
	}
}

// getOrCreateStream gets or creates stream data while sweeping expired entries.
func (m *MemoryStreamManager) getOrCreateStream(sessionID, messageID string) *memoryStreamData {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	m.evictExpiredLocked(now)
	m.touchSessionLocked(sessionID, now)

	if _, exists := m.streams[sessionID]; !exists {
		m.streams[sessionID] = make(map[string]*memoryStreamData)
	}

	if _, exists := m.streams[sessionID][messageID]; !exists {
		m.streams[sessionID][messageID] = &memoryStreamData{
			events:      make([]interfaces.StreamEvent, 0),
			lastUpdated: now,
		}
	}

	return m.streams[sessionID][messageID]
}

// getStream gets existing non-expired stream data (returns nil if missing or expired).
func (m *MemoryStreamManager) getStream(sessionID, messageID string, touchLive bool) *memoryStreamData {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	m.evictExpiredLocked(now)
	if touchLive {
		m.touchSessionLocked(sessionID, now)
	}

	if sessionMap, exists := m.streams[sessionID]; exists {
		return sessionMap[messageID]
	}
	return nil
}

func cloneStreamEvent(ev interfaces.StreamEvent) interfaces.StreamEvent {
	if ev.Data != nil {
		copied := make(map[string]interface{}, len(ev.Data))
		for k, v := range ev.Data {
			copied[k] = v
		}
		ev.Data = copied
	}
	return ev
}

// AppendEvent appends a single event to the stream.
func (m *MemoryStreamManager) AppendEvent(
	_ context.Context,
	sessionID, messageID string,
	event interfaces.StreamEvent,
) error {
	stream := m.getOrCreateStream(sessionID, messageID)

	stream.mu.Lock()
	defer stream.mu.Unlock()

	ev := cloneStreamEvent(event)
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}

	stream.events = append(stream.events, ev)
	stream.lastUpdated = time.Now()

	return nil
}

// ResetEvents drops the message's events, keeping its steer events.
func (m *MemoryStreamManager) ResetEvents(_ context.Context, sessionID, messageID string) error {
	stream := m.getStream(sessionID, messageID, false)
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.events = make([]interfaces.StreamEvent, 0)
	stream.lastUpdated = time.Now()
	return nil
}

// GetEvents gets events starting from offset, returning events slice, next offset, and error.
func (m *MemoryStreamManager) GetEvents(
	_ context.Context,
	sessionID, messageID string,
	fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	if fromOffset < 0 {
		fromOffset = 0
	}
	stream := m.getStream(sessionID, messageID, true)
	if stream == nil {
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	stream.mu.Lock()
	stream.lastUpdated = time.Now()
	if fromOffset >= len(stream.events) {
		stream.mu.Unlock()
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	events := stream.events[fromOffset:]
	nextOffset := len(stream.events)
	eventsCopy := make([]interfaces.StreamEvent, len(events))
	for i := range events {
		eventsCopy[i] = cloneStreamEvent(events[i])
	}
	stream.mu.Unlock()

	return eventsCopy, nextOffset, nil
}

// AppendSteerEvents appends control events to the dedicated steer sub-list.
func (m *MemoryStreamManager) AppendSteerEvents(
	_ context.Context,
	sessionID, messageID string,
	events []interfaces.StreamEvent,
) error {
	stream := m.getOrCreateStream(sessionID, messageID)

	stream.mu.Lock()
	defer stream.mu.Unlock()

	seen := make(map[string]bool, len(stream.steerEvents))
	for _, event := range stream.steerEvents {
		if event.ID != "" {
			seen[event.ID] = true
		}
	}
	for i := range events {
		ev := cloneStreamEvent(events[i])
		if ev.ID != "" && seen[ev.ID] {
			continue
		}
		if ev.ID != "" {
			seen[ev.ID] = true
		}
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now()
		}
		stream.steerEvents = append(stream.steerEvents, ev)
	}
	stream.lastUpdated = time.Now()
	return nil
}

// GetSteerEvents drains the steer sub-list starting fromOffset.
func (m *MemoryStreamManager) GetSteerEvents(
	_ context.Context,
	sessionID, messageID string,
	fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	if fromOffset < 0 {
		fromOffset = 0
	}
	stream := m.getStream(sessionID, messageID, true)
	if stream == nil {
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	stream.mu.Lock()
	stream.lastUpdated = time.Now()
	if fromOffset >= len(stream.steerEvents) {
		stream.mu.Unlock()
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	events := stream.steerEvents[fromOffset:]
	nextOffset := len(stream.steerEvents)
	eventsCopy := make([]interfaces.StreamEvent, len(events))
	for i := range events {
		eventsCopy[i] = cloneStreamEvent(events[i])
	}
	stream.mu.Unlock()
	return eventsCopy, nextOffset, nil
}

// UpdateSteerEventData merges keys into a queued steer event's Data map.
func (m *MemoryStreamManager) UpdateSteerEventData(
	_ context.Context,
	sessionID, messageID, eventID string,
	data map[string]interface{},
) (bool, error) {
	stream := m.getStream(sessionID, messageID, false)
	if stream == nil {
		return false, nil
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	for i := range stream.steerEvents {
		if stream.steerEvents[i].ID != eventID {
			continue
		}
		merged := make(map[string]interface{}, len(stream.steerEvents[i].Data)+len(data))
		for k, v := range stream.steerEvents[i].Data {
			merged[k] = v
		}
		for k, v := range data {
			merged[k] = v
		}
		stream.steerEvents[i].Data = merged
		stream.lastUpdated = time.Now()
		return true, nil
	}
	return false, nil
}

// DeleteSteerEvent removes a queued steer event by ID.
func (m *MemoryStreamManager) DeleteSteerEvent(
	_ context.Context,
	sessionID, messageID, eventID string,
) (bool, error) {
	stream := m.getStream(sessionID, messageID, false)
	if stream == nil {
		return false, nil
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	for i := range stream.steerEvents {
		if stream.steerEvents[i].ID != eventID {
			continue
		}
		if stream.steerEvents[i].Data != nil {
			if consumed, _ := stream.steerEvents[i].Data["consumed"].(bool); consumed {
				return false, nil
			}
		}
		stream.steerEvents = append(stream.steerEvents[:i], stream.steerEvents[i+1:]...)
		stream.lastUpdated = time.Now()
		return true, nil
	}
	return false, nil
}

// SetLiveRun records the session's currently generating assistant message.
func (m *MemoryStreamManager) SetLiveRun(
	_ context.Context,
	sessionID, assistantMessageID, requestID string,
) error {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictExpiredLocked(now)
	if existing, ok := m.liveRuns[sessionID]; ok && existing.assistantMessageID != assistantMessageID {
		return ErrLiveRunExists
	}
	m.liveRuns[sessionID] = liveRunMarker{
		assistantMessageID: assistantMessageID,
		requestID:          requestID,
		updatedAt:          now,
	}
	return nil
}

// ClaimLiveRun overwrites the live-run marker for follow-up handoff.
func (m *MemoryStreamManager) ClaimLiveRun(
	_ context.Context,
	sessionID, assistantMessageID, requestID string,
) error {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictExpiredLocked(now)
	m.liveRuns[sessionID] = liveRunMarker{
		assistantMessageID: assistantMessageID,
		requestID:          requestID,
		updatedAt:          now,
	}
	return nil
}

// GetLiveRun returns the session's generating assistant message, if any.
func (m *MemoryStreamManager) GetLiveRun(
	_ context.Context,
	sessionID string,
) (string, string, error) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictExpiredLocked(now)
	marker, ok := m.liveRuns[sessionID]
	if !ok {
		return "", "", nil
	}
	marker.updatedAt = now
	m.liveRuns[sessionID] = marker
	return marker.assistantMessageID, marker.requestID, nil
}

// ClearLiveRun drops the marker only when it still points at assistantMessageID.
func (m *MemoryStreamManager) ClearLiveRun(
	_ context.Context,
	sessionID, assistantMessageID string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if marker, ok := m.liveRuns[sessionID]; ok && marker.assistantMessageID == assistantMessageID {
		delete(m.liveRuns, sessionID)
	}
	return nil
}

// DropMessageStreams removes event and steer buffers for deleted messages so a
// later rewind cannot replay or steer a turn that no longer exists.
func (m *MemoryStreamManager) DropMessageStreams(
	_ context.Context, sessionID string, messageIDs []string,
) error {
	if sessionID == "" || len(messageIDs) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionMap := m.streams[sessionID]
	for _, messageID := range messageIDs {
		if sessionMap != nil {
			delete(sessionMap, messageID)
		}
		if marker, ok := m.liveRuns[sessionID]; ok && marker.assistantMessageID == messageID {
			delete(m.liveRuns, sessionID)
		}
	}
	if sessionMap != nil && len(sessionMap) == 0 {
		delete(m.streams, sessionID)
	}
	return nil
}

// Ensure MemoryStreamManager implements StreamManager interface.
var _ interfaces.StreamManager = (*MemoryStreamManager)(nil)
