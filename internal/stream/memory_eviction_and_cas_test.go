package stream

import (
	"context"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestMemoryStreamManagerEvictsExpiredStreamsAndLiveRuns(t *testing.T) {
	ttl := 40 * time.Millisecond
	mgr := NewMemoryStreamManagerWithTTL(ttl)
	ctx := context.Background()

	require.NoError(t, mgr.SetLiveRun(ctx, "sess-stale", "assist-stale", "req-stale"))
	require.NoError(t, mgr.AppendEvent(ctx, "sess-stale", "assist-stale", interfaces.StreamEvent{
		ID:      "e1",
		Type:    types.ResponseTypeAnswer,
		Content: "stale-chunk",
	}))

	require.NoError(t, mgr.SetLiveRun(ctx, "sess-active", "assist-active", "req-active"))
	require.NoError(t, mgr.AppendEvent(ctx, "sess-active", "assist-active", interfaces.StreamEvent{
		ID:      "e2",
		Type:    types.ResponseTypeAnswer,
		Content: "active-chunk",
	}))

	time.Sleep(25 * time.Millisecond)
	// Polling active session refreshes its live-run and stream TTL.
	activeEvents, _, err := mgr.GetEvents(ctx, "sess-active", "assist-active", 0)
	require.NoError(t, err)
	require.Len(t, activeEvents, 1)

	time.Sleep(25 * time.Millisecond)

	// Stale session exceeded 40ms without activity and must be evicted.
	staleLiveID, _, err := mgr.GetLiveRun(ctx, "sess-stale")
	require.NoError(t, err)
	require.Empty(t, staleLiveID, "expired memory liveRun must be evicted")

	staleEvents, _, err := mgr.GetEvents(ctx, "sess-stale", "assist-stale", 0)
	require.NoError(t, err)
	require.Empty(t, staleEvents, "expired memory stream buffer must be evicted")

	// Active session was refreshed at t=25ms so at t=50ms (< 25+40=65ms) it remains live.
	activeLiveID, _, err := mgr.GetLiveRun(ctx, "sess-active")
	require.NoError(t, err)
	require.Equal(t, "assist-active", activeLiveID)
}

func TestRedisClearLiveRunExactMatchCannotBeSpoofedByRequestID(t *testing.T) {
	mgr, mini := newTestRedisStreamManager(t, time.Hour)
	ctx := context.Background()

	// Even if a stored JSON payload contains a crafted request_id or whitespace variation,
	// ClearLiveRun must decode JSON and compare assistant_message_id exactly.
	require.NoError(t, mini.Set(
		mgr.buildLiveRunKey("sess-spoof"),
		`{"assistant_message_id": "assist-live", "request_id": "\"assistant_message_id\":\"assist-other\""}`,
	))

	require.NoError(t, mgr.ClearLiveRun(ctx, "sess-spoof", "assist-other"))
	liveID, _, err := mgr.GetLiveRun(ctx, "sess-spoof")
	require.NoError(t, err)
	require.Equal(t, "assist-live", liveID, "ClearLiveRun for a different message ID must keep live run")

	// Spaced JSON formatting ("assistant_message_id": "assist-live") must still clear cleanly when matched!
	require.NoError(t, mgr.ClearLiveRun(ctx, "sess-spoof", "assist-live"))
	liveID, _, err = mgr.GetLiveRun(ctx, "sess-spoof")
	require.NoError(t, err)
	require.Empty(t, liveID, "ClearLiveRun must clear matching assistant_message_id regardless of JSON spacing")
}
