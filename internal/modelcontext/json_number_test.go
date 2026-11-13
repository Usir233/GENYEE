package modelcontext

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

var numberLiterals = []string{
	"9007199254740993", "18446744073709551615", "-9007199254740993",
	"0.12345678901234567890123456789", "1e400", "-0", "1.00", "9e2", "42",
}

func requireNumberLiteral(t *testing.T, raw, want string) {
	t.Helper()
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &object))
	require.Equal(t, want, string(object["number"]), "numeric value changed in %s", raw)
	var nested []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(object["nested"], &nested))
	require.Len(t, nested, 1)
	require.Equal(t, want, string(nested[0]["number"]), "nested numeric value changed")
}

func numberedArguments(number, source string) string {
	return fmt.Sprintf(`{"number":%s,"nested":[{"number":%s}],"source":%q}`, number, number, source)
}

func TestModelContextJSONNumbersSurviveToolDecode(t *testing.T) {
	for _, tool := range []string{"call_mcp_tool", "mcp_example", "run_operation", "wiki_read_issue"} {
		for _, number := range numberLiterals {
			t.Run(tool+"/"+number, func(t *testing.T) {
				registry := NewRegistry(true)
				registry.RegisterKnowledgeBase("kb-id")
				raw := numberedArguments(number, "kb://1")
				calls := []types.LLMToolCall{{Function: types.FunctionCall{Name: tool, Arguments: raw}}}
				registry.DecodeToolCalls(calls)
				requireNumberLiteral(t, calls[0].Function.Arguments, number)
				require.Contains(t, calls[0].Function.Arguments, "genyee://kb/kb-id")
				require.Equal(t, raw, calls[0].ModelArguments)
				require.Equal(t, ArgumentResolutionResolved, calls[0].ArgumentResolution)
				require.Empty(t, calls[0].UnresolvedHandles)
				first := calls[0]
				registry.DecodeToolCalls(calls)
				require.Equal(t, first, calls[0], "stream completion can decode an assembled call twice")
			})
		}
	}
}

func TestModelContextJSONNumbersSurviveHistoryEncoding(t *testing.T) {
	for _, tool := range []string{"mcp_example", "run_operation", "wiki_read_issue"} {
		for _, number := range numberLiterals {
			t.Run(tool+"/"+number, func(t *testing.T) {
				registry := NewRegistry(true)
				raw := numberedArguments(number, "genyee://kb/kb-id")
				messages := []chat.Message{{Role: "assistant", ToolCalls: []chat.ToolCall{{
					Function: chat.FunctionCall{Name: tool, Arguments: raw},
				}}}}
				encoded := registry.EncodeMessages(messages)
				got := encoded[0].ToolCalls[0].Function.Arguments
				requireNumberLiteral(t, got, number)
				require.Contains(t, got, "kb://1")
				require.Equal(t, raw, messages[0].ToolCalls[0].Function.Arguments, "durable history stays unchanged")
			})
		}
	}
}

func TestModelContextJSONNumbersSurvivePrivateResultEncoding(t *testing.T) {
	for _, number := range numberLiterals {
		t.Run(number, func(t *testing.T) {
			registry := NewRegistry(true)
			raw := fmt.Sprintf(`{"id":"issue-uuid","number":%s,"nested":[{"number":%s}]}`, number, number)
			result := &types.ToolResult{Success: true, Output: raw}
			got := registry.ModelToolResultForTool("wiki_read_issue", result)
			requireNumberLiteral(t, got, number)
			require.Contains(t, got, `"id":"i1"`)
			require.Equal(t, raw, result.Output)
		})
	}
}

func TestModelContextJSONNumbersSurviveMCPEnvelopeNormalization(t *testing.T) {
	registry := NewRegistry(true)
	registry.mcpTools.Register("server-id/tool")
	registry.RegisterKnowledgeBase("kb-id")
	inner := numberedArguments("9007199254740993", "kb://1")
	wrapped, err := json.Marshal(inner)
	require.NoError(t, err)
	raw := `{"tool_ref":"mt1","arguments":` + string(wrapped) + `}`
	calls := []types.LLMToolCall{{Function: types.FunctionCall{Name: "call_mcp_tool", Arguments: raw}}}
	registry.DecodeToolCalls(calls)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &envelope))
	requireNumberLiteral(t, string(envelope["arguments"]), "9007199254740993")
	require.Contains(t, string(envelope["arguments"]), "genyee://kb/kb-id")
	require.Equal(t, `"server-id/tool"`, string(envelope["tool_ref"]))
	require.Equal(t, raw, calls[0].ModelArguments)
}

func TestModelContextJSONRewriteStillRejectsInvalidDocuments(t *testing.T) {
	for _, raw := range []string{
		`{"source":"kb://1"} {}`, `{"source":"kb://1"} trailing`,
		"{\"source\":\"kb://1\"}\u00a0", `{"source":"kb://1",}`, `{"source":"kb://1"`,
	} {
		t.Run(raw, func(t *testing.T) {
			registry := NewRegistry(true)
			registry.RegisterKnowledgeBase("kb-id")
			calls := []types.LLMToolCall{{Function: types.FunctionCall{Name: "run_operation", Arguments: raw}}}
			registry.DecodeToolCalls(calls)
			require.Equal(t, raw, calls[0].Function.Arguments, "invalid JSON must not become a valid tool call")
		})
	}
}
