package types

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestToolArgumentNumbersSurviveDBAndJSONDecoding(t *testing.T) {
	for _, number := range []string{"9007199254740993", "18446744073709551615", "0.12345678901234567890123456789", "1e400", "-0", "1.00", "9e2", "42"} {
		for _, mode := range []string{"json", "db_bytes", "db_string"} {
			t.Run(number+"/"+mode, func(t *testing.T) {
				args := map[string]any{"number": json.Number(number), "nested": []any{json.Number(number)}}
				call := ToolCall{ID: "id", Name: "name", Args: args, Target: &ToolCallTarget{Name: "target", Args: args, ServiceName: "service", ToolName: "tool"}, Duration: 123, Reflection: "reflection", ProviderMetadata: ToolCallMetadata{"signature": json.RawMessage(`"sig"`)}, Result: &ToolResult{Success: true, Output: "result", Data: map[string]any{"count": float64(3)}, Images: []string{"image"}}}
				original := AgentSteps{{Iteration: 2, ReasoningContent: "reasoning", ReasoningSignature: "sig", ReasoningMetadata: ProviderMetadata{"provider": json.RawMessage(`{"key":"value"}`)}, ToolCalls: []ToolCall{call}}}
				value, err := original.Value()
				require.NoError(t, err)
				var decoded AgentSteps
				switch mode {
				case "json":
					err = json.Unmarshal(value.([]byte), &decoded)
				case "db_bytes":
					err = decoded.Scan(value)
				case "db_string":
					err = decoded.Scan(string(value.([]byte)))
				}
				require.NoError(t, err)
				encoded, err := json.Marshal(decoded)
				require.NoError(t, err)
				require.Equal(t, string(value.([]byte)), string(encoded), "all serialized argument values and other fields survive unchanged")
				require.IsType(t, float64(0), decoded[0].ToolCalls[0].Result.Data["count"], "result data decoding must keep its existing representation")
			})
		}
	}
}
func TestToolArgumentDecodingPreservesNullAndMissingCompatibility(t *testing.T) {
	for _, target := range []bool{false, true} {
		kind := "call"
		if target {
			kind = "target"
		}
		for _, raw := range []string{`{}`, `null`, `{"args":null}`, `{"args":{}}`} {
			t.Run(kind+"/"+raw, func(t *testing.T) {
				old := map[string]any{"old": "preserved"}
				want := old
				if raw == `{"args":null}` {
					want = nil
				}
				if raw == `{"args":{}}` {
					want = old
				} // decoding an empty object merges into an existing map
				if target {
					value := ToolCallTarget{Name: "old", Args: old}
					require.NoError(t, json.Unmarshal([]byte(raw), &value))
					require.Equal(t, want, value.Args)
					require.Equal(t, "old", value.Name)
				} else {
					value := ToolCall{ID: "old", Args: old}
					require.NoError(t, json.Unmarshal([]byte(raw), &value))
					require.Equal(t, want, value.Args)
					require.Equal(t, "old", value.ID)
				}
			})
		}
	}
	var call *ToolCall
	require.NoError(t, json.Unmarshal([]byte(`null`), &call))
	require.Nil(t, call)
	var target *ToolCallTarget
	require.NoError(t, json.Unmarshal([]byte(`null`), &target))
	require.Nil(t, target)
}
func TestToolArgumentDecodingRejectsInvalidDocuments(t *testing.T) {
	for _, raw := range []string{`{"args":{"number":1}} {}`, `{"args":{"number":1}} trailing`, `{"args":{"number":1},}`, `{"args":[1]}`, `{"args":true}`, `{"args":{"number":NaN}}`} {
		t.Run(raw, func(t *testing.T) {
			var call ToolCall
			require.Error(t, json.Unmarshal([]byte(raw), &call))
			var target ToolCallTarget
			require.Error(t, json.Unmarshal([]byte(raw), &target))
		})
	}
}

func TestToolArgumentDecodingKeepsMapMergeAndFieldMatching(t *testing.T) {
	for _, raw := range []string{`{"args":{"number":9007199254740993},"args":{"next":"value"}}`, `{"ARGS":{"number":9007199254740993,"next":"value"}}`} {
		t.Run(raw, func(t *testing.T) {
			call := ToolCall{Args: map[string]any{"old": "value"}}
			target := ToolCallTarget{Args: map[string]any{"old": "value"}}
			require.NoError(t, json.Unmarshal([]byte(raw), &call))
			require.NoError(t, json.Unmarshal([]byte(raw), &target))
			for _, args := range []map[string]any{call.Args, target.Args} {
				require.Equal(t, "value", args["old"])
				require.Equal(t, "value", args["next"])
				encoded, err := json.Marshal(args)
				require.NoError(t, err)
				require.Contains(t, string(encoded), "9007199254740993")
			}
		})
	}
}
