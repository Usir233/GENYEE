package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/application/service"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/gin-gonic/gin"
)

func TestEmbedWebhookRelayVerifiesMessageIdentityAndLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("SSRF_WHITELIST_EXTRA", "127.0.0.1")
	bodies := make(chan map[string]any, 20)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode webhook: %v", err)
		}
		bodies <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()
	ch := testEmbedChannel()
	ch.ID = t.Name() + time.Now().Format("150405.000000000")
	ch.WebhookURL = hook.URL
	storedQuery := "[Host context]\nuserId: 21\n\nhello"
	messages := &stubMessageServiceForEmbedRelay{msgs: []*types.Message{
		{ID: "user-1", SessionID: testEmbedSessionID, Role: "user", Content: storedQuery, IsCompleted: true},
		{ID: "user-2", SessionID: testEmbedSessionID, Role: "user", Content: storedQuery, IsCompleted: true},
		{ID: "answer-1", SessionID: testEmbedSessionID, Role: "assistant", Content: "same answer", IsCompleted: false},
		{ID: "answer-2", SessionID: testEmbedSessionID, Role: "assistant", Content: "same answer", IsCompleted: true},
		{ID: "foreign", SessionID: "foreign-session", Role: "user", Content: "hello", IsCompleted: true},
		{ID: "suspended", SessionID: testEmbedSessionID, Role: "assistant", Content: "awaiting reply", IsCompleted: true, RunState: types.MessageRunStateAwaitingInput},
		{ID: "empty", SessionID: testEmbedSessionID, Role: "assistant", IsCompleted: true},
		{ID: "system", SessionID: testEmbedSessionID, Role: "system", Content: "secret", IsCompleted: true},
	}}
	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{sessions: map[string]*types.Session{testEmbedSessionID: validEmbedSession(ch)}},
		messageHandler: &MessageHandler{MessageService: messages},
	}
	sig := service.SignEmbedSessionHandle(ch, testEmbedSessionID)
	relay := func(eventType, messageID, signature string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"type": eventType, "message_id": messageID})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/sessions/"+testEmbedSessionID+"/events", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("X-Embed-Session", signature)
		c.Params = gin.Params{{Key: "session_id", Value: testEmbedSessionID}}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.EmbedChannelContextKey, ch))
		h.EmbedRelayWebhookEvent(c)
		return w
	}
	for _, tc := range []struct {
		name, event, id string
		status          int
	}{
		{"missing id", "message_sent", "", 400},
		{"unknown type", "forged", "user-1", 400},
		{"unpersisted id", "message_sent", "missing", 403},
		{"foreign session", "message_sent", "foreign", 403},
		{"wrong user role", "message_sent", "answer-2", 403},
		{"wrong assistant role", "message_received", "user-1", 403},
		{"system role", "message_received", "system", 403},
		{"not yet persisted complete", "message_received", "answer-1", 403},
		{"suspended", "message_received", "suspended", 403},
		{"empty content", "message_received", "empty", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := relay(tc.event, tc.id, sig)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body)
			}
		})
	}
	if w := relay("message_sent", "user-1", "invalid-signature"); w.Code != http.StatusForbidden {
		t.Fatalf("invalid session signature status = %d: %s", w.Code, w.Body)
	}
	messages.err = errors.New("storage unavailable")
	if w := relay("message_sent", "user-1", sig); w.Code != http.StatusInternalServerError {
		t.Fatalf("verification failure status = %d: %s", w.Code, w.Body)
	}
	messages.err = nil

	// Once persistence finishes the same ID can be delivered. Earlier rejected
	// attempts must not consume its dedup entry. Repeated content has distinct IDs.
	messages.msgs[2].IsCompleted = true
	for _, id := range []string{"user-1", "user-1", "user-2", "answer-1", "answer-1", "answer-2"} {
		event := "message_sent"
		if id == "answer-1" || id == "answer-2" {
			event = "message_received"
		}
		if w := relay(event, id, sig); w.Code != http.StatusOK {
			t.Fatalf("relay %s status = %d: %s", id, w.Code, w.Body)
		}
	}
	seen := map[string]bool{}
	for len(seen) < 4 {
		select {
		case body := <-bodies:
			id, _ := body["message_id"].(string)
			if seen[id] {
				t.Fatalf("duplicate webhook for %q", id)
			}
			seen[id] = true
			if body["session_id"] != testEmbedSessionID || body["channel_id"] != ch.ID {
				t.Fatalf("incorrect scope: %#v", body)
			}
			if body["type"] == "message_sent" && body["query"] != storedQuery {
				t.Fatalf("host context lost: %#v", body)
			}
			if body["type"] == "message_received" && body["content"] != "same answer" {
				t.Fatalf("incorrect answer: %#v", body)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing webhook deliveries: %#v", seen)
		}
	}
	select {
	case body := <-bodies:
		t.Fatalf("unexpected duplicate/rejected webhook: %#v", body)
	case <-time.After(50 * time.Millisecond):
	}
}
