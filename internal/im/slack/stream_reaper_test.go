package slack

import (
	"context"
	"sync"
	"testing"
	"time"
)

func registerTestSlackStream(t *testing.T, id string, state *slackStreamState) {
	t.Helper()
	slackStreamsMu.Lock()
	slackStreams[id] = state
	slackStreamsMu.Unlock()
	t.Cleanup(func() {
		slackStreamsMu.Lock()
		delete(slackStreams, id)
		slackStreamsMu.Unlock()
	})
}

func hasTestSlackStream(id string) bool {
	slackStreamsMu.Lock()
	defer slackStreamsMu.Unlock()
	_, ok := slackStreams[id]
	return ok
}

func TestStreamReaperPreservesLiveOwnerAndReclaimsAbandonedState(t *testing.T) {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registerTestSlackStream(t, "sl-live-silent", &slackStreamState{
		lastActivityAt: now.Add(-2 * streamOrphanTTL),
		ownerDone:      ctx.Done(),
	})
	registerTestSlackStream(t, "sl-abandoned", &slackStreamState{
		lastActivityAt: now.Add(-2 * streamOrphanTTL),
	})
	registerTestSlackStream(t, "sl-recent", &slackStreamState{
		lastActivityAt: now,
	})

	reapOrphanStreams(now)
	if !hasTestSlackStream("sl-live-silent") {
		t.Fatal("active silent Slack stream was prematurely reaped")
	}
	if hasTestSlackStream("sl-abandoned") {
		t.Fatal("abandoned Slack stream was not reaped")
	}
	if !hasTestSlackStream("sl-recent") {
		t.Fatal("recent Slack stream was prematurely reaped")
	}

	cancel()
	reapOrphanStreams(now.Add(streamReaperInterval))
	if !hasTestSlackStream("sl-live-silent") {
		t.Fatal("cancelled Slack stream lost its final-delivery grace period")
	}
	reapOrphanStreams(now.Add(streamOrphanTTL + time.Second))
	if hasTestSlackStream("sl-live-silent") {
		t.Fatal("cancelled abandoned Slack stream was not reclaimed")
	}
}

func TestStopStreamReaperRestartsOnNewAdapterAndIsConcurrentSafe(t *testing.T) {
	_ = NewWebhookAdapter(nil, "secret")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			StopStreamReaper()
		}()
	}
	wg.Wait()

	_ = NewAdapter(nil, nil)
	t.Cleanup(StopStreamReaper)

	if !isStreamReaperRunning() {
		t.Fatal("expected Slack stream reaper to restart when NewAdapter is called after StopStreamReaper")
	}
}
