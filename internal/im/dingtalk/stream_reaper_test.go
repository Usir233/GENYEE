package dingtalk

import (
	"context"
	"sync"
	"testing"
	"time"
)

func registerTestDingTalkStream(t *testing.T, id string, state *streamState) {
	t.Helper()
	streamsMu.Lock()
	dStreams[id] = state
	streamsMu.Unlock()
	t.Cleanup(func() {
		streamsMu.Lock()
		delete(dStreams, id)
		streamsMu.Unlock()
	})
}

func hasTestDingTalkStream(id string) bool {
	streamsMu.Lock()
	defer streamsMu.Unlock()
	_, ok := dStreams[id]
	return ok
}

func TestStreamReaperPreservesLiveOwnerAndReclaimsAbandonedState(t *testing.T) {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registerTestDingTalkStream(t, "dt-live-silent", &streamState{
		lastActivityAt: now.Add(-2 * streamOrphanTTL),
		ownerDone:      ctx.Done(),
	})
	registerTestDingTalkStream(t, "dt-abandoned", &streamState{
		lastActivityAt: now.Add(-2 * streamOrphanTTL),
	})
	registerTestDingTalkStream(t, "dt-recent", &streamState{
		lastActivityAt: now,
	})

	reapOrphanStreams(now)
	if !hasTestDingTalkStream("dt-live-silent") {
		t.Fatal("active silent DingTalk stream was prematurely reaped")
	}
	if hasTestDingTalkStream("dt-abandoned") {
		t.Fatal("abandoned DingTalk stream was not reaped")
	}
	if !hasTestDingTalkStream("dt-recent") {
		t.Fatal("recent DingTalk stream was prematurely reaped")
	}

	cancel()
	reapOrphanStreams(now.Add(streamReaperInterval))
	if !hasTestDingTalkStream("dt-live-silent") {
		t.Fatal("cancelled DingTalk stream lost its final-delivery grace period")
	}
	reapOrphanStreams(now.Add(streamOrphanTTL + time.Second))
	if hasTestDingTalkStream("dt-live-silent") {
		t.Fatal("cancelled abandoned DingTalk stream was not reclaimed")
	}
}

func TestStopStreamReaperRestartsOnNewAdapterAndIsConcurrentSafe(t *testing.T) {
	_ = NewAdapter("cid", "sec", "")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			StopStreamReaper()
		}()
	}
	wg.Wait()

	_ = NewWebhookAdapter("cid-2", "sec-2", "")
	t.Cleanup(StopStreamReaper)

	if !isStreamReaperRunning() {
		t.Fatal("expected DingTalk stream reaper to restart when NewWebhookAdapter is called after StopStreamReaper")
	}
}
