package types

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringArrayAndEncryptedConfigsScanFromSQLiteString(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	// 1. StringArray scanned from SQLite TEXT string must preserve KB restrictions.
	var kbIDs StringArray
	require.NoError(t, kbIDs.Scan(`["kb-allowed-1","kb-allowed-2"]`))
	require.Equal(t, StringArray{"kb-allowed-1", "kb-allowed-2"}, kbIDs)
	scope := TenantAPIKeyScope{KnowledgeBaseIDs: kbIDs}
	assert.True(t, scope.IsKnowledgeBaseRestricted())
	assert.True(t, scope.AllowsKnowledgeBase("kb-allowed-1"))
	assert.False(t, scope.AllowsKnowledgeBase("kb-forbidden"))

	// 2. Encrypted configs serialized via Value() and scanned back as SQLite string must decrypt.
	mpOrig := ModelParameters{APIKey: "sk-model-secret", AppSecret: "app-secret"}
	mpRaw, err := mpOrig.Value()
	require.NoError(t, err)
	var mpScanned ModelParameters
	require.NoError(t, mpScanned.Scan(string(mpRaw.([]byte))))
	assert.Equal(t, "sk-model-secret", mpScanned.APIKey)
	assert.Equal(t, "app-secret", mpScanned.AppSecret)

	mcpOrig := MCPAuthConfig{APIKey: "mcp-key", Token: "mcp-tok", ClientSecret: "mcp-cs"}
	mcpRaw, err := mcpOrig.Value()
	require.NoError(t, err)
	var mcpScanned MCPAuthConfig
	require.NoError(t, mcpScanned.Scan(string(mcpRaw.([]byte))))
	assert.Equal(t, "mcp-key", mcpScanned.APIKey)
	assert.Equal(t, "mcp-tok", mcpScanned.Token)
	assert.Equal(t, "mcp-cs", mcpScanned.ClientSecret)

	sbOrig := StorageBackendConfig{AccessKeyID: "ak-123", SecretAccessKey: "sk-456", BucketName: "bkt"}
	sbRaw, err := sbOrig.Value()
	require.NoError(t, err)
	var sbScanned StorageBackendConfig
	require.NoError(t, sbScanned.Scan(string(sbRaw.([]byte))))
	assert.Equal(t, "ak-123", sbScanned.AccessKeyID)
	assert.Equal(t, "sk-456", sbScanned.SecretAccessKey)

	apOrig := &APIPrincipalConfig{Mode: APIPrincipalModeSignedToken, HMACSecret: "hmac-sec"}
	apRaw, err := apOrig.Value()
	require.NoError(t, err)
	var apScanned APIPrincipalConfig
	require.NoError(t, apScanned.Scan(string(apRaw.([]byte))))
	assert.Equal(t, "hmac-sec", apScanned.HMACSecret)

	tsOrig := &TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &E2BSandboxConfig{APIKey: "e2b-secret"},
	}
	tsRaw, err := tsOrig.Value()
	require.NoError(t, err)
	var tsScanned TenantSandboxConfig
	require.NoError(t, tsScanned.Scan(string(tsRaw.([]byte))))
	require.NotNil(t, tsScanned.E2B)
	assert.Equal(t, "e2b-secret", tsScanned.E2B.APIKey)

	vsOrig := ConnectionConfig{Host: "qdrant", Password: "vs-pw", APIKey: "vs-key"}
	vsRaw, err := vsOrig.Value()
	require.NoError(t, err)
	var vsScanned ConnectionConfig
	require.NoError(t, vsScanned.Scan(string(vsRaw.([]byte))))
	assert.Equal(t, "vs-pw", vsScanned.Password)
	assert.Equal(t, "vs-key", vsScanned.APIKey)

	wsOrig := WebSearchProviderParameters{APIKey: "tavily-secret"}
	wsRaw, err := wsOrig.Value()
	require.NoError(t, err)
	var wsScanned WebSearchProviderParameters
	require.NoError(t, wsScanned.Scan(string(wsRaw.([]byte))))
	assert.Equal(t, "tavily-secret", wsScanned.APIKey)

	// 3. Empty string "" and empty []byte{} must not fail Scan.
	var agentCfg AgentConfig
	require.NoError(t, agentCfg.Scan(""))
	require.NoError(t, agentCfg.Scan([]byte{}))

	var sessAgentCfg SessionAgentConfig
	require.NoError(t, sessAgentCfg.Scan(""))

	var tok TokenUsage
	require.NoError(t, tok.Scan(""))

	var customCfg CustomAgentConfig
	require.NoError(t, customCfg.Scan(""))

	var autoTag AutoTagConfig
	require.NoError(t, autoTag.Scan(""))

	var imgs MessageImages
	require.NoError(t, imgs.Scan(""))

	var atts MessageAttachments
	require.NoError(t, atts.Scan(""))

	var mentioned MentionedItems
	require.NoError(t, mentioned.Scan(""))

	var execCtx MessageExecutionContext
	require.NoError(t, execCtx.Scan(""))

	var steps AgentSteps
	require.NoError(t, steps.Scan(""))

	var sugg SuggestionItems
	require.NoError(t, sugg.Scan(""))

	var idx IndexingStrategy
	require.NoError(t, idx.Scan(""))
	assert.Equal(t, DefaultIndexingStrategy(), idx)
}

func TestPaginationNilReceiverAndOverflowSafety(t *testing.T) {
	var nilPage *Pagination
	assert.Equal(t, 1, nilPage.GetPage())
	assert.Equal(t, 20, nilPage.GetPageSize())
	assert.Equal(t, 20, nilPage.Limit())
	assert.Equal(t, 0, nilPage.Offset())

	res := NewPageResult(42, nilPage, []string{"a"})
	require.NotNil(t, res)
	assert.Equal(t, 1, res.Page)
	assert.Equal(t, 20, res.PageSize)

	huge := &Pagination{Page: math.MaxInt, PageSize: 1000}
	assert.Equal(t, math.MaxInt, huge.Offset(), "Offset must clamp instead of overflowing negative")
}

func TestNewVectorStoreResponseNilSafe(t *testing.T) {
	var resp VectorStoreResponse
	require.NotPanics(t, func() {
		resp = NewVectorStoreResponse(nil, "user", false)
	})
	assert.Equal(t, "user", resp.Source)
	assert.False(t, resp.ReadOnly)
}

func TestRenderPromptPlaceholdersNilAndImmutableCallerMap(t *testing.T) {
	var out string
	require.NotPanics(t, func() {
		out = RenderPromptPlaceholders("Today: {{current_time}}", nil)
	})
	assert.NotContains(t, out, "{{current_time}}")

	shared := PlaceholderValues{"query": "hello"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := RenderPromptPlaceholders("Q={{query}} D={{current_time}} W={{current_week}} Y={{yesterday}}", shared)
			assert.Contains(t, got, "Q=hello")
		}()
	}
	wg.Wait()
	assert.Len(t, shared, 1, "caller PlaceholderValues map must not be mutated")
}

func TestNilContextHelpersDoNotPanic(t *testing.T) {
	var nilCtx context.Context
	require.NotPanics(t, func() {
		_ = CallerFromContext(nilCtx)
		ctx := WithCaller(nilCtx, Caller{TenantID: 7, UserID: "u1"})
		assert.Equal(t, TenantRoleViewer, CallerFromContext(ctx).Role)
		_ = WithExecutionTenant(nilCtx, 7)

		_, _ = TenantIDFromContext(nilCtx)
		_, _ = TenantInfoFromContext(nilCtx)
		_, _ = RequestIDFromContext(nilCtx)
		_, _ = UserIDFromContext(nilCtx)
		_ = WikiEditSourceFromContext(nilCtx)
		_ = WithWikiEditSource(nilCtx, WikiEditSourceUser)
		_ = WikiShrinkAllowedFromContext(nilCtx)
		_ = WithWikiShrinkAllowed(nilCtx)
		_ = IsEmbedQuery(nilCtx)
		_ = WithEmbedQuery(nilCtx)
		_ = TenantRoleFromContext(nilCtx)
		_ = IsSystemAdminFromContext(nilCtx)
		_, _ = SessionTenantIDFromContext(nilCtx)
		_, _ = LanguageFromContext(nilCtx)
		_, _ = GrantScopeFromContext(nilCtx)
		_ = WithGrantScope(nilCtx, GrantScope{Scope: AgentGrantScopeSession, ScopeID: "s1"})
		_, _ = StandingGrantScopeFromContext(nilCtx)
		_ = WithStandingGrantScope(nilCtx, GrantScope{Scope: AgentGrantScopeAutomation, ScopeID: "a1"})
		_ = AccessLevelFromContext(nilCtx)
		_ = WithAccessLevel(nilCtx, AssistantAccessReadOnly)
		_ = WithRunMode(nilCtx, RunModeAttended)
		_, _ = WikiPageWriteSnapshotFromContext(nilCtx)
		_ = WithWikiPageWriteSnapshot(nilCtx, nil)
		_ = WithTenantAPIKeyScope(nilCtx, TenantAPIKeyScope{KeyID: 1})
		_ = WithMemoryDisabled(nilCtx)

		var nilReq *PipelineRequest
		_ = nilReq.ChatModelContext(nilCtx)
	})
}
