package im

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestComputeBotIdentity_HashesWebhookSecrets(t *testing.T) {
	mmSecret := "mm-outgoing-secret-token-xyz"
	mmCh := &IMChannel{
		Platform:    string(PlatformMattermost),
		Credentials: types.JSON(fmt.Sprintf(`{"outgoing_token":%q}`, mmSecret)),
	}
	gotMM := mmCh.computeBotIdentity()
	if gotMM == "" {
		t.Fatal("expected non-empty mattermost bot_identity")
	}
	if strings.Contains(gotMM, mmSecret) {
		t.Fatalf("computeBotIdentity leaked raw mattermost outgoing_token in %q", gotMM)
	}
	wantMMHash := sha256.Sum256([]byte(mmSecret))
	wantMM := "mattermost:wh:" + hex.EncodeToString(wantMMHash[:])
	if gotMM != wantMM {
		t.Fatalf("mattermost bot_identity = %q, want %q", gotMM, wantMM)
	}

	// Normal Telegram token (<bot_id>:<secret>) keeps numeric bot_id without secret.
	tgNormal := &IMChannel{
		Platform:    string(PlatformTelegram),
		Credentials: types.JSON(`{"bot_token":"123456789:AAH-super-secret-token"}`),
	}
	if gotTG := tgNormal.computeBotIdentity(); gotTG != "telegram:123456789" {
		t.Fatalf("normal telegram bot_identity = %q, want telegram:123456789", gotTG)
	}

	// Malformed Telegram token without colon must never fall back to storing raw secret.
	tgRawSecret := "telegram-secret-without-colon"
	tgMalformed := &IMChannel{
		Platform:    string(PlatformTelegram),
		Credentials: types.JSON(fmt.Sprintf(`{"bot_token":%q}`, tgRawSecret)),
	}
	gotTGMalformed := tgMalformed.computeBotIdentity()
	if gotTGMalformed == "" {
		t.Fatal("expected non-empty fallback telegram bot_identity")
	}
	if strings.Contains(gotTGMalformed, tgRawSecret) {
		t.Fatalf("computeBotIdentity leaked raw telegram bot_token in %q", gotTGMalformed)
	}
}

type w5RecordingAdapter struct {
	mu      sync.Mutex
	replies []string
}

func (*w5RecordingAdapter) Platform() Platform                                   { return Platform("test") }
func (*w5RecordingAdapter) VerifyCallback(*gin.Context) error                    { return nil }
func (*w5RecordingAdapter) ParseCallback(*gin.Context) (*IncomingMessage, error) { return nil, nil }
func (*w5RecordingAdapter) HandleURLVerification(*gin.Context) bool              { return false }
func (a *w5RecordingAdapter) SendReply(_ context.Context, _ *IncomingMessage, r *ReplyMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r != nil {
		a.replies = append(a.replies, r.Content)
	}
	return nil
}

func (a *w5RecordingAdapter) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.replies)
}

type w5TenantServiceStub struct {
	interfaces.TenantService
}

func (*w5TenantServiceStub) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id, Name: fmt.Sprintf("tenant-%d", id)}, nil
}

type w5SessionServiceStub struct {
	interfaces.SessionService
	searchCalls int
}

func (*w5SessionServiceStub) CreateSession(_ context.Context, s *types.Session) (*types.Session, error) {
	cp := *s
	if cp.ID == "" {
		cp.ID = uuid.New().String()
	}
	return &cp, nil
}

func (s *w5SessionServiceStub) SearchKnowledge(
	_ context.Context, _ []string, _ []string, _ []types.TagScope, _ string, _ *types.KnowledgeSearchOptions,
) (*types.RetrievalResult, error) {
	s.searchCalls++
	return &types.RetrievalResult{
		Results: []*types.SearchResult{{KnowledgeTitle: "Leaked Doc", Content: "Sensitive passage"}},
	}, nil
}

type w5AgentServiceStub struct {
	interfaces.CustomAgentService
}

func (*w5AgentServiceStub) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	return &types.CustomAgent{ID: id, Name: "Bot " + id}, nil
}

type w5KBServiceStub struct {
	interfaces.KnowledgeBaseService
	listCalls int
}

func (k *w5KBServiceStub) ListKnowledgeBases(_ context.Context) ([]*types.KnowledgeBase, error) {
	k.listCalls++
	return []*types.KnowledgeBase{{ID: "kb-secret", Name: "Secret KB"}}, nil
}

func (k *w5KBServiceStub) ListKnowledgeBasesByTenantID(_ context.Context, _ uint64) ([]*types.KnowledgeBase, error) {
	k.listCalls++
	return []*types.KnowledgeBase{{ID: "kb-secret", Name: "Secret KB"}}, nil
}

func TestHandleMessage_ScopesDedupPerChannel(t *testing.T) {
	for _, useRedis := range []bool{false, true} {
		name := "memory"
		if useRedis {
			name = "redis"
		}
		t.Run(name, func(t *testing.T) {
			db := newLifecycleTestDB(t)
			if err := db.Exec(`CREATE TABLE im_channel_sessions (
				id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
				platform TEXT NOT NULL,
				user_id TEXT NOT NULL,
				chat_id TEXT NOT NULL DEFAULT '',
				thread_id TEXT NOT NULL DEFAULT '',
				session_id TEXT NOT NULL,
				tenant_id INTEGER NOT NULL,
				agent_id TEXT DEFAULT '',
				im_channel_id TEXT DEFAULT '',
				status TEXT NOT NULL DEFAULT 'active',
				metadata TEXT DEFAULT '{}',
				created_at DATETIME,
				updated_at DATETIME,
				deleted_at DATETIME
			)`).Error; err != nil {
				t.Fatalf("create im_channel_sessions: %v", err)
			}

			var rdb *redis.Client
			if useRedis {
				mr := miniredis.RunT(t)
				rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
				t.Cleanup(func() { _ = rdb.Close() })
			}

			svc := NewService(
				db,
				&w5SessionServiceStub{},
				nil,
				&w5TenantServiceStub{},
				&w5AgentServiceStub{},
				nil,
				&w5KBServiceStub{},
				nil,
				nil,
				nil,
				nil,
				rdb,
				nil,
				nil,
				nil,
				nil,
			)
			t.Cleanup(svc.Stop)

			adapters := map[string]*w5RecordingAdapter{
				"ch-tenant-1": {},
				"ch-tenant-2": {},
			}
			svc.RegisterAdapterFactory(
				"test",
				func(
					_ context.Context, ch *IMChannel, _ func(context.Context, *IncomingMessage) error,
				) (Adapter, context.CancelFunc, error) {
					return adapters[ch.ID], func() {}, nil
				},
			)

			ch1 := &IMChannel{
				ID: "ch-tenant-1", TenantID: 101, AgentID: "ag-1", Platform: "test",
				Enabled: true, Mode: "webhook", SessionMode: "user", Credentials: types.JSON(`{}`),
			}
			ch2 := &IMChannel{
				ID: "ch-tenant-2", TenantID: 202, AgentID: "ag-2", Platform: "test",
				Enabled: true, Mode: "webhook", SessionMode: "user", Credentials: types.JSON(`{}`),
			}
			if err := db.Create(ch1).Error; err != nil {
				t.Fatalf("insert ch1: %v", err)
			}
			if err := db.Create(ch2).Error; err != nil {
				t.Fatalf("insert ch2: %v", err)
			}
			if err := svc.StartChannel(ch1); err != nil {
				t.Fatalf("start ch1: %v", err)
			}
			if err := svc.StartChannel(ch2); err != nil {
				t.Fatalf("start ch2: %v", err)
			}

			// Identical platform-local MessageID ("42") on two different channels/tenants
			// must NOT suppress the second tenant's message.
			msgA := &IncomingMessage{
				Platform: Platform("test"), MessageID: "42", UserID: "u1", ChatID: "c1", Content: "/help",
			}
			msgB := &IncomingMessage{
				Platform: Platform("test"), MessageID: "42", UserID: "u2", ChatID: "c2", Content: "/help",
			}
			if err := svc.HandleMessage(context.Background(), msgA, ch1.ID); err != nil {
				t.Fatalf("HandleMessage ch1: %v", err)
			}
			if err := svc.HandleMessage(context.Background(), msgB, ch2.ID); err != nil {
				t.Fatalf("HandleMessage ch2: %v", err)
			}
			if adapters[ch1.ID].count() != 1 {
				t.Fatalf("ch1 reply count = %d, want 1", adapters[ch1.ID].count())
			}
			if adapters[ch2.ID].count() != 1 {
				t.Fatalf(
					"ch2 reply count = %d, want 1 (cross-channel MessageID collision dropped tenant 2 message)",
					adapters[ch2.ID].count(),
				)
			}

			// Replaying the same MessageID on the SAME channel must still be deduplicated.
			if err := svc.HandleMessage(context.Background(), msgA, ch1.ID); err != nil {
				t.Fatalf("HandleMessage duplicate ch1: %v", err)
			}
			if adapters[ch1.ID].count() != 1 {
				t.Fatalf("ch1 reply count after replay = %d, want 1", adapters[ch1.ID].count())
			}
		})
	}
}

func TestHandleMessage_DoesNotRestartDisabledChannel(t *testing.T) {
	db := newLifecycleTestDB(t)
	var counters lifecycleFactoryCounters
	svc := NewService(db, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	t.Cleanup(svc.Stop)
	svc.RegisterAdapterFactory("test", counters.factory())

	disabled := &IMChannel{
		ID:          "ch-disabled",
		TenantID:    1,
		AgentID:     "agent-1",
		Platform:    "test",
		Enabled:     false,
		Mode:        "webhook",
		SessionMode: "user",
		Credentials: types.JSON(`{}`),
	}
	if err := db.Create(disabled).Error; err != nil {
		t.Fatalf("create disabled channel: %v", err)
	}
	// Ensure SQLite persisted enabled=0 (GORM zero-value bool handling).
	if err := db.Model(&IMChannel{}).Where("id = ?", disabled.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("force enabled=false: %v", err)
	}

	err := svc.HandleMessage(context.Background(), &IncomingMessage{
		Platform:  Platform("test"),
		MessageID: "m-1",
		UserID:    "u-1",
		Content:   "hello",
	}, disabled.ID)
	if err == nil {
		t.Fatal("expected HandleMessage on disabled channel to return error, got nil")
	}
	if got := counters.starts.Load(); got != 0 {
		t.Fatalf("disabled channel was started %d times, want 0", got)
	}
}

func TestInfoAndSearchCommands_EnforceAgentKBScope(t *testing.T) {
	kbSvc := &w5KBServiceStub{}
	sessSvc := &w5SessionServiceStub{}

	disabledKBAgent := &types.CustomAgent{
		ID:   "agent-no-kb",
		Name: "NoKB Agent",
		Config: types.CustomAgentConfig{
			KBSelectionMode: "none",
			// Stale KnowledgeBases left over in JSON after switching mode to "none".
			KnowledgeBases: []string{"kb-secret"},
		},
	}
	cmdCtx := &CommandContext{
		TenantID:    1,
		AgentName:   disabledKBAgent.Name,
		CustomAgent: disabledKBAgent,
	}

	infoCmd := newInfoCommand(kbSvc)
	infoRes, err := infoCmd.Execute(context.Background(), cmdCtx, nil)
	if err != nil {
		t.Fatalf("InfoCommand.Execute error: %v", err)
	}
	if strings.Contains(infoRes.Content, "Secret KB") || strings.Contains(infoRes.Content, "kb-secret") {
		t.Fatalf("InfoCommand leaked disabled KB when KBSelectionMode=none: %s", infoRes.Content)
	}
	if !strings.Contains(infoRes.Content, "未配置") {
		t.Fatalf("InfoCommand expected 未配置 for KBSelectionMode=none, got: %s", infoRes.Content)
	}

	// Nil kbService must not panic even if KBSelectionMode="all".
	nilInfoCmd := newInfoCommand(nil)
	if _, err := nilInfoCmd.Execute(context.Background(), &CommandContext{
		TenantID:  1,
		AgentName: "AllKB",
		CustomAgent: &types.CustomAgent{
			ID:     "agent-all",
			Config: types.CustomAgentConfig{KBSelectionMode: "all"},
		},
	}, nil); err != nil {
		t.Fatalf("InfoCommand with nil kbService returned error: %v", err)
	}

	searchCmd := newSearchCommand(sessSvc, kbSvc)
	searchRes, err := searchCmd.Execute(context.Background(), cmdCtx, []string{"退款政策"})
	if err != nil {
		t.Fatalf("SearchCommand.Execute error: %v", err)
	}
	if sessSvc.searchCalls != 0 {
		t.Fatalf("SearchCommand called SearchKnowledge %d times when KBSelectionMode=none, want 0", sessSvc.searchCalls)
	}
	if strings.Contains(searchRes.Content, "Leaked Doc") {
		t.Fatalf("SearchCommand leaked search result when KBSelectionMode=none: %s", searchRes.Content)
	}
}
