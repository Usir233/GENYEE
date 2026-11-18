package wechat

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/types"
)

type wcRoundTripFunc func(*http.Request) (*http.Response, error)

func (f wcRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestQRCodeService_IsolatesTenantsAndRedactsSecretsOnDecodeError(t *testing.T) {
	svc := NewQRCodeService()
	var statusHits atomic.Int32
	secretBotToken := "ilink-bot-secret-token-999"

	svc.client = &http.Client{
		Transport: wcRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/ilink/bot/get_bot_qrcode":
				qrBody := `{"qrcode":"qr-tenant-100","qrcode_img_content":"https://ilink.example/qr"}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(qrBody)),
					Header:     make(http.Header),
				}, nil
			case "/ilink/bot/get_qrcode_status":
				statusHits.Add(1)
				if r.URL.Query().Get("qrcode") == "qr-malformed" {
					// Truncated JSON containing a sensitive bot_token fragment.
					malformed := `{"status":"confirmed","bot_token":"` + secretBotToken
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(malformed)),
						Header:     make(http.Header),
					}, nil
				}
				confirmed := `{"status":"confirmed","bot_token":"` + secretBotToken + `","ilink_bot_id":"bot-1"}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(confirmed)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
		}),
	}

	ctxTenant100 := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(100))
	ctxTenant200 := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(200))

	qrRes, err := svc.GetLoginQRCode(ctxTenant100)
	if err != nil {
		t.Fatalf("GetLoginQRCode: %v", err)
	}
	if qrRes.QRCode != "qr-tenant-100" {
		t.Fatalf("QRCode = %q, want qr-tenant-100", qrRes.QRCode)
	}

	// Cross-tenant polling of tenant 100's QR token by tenant 200 must fail closed
	// without hitting the upstream status API or returning tenant 100's bot_token.
	if _, err := svc.PollQRCodeStatus(ctxTenant200, qrRes.QRCode); err == nil {
		t.Fatal("expected cross-tenant PollQRCodeStatus to be rejected, got nil")
	}
	if statusHits.Load() != 0 {
		t.Fatalf("cross-tenant PollQRCodeStatus hit upstream %d times, want 0", statusHits.Load())
	}

	// Same tenant succeeds.
	loginRes, err := svc.PollQRCodeStatus(ctxTenant100, qrRes.QRCode)
	if err != nil {
		t.Fatalf("same-tenant PollQRCodeStatus failed: %v", err)
	}
	if loginRes.BotToken != secretBotToken {
		t.Fatalf("BotToken = %q, want %q", loginRes.BotToken, secretBotToken)
	}

	// Malformed upstream JSON must NOT dump raw body containing bot_token into error.
	_, err = svc.PollQRCodeStatus(context.Background(), "qr-malformed")
	if err == nil {
		t.Fatal("expected decode error for malformed upstream JSON, got nil")
	}
	if strings.Contains(err.Error(), secretBotToken) {
		t.Fatalf("PollQRCodeStatus decode error leaked bot_token: %v", err)
	}
}

func TestLongPollClient_WaitsForInFlightHandlersOnStop(t *testing.T) {
	handlerEntered := make(chan struct{})
	releaseHandler := make(chan struct{})
	var handlerFinished atomic.Bool

	var pollCalls atomic.Int32
	lc := NewLongPollClient("bot-tok", "bot-id", func(_ context.Context, _ *im.IncomingMessage) error {
		close(handlerEntered)
		<-releaseHandler
		handlerFinished.Store(true)
		return nil
	})
	lc.httpClient = &http.Client{
		Transport: wcRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if pollCalls.Add(1) == 1 {
				body := `{"ret":0,"errcode":0,"get_updates_buf":"cur-1",` +
					`"msgs":[{"message_id":1,"from_user_id":"u1","message_type":1,` +
					`"context_token":"ct","item_list":[{"type":1,"text_item":{"text":"hello"}}]}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan error, 1)
	go func() {
		startDone <- lc.Start(ctx)
	}()

	select {
	case <-handlerEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message handler to start")
	}

	// Cancel polling while handler is still in flight; Start must wait for
	// in-flight message handlers to complete before returning.
	cancel()
	select {
	case err := <-startDone:
		_ = err
		if !handlerFinished.Load() {
			t.Fatal("LongPollClient.Start returned before in-flight message handler finished")
		}
	case <-time.After(40 * time.Millisecond):
		// Expected: Start is waiting on wg.Wait().
	}

	close(releaseHandler)
	select {
	case err := <-startDone:
		_ = err
		if !handlerFinished.Load() {
			t.Fatal("handler should have finished before Start returned")
		}
	case <-time.After(time.Second):
		t.Fatal("LongPollClient.Start did not return after handler finished")
	}
}
