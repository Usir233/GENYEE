package yunzhijia

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
	ws "github.com/gorilla/websocket"
)

func TestSendReplyAndWebSocketDial_RedactYzjTokenFromErrors(t *testing.T) {
	secretToken := "YZJ_SUPER_SECRET_TOKEN_987654"
	sendURL := "https://www.yunzhijia.com/gateway/robot/webhook/send?yzjtoken=" + secretToken

	adapter := NewAdapter(sendURL, "", "app-id", "app-secret", 10, "yunzhijia.com")
	adapter.httpClient.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})

	err := adapter.SendReply(context.Background(), &im.IncomingMessage{
		ChatType: im.ChatTypeDirect,
		UserID:   "u-1",
	}, &im.ReplyMessage{Content: "hello"})
	if err == nil {
		t.Fatal("expected SendReply error, got nil")
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("SendReply leaked yzjtoken in error: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected unwrapped DeadlineExceeded preserved, got: %v", err)
	}

	wsURL := "wss://127.0.0.1:1/ws?yzjtoken=" + secretToken
	lc := NewLongConnClient("ch-1", wsURL, func(context.Context, *im.IncomingMessage) error { return nil })
	dialErr := lc.connectAndRun(context.Background())
	if dialErr == nil {
		t.Fatal("expected connectAndRun dial error, got nil")
	}
	if strings.Contains(dialErr.Error(), secretToken) {
		t.Fatalf("connectAndRun leaked yzjtoken in error: %v", dialErr)
	}
}

func TestLongConnClient_StopRacingWithDialClosesConnectionImmediately(t *testing.T) {
	upgrader := ws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Hold socket open silently; if client fails to detect Stop() right after
		// DialContext, ReadMessage would block for webSocketReadTimeout (45s).
		<-r.Context().Done()
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	lc := NewLongConnClient("ch-race", wsURL, func(context.Context, *im.IncomingMessage) error { return nil })
	dialer := *ws.DefaultDialer
	lc.dialer = &dialer

	// Simulate Stop() landing right before connectAndRun stores c.conn.
	lc.closed.Store(true)

	done := make(chan error, 1)
	go func() {
		done <- lc.connectAndRun(context.Background())
	}()

	select {
	case err := <-done:
		_ = err
	case <-time.After(500 * time.Millisecond):
		lc.closeConn()
		t.Fatal("connectAndRun blocked on ReadMessage despite Stop() having already set closed=true")
	}
}
