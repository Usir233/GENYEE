package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/application/service"
	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type callbackOrderProbeAdapter struct {
	im.Adapter
	verifyCalled    atomic.Bool
	handshakeCalled atomic.Bool
	verifyErr       error
}

func (a *callbackOrderProbeAdapter) Platform() im.Platform { return im.PlatformWeCom }

func (a *callbackOrderProbeAdapter) VerifyCallback(*gin.Context) error {
	a.verifyCalled.Store(true)
	return a.verifyErr
}

func (a *callbackOrderProbeAdapter) HandleURLVerification(c *gin.Context) bool {
	a.handshakeCalled.Store(true)
	c.String(http.StatusOK, "decrypted-secret-echo")
	return true
}

func TestIMCallbackVerifiesSignatureBeforeHandshakeAndToggleRedactsSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.Exec(`CREATE TABLE im_channels (
		id TEXT PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		agent_id TEXT NOT NULL,
		platform TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		enabled NUMERIC NOT NULL DEFAULT 1,
		mode TEXT NOT NULL DEFAULT 'websocket',
		output_mode TEXT NOT NULL DEFAULT 'stream',
		locale TEXT NOT NULL DEFAULT '',
		knowledge_base_id TEXT DEFAULT '',
		bot_identity TEXT NOT NULL DEFAULT '',
		session_mode TEXT NOT NULL DEFAULT 'user',
		credentials TEXT NOT NULL DEFAULT '{}',
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("CREATE TABLE im_channels: %v", err)
	}

	probe := &callbackOrderProbeAdapter{verifyErr: errors.New("forged signature")}
	imSvc := im.NewService(db, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	defer imSvc.Stop()
	imSvc.RegisterAdapterFactory("wecom", func(context.Context, *im.IMChannel, func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		return probe, nil, nil
	})

	ch := &im.IMChannel{
		ID:          "ch-probe-1",
		TenantID:    77,
		AgentID:     "agent-1",
		Platform:    "wecom",
		Mode:        "webhook",
		Enabled:     true,
		Credentials: types.JSON(`{"corp_id":"ww1","agent_secret":"super-secret-wecom-key"}`),
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("db.Create(IMChannel): %v", err)
	}

	h := NewIMHandler(imSvc)

	// 1. Unauthenticated challenge request MUST fail VerifyCallback (403) BEFORE HandleURLVerification runs.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/im/callback/ch-probe-1?echostr=cipher", nil)
	c.Params = gin.Params{{Key: "channel_id", Value: "ch-probe-1"}}
	h.IMCallback(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("IMCallback status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	if !probe.verifyCalled.Load() || probe.handshakeCalled.Load() {
		t.Fatalf("expected VerifyCallback=true and HandleURLVerification=false, got verify=%v handshake=%v",
			probe.verifyCalled.Load(), probe.handshakeCalled.Load())
	}

	// 2. ToggleIMChannel MUST redact credentials in HTTP response.
	wToggle := httptest.NewRecorder()
	cToggle, _ := gin.CreateTestContext(wToggle)
	reqToggle := httptest.NewRequest(http.MethodPost, "/api/v1/im-channels/ch-probe-1/toggle", nil)
	reqToggle = reqToggle.WithContext(context.WithValue(reqToggle.Context(), types.TenantIDContextKey, uint64(77)))
	cToggle.Request = reqToggle
	cToggle.Params = gin.Params{{Key: "id", Value: "ch-probe-1"}}
	h.ToggleIMChannel(cToggle)
	if wToggle.Code != http.StatusOK {
		t.Fatalf("ToggleIMChannel status = %d, want 200; body=%s", wToggle.Code, wToggle.Body.String())
	}
	if strings.Contains(wToggle.Body.String(), "super-secret-wecom-key") {
		t.Fatalf("ToggleIMChannel leaked raw credential in response: %s", wToggle.Body.String())
	}
}

func TestMaskTenantAPIKeyHidesFullSecretOnList(t *testing.T) {
	raw := "sk-1234567890abcdefghijklmnopqrstuv"
	key := &types.TenantAPIKey{
		ID:     9,
		Name:   "ci-key",
		APIKey: raw,
	}
	resp := tenantAPIKeyForResponse(key)
	if resp.APIKey == raw {
		t.Fatalf("tenantAPIKeyForResponse leaked full secret: %q", resp.APIKey)
	}
	if !strings.Contains(resp.APIKey, "****") {
		t.Fatalf("expected masked API key, got %q", resp.APIKey)
	}
}

type stubMessageServiceForEmbedRelay struct {
	interfaces.MessageService
	msgs []*types.Message
}

func (s *stubMessageServiceForEmbedRelay) GetRecentMessagesBySession(_ context.Context, _ string, _ int) ([]*types.Message, error) {
	return s.msgs, nil
}

func TestEmbedRelayWebhookEventBlocksSessionOverrideAndForgedContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	var webhookHits atomic.Int32
	hookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer hookSrv.Close()
	t.Setenv("SSRFSafeURLWhitelist", "127.0.0.1")

	ch := testEmbedChannel()
	ch.WebhookURL = hookSrv.URL
	ch.WebhookSecret = "whsec_test_123"
	sig := service.SignEmbedSessionHandle(ch, testEmbedSessionID)

	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{
				testEmbedSessionID: validEmbedSession(ch),
			},
		},
		messageHandler: &MessageHandler{
			MessageService: &stubMessageServiceForEmbedRelay{
				msgs: []*types.Message{
					{
						ID:        "msg-real-user-1",
						SessionID: testEmbedSessionID,
						Role:      "user",
						Content:   "legitimate visitor question",
					},
				},
			},
		},
	}

	makeRelayReq := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/sessions/"+testEmbedSessionID+"/events", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("X-Embed-Session", sig)
		c.Params = gin.Params{{Key: "session_id", Value: testEmbedSessionID}}
		ctx := context.WithValue(c.Request.Context(), types.EmbedChannelContextKey, ch)
		c.Request = c.Request.WithContext(ctx)
		h.EmbedRelayWebhookEvent(c)
		return w
	}

	// 1. Overriding session_id in body must be rejected with 403.
	wOverride := makeRelayReq(`{"type":"message_sent","session_id":"victim-other-session","query":"legitimate visitor question"}`)
	if wOverride.Code != http.StatusForbidden {
		t.Fatalf("session_id override status = %d, want 403; body=%s", wOverride.Code, wOverride.Body.String())
	}

	// 2. Forged content not in session history must be rejected with 403.
	wForged := makeRelayReq(`{"type":"message_sent","session_id":"` + testEmbedSessionID + `","query":"FORGED ADMIN COMMAND"}`)
	if wForged.Code != http.StatusForbidden {
		t.Fatalf("forged message status = %d, want 403; body=%s", wForged.Code, wForged.Body.String())
	}

	// 3. Matching persisted message succeeds.
	wValid := makeRelayReq(`{"type":"message_sent","session_id":"` + testEmbedSessionID + `","query":"legitimate visitor question"}`)
	if wValid.Code != http.StatusOK {
		t.Fatalf("valid message status = %d, want 200; body=%s", wValid.Code, wValid.Body.String())
	}
	time.Sleep(30 * time.Millisecond)
}

type stubMCPServiceLookupForEmbed struct {
	interfaces.MCPServiceService
	svc *types.MCPService
}

func (s *stubMCPServiceLookupForEmbed) GetMCPServiceByID(_ context.Context, _ uint64, _ string) (*types.MCPService, error) {
	if s.svc == nil {
		return nil, errors.New("not found")
	}
	return s.svc, nil
}

func TestEmbedMCPOAuthAuthorizeURLIgnoresClientRedirectURI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	ch := testEmbedChannel()
	sig := service.SignEmbedSessionHandle(ch, testEmbedSessionID)

	// Non-OAuth MCP service reaches GetMCPServiceByID without failing on missing/stripped redirect_uri,
	// proving EmbedMCPOAuthAuthorizeURL strips client redirect_uri rather than requiring it from the body.
	nonOAuthSvc := &types.MCPService{ID: "mcp-1", TenantID: testEmbedTenantID}
	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{
				testEmbedSessionID: validEmbedSession(ch),
			},
		},
		mcpOAuthHandler: &MCPOAuthHandler{
			svc:   &stubMCPServiceLookupForEmbed{svc: nonOAuthSvc},
			oauth: nil,
		},
	}

	// When oauth is nil, EmbedMCPOAuthAuthorizeURL fails safely with 500 unavailable.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/sessions/"+testEmbedSessionID+"/mcp-services/mcp-1/oauth/authorize-url",
		bytes.NewBufferString(`{"redirect_uri":"https://attacker.example/steal"}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Embed-Session", sig)
	c.Params = gin.Params{{Key: "session_id", Value: testEmbedSessionID}, {Key: "id", Value: "mcp-1"}}
	c.Set(types.TenantIDContextKey.String(), testEmbedTenantID)
	ctx := context.WithValue(c.Request.Context(), types.EmbedChannelContextKey, ch)
	c.Request = c.Request.WithContext(ctx)

	h.EmbedMCPOAuthAuthorizeURL(c)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if strings.Contains(w.Body.String(), "attacker.example") {
		t.Fatalf("response echoed untrusted redirect_uri: %s", w.Body.String())
	}
}

func TestValidateParserEngineOutboundURLsRejectsODLHybridOverride(t *testing.T) {
	err := validateParserEngineOutboundURLs(&types.ParserEngineConfig{
		ODLHybridURL: "https://example.com/odl",
	})
	if err == nil {
		t.Fatal("expected validateParserEngineOutboundURLs to reject odl_hybrid_url override")
	}
}
