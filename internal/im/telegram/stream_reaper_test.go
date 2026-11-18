package telegram

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
)

func registerTestTelegramStream(t *testing.T, id string, state *streamState) {
	t.Helper()
	streamsMu.Lock()
	streams[id] = state
	streamsMu.Unlock()
	t.Cleanup(func() {
		streamsMu.Lock()
		delete(streams, id)
		streamsMu.Unlock()
	})
}

func hasTestTelegramStream(id string) bool {
	streamsMu.Lock()
	defer streamsMu.Unlock()
	_, ok := streams[id]
	return ok
}

func TestStreamReaperPreservesLiveOwnerAndReclaimsAbandonedState(t *testing.T) {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registerTestTelegramStream(t, "tg-live-silent", &streamState{
		lastActivityAt: now.Add(-2 * streamOrphanTTL),
		ownerDone:      ctx.Done(),
	})
	registerTestTelegramStream(t, "tg-abandoned", &streamState{
		lastActivityAt: now.Add(-2 * streamOrphanTTL),
	})
	registerTestTelegramStream(t, "tg-recent", &streamState{
		lastActivityAt: now,
	})

	reapOrphanStreams(now)
	if !hasTestTelegramStream("tg-live-silent") {
		t.Fatal("active silent Telegram stream was prematurely reaped")
	}
	if hasTestTelegramStream("tg-abandoned") {
		t.Fatal("abandoned Telegram stream was not reaped")
	}
	if !hasTestTelegramStream("tg-recent") {
		t.Fatal("recent Telegram stream was prematurely reaped")
	}

	cancel()
	reapOrphanStreams(now.Add(streamReaperInterval))
	if !hasTestTelegramStream("tg-live-silent") {
		t.Fatal("cancelled Telegram stream lost its final-delivery grace period")
	}
	reapOrphanStreams(now.Add(streamOrphanTTL + time.Second))
	if hasTestTelegramStream("tg-live-silent") {
		t.Fatal("cancelled abandoned Telegram stream was not reclaimed")
	}
}

func TestStopStreamReaperRestartsOnNewAdapterAndIsConcurrentSafe(t *testing.T) {
	_ = NewWebhookAdapter("123:token", "secret")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			StopStreamReaper()
		}()
	}
	wg.Wait()

	_ = NewWebhookAdapter("123:token-2", "secret-2")
	t.Cleanup(StopStreamReaper)

	if !isStreamReaperRunning() {
		t.Fatal("expected Telegram stream reaper to restart when NewWebhookAdapter is called after StopStreamReaper")
	}
}

type repeatingByteReader struct {
	b         byte
	remaining int64
}

func (r *repeatingByteReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = r.b
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}

func TestDownloadFileBoundsOversizedAttachmentStream(t *testing.T) {
	const oversizedBytes = (33 << 20)
	origClient := httpClient
	httpClient = &http.Client{
		Transport: tgRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/getFile") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"file_path":"docs/huge.pdf"}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(&repeatingByteReader{b: 'x', remaining: oversizedBytes}),
				Header:     make(http.Header),
			}, nil
		}),
	}
	t.Cleanup(func() { httpClient = origClient })

	adapter := NewWebhookAdapter("123:token", "secret")
	t.Cleanup(StopStreamReaper)

	rc, _, err := adapter.DownloadFile(context.Background(), &im.IncomingMessage{
		FileKey:  "file-1",
		FileName: "huge.pdf",
	})
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	defer func() { _ = rc.Close() }()

	_, readErr := io.ReadAll(rc)
	if readErr == nil {
		t.Fatal("expected oversized Telegram attachment download to fail, got nil")
	}
}
