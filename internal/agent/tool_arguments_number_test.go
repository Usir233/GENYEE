package agent

import (
	"context"
	"encoding/json"
	agenttools "github.com/Usir233/GENYEE/internal/agent/tools"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
	"testing"
)

type numberCaptureTool struct {
	agenttools.BaseTool
	received json.RawMessage
}

func (t *numberCaptureTool) Execute(_ context.Context, raw json.RawMessage) (*types.ToolResult, error) {
	t.received = append(json.RawMessage(nil), raw...)
	return &types.ToolResult{Success: true, Output: "done"}, nil
}

var toolArgumentNumberLiterals = []string{"9007199254740993", "18446744073709551615", "-9007199254740993", "0.12345678901234567890123456789", "1e400", "-0", "1.00", "9e2", "42"}

func exactNumberArguments(number string) string {
	return `{"number":` + number + `,"nested":[{"number":` + number + `}],"label":"unchanged","enabled":true,"empty":null}`
}
func requireToolNumber(t *testing.T, raw, number string) {
	t.Helper()
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &obj))
	require.Equal(t, number, string(obj["number"]), raw)
	var nested []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(obj["nested"], &nested))
	require.Len(t, nested, 1)
	require.Equal(t, number, string(nested[0]["number"]), raw)
}
func TestToolArgumentNumbersSurviveExecutionAndReplay(t *testing.T) {
	for _, number := range toolArgumentNumberLiterals {
		for _, repair := range []bool{false, true} {
			name := number + "/valid"
			if repair {
				name = number + "/repaired"
			}
			t.Run(name, func(t *testing.T) {
				engine := newTestEngine(t, &mockChat{})
				engine.toolRegistry = agenttools.NewToolRegistry()
				tool := &numberCaptureTool{BaseTool: agenttools.NewBaseTool("number_capture", "", json.RawMessage(`{"type":"object"}`))}
				engine.toolRegistry.RegisterTool(tool)
				raw := exactNumberArguments(number)
				if repair {
					raw = raw[:len(raw)-1] + ",}"
				}
				tc := types.LLMToolCall{ID: "call-number", Function: types.FunctionCall{Name: tool.Name(), Arguments: raw}}
				call := engine.runToolCall(t.Context(), tc, 0, 0, 1, "session", "message")
				require.True(t, call.Result.Success, call.Result.Error)
				requireToolNumber(t, string(tool.received), number)
				messages := engine.appendToolResults(nil, types.AgentStep{ToolCalls: []types.ToolCall{call}})
				requireToolNumber(t, messages[0].ToolCalls[0].Function.Arguments, number)
				// Trace must show the same numeric values as execution, not a rounded ID.
				audit := buildToolSpanInput(tc, call.Args, false)
				for _, key := range []string{"model_arguments", "resolved_arguments"} {
					if repair && key == "model_arguments" {
						require.Equal(t, raw, audit[key])
						continue
					}
					encoded, err := json.Marshal(audit[key])
					require.NoError(t, err)
					requireToolNumber(t, string(encoded), number)
				}
			})
		}
	}
}
func TestToolArgumentNumbersSurviveSnapshotAndClosure(t *testing.T) {
	for _, number := range toolArgumentNumberLiterals {
		t.Run(number, func(t *testing.T) {
			args := map[string]any{"number": json.Number(number), "nested": []any{map[string]any{"number": json.Number(number)}}}
			call := types.ToolCall{ID: "done", Name: "number_capture", Args: args, Target: &types.ToolCallTarget{Name: "target", Args: args}, Result: &types.ToolResult{Success: true, Output: "done"}}
			snap := RunSnapshot{Version: RunSnapshotVersion, CompletedSteps: []types.AgentStep{{ToolCalls: []types.ToolCall{call}}}, PendingRound: PendingRound{Step: types.AgentStep{ToolCalls: []types.ToolCall{call}}, Calls: []PendingCall{{ID: "done"}, {ID: "waiting", Name: "number_capture", Arguments: exactNumberArguments(number)}}}}
			data, err := json.Marshal(snap)
			require.NoError(t, err)
			decoded, err := DecodeRunSnapshot(data)
			require.NoError(t, err)
			for _, step := range decoded.ClosedSteps("expired") {
				for _, tc := range step.ToolCalls {
					raw, err := json.Marshal(tc.Args)
					require.NoError(t, err)
					requireToolNumber(t, string(raw), number)
					if tc.Target != nil {
						raw, err = json.Marshal(tc.Target.Args)
						require.NoError(t, err)
						requireToolNumber(t, string(raw), number)
					}
				}
			}
		})
	}
}
func TestToolArgumentNumbersSurviveActualResume(t *testing.T) {
	const number = "9007199254740993"
	snap, err := DecodeRunSnapshot(suspendedSnapshot(t))
	require.NoError(t, err)
	args := map[string]any{"number": json.Number(number), "nested": []any{map[string]any{"number": json.Number(number)}}}
	snap.PendingRound.Step.ToolCalls[0].Args = args
	for i := range snap.PendingRound.Calls {
		snap.PendingRound.Calls[i].Arguments = exactNumberArguments(number)
	}
	snap.CompletedSteps = []types.AgentStep{{ToolCalls: []types.ToolCall{{ID: "previous", Name: "first_tool", Args: args, Result: &types.ToolResult{Success: true, Output: "done"}}}}}
	raw, err := json.Marshal(snap)
	require.NoError(t, err)
	f := newSuspendFixture(t, "first_tool", false)
	model := f.engine.chatModel.(*mockChat)
	model.responses = []mockResponse{{chunks: []types.StreamResponse{{ResponseType: types.ResponseTypeAnswer, Content: "done", Done: true, FinishReason: "stop"}}}}
	state, err := f.engine.Resume(t.Context(), "sess-1", "msg-1", nil, raw, answeredPrompt("call-b"))
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Zero(t, f.first.calls)
	require.Equal(t, 1, f.after.calls)
	require.NotNil(t, f.blocked.answered)
	require.Len(t, model.calls, 1)
	count := 0
	for _, message := range model.calls[0] {
		for _, tc := range message.ToolCalls {
			requireToolNumber(t, tc.Function.Arguments, number)
			count++
		}
	}
	require.Equal(t, 4, count)
}
func TestToolArgumentInvalidAndTruncatedPayloadsRemainRefused(t *testing.T) {
	for _, raw := range []string{`{"number":9007199254740993`, `{"number":NaN}`, `[9007199254740993]`} {
		t.Run(raw, func(t *testing.T) {
			engine := newTestEngine(t, &mockChat{})
			engine.toolRegistry = agenttools.NewToolRegistry()
			tool := &numberCaptureTool{BaseTool: agenttools.NewBaseTool("number_capture", "", json.RawMessage(`{"type":"object"}`))}
			engine.toolRegistry.RegisterTool(tool)
			tc := engine.runToolCall(t.Context(), types.LLMToolCall{Function: types.FunctionCall{Name: tool.Name(), Arguments: raw}}, 0, 0, 1, "s", "m")
			require.False(t, tc.Result.Success)
			require.Empty(t, tool.received)
			require.Equal(t, raw, tc.Args["_raw"])
		})
	}
}
func TestToolArgumentLegacySnapshotRemainsReadable(t *testing.T) {
	snap, err := DecodeRunSnapshot([]byte(`{"version":1,"completed_steps":[{"tool_calls":[{"id":"legacy","name":"tool","args":{"number":9007199254740992}}]}]}`))
	require.NoError(t, err)
	encoded, err := json.Marshal(snap.CompletedSteps[0].ToolCalls[0].Args)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "9007199254740992", "already rounded persisted values cannot be reconstructed")
}
