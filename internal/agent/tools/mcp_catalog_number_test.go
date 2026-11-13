package tools

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMCPTargetNumbersPreserveProxyAndDirectArguments(t *testing.T) {
	for _, number := range []string{"9007199254740993", "18446744073709551615", "0.12345678901234567890123456789", "1e400", "1.00", "42"} {
		for _, mode := range []string{"proxy", "direct"} {
			t.Run(number+"/"+mode, func(t *testing.T) {
				ctx, registry, catalog, _, _ := catalogFixture(t, 1)
				described := describeTool(ctx, t, registry, "server-1", "tool_000")
				args := json.RawMessage(`{"number":` + number + `,"nested":[` + number + `]}`)
				name := ToolCallMCPTool
				raw, err := json.Marshal(map[string]any{"tool_ref": described.ToolRef, "arguments": args})
				require.NoError(t, err)
				if mode == "direct" {
					tool := catalog.cachedTool(described.ToolRef)
					require.NotNil(t, tool)
					bound := &MCPRegisteredTool{MCPTool: tool, catalog: catalog, ref: described.ToolRef}
					registry.RegisterTool(bound)
					name = bound.Name()
					raw = args
				}
				target := registry.MCPCallTarget(ctx, name, raw)
				require.NotNil(t, target)
				encoded, err := json.Marshal(target.Args)
				require.NoError(t, err)
				var value map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(encoded, &value))
				require.Equal(t, number, string(value["number"]))
				require.Equal(t, "["+number+"]", string(value["nested"]))
				require.Equal(t, "订单", target.ServiceName)
				require.Equal(t, "tool_000", target.ToolName)
			})
		}
	}
}
