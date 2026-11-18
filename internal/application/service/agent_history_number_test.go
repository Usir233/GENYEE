package service

import (
	"encoding/json"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAgentHistoryPreservesNumbersAfterDatabaseScan(t *testing.T) {
	for _, number := range []string{
		"9007199254740993",
		"18446744073709551615",
		"0.12345678901234567890123456789",
		"1e400",
		"-0",
		"1.00",
		"42",
	} {
		for _, scanString := range []bool{false, true} {
			mode := "bytes"
			if scanString {
				mode = "string"
			}
			t.Run(number+"/"+mode, func(t *testing.T) {
				original := types.AgentSteps{
					{
						ToolCalls: []types.ToolCall{
							{
								ID:   "number-call",
								Name: "call_mcp_tool",
								Args: map[string]any{
									"tool_ref":  "stable-ref",
									"arguments": map[string]any{"id": json.Number(number)},
								},
								Result: &types.ToolResult{Success: true, Output: "done"},
							},
						},
					},
				}
				dbValue, err := original.Value()
				require.NoError(t, err)
				if scanString {
					dbValue = string(dbValue.([]byte))
				}
				var restored types.AgentSteps
				require.NoError(t, restored.Scan(dbValue))
				messages := buildAssistantHistoryMessages(&types.Message{Role: "assistant", AgentSteps: restored})
				require.Len(t, messages, 2)
				require.Len(t, messages[0].ToolCalls, 1)
				var envelope struct {
					Arguments map[string]json.RawMessage `json:"arguments"`
				}
				require.NoError(t, json.Unmarshal([]byte(messages[0].ToolCalls[0].Function.Arguments), &envelope))
				require.Equal(t, number, string(envelope.Arguments["id"]))
				require.Equal(t, "number-call", messages[1].ToolCallID)
			})
		}
	}
}
