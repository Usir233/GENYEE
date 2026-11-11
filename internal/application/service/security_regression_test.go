package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type memEmbedChannelRepo struct {
	interfaces.EmbedChannelRepository
	rows map[string]*types.EmbedChannel
}

func newMemEmbedChannelRepo() *memEmbedChannelRepo {
	return &memEmbedChannelRepo{rows: make(map[string]*types.EmbedChannel)}
}

func (r *memEmbedChannelRepo) Create(_ context.Context, ch *types.EmbedChannel) error {
	clone := *ch
	if clone.ID == "" {
		clone.ID = "ch-sec-1"
	}
	r.rows[clone.ID] = &clone
	ch.ID = clone.ID
	return nil
}

func (r *memEmbedChannelRepo) GetByID(_ context.Context, id string) (*types.EmbedChannel, error) {
	row, ok := r.rows[id]
	if !ok {
		return nil, nil
	}
	clone := *row
	return &clone, nil
}

func (r *memEmbedChannelRepo) ListByAgent(_ context.Context, tenantID uint64, agentID string) ([]*types.EmbedChannel, error) {
	var out []*types.EmbedChannel
	for _, row := range r.rows {
		if row.TenantID == tenantID && row.AgentID == agentID {
			clone := *row
			out = append(out, &clone)
		}
	}
	return out, nil
}

func (r *memEmbedChannelRepo) ListByTenant(_ context.Context, tenantID uint64) ([]*types.EmbedChannel, error) {
	var out []*types.EmbedChannel
	for _, row := range r.rows {
		if row.TenantID == tenantID {
			clone := *row
			out = append(out, &clone)
		}
	}
	return out, nil
}

func (r *memEmbedChannelRepo) Update(_ context.Context, ch *types.EmbedChannel) error {
	clone := *ch
	r.rows[clone.ID] = &clone
	return nil
}

func (r *memEmbedChannelRepo) Delete(_ context.Context, tenantID uint64, id string) error {
	if row, ok := r.rows[id]; ok && row.TenantID == tenantID {
		delete(r.rows, id)
	}
	return nil
}

func TestEmbedSessionTokenInvalidatedOnRotateTokenAndWebhookSecretEncrypted(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	repo := newMemEmbedChannelRepo()
	repo.rows["ch-1"] = &types.EmbedChannel{
		ID:            "ch-1",
		TenantID:      10,
		AgentID:       "ag-1",
		Enabled:       true,
		PublishToken:  "em_initial_publish_token",
		WebhookSecret: "legacy-plaintext-webhook-secret",
	}

	svc := &embedChannelService{repo: repo, redis: rdb}
	ctx := context.Background()

	// Legacy plaintext WebhookSecret remains readable.
	owned, err := svc.GetOwnedChannel(ctx, 10, "ch-1")
	if err != nil || owned.WebhookSecret != "legacy-plaintext-webhook-secret" {
		t.Fatalf("GetOwnedChannel(legacy) = (%#v, %v)", owned, err)
	}

	// Updating WebhookSecret persists enc:v1: ciphertext while returning decrypted value.
	newSecret := "whsec_fresh_secret_99"
	updated, err := svc.Update(ctx, 10, "ch-1", &types.EmbedChannel{}, nil, nil, nil, nil, nil, nil, &newSecret)
	if err != nil {
		t.Fatalf("Update(webhookSecret): %v", err)
	}
	if updated.WebhookSecret != newSecret {
		t.Fatalf("updated.WebhookSecret = %q, want %q", updated.WebhookSecret, newSecret)
	}
	if !strings.HasPrefix(repo.rows["ch-1"].WebhookSecret, "enc:v1:") {
		t.Fatalf("stored WebhookSecret = %q, want enc:v1: ciphertext", repo.rows["ch-1"].WebhookSecret)
	}

	// Mint an ems_ session token before rotating PublishToken.
	emsTok, _, err := svc.IssueSessionToken(ctx, "ch-1")
	if err != nil {
		t.Fatalf("IssueSessionToken: %v", err)
	}
	resolvedID, err := svc.ResolveSessionToken(ctx, emsTok)
	if err != nil || resolvedID != "ch-1" {
		t.Fatalf("ResolveSessionToken(before rotate) = (%q, %v), want (ch-1, nil)", resolvedID, err)
	}

	// RotateToken must immediately invalidate the previously issued ems_ token.
	if _, _, err := svc.RotateToken(ctx, 10, "ch-1"); err != nil {
		t.Fatalf("RotateToken: %v", err)
	}
	if _, err := svc.ResolveSessionToken(ctx, emsTok); err == nil {
		t.Fatal("expected old ems_ session token to be rejected after RotateToken")
	}

	// Newly minted ems_ token after rotation succeeds.
	freshTok, _, err := svc.IssueSessionToken(ctx, "ch-1")
	if err != nil {
		t.Fatalf("IssueSessionToken(after rotate): %v", err)
	}
	if resolvedID, err := svc.ResolveSessionToken(ctx, freshTok); err != nil || resolvedID != "ch-1" {
		t.Fatalf("ResolveSessionToken(after rotate) = (%q, %v)", resolvedID, err)
	}
}

func TestSandboxTerminalTicketSingleUseJTI(t *testing.T) {
	isolateJWTSecret(t)
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef-jwt")

	ticket, err := IssueSandboxTerminalTicket("user-1", 42, "sess-1", "tok-1", time.Minute)
	if err != nil {
		t.Fatalf("IssueSandboxTerminalTicket: %v", err)
	}

	claims, err := ParseSandboxTerminalTicket(ticket)
	if err != nil || claims == nil || claims.UserID != "user-1" || claims.TenantID != 42 {
		t.Fatalf("first ParseSandboxTerminalTicket failed: claims=%#v err=%v", claims, err)
	}

	if _, err := ParseSandboxTerminalTicket(ticket); err == nil {
		t.Fatal("expected second replay of same terminal ticket jti to be rejected")
	}
}

func TestParserEngineRejectsODLHybridURLOverride(t *testing.T) {
	if err := validateParserEngineOverrideURLs(map[string]string{
		"odl_hybrid_url": "http://127.0.0.1:5002",
	}); err == nil {
		t.Fatal("expected validateParserEngineOverrideURLs to reject non-empty odl_hybrid_url")
	}
}

func TestKnowledgeBaseSecretsEncryptedAtRestAndLegacyPlaintextCompatible(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	ctx := context.Background()

	kb := &types.KnowledgeBase{
		ID:   "kb-enc-1",
		Name: "enc-test",
		StorageConfig: types.StorageConfig{
			SecretID:  "ak-plain-123",
			SecretKey: "sk-plain-456",
		},
		VLMConfig: types.VLMConfig{
			APIKey: "vlm-key-789",
		},
	}

	stored, err := encryptKBSecretsClone(kb)
	if err != nil {
		t.Fatalf("encryptKBSecretsClone: %v", err)
	}
	if !strings.HasPrefix(stored.StorageConfig.SecretID, "enc:v1:") ||
		!strings.HasPrefix(stored.StorageConfig.SecretKey, "enc:v1:") ||
		!strings.HasPrefix(stored.VLMConfig.APIKey, "enc:v1:") {
		t.Fatalf("expected all KB secrets to carry enc:v1: prefix, got %+v / %+v", stored.StorageConfig, stored.VLMConfig)
	}

	loaded := decryptKBSecretsClone(ctx, stored)
	if loaded.StorageConfig.SecretID != "ak-plain-123" ||
		loaded.StorageConfig.SecretKey != "sk-plain-456" ||
		loaded.VLMConfig.APIKey != "vlm-key-789" {
		t.Fatalf("round-trip decrypt mismatch: %+v / %+v", loaded.StorageConfig, loaded.VLMConfig)
	}

	// Historical plaintext row must remain readable without error.
	legacy := decryptKBSecretsClone(ctx, kb)
	if legacy.StorageConfig.SecretID != "ak-plain-123" ||
		legacy.StorageConfig.SecretKey != "sk-plain-456" ||
		legacy.VLMConfig.APIKey != "vlm-key-789" {
		t.Fatalf("legacy plaintext compatibility failed: %+v / %+v", legacy.StorageConfig, legacy.VLMConfig)
	}
}

func TestCreateKnowledgeBaseBlocksDirectVLMBaseURLSSRF(t *testing.T) {
	svc := &knowledgeBaseService{}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	kb := &types.KnowledgeBase{
		Name: "ssrf-test",
		VLMConfig: types.VLMConfig{
			Enabled:   true,
			BaseURL:   "http://169.254.169.254/latest/meta-data/",
			ModelName: "vision-model",
		},
	}
	if _, err := svc.CreateKnowledgeBase(ctx, kb); err == nil {
		t.Fatal("expected CreateKnowledgeBase to reject raw VLMConfig.BaseURL")
	}
}
