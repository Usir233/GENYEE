package mattermost

import (
	"context"
	"sync"
	"testing"
	"time"
)

func registerTestMattermostStream(t *testing.T, id string, state *mmStreamState) {
	t.Helper()
	mmStreamsMu.Lock()
	mmStreams[id] = state
	mmStreamsMu.Unlock()
	t.Cleanup(func() {
		mmStreamsMu.Lock()
		delete(mmStreams, id)
		mmStreamsMu.Unlock()
	})
}

func hasTestMattermostStream(id string) bool {
	mmStreamsMu.Lock()
	defer mmStreamsMu.Unlock()
	_, ok := mmStreams[id]
	return ok
}

func TestStreamReaperPreservesLiveOwnerAndReclaimsAbandonedState(t *testing.T) {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registerTestMattermostStream(t, "mm-live-silent", &mmStreamState{
		lastActivityAt: now.Add(-2 * streamOrphanTTL),
		ownerDone:      ctx.Done(),
	})
	registerTestMattermostStream(t, "mm-abandoned", &mmStreamState{
		lastActivityAt: now.Add(-2 * streamOrphanTTL),
	})
	registerTestMattermostStream(t, "mm-recent", &mmStreamState{
		lastActivityAt: now,
	})

	reapOrphanStreams(now)
	if !hasTestMattermostStream("mm-live-silent") {
		t.Fatal("active silent Mattermost stream was prematurely reaped")
	}
	if hasTestMattermostStream("mm-abandoned") {
		t.Fatal("abandoned Mattermost stream was not reaped")
	}
	if !hasTestMattermostStream("mm-recent") {
		t.Fatal("recent Mattermost stream was prematurely reaped")
	}

	cancel()
	reapOrphanStreams(now.Add(streamReaperInterval))
	if !hasTestMattermostStream("mm-live-silent") {
		t.Fatal("cancelled Mattermost stream lost its final-delivery grace period")
	}
	reapOrphanStreams(now.Add(streamOrphanTTL + time.Second))
	if hasTestMattermostStream("mm-live-silent") {
		t.Fatal("cancelled abandoned Mattermost stream was not reclaimed")
	}
}

func TestStopStreamReaperRestartsOnNewAdapterAndIsConcurrentSafe(t *testing.T) {
	_ = NewAdapter(nil, "token", "bot", false)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			StopStreamReaper()
		}()
	}
	wg.Wait()

	_ = NewAdapter(nil, "token-2", "bot-2", false)
	t.Cleanup(StopStreamReaper)

	if !isStreamReaperRunning() {
		t.Fatal("expected Mattermost stream reaper to restart when NewAdapter is called after StopStreamReaper")
	}
}
