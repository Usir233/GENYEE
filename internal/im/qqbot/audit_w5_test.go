package qqbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
	ws "github.com/gorilla/websocket"
)

func TestClient_GatewayURLUsesConfiguredAPIBaseAndValidatesOpenIDs(t *testing.T) {
	withQQBotSSRFWhitelist(t, "127.0.0.1")

	var gatewayHits, msgHits atomic.Int32
	var wsURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gateway":
			gatewayHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"url": wsURL})
		default:
			msgHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	wsURL = "wss://127.0.0.1/ws"

	client, err := NewClient("app-1", "sec-1", srv.URL, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.accessToken = "cached-tok"
	client.expiresAt = time.Now().Add(time.Hour)

	gotGW, err := client.GatewayURL(context.Background())
	if err != nil {
		t.Fatalf("GatewayURL() error = %v", err)
	}
	if gotGW != wsURL || gatewayHits.Load() != 1 {
		t.Fatalf("GatewayURL() = %q (hits=%d), want %q from configured apiBaseURL", gotGW, gatewayHits.Load(), wsURL)
	}

	for _, badID := range []string{"", "../admin", "u1/../../v2", "u1?x=1", "u1#frag", "u 1"} {
		if err := client.SendC2CMessage(context.Background(), badID, "hi", "m1"); err == nil {
			t.Errorf("SendC2CMessage(%q) = nil, want error", badID)
		}
		if err := client.SendGroupMessage(context.Background(), badID, "hi", "m1"); err == nil {
			t.Errorf("SendGroupMessage(%q) = nil, want error", badID)
		}
	}
	if msgHits.Load() != 0 {
		t.Fatalf("malicious openIDs triggered %d HTTP requests, want 0", msgHits.Load())
	}
}

func TestLongConnClient_ConcurrentSeqHeartbeatAndStopSafety(t *testing.T) {
	withQQBotSSRFWhitelist(t, "127.0.0.1")

	upgrader := ws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	dispatched := make(chan struct{}, 32)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send opHello with a 5ms heartbeat interval so heartbeatLoop races with seq updates.
		_ = conn.WriteJSON(map[string]any{
			"op": opHello,
			"d":  map[string]any{"heartbeat_interval": 5},
		})

		// Read identify + heartbeats in background so server socket doesn't stall.
		done := make(chan struct{})
		defer close(done)
		go func() {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()

		for seq := int64(1); seq <= 25; seq++ {
			s := seq
			ev, _ := json.Marshal(messageEvent{
				ID:      "m",
				Content: "hi",
				Author:  qqbotAuthor{UserOpenID: "u1"},
			})
			if err := conn.WriteJSON(gatewayPayload{
				Op: opDispatch,
				S:  &s,
				T:  eventC2CMessageCreate,
				D:  ev,
			}); err != nil {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		<-done
	}))
	defer srv.Close()

	wsAddr := "ws" + strings.TrimPrefix(srv.URL, "http")
	client := &Client{
		appID:        "app",
		clientSecret: "sec",
		apiBaseURL:   srv.URL,
		gatewayURL:   wsAddr,
		accessToken:  "tok",
		expiresAt:    time.Now().Add(time.Hour),
		httpClient:   srv.Client(),
	}

	lc := NewLongConnClient(client, func(_ context.Context, _ *im.IncomingMessage) error {
		select {
		case dispatched <- struct{}{}:
		default:
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- lc.Start(ctx)
	}()

	// Wait until several dispatch events and heartbeats have raced.
	for i := 0; i < 10; i++ {
		select {
		case <-dispatched:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for dispatched qqbot messages")
		}
	}

	lc.Stop()
	cancel()

	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("LongConnClient.Start did not exit promptly after Stop()")
	}
}
