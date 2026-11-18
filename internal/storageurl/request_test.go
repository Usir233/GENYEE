package storageurl

import (
	"context"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func publicRewriter(url string) *Rewriter {
	return NewRewriter(stubResolver(url), "TEST")
}

func TestNewRequestRewriter_HandleModeIsDisabled(t *testing.T) {
	w := NewRequestRewriter(context.Background(), ModeHandle, &stubFileService{}, nil)
	assert.False(t, w.Enabled(), "the default mode must not resolve anything")
}

func TestNewRequestRewriter_PublicModeIsEnabled(t *testing.T) {
	w := NewRequestRewriter(context.Background(), ModePublic, &stubFileService{}, nil)
	require.True(t, w.Enabled())
	assert.Equal(t, "https://cdn.example.com/resource://xifDo7NTSL300Lp1goVutw",
		w.Ref(context.Background(), "resource://xifDo7NTSL300Lp1goVutw"))
}

func TestRewriteMessages(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	ctx := context.Background()

	messages := []*types.Message{
		nil,
		{
			Content: "answer ![fig](resource://xifDo7NTSL300Lp1goVutw)",
			Images: types.MessageImages{{
				URL:     "resource://aaaabbbbccccddddeeeeff",
				Caption: "shows ![inline](minio://bucket/10000/exports/a.png)",
			}},
			KnowledgeReferences: types.References{{
				Content:   "chunk ![c](resource://xifDo7NTSL300Lp1goVutw)",
				ImageInfo: `[{"url":"resource://xifDo7NTSL300Lp1goVutw"}]`,
			}},
			AgentSteps: types.AgentSteps{{
				Thought: "looking at ![t](resource://xifDo7NTSL300Lp1goVutw)",
				ToolCalls: []types.ToolCall{{
					Reflection: "saw ![r](resource://xifDo7NTSL300Lp1goVutw)",
					Result:     &types.ToolResult{Output: "chart ![o](resource://xifDo7NTSL300Lp1goVutw)"},
				}},
			}},
		},
	}

	w.RewriteMessages(ctx, messages)

	message := messages[1]
	assert.Equal(t, "answer ![fig](https://cdn.example.com/x.png)", message.Content)
	assert.Equal(t, "https://cdn.example.com/x.png", message.Images[0].URL)
	assert.Equal(t, "shows ![inline](https://cdn.example.com/x.png)", message.Images[0].Caption)
	assert.Equal(t, "chunk ![c](https://cdn.example.com/x.png)", message.KnowledgeReferences[0].Content)
	assert.Equal(t, `[{"url":"https://cdn.example.com/x.png"}]`, message.KnowledgeReferences[0].ImageInfo)
	assert.Equal(t, "looking at ![t](https://cdn.example.com/x.png)", message.AgentSteps[0].Thought)
	assert.Equal(t, "saw ![r](https://cdn.example.com/x.png)", message.AgentSteps[0].ToolCalls[0].Reflection)
	assert.Equal(t, "chart ![o](https://cdn.example.com/x.png)", message.AgentSteps[0].ToolCalls[0].Result.Output)
}

func TestRewriteMessages_DisabledLeavesHandles(t *testing.T) {
	w := NewRewriter(nil, "TEST")
	messages := []*types.Message{{Content: "![a](resource://xifDo7NTSL300Lp1goVutw)"}}
	w.RewriteMessages(context.Background(), messages)
	assert.Equal(t, "![a](resource://xifDo7NTSL300Lp1goVutw)", messages[0].Content)
}

func TestRewriteMessagesResponse_DoesNotMutateOriginals(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	original := &types.Message{
		Content: "![a](resource://xifDo7NTSL300Lp1goVutw)",
		KnowledgeReferences: types.References{{
			Content: "chunk ![c](resource://xifDo7NTSL300Lp1goVutw)",
		}},
	}
	messages := []*types.Message{original}

	out := w.RewriteMessagesResponse(context.Background(), messages)

	require.Len(t, out, 1)
	assert.NotSame(t, original, out[0])
	assert.Equal(t, "![a](resource://xifDo7NTSL300Lp1goVutw)", original.Content)
	assert.Equal(t, "![a](https://cdn.example.com/x.png)", out[0].Content)
	assert.Equal(t, "chunk ![c](resource://xifDo7NTSL300Lp1goVutw)", original.KnowledgeReferences[0].Content)
	assert.Equal(t, "chunk ![c](https://cdn.example.com/x.png)", out[0].KnowledgeReferences[0].Content)
}

// SSE references payloads share their *SearchResult pointers with the stream
// replay buffer and the assistant message being persisted, so rewriting must not
// mutate the originals.
func TestCopyReferences_DoesNotMutateOriginals(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	original := &types.SearchResult{
		Content:        "chunk ![c](resource://xifDo7NTSL300Lp1goVutw)",
		MatchedContent: "match ![m](resource://xifDo7NTSL300Lp1goVutw)",
		MatchedImages:  []types.MatchedImage{{ChunkID: "image-1", URL: "resource://xifDo7NTSL300Lp1goVutw"}},
		ImageInfo:      `[{"url":"resource://xifDo7NTSL300Lp1goVutw"}]`,
	}
	refs := []*types.SearchResult{original, nil}

	out := w.CopyReferences(context.Background(), refs)

	require.Len(t, out, 2)
	assert.NotSame(t, original, out[0])
	assert.Equal(t, "chunk ![c](resource://xifDo7NTSL300Lp1goVutw)", original.Content,
		"the replay buffer's copy must be untouched")
	assert.Equal(t, "chunk ![c](https://cdn.example.com/x.png)", out[0].Content)
	assert.Equal(t, "match ![m](https://cdn.example.com/x.png)", out[0].MatchedContent)
	assert.Equal(t, `[{"url":"https://cdn.example.com/x.png"}]`, out[0].ImageInfo)
	assert.Equal(t, "resource://xifDo7NTSL300Lp1goVutw", original.MatchedImages[0].URL)
	assert.Equal(t, "https://cdn.example.com/x.png", out[0].MatchedImages[0].URL)
	assert.Nil(t, out[1])
}

func TestCopyReferences_DisabledReturnsInput(t *testing.T) {
	w := NewRewriter(nil, "TEST")
	refs := []*types.SearchResult{{Content: "![a](resource://xifDo7NTSL300Lp1goVutw)"}}
	assert.Equal(t, refs, w.CopyReferences(context.Background(), refs))
}

// Agent tool metadata is tool-defined, so every string leaf is rewritten — and
// the source map, which the replay buffer also holds, must not be mutated.
func TestCopyData_RewritesNestedStringsWithoutMutating(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	data := map[string]interface{}{
		"tool_name":   "chart_export",
		"duration_ms": 42,
		"output":      "![chart](resource://xifDo7NTSL300Lp1goVutw)",
		"nested": map[string]interface{}{
			"images": []interface{}{"resource://xifDo7NTSL300Lp1goVutw", "http://example.com/x.png"},
		},
	}

	out := w.CopyData(context.Background(), data)

	assert.Equal(t, "![chart](resource://xifDo7NTSL300Lp1goVutw)", data["output"],
		"the replay buffer's map must be untouched")
	assert.Equal(t, "![chart](https://cdn.example.com/x.png)", out["output"])
	assert.Equal(t, "chart_export", out["tool_name"])
	assert.Equal(t, 42, out["duration_ms"])

	nested := out["nested"].(map[string]interface{})["images"].([]interface{})
	assert.Equal(t, "https://cdn.example.com/x.png", nested[0])
	assert.Equal(t, "http://example.com/x.png", nested[1])
}

// The references SSE event carries its results twice: in
// StreamResponse.KnowledgeReferences and again in Data. An in-memory stream
// manager keeps the typed slice, so CopyData must traverse it or the Data copy
// leaks the handles the caller asked to have resolved.
func TestCopyData_RewritesTypedReferenceSlices(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	original := &types.SearchResult{Content: "figure ![f](resource://xifDo7NTSL300Lp1goVutw)"}
	data := map[string]interface{}{
		"references": types.References{original},
		"tags":       []string{"resource://xifDo7NTSL300Lp1goVutw", "plain"},
		"metadata":   map[string]string{"thumb": "resource://xifDo7NTSL300Lp1goVutw"},
	}

	out := w.CopyData(context.Background(), data)

	refs := out["references"].(types.References)
	assert.Equal(t, "figure ![f](https://cdn.example.com/x.png)", refs[0].Content)
	assert.Equal(t, "figure ![f](resource://xifDo7NTSL300Lp1goVutw)", original.Content)
	assert.Equal(t,
		[]string{"https://cdn.example.com/x.png", "plain"},
		out["tags"].([]string),
	)
	assert.Equal(t,
		map[string]string{"thumb": "https://cdn.example.com/x.png"},
		out["metadata"].(map[string]string),
	)
}

// Copying every metadata map on every SSE event would be pure garbage, so an
// unchanged map must be returned as-is.
func TestCopyData_ReturnsInputWhenNothingChanges(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	data := map[string]interface{}{"tool_name": "chart_export", "duration_ms": 42}
	assert.Equal(t, data, w.CopyData(context.Background(), data))
}

func TestCopyData_NilAndDisabled(t *testing.T) {
	assert.Nil(t, publicRewriter("https://x/y.png").CopyData(context.Background(), nil))
	data := map[string]interface{}{"output": "![a](resource://xifDo7NTSL300Lp1goVutw)"}
	assert.Equal(t, data, NewRewriter(nil, "TEST").CopyData(context.Background(), data))
}

func TestDefaultMode(t *testing.T) {
	ctx := context.Background()
	t.Setenv(EnvVar, "")
	assert.Equal(t, ModeHandle, DefaultMode(ctx))

	t.Setenv(EnvVar, "public")
	assert.Equal(t, ModePublic, DefaultMode(ctx))

	t.Setenv(EnvVar, "nonsense")
	assert.Equal(t, ModeHandle, DefaultMode(ctx), "a typo must degrade to the safe default")
}

// Anonymous surfaces (embed channels) pin the mode: neither the query parameter
// nor the deployment default may hand a visitor a credential-free URL. The
// downgrade is silent so a client that forwards the parameter keeps working.
func TestResolveMode_ForcedHandleModeWins(t *testing.T) {
	t.Setenv(EnvVar, "public")
	ctx := WithForcedHandleMode(context.Background())

	for _, queryValue := range []string{"", "public", "handle", "nonsense"} {
		mode, err := ResolveMode(ctx, queryValue)
		require.NoError(t, err, "queryValue=%q", queryValue)
		assert.Equal(t, ModeHandle, mode, "queryValue=%q", queryValue)
	}
}

// A KB-restricted API key is denied the /files proxy, so it must not receive
// anonymous file URLs through this parameter either.
func TestResolveMode_RejectsPublicForKBRestrictedKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})

	_, err := ResolveMode(ctx, "public")
	assert.ErrorIs(t, err, ErrPublicModeForbidden)

	// The deployment default must not smuggle it in either.
	t.Setenv(EnvVar, "public")
	_, err = ResolveMode(ctx, "")
	assert.ErrorIs(t, err, ErrPublicModeForbidden)

	// The default mode stays available: only public URLs are off limits.
	mode, err := ResolveMode(ctx, "handle")
	require.NoError(t, err)
	assert.Equal(t, ModeHandle, mode)
}

// A full-access or tenant-wide key is unaffected.
func TestResolveMode_AllowsPublicForUnrestrictedKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityRetrieve)},
	})
	mode, err := ResolveMode(ctx, "public")
	require.NoError(t, err)
	assert.Equal(t, ModePublic, mode)
}

func TestResolveMode_QueryWinsOverDeployment(t *testing.T) {
	ctx := context.Background()
	t.Setenv(EnvVar, "public")

	mode, err := ResolveMode(ctx, "handle")
	require.NoError(t, err)
	assert.Equal(t, ModeHandle, mode, "an explicit query value must win")

	mode, err = ResolveMode(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, ModePublic, mode)

	_, err = ResolveMode(ctx, "yes-please")
	assert.Error(t, err, "an invalid query value is a client error")
}

func tenantRequestContext(tenantID uint64) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{ID: tenantID})
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	return ctx
}

func TestNewRequestRewriter_BlocksCrossTenantResourceAndProviderURIs(t *testing.T) {
	t.Setenv("APP_EXTERNAL_URL", "https://genyee.example.com")
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "genyee-test-aes-key-32bytes-ok!!")

	const (
		ownRef   = "resource://OwnTenantHandle1234567"
		otherRef = "resource://OtherTenantHandle12345"
	)
	catalog := &resolverResourceCatalog{resources: map[string]*types.StoredResource{
		ownRef: {
			TenantID:     7,
			Provider:     "local",
			PhysicalPath: "local://7/exports/own.png",
		},
		otherRef: {
			TenantID:     9,
			Provider:     "local",
			PhysicalPath: "local://9/exports/secret.png",
		},
	}}
	ctx := tenantRequestContext(7)
	w := NewRequestRewriter(ctx, ModePublic, &stubFileService{}, nil, WithRequestResources(catalog, nil))

	assert.Equal(t, "https://cdn.example.com/"+ownRef, w.Ref(ctx, ownRef),
		"same-tenant resource handle should resolve")
	assert.Equal(t, otherRef, w.Ref(ctx, otherRef),
		"cross-tenant resource handle must be denied")

	// Same-tenant bare provider URIs succeed when tenant segment matches cleanly.
	localSigned := w.Ref(ctx, "local://7/exports/a.png")
	assert.Contains(t, localSigned, "https://genyee.example.com/api/v1/files/presigned")
	assert.Contains(t, localSigned, "tenant_id=7")
	assert.Equal(t, "https://cdn.example.com/minio://bucket/7/exports/a.png",
		w.Ref(ctx, "minio://bucket/7/exports/a.png"))

	// Cross-tenant, numeric-bucket spoof, and traversal URIs must all be rejected.
	deniedRefs := []string{
		"local://9/exports/a.png",
		"local://7/../9/exports/a.png",
		"minio://bucket/9/exports/a.png",
		"minio://7/9/exports/a.png", // numeric bucket '7' must not impersonate tenant 7
		"s3://my-bucket/9/doc/a.png",
		"cos://my-bucket/ap-guangzhou/9/doc/a.png",
		"oss://my-bucket/prefix/9/doc/a.png",
		"tos://my-bucket/9/doc/a.png",
		"obs://my-bucket/9/doc/a.png",
	}
	for _, ref := range deniedRefs {
		assert.Equal(t, ref, w.Ref(ctx, ref), "must reject unauthorized ref %s", ref)
	}

	// Strict auth or missing tenant context must also fail closed.
	strictNoCatalog := NewRequestRewriter(ctx, ModePublic, &stubFileService{}, nil, WithStrictAuth())
	assert.Equal(t, ownRef, strictNoCatalog.Ref(ctx, ownRef),
		"WithStrictAuth without catalog must fail closed")
	noTenantRewriter := NewRequestRewriter(
		context.Background(), ModePublic, &stubFileService{}, nil, WithRequestResources(catalog, nil),
	)
	assert.Equal(t, ownRef, noTenantRewriter.Ref(context.Background(), ownRef),
		"missing tenant context with catalog must fail closed")
}

func TestRewriteMessages_BlocksRawURIsInUserRoleMessages(t *testing.T) {
	const (
		ownRef   = "resource://OwnTenantHandle1234567"
		otherRef = "resource://OtherTenantHandle12345"
	)
	catalog := &resolverResourceCatalog{resources: map[string]*types.StoredResource{
		ownRef:   {TenantID: 7, Provider: "local", PhysicalPath: "local://7/exports/own.png"},
		otherRef: {TenantID: 9, Provider: "local", PhysicalPath: "local://9/exports/other.png"},
	}}
	ctx := tenantRequestContext(7)
	w := NewRequestRewriter(ctx, ModePublic, &stubFileService{}, nil, WithRequestResources(catalog, nil))

	userContent := "check ![x](" + otherRef + ") and ![y](local://7/exports/a.png) and ![z](s3://b/7/a.png)"
	userCaption := "cap ![c](minio://b/7/a.png)"
	messages := []*types.Message{
		{
			Role:    "user",
			Content: userContent,
			Images: types.MessageImages{
				{URL: ownRef, Caption: userCaption},
				{URL: otherRef},
				{URL: "local://7/exports/a.png"},
				{URL: "oss://bucket/7/exports/a.png"},
			},
		},
		{
			Role:    "assistant",
			Content: "here is ![own](" + ownRef + ") and ![other](" + otherRef + ")",
		},
	}

	w.RewriteMessages(ctx, messages)

	assert.Equal(t, userContent, messages[0].Content, "user prose must never be rewritten")
	assert.Equal(t, userCaption, messages[0].Images[0].Caption, "user caption must never be rewritten")
	assert.Equal(t, "https://cdn.example.com/"+ownRef, messages[0].Images[0].URL,
		"authorized same-tenant resource:// attachment on user message may resolve")
	assert.Equal(t, otherRef, messages[0].Images[1].URL,
		"cross-tenant resource:// attachment on user message must not resolve")
	assert.Equal(t, "local://7/exports/a.png", messages[0].Images[2].URL,
		"bare local:// URI on user message must never resolve")
	assert.Equal(t, "oss://bucket/7/exports/a.png", messages[0].Images[3].URL,
		"bare oss:// URI on user message must never resolve")

	assert.Equal(t,
		"here is ![own](https://cdn.example.com/"+ownRef+") and ![other]("+otherRef+")",
		messages[1].Content,
	)
}
