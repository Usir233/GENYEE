package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	sdk "github.com/Usir233/GENYEE/client"
)

type emptySessChatSvc struct {
	fakeSvc
}

func (s *emptySessChatSvc) CreateSession(_ context.Context, _ *sdk.CreateSessionRequest) (*sdk.Session, error) {
	return nil, nil
}

func TestW8_MCP_BoundsValidationTerminalErrorAndRedaction(t *testing.T) {
	// 1. Secret tokens in upstream errors must be redacted before surfacing to MCP clients.
	resErr := toolErrorResult(
		errors.New("upstream failed with Authorization: Bearer secret_jwt_token and gy_live_secret123"),
	)
	require.NotNil(t, resErr)
	txt := resErr.Content[0].(*mcpsdk.TextContent).Text
	if strings.Contains(txt, "secret_jwt_token") || strings.Contains(txt, "gy_live_secret123") {
		t.Fatalf("toolErrorResult leaked secret token: %q", txt)
	}

	// 2. Nil session from CreateSession must return structured error instead of panicking.
	cNil, _ := newTestServer(t, &emptySessChatSvc{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := cNil.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "chat",
		Arguments: map[string]any{"kb_id": "kb_1", "query": "hi"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected IsError when CreateSession returns nil session")

	// 3. Terminal error frame with nil streamErr must still fail the MCP tool call.
	svcTermErr := &fakeSvc{
		kbStreamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeError, Content: "model crashed", Done: true},
		},
	}
	cTerm, _ := newTestServer(t, svcTermErr)
	res, err = cTerm.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "chat",
		Arguments: map[string]any{"kb_id": "kb_1", "query": "hi"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected IsError when SSE emits terminal error frame with nil transport error")

	// 4. Negative limits/pages, out-of-range thresholds, invalid doc status, and inverted time window must fail.
	cVal, _ := newTestServer(t, &fakeSvc{})
	badCalls := []struct {
		tool string
		args map[string]any
	}{
		{"search_chunks", map[string]any{"kb_id": "kb_1", "query": "q", "limit": -1}},
		{"search_chunks", map[string]any{"kb_id": "kb_1", "query": "q", "vector_threshold": 1.5}},
		{"search_chunks", map[string]any{"kb_id": "kb_1", "query": "q", "keyword_threshold": -0.1}},
		{"chunk_list", map[string]any{"doc_id": "doc_1", "limit": -5}},
		{"doc_list", map[string]any{"kb_id": "kb_1", "page": -1}},
		{"doc_list", map[string]any{"kb_id": "kb_1", "page_size": -10}},
		{"doc_list", map[string]any{"kb_id": "kb_1", "status": "bogus_status"}},
		{
			"doc_list",
			map[string]any{"kb_id": "kb_1", "start_time": "2026-06-01T00:00:00Z", "end_time": "2026-05-01T00:00:00Z"},
		},
	}
	for _, bc := range badCalls {
		res, err := cVal.CallTool(ctx, &mcpsdk.CallToolParams{Name: bc.tool, Arguments: bc.args})
		require.NoError(t, err)
		require.True(t, res.IsError, "tool %s with args %v must return IsError=true", bc.tool, bc.args)
	}
}
