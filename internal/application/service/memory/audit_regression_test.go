package memory

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tenantRecordingModelService struct {
	*stubModelService
	mu              sync.Mutex
	seenTenantIDs   []uint64
	seenForeignInfo bool
}

func (m *tenantRecordingModelService) record(ctx context.Context, expectedTenant uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tid, _ := types.TenantIDFromContext(ctx)
	m.seenTenantIDs = append(m.seenTenantIDs, tid)
	if ti, ok := types.TenantInfoFromContext(ctx); ok && ti != nil && ti.ID != expectedTenant {
		m.seenForeignInfo = true
	}
}

func (m *tenantRecordingModelService) ListModels(ctx context.Context) ([]*types.Model, error) {
	m.record(ctx, 7)
	return m.stubModelService.ListModels(ctx)
}

func (m *tenantRecordingModelService) GetEmbeddingModel(
	ctx context.Context,
	modelID string,
) (embedding.Embedder, error) {
	m.record(ctx, 7)
	return m.stubModelService.GetEmbeddingModel(ctx, modelID)
}

func (m *tenantRecordingModelService) GetChatModel(ctx context.Context, modelID string) (chat.Chat, error) {
	m.record(ctx, 7)
	return m.stubModelService.GetChatModel(ctx, modelID)
}

func TestFamiliarKnowledgeIDs_HonorsWorkspaceAgentAndUserSwitches(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	ref := []types.MemoryDocAffinity{{KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Title: "Spec"}}
	for i := 0; i < types.MemoryDocAffinityMinHits; i++ {
		svc.RecordAnswerSources(ctx, ref)
	}
	require.Equal(t, []string{"doc-1"}, svc.FamiliarKnowledgeIDs(ctx))

	disabled := false
	agentOffCtx := types.ApplyAgentMemoryPreference(ctx, &disabled)
	assert.Empty(t, svc.FamiliarKnowledgeIDs(agentOffCtx), "agent memory opt-out must suppress doc affinity boost")

	require.NoError(t, svc.SetEnabled(ctx, false))
	assert.Empty(t, svc.FamiliarKnowledgeIDs(ctx), "user memory opt-out must suppress doc affinity boost")

	require.NoError(t, svc.SetEnabled(ctx, true))
	tenantRepo.set(1, &types.MemoryConfig{Enabled: false})
	assert.Empty(t, svc.FamiliarKnowledgeIDs(ctx), "workspace memory disabled must suppress doc affinity boost")
}

func TestRecall_DoesNotResurrectStaleSubjectBlockWhenResidentLoadSucceedsEmpty(t *testing.T) {
	svc, db, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	item, err := svc.Remember(ctx, types.MemoryItem{
		Kind:       types.MemoryKindPreference,
		Content:    "旧的偏好必须消失",
		Importance: 5,
	})
	require.NoError(t, err)
	require.Contains(t, svc.Recall(ctx, "随便问问").Prompt, "旧的偏好必须消失")

	// Simulate item archival where a block rebuild did not overwrite subject.BlockText yet.
	require.NoError(t, db.Model(&types.MemoryItem{}).Where("id = ?", item.ID).
		Update("status", types.MemoryStatusArchived).Error)

	recall := svc.Recall(ctx, "随便问问")
	assert.NotContains(t, recall.Prompt, "旧的偏好必须消失",
		"successful empty resident load must not fall back to stale subject.BlockText")
}

func TestExecutionTenant_ModelLookupsAndScheduledExtractionUseCallerWorkspace(t *testing.T) {
	svc, tenantRepo, baseModels := newVectorHarness(t)
	baseModels.workspaceModels = []*types.Model{
		{ID: "chat-7", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive, IsDefault: true},
		{ID: "embed-1", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive},
	}
	baseModels.response = `{"statement":"合并后的偏好"}`
	recorder := &tenantRecordingModelService{stubModelService: baseModels}
	enqueuer := &stubEnqueuer{}
	svc.modelService = recorder
	svc.enqueuer = enqueuer

	baseCtx := enabledCtx(t, tenantRepo, 7, "alice")
	// Shared agent belongs to workspace 99: TenantIDContextKey=99 and TenantInfo.ID=99,
	// while Caller.TenantID remains 7.
	execCtx := context.WithValue(baseCtx, types.TenantInfoContextKey, &types.Tenant{ID: 99})
	execCtx = types.WithExecutionTenant(execCtx, 99)

	_, err := svc.Remember(execCtx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "回答风格", Content: "回答直接给结论",
	})
	require.NoError(t, err)
	_, err = svc.Remember(execCtx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "回答风格", Content: "直接给结论别铺垫",
	})
	require.NoError(t, err)

	_ = svc.Recall(execCtx, "别铺垫")
	_, err = svc.ConsolidateNow(execCtx)
	require.NoError(t, err)

	svc.ScheduleExtraction(execCtx, "sess-shared", "msg-1", "foreign-model-99")
	require.Len(t, enqueuer.tasks, 1)
	var payload types.MemoryExtractPayload
	require.NoError(t, json.Unmarshal(enqueuer.tasks[0].Payload(), &payload))
	assert.Equal(t, uint64(7), payload.TenantID)
	assert.Empty(
		t,
		payload.ChatModelID,
		"foreign workspace chat model ID must not be persisted into caller extraction task",
	)

	recorder.mu.Lock()
	seenIDs := append([]uint64(nil), recorder.seenTenantIDs...)
	seenForeignInfo := recorder.seenForeignInfo
	recorder.mu.Unlock()

	require.NotEmpty(t, seenIDs)
	for _, tid := range seenIDs {
		assert.Equal(t, uint64(7), tid, "all ModelService calls for personal memory must run in caller workspace 7")
	}
	assert.False(t, seenForeignInfo, "foreign TenantInfo must not leak into caller memory ModelService calls")
}
