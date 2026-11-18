package agent

import (
	"context"
	"encoding/json"
	"testing"

	agenttools "github.com/Usir233/GENYEE/internal/agent/tools"
	"github.com/Usir233/GENYEE/internal/event"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

// Deliver every prepared chunk before cancelling and closing the provider
// channel. This makes cancellation during an active stream deterministic.
type interruptedReasoningChat struct {
	mockChat
	cancel context.CancelFunc
}

func (m *interruptedReasoningChat) ChatStream(
	ctx context.Context,
	msgs []chat.Message,
	opts *chat.ChatOptions,
) (<-chan types.StreamResponse,
	error,
) {
	source, err := m.mockChat.ChatStream(ctx, msgs, opts)
	if err != nil {
		return nil, err
	}
	out := make(chan types.StreamResponse)
	go func() {
		for chunk := range source {
			out <- chunk
		}
		m.cancel()
		close(out)
	}()
	return out, nil
}

func TestInterruptedReasoningPersistsWithoutCompletingOrExecutingTools(t *testing.T) {
	metadata := types.ProviderMetadata{"provider": json.RawMessage(`{"encrypted":"opaque-state"}`)}
	cases := []struct {
		name, thought, reasoning, signature, finish string
		metadata                                    types.ProviderMetadata
		tool, prior                                 bool
	}{
		{name: "reasoning", reasoning: "partial reasoning"},
		{name: "signature", signature: "provider-signature"},
		{name: "metadata", metadata: metadata},
		{name: "answer", thought: "partial preamble"},
		{
			name:      "combined",
			thought:   "partial preamble",
			reasoning: "partial reasoning",
			signature: "provider-signature",
			metadata:  metadata,
		},

		{name: "unexecuted tool", reasoning: "partial reasoning", tool: true},
		{name: "prior successful tools", reasoning: "partial reasoning", prior: true},
		{name: "overflow finish", reasoning: "partial reasoning", finish: "model_context_window_exceeded"},
		{name: "empty"},
	}
	for _, tc := range cases {
		for _, explicitError := range []bool{false, true} {
			ending := "silent_close"
			if explicitError {
				ending = "error_chunk"
			}
			t.Run(tc.name+"/"+ending, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				chunks := []types.StreamResponse{{
					ResponseType: types.ResponseTypeThinking,
					Content:      tc.reasoning,
					FinishReason: tc.finish,
					Data: map[string]any{
						"reasoning_signature": tc.signature,
						"reasoning_metadata":  tc.metadata,
					},
				}}
				if tc.thought != "" {
					chunks = append(
						chunks,
						types.StreamResponse{
							ResponseType: types.ResponseTypeAnswer,
							Content:      tc.thought,
						},
					)
				}
				if tc.tool {
					chunks = append(
						chunks,
						types.StreamResponse{
							ResponseType: types.ResponseTypeAnswer,
							ToolCalls: []types.LLMToolCall{{
								ID: "must-not-run",
								Function: types.FunctionCall{
									Name:      "write_test",
									Arguments: "{\"content\":\"partial\"}",
								},
							}},
						},
					)
				}
				if explicitError {
					chunks = append(
						chunks,
						types.StreamResponse{
							ResponseType: types.ResponseTypeError,
							Content:      "context canceled",
							Done:         true,
						},
					)
				}
				model := &interruptedReasoningChat{
					mockChat: mockChat{responses: []mockResponse{{chunks: chunks}}},
					cancel:   cancel,
				}
				engine := newTestEngine(t, model)
				engine.toolRegistry = agenttools.NewToolRegistry()
				tool := newCountingTool("write_test")
				engine.toolRegistry.RegisterTool(tool)
				state := &types.AgentState{}
				if tc.prior {
					state.RoundSteps = []types.AgentStep{{ToolCalls: []types.ToolCall{{
						ID:   "prior",
						Name: "write_test",
						Result: &types.ToolResult{
							Success: true,
							Output:  "previous result",
						},
					}}}}
				}
				var completed *event.AgentCompleteData
				engine.eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
					v := evt.Data.(event.AgentCompleteData)
					completed = &v
					return nil
				})
				engine.beginTurn("question", nil, state)
				_, err := engine.executeLoop(
					ctx,
					state,
					"question",
					emptyMessages(),
					emptyTools(),
					"session",
					"message",
				)
				if explicitError {
					require.Error(t, err, "provider error remains an error for callers")
				} else {
					require.NoError(t, err)
				}
				require.False(t, state.IsComplete)
				require.Empty(t, state.FinalAnswer)
				require.Zero(t, tool.calls)
				require.Equal(t, 1, model.callCount, "cancelled turns cannot retry or synthesize")
				count := 0
				if tc.name != "empty" {
					count++
				}
				if tc.prior {
					count++
				}
				require.Len(t, state.RoundSteps, count)
				if tc.name != "empty" {
					step := state.RoundSteps[len(state.RoundSteps)-1]
					require.Equal(t, tc.thought, step.Thought)
					require.Equal(t, tc.reasoning, step.ReasoningContent)
					require.Equal(t, tc.signature, step.ReasoningSignature)
					require.Equal(t, tc.metadata, step.ReasoningMetadata)
					require.Empty(t, step.ToolCalls)
				}
				require.NotNil(t, completed)
				require.Empty(t, completed.FinalAnswer)
				require.Equal(
					t,
					state.RoundSteps,
					completed.AgentSteps,
					"the completion event is the actual persistence input",
				)
			})
		}
	}
}

func TestInterruptedReasoningFailureWithoutParentCancellationStillRetries(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{
				ResponseType: types.ResponseTypeThinking,
				Content:      "failed attempt reasoning",
			},
			{
				ResponseType: types.ResponseTypeError,
				Content:      "upstream 503",
				Done:         true,
			},
		}},

		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "recovered",
			Done:         true,
			FinishReason: "stop",
		}}},
	}}
	engine := newTestEngine(t, model)
	engine.toolRegistry = agenttools.NewToolRegistry()
	state, err := engine.Execute(t.Context(), "session", "message", "question", nil)
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Equal(t, "recovered", state.FinalAnswer)
	require.Equal(t, 2, model.callCount)
	for _, step := range state.RoundSteps {
		require.Empty(t, step.ReasoningContent, "a failed attempt must not masquerade as the successful response")
	}
}
