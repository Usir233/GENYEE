package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/application/service"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/gin-gonic/gin"
)

func TestEmbedRelayWebhookEventRejectsStaleMessagesAndHonorsCreatedAtFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("SSRF_WHITELIST_EXTRA", "127.0.0.1")

	bodies := make(chan map[string]any, 10)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies <- body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	now := time.Now()
	ch := testEmbedChannel()
	ch.ID = t.Name() + now.Format("150405.000000000")
	ch.WebhookURL = hook.URL

	messages := &stubMessageServiceForEmbedRelay{msgs: []*types.Message{
		{
			ID:          "fresh-updated",
			SessionID:   testEmbedSessionID,
			Role:        "user",
			Content:     "fresh question",
			IsCompleted: true,
			CreatedAt:   now.Add(-45 * time.Minute),
			UpdatedAt:   now.Add(-5 * time.Minute),
		},
		{
			ID:          "stale-updated",
			SessionID:   testEmbedSessionID,
			Role:        "user",
			Content:     "old question",
			IsCompleted: true,
			CreatedAt:   now.Add(-40 * time.Minute),
			UpdatedAt:   now.Add(-31 * time.Minute),
		},
		{
			ID:          "fresh-created-fallback",
			SessionID:   testEmbedSessionID,
			Role:        "assistant",
			Content:     "fresh answer via CreatedAt",
			IsCompleted: true,
			CreatedAt:   now.Add(-10 * time.Minute),
		},
		{
			ID:          "stale-created-fallback",
			SessionID:   testEmbedSessionID,
			Role:        "assistant",
			Content:     "stale answer via CreatedAt",
			IsCompleted: true,
			CreatedAt:   now.Add(-35 * time.Minute),
		},
	}}
	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{testEmbedSessionID: validEmbedSession(ch)},
		},
		messageHandler: &MessageHandler{MessageService: messages},
	}
	sig := service.SignEmbedSessionHandle(ch, testEmbedSessionID)

	relay := func(eventType, messageID string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"type": eventType, "message_id": messageID})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(
			http.MethodPost, "/sessions/"+testEmbedSessionID+"/events", bytes.NewReader(body),
		)
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("X-Embed-Session", sig)
		c.Params = gin.Params{{Key: "session_id", Value: testEmbedSessionID}}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.EmbedChannelContextKey, ch))
		h.EmbedRelayWebhookEvent(c)
		return w
	}

	if w := relay("message_sent", "stale-updated"); w.Code != http.StatusForbidden {
		t.Fatalf("stale UpdatedAt status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := relay("message_received", "stale-created-fallback"); w.Code != http.StatusForbidden {
		t.Fatalf("stale CreatedAt fallback status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := relay("message_sent", "fresh-updated"); w.Code != http.StatusOK {
		t.Fatalf("fresh UpdatedAt status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if w := relay("message_received", "fresh-created-fallback"); w.Code != http.StatusOK {
		t.Fatalf("fresh CreatedAt fallback status = %d, want 200: %s", w.Code, w.Body.String())
	}

	delivered := map[string]bool{}
	for len(delivered) < 2 {
		select {
		case body := <-bodies:
			id, _ := body["message_id"].(string)
			delivered[id] = true
		case <-time.After(time.Second):
			t.Fatalf("missing fresh deliveries, got %#v", delivered)
		}
	}
	if !delivered["fresh-updated"] || !delivered["fresh-created-fallback"] {
		t.Fatalf("unexpected delivered set: %#v", delivered)
	}
	select {
	case extra := <-bodies:
		t.Fatalf("unexpected stale webhook delivery: %#v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestClaimEmbedRelayedEventOnceBoundsDedupMapSize(t *testing.T) {
	base := time.Now()
	prefix := t.Name() + "|"
	for i := 0; i < embedRelayedEventMaxEntries+500; i++ {
		key := fmt.Sprintf("%s%d", prefix, i)
		if !claimEmbedRelayedEventOnce(key, base.Add(time.Duration(i)*time.Millisecond)) {
			t.Fatalf("first claim for %q unexpectedly failed", key)
		}
	}
	embedRelayedMessageSeen.mu.Lock()
	gotLen := len(embedRelayedMessageSeen.entries)
	embedRelayedMessageSeen.mu.Unlock()
	if gotLen > embedRelayedEventMaxEntries {
		t.Fatalf("embedRelayedMessageSeen size = %d, exceeds cap %d", gotLen, embedRelayedEventMaxEntries)
	}
}
