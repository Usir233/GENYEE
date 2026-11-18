package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/agent/collaboration"
	agenttools "github.com/Usir233/GENYEE/internal/agent/tools"
	"github.com/Usir233/GENYEE/internal/background"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newCollaborationStoreForAgentTest(t *testing.T) (*collaboration.Store, collaboration.Scope) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "collab.db")+"?_busy_timeout=5000&_journal_mode=WAL"),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)},
	)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&collaboration.Record{}))
	store := collaboration.NewStore(db)
	scope := collaboration.Scope{TenantID: 1, UserID: "u-1", SessionID: "sess-1", ID: uuid.NewString()}
	return store, scope
}

func TestResumeReplaysBackgroundInjectionsAndDisplaySteps(t *testing.T) {
	// Round 1 injects a background notice, then suspends on an approval-gated tool call.
	model1 := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		ToolCalls: []types.LLMToolCall{{
			ID:       "call-suspend",
			Type:     "function",
			Function: types.FunctionCall{Name: suspendingToolName, Arguments: `{}`},
		}},
		Done:         true,
		FinishReason: "tool_calls",
	}}}}}
	engine1 := newTestEngine(t, model1)
	engine1.EnableSuspension(json.RawMessage(`{}`))
	engine1.SetBackgroundRun(&fakeBackgroundRun{roundStart: []background.Delivery{
		{Text: "async_job (sj_42) finished with exit 0.", AckIDs: []string{"sj_42"}},
	}})
	blocked := &suspendingTool{
		BaseTool: agenttools.NewBaseTool(suspendingToolName, "", json.RawMessage(`{"type":"object"}`)),
	}
	engine1.toolRegistry = agenttools.NewToolRegistry()
	engine1.toolRegistry.RegisterTool(blocked)

	state1 := &types.AgentState{}
	engine1.beginTurn("check job", nil, state1)
	_, err := engine1.executeLoop(
		context.Background(),
		state1,
		"check job",
		emptyMessages(),
		emptyTools(),
		"sess-1",
		"msg-1",
	)
	require.NoError(t, err)
	require.NotNil(t, state1.Suspended)
	require.NotEmpty(t, blocked.snapshot)

	snap, err := DecodeRunSnapshot(blocked.snapshot)
	require.NoError(t, err)
	// PendingRound has no Thought and 0 completed ToolCalls, only BackgroundInjections:
	// DisplaySteps must still include it.
	steps := snap.DisplaySteps()
	require.Len(t, steps, 1, "DisplaySteps must preserve a pending step that carries BackgroundInjections")
	require.Len(t, steps[0].BackgroundInjections, 1)

	// Resume on a fresh engine: the previously ACKed background notice must be
	// replayed into the model transcript before the resumed round's tool output.
	model2 := &mockChat{responses: []mockResponse{answer("job sj_42 succeeded")}}
	engine2 := newTestEngine(t, model2)
	engine2.toolRegistry = agenttools.NewToolRegistry()
	engine2.toolRegistry.RegisterTool(&suspendingTool{
		BaseTool: agenttools.NewBaseTool(suspendingToolName, "", json.RawMessage(`{"type":"object"}`)),
	})

	resumedState, err := engine2.Resume(
		context.Background(),
		"sess-1",
		"msg-1",
		nil,
		blocked.snapshot,
		&types.AgentPrompt{ID: "p1", ToolCallID: "call-suspend"},
	)
	require.NoError(t, err)
	require.Equal(t, "job sj_42 succeeded", resumedState.FinalAnswer)
	require.Len(t, model2.calls, 1)
	assert.Contains(t,
		userMessageContaining(model2.calls[0], "<background_notices>"),
		"async_job (sj_42) finished with exit 0.",
		"Resume must replay BackgroundInjections into reconstructed LLM messages",
	)
}

type cancellingTool struct {
	agenttools.BaseTool
	cancel context.CancelFunc
}

func (t *cancellingTool) Execute(_ context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	if t.cancel != nil {
		t.cancel()
	}
	return &types.ToolResult{Success: true, Output: "tool completed just before stop"}, nil
}

func TestExecuteLoopCancelAtRoundBoundaryLeavesIsCompleteFalse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			ToolCalls: []types.LLMToolCall{{
				ID:       "call-1",
				Type:     "function",
				Function: types.FunctionCall{Name: "cancel_after_run", Arguments: `{}`},
			}},
			Done:         true,
			FinishReason: "tool_calls",
		}}},
		answer("should never be called"),
	}}
	engine := newTestEngine(t, model)
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(&cancellingTool{
		BaseTool: agenttools.NewBaseTool("cancel_after_run", "", json.RawMessage(`{"type":"object"}`)),
		cancel:   cancel,
	})

	state := &types.AgentState{}
	gotState, err := engine.executeLoop(ctx, state, "stop me", emptyMessages(), emptyTools(), "sess-1", "msg-1")
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, gotState)
	assert.False(t, gotState.IsComplete, "cancelled run with empty FinalAnswer must not mark state.IsComplete=true")
	assert.Empty(t, gotState.FinalAnswer)
}

func TestNaturalStopContinuationPlacesDraftBoundaryBeforeInjectedInput(t *testing.T) {
	store, scope := newCollaborationStoreForAgentTest(t)
	rt := collaboration.New(store, nil)
	binding, err := rt.AcquireParent(context.Background(), scope)
	require.NoError(t, err)

	model := &mockChat{responses: []mockResponse{
		answer("draft answer before background reminder"),
		answer("final answer after background reminder"),
	}}
	engine := newTestEngine(t, model)
	engine.SetCollaboration(binding)
	engine.SetBackgroundRun(&fakeBackgroundRun{beforeFinal: []background.Delivery{
		{Text: "Background job sj_99 finished."},
	}})

	state := &types.AgentState{}
	_, err = engine.executeLoop(
		context.Background(),
		state,
		"run task",
		emptyMessages(),
		emptyTools(),
		scope.SessionID,
		scope.ID,
	)
	require.NoError(t, err)
	require.Len(t, model.calls, 2)

	round2Msgs := model.calls[1]
	draftIdx, boundaryIdx, injectedIdx := -1, -1, -1
	for i, m := range round2Msgs {
		if m.Role == "assistant" && m.Content == "draft answer before background reminder" {
			draftIdx = i
		}
		if m.Role == "system" && m.Content == collaborationDeliveryBoundary {
			boundaryIdx = i
		}
		if m.Role == "user" && strings.Contains(m.Content, "Background job sj_99 finished.") {
			injectedIdx = i
		}
	}
	require.NotEqual(t, -1, draftIdx)
	require.NotEqual(t, -1, boundaryIdx)
	require.NotEqual(t, -1, injectedIdx)
	assert.Less(t, draftIdx, boundaryIdx, "draft boundary must follow assistant draft")
	assert.Less(
		t,
		boundaryIdx,
		injectedIdx,
		"draft boundary must precede newly injected input (matching Resume replay)",
	)
}

func TestResumeEarlyErrorEndsCollaborationParentLease(t *testing.T) {
	store, scope := newCollaborationStoreForAgentTest(t)
	rt := collaboration.New(store, nil)
	binding, err := rt.AcquireParent(context.Background(), scope)
	require.NoError(t, err)

	engine := newTestEngine(t, &mockChat{})
	engine.SetCollaboration(binding)

	// Corrupt snapshot causes early error in Resume; parent lease must be ended
	// rather than left active until the 30s lease expires.
	_, err = engine.Resume(context.Background(), scope.SessionID, scope.ID, nil, []byte(`{invalid`), nil)
	require.Error(t, err)

	group, readErr := store.Read(context.Background(), scope)
	require.NoError(t, readErr)
	assert.NotEqual(t, "active", group.Status, "early Resume failure must close/cancel collaboration group")
}

func TestCollaborationSpawnAcrossRoundsWithRepeatedToolCallID(t *testing.T) {
	store, scope := newCollaborationStoreForAgentTest(t)
	rt := collaboration.New(store, nil)
	binding, err := rt.AcquireParent(context.Background(), scope)
	require.NoError(t, err)

	// Simulate an OpenAI-compatible provider (e.g. Ollama) that omits tool call IDs
	// across rounds so NormalizeToolCallID assigns "spawn_agent_0" in both Round 1 and Round 2.
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			ToolCalls: []types.LLMToolCall{{
				ID:   "",
				Type: "function",
				Function: types.FunctionCall{
					Name:      "spawn_agent",
					Arguments: `{"task_name":"worker_one","message":"analyze module one"}`,
				},
			}},
			Done:         true,
			FinishReason: "tool_calls",
		}}},
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			ToolCalls: []types.LLMToolCall{{
				ID:   "",
				Type: "function",
				Function: types.FunctionCall{
					Name:      "spawn_agent",
					Arguments: `{"task_name":"worker_two","message":"analyze module two"}`,
				},
			}},
			Done:         true,
			FinishReason: "tool_calls",
		}}},
		answer("spawned both workers"),
		answer("integrated both workers"),
	}}
	engine := newTestEngine(t, model)
	engine.SetCollaboration(binding)
	engine.toolRegistry = agenttools.NewToolRegistry()
	for _, ct := range agenttools.NewCollaborationTools(binding, func(context.Context, string) (string, error) {
		return "{}", nil
	}) {
		engine.toolRegistry.RegisterTool(ct)
	}
	// Complete child runs immediately in store before BeforeFinish checks PendingAnswer?
	// Wait: BeforeFinish waits for active children unless they finish! Let's start a tiny runner!
	rt.Run = func(_ context.Context, w collaboration.Work) (collaboration.Result, error) {
		return collaboration.Result{Answer: "done " + w.Member.Name}, nil
	}
	rt.Poll = 5_000_000 // 5ms
	rt.Start(context.Background())
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	state := &types.AgentState{}
	_, err = engine.executeLoop(
		context.Background(),
		state,
		"delegate two slices",
		emptyMessages(),
		emptyTools(),
		scope.SessionID,
		scope.ID,
	)
	require.NoError(t, err)

	group, err := store.Read(context.Background(), scope)
	require.NoError(t, err)
	require.Len(
		t,
		group.Members,
		2,
		"both rounds must spawn distinct child agents despite identical normalized tool_call_id",
	)
	assert.Equal(t, "worker_one", group.Members[0].Name)
	assert.Equal(t, "worker_two", group.Members[1].Name)
	assert.NotEqual(t, group.Members[0].ID, group.Members[1].ID)
}
