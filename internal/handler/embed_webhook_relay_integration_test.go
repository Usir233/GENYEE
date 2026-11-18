package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/application/service"
	"github.com/Usir233/GENYEE/internal/middleware"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The relay must pass both channel authentication and the message service's
// owner-scoped read using the session principal installed by ensureEmbedSession.
// All services/repositories exercised here are real; the only network peer is
// a local webhook receiver, and the SQLite file belongs to this test alone.
func TestEmbedWebhookRelaySQLiteAuthIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("SSRF_WHITELIST_EXTRA", "127.0.0.1")

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "embed-relay.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(
		t,
		db.AutoMigrate(
			&types.Tenant{}, &types.EmbedChannel{}, &types.Session{}, &types.Message{},
			&types.MessageArtifactRecord{},
		),
	)
	// Session reads check for IM ownership too. No IM sessions exist in this
	// fixture, but retain the actual SQL lookup rather than stubbing it out.
	require.NoError(t, db.Exec(`CREATE TABLE im_channel_sessions (
		id TEXT PRIMARY KEY, session_id TEXT NOT NULL, platform TEXT NOT NULL
	)`).Error)

	type delivery struct {
		raw       []byte
		signature string
	}
	deliveries := make(chan delivery, 20)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read webhook body: %v", readErr)
		}
		deliveries <- delivery{raw: raw, signature: r.Header.Get("X-Genyee-Signature")}
		w.WriteHeader(http.StatusOK)
	}))
	defer webhook.Close()

	tenantRepo := repository.NewTenantRepository(db)
	tenantSvc := service.NewTenantService(tenantRepo, nil, nil)
	tenant := &types.Tenant{Name: "embed workspace", Status: "active"}
	otherTenant := &types.Tenant{Name: "other workspace", Status: "active"}
	require.NoError(t, tenantRepo.CreateTenant(t.Context(), tenant))
	require.NoError(t, tenantRepo.CreateTenant(t.Context(), otherTenant))
	channelRepo := repository.NewEmbedChannelRepository(db)
	channelSvc := service.NewEmbedChannelService(channelRepo, nil, nil, nil)
	channel := &types.EmbedChannel{
		TenantID: tenant.ID, Enabled: true, PublishToken: "em_integration_publish",
		AllowedOrigins: types.JSON(`["https://partner.example.com"]`),
		WebhookURL:     webhook.URL, WebhookSecret: "integration-webhook-secret",
	}
	require.NoError(t, channelRepo.Create(t.Context(), channel))

	sessionRepo := repository.NewSessionRepository(db)
	messageRepo := repository.NewMessageRepository(db)
	sessionSvc := service.NewSessionService(
		nil, sessionRepo, messageRepo,
		nil, nil, nil, nil, tenantSvc,
		nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, service.HostSandboxManager{},
		nil, nil, nil, nil, nil,
	)
	messageSvc := service.NewMessageService(messageRepo, sessionRepo, tenantSvc, nil, nil, nil, nil, nil)

	createSession := func(tenantID uint64, channelID string) (*types.Session, context.Context) {
		t.Helper()
		ctx := context.WithValue(t.Context(), types.TenantIDContextKey, tenantID)
		session, createErr := sessionSvc.CreateSession(ctx, &types.Session{
			TenantID: tenantID, Description: service.EmbedSessionDescription(channelID),
		})
		require.NoError(t, createErr)
		principal := types.EmbedSessionPrincipal(tenantID, channelID, session.ID)
		require.NoError(t, sessionSvc.SetSessionOwnerID(ctx, tenantID, session.ID, principal.StorageID()))
		stored, getErr := sessionRepo.GetByID(ctx, tenantID, session.ID)
		require.NoError(t, getErr)
		require.Equal(t, principal.StorageID(), stored.UserID)
		require.Equal(t, types.AuthSourceEmbed, stored.OwnerAuthSource)
		ctx = types.WithPrincipalAuthSource(types.WithPrincipal(ctx, principal), types.AuthSourceEmbed)
		return stored, ctx
	}
	createMessage := func(
		ctx context.Context, session *types.Session, role, content string, completed bool,
	) *types.Message {
		t.Helper()
		message, createErr := messageSvc.CreateMessage(ctx, &types.Message{
			SessionID: session.ID, Role: role, Content: content, IsCompleted: completed,
		})
		require.NoError(t, createErr)
		return message
	}
	own, ownCtx := createSession(tenant.ID, channel.ID)
	visitor, visitorCtx := createSession(tenant.ID, channel.ID)
	external, externalCtx := createSession(tenant.ID, "another-channel")
	crossTenant, crossTenantCtx := createSession(otherTenant.ID, channel.ID)
	storedQuery := "[Host context]\nuserId: 21\n\nhello"
	user := createMessage(ownCtx, own, "user", storedQuery, true)
	assistant := createMessage(ownCtx, own, "assistant", "persisted assistant reply", true)
	pending := createMessage(ownCtx, own, "assistant", "not finished", false)
	retry := createMessage(ownCtx, own, "user", "deliver after storage recovers", true)
	visitorMessage := createMessage(visitorCtx, visitor, "user", "other visitor secret", true)
	externalMessage := createMessage(externalCtx, external, "user", "other channel secret", true)
	crossTenantMessage := createMessage(crossTenantCtx, crossTenant, "user", "other workspace secret", true)

	h := &EmbedChannelHandler{sessionService: sessionSvc, messageHandler: &MessageHandler{MessageService: messageSvc}}
	router := gin.New()
	router.POST("/api/v1/embed/:channel_id/sessions/:session_id/events",
		middleware.EmbedAuth(channelSvc, tenantSvc, nil), func(c *gin.Context) {
			h.EmbedRelayWebhookEvent(c)
			if c.Writer.Status() == http.StatusOK {
				// No test middleware injects auth context. The real middleware's
				// channel user must resolve to the session owner after verification.
				ctx := c.Request.Context()
				require.Equal(t, tenant.ID, types.MustTenantIDFromContext(ctx))
				require.Equal(t, types.TenantRoleViewer, types.TenantRoleFromContext(ctx))
				require.Equal(t, own.UserID, types.SessionOwnerIDFromContext(ctx))
				principal, ok := types.PrincipalFromContext(ctx)
				require.True(t, ok)
				require.Equal(t, types.EmbedSessionPrincipal(tenant.ID, channel.ID, own.ID), principal)
			}
		})
	relay := func(sessionID, messageID, eventType, signature, token string) *httptest.ResponseRecorder {
		t.Helper()
		body, marshalErr := json.Marshal(map[string]any{
			"type": eventType, "message_id": messageID, "query": "forged client override", "content": "forged reply",
		})
		require.NoError(t, marshalErr)
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/embed/"+channel.ID+"/sessions/"+sessionID+"/events", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://partner.example.com")
		req.Header.Set("Authorization", "Embed "+token)
		req.Header.Set("X-Embed-Session", signature)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	ownSig := service.SignEmbedSessionHandle(channel, own.ID)
	assertDelivery := func(message *types.Message, eventType, contentKey string) {
		t.Helper()
		select {
		case delivered := <-deliveries:
			var payload map[string]any
			require.NoError(t, json.Unmarshal(delivered.raw, &payload))
			require.Equal(t, eventType, payload["type"])
			require.Equal(t, channel.ID, payload["channel_id"])
			require.Equal(t, own.ID, payload["session_id"])
			require.Equal(t, message.ID, payload["message_id"])
			require.Equal(t, message.Content, payload[contentKey])
			require.Equal(
				t, "sha256="+service.SignEmbedWebhookBody(channel.WebhookSecret, delivered.raw),
				delivered.signature,
			)
		case <-time.After(2 * time.Second):
			t.Fatal("authorized persisted message did not reach the local webhook")
		}
	}

	for _, pair := range []struct {
		message               *types.Message
		eventType, contentKey string
	}{
		{user, "message_sent", "query"}, {assistant, "message_received", "content"},
	} {
		w := relay(own.ID, pair.message.ID, pair.eventType, ownSig, channel.PublishToken)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assertDelivery(pair.message, pair.eventType, pair.contentKey)
	}

	for _, tc := range []struct {
		name, sessionID, messageID, eventType, signature, token string
		status                                                  int
	}{
		{"invalid publish token", own.ID, user.ID, "message_sent", ownSig, "wrong-token", http.StatusUnauthorized},
		{
			"invalid session signature", own.ID, user.ID, "message_sent", "wrong-signature", channel.PublishToken,
			http.StatusForbidden,
		},
		{
			"other visitor handle", visitor.ID, visitorMessage.ID, "message_sent", ownSig, channel.PublishToken,
			http.StatusForbidden,
		},
		{
			"other channel session", external.ID, externalMessage.ID, "message_sent",
			service.SignEmbedSessionHandle(channel, external.ID), channel.PublishToken, http.StatusForbidden,
		},
		{
			"other workspace session", crossTenant.ID, crossTenantMessage.ID, "message_sent",
			service.SignEmbedSessionHandle(channel, crossTenant.ID), channel.PublishToken, http.StatusNotFound,
		},
		{
			"other visitor message ID", own.ID, visitorMessage.ID, "message_sent", ownSig, channel.PublishToken,
			http.StatusForbidden,
		},
		{
			"other channel message ID", own.ID, externalMessage.ID, "message_sent", ownSig, channel.PublishToken,
			http.StatusForbidden,
		},
		{
			"other workspace message ID", own.ID, crossTenantMessage.ID, "message_sent", ownSig,
			channel.PublishToken, http.StatusForbidden,
		},
		{
			"incomplete assistant", own.ID, pending.ID, "message_received", ownSig, channel.PublishToken,
			http.StatusForbidden,
		},
		{"wrong role", own.ID, user.ID, "message_received", ownSig, channel.PublishToken, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := relay(tc.sessionID, tc.messageID, tc.eventType, tc.signature, tc.token)
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}

	// Break a real message-table read after middleware/session checks succeed.
	// The rejected attempt must not consume dedup: retry after restoring the
	// same table must deliver the exact already-persisted row.
	require.NoError(t, db.Exec("ALTER TABLE messages RENAME TO unavailable_messages").Error)
	w := relay(own.ID, retry.ID, "message_sent", ownSig, channel.PublishToken)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	require.NoError(t, db.Exec("ALTER TABLE unavailable_messages RENAME TO messages").Error)
	select {
	case delivered := <-deliveries:
		t.Fatalf("rejected request reached webhook: %s", delivered.raw)
	case <-time.After(50 * time.Millisecond):
	}
	w = relay(own.ID, retry.ID, "message_sent", ownSig, channel.PublishToken)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assertDelivery(retry, "message_sent", "query")
}
