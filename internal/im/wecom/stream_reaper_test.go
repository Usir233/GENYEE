package wecom

import (
	"context"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
)

func TestLongConnStreamReaperPreservesLiveOwnerAndCleansUpOnStop(t *testing.T) {
	client := &LongConnClient{}
	incoming := &im.IncomingMessage{
		Extra: map[string]string{"req_id": "req-1"},
	}

	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	liveID, err := client.StartStream(liveCtx, incoming)
	if err != nil {
		t.Fatalf("StartStream(live): %v", err)
	}

	abandonedCtx, abandonedCancel := context.WithCancel(context.Background())
	abandonedID, err := client.StartStream(abandonedCtx, incoming)
	if err != nil {
		t.Fatalf("StartStream(abandoned): %v", err)
	}
	abandonedCancel()

	now := time.Now()
	client.streamBufsMu.Lock()
	client.streamBufs[liveID].lastActivityAt = now.Add(-2 * wecomStreamOrphanTTL)
	client.streamBufs[abandonedID].lastActivityAt = now.Add(-2 * wecomStreamOrphanTTL)
	client.streamBufsMu.Unlock()

	client.reapOrphanStreams(now)

	client.streamBufsMu.Lock()
	_, liveExists := client.streamBufs[liveID]
	_, abandonedExists := client.streamBufs[abandonedID]
	client.streamBufsMu.Unlock()

	if !liveExists {
		t.Fatal("active silent WeCom stream was prematurely reaped")
	}
	if abandonedExists {
		t.Fatal("abandoned WeCom stream was not reaped")
	}

	client.Stop()
	client.streamBufsMu.Lock()
	remaining := len(client.streamBufs)
	client.streamBufsMu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected Stop() to clear WeCom stream buffers, got %d remaining", remaining)
	}
}
