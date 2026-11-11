package mcp

import (
	"context"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPToolsRejectPathTraversalIDs(t *testing.T) {
	svc := &fakeSvc{}
	c, _ := newTestServer(t, svc)

	cases := []struct {
		tool string
		args map[string]any
	}{
		{tool: "kb_view", args: map[string]any{"kb_id": "../tenants/1/api-keys"}},
		{tool: "doc_list", args: map[string]any{"kb_id": "../tenants/1/api-keys"}},
		{tool: "doc_view", args: map[string]any{"doc_id": "../tenants/1/api-keys"}},
		{tool: "doc_download", args: map[string]any{"doc_id": "../tenants/1/api-keys"}},
		{tool: "search_chunks", args: map[string]any{"kb_id": "../../admin", "query": "hello"}},
		{tool: "chat", args: map[string]any{"kb_id": "kb_1", "query": "hi", "session_id": "../admin"}},
		{tool: "session_ask", args: map[string]any{"agent_id": "../admin", "query": "hi"}},
		{tool: "chunk_list", args: map[string]any{"doc_id": ".."}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			res, err := c.CallTool(ctx, &mcpsdk.CallToolParams{
				Name:      tc.tool,
				Arguments: tc.args,
			})
			require.NoError(t, err)
			assert.True(t, res.IsError, "expected %s to reject traversal argument", tc.tool)
		})
	}

	assert.Empty(t, svc.calls.kbViewID)
	assert.Empty(t, svc.calls.docViewID)
	assert.Empty(t, svc.calls.openDocID)
	assert.Empty(t, svc.calls.chunkDocID)
}
