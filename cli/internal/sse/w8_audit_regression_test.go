package sse_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/cli/internal/format"
	"github.com/Usir233/GENYEE/cli/internal/sse"
	sdk "github.com/Usir233/GENYEE/client"
)

func TestW8_TextRenderer_StripsSplitOSC52AndControlSequences(t *testing.T) {
	var buf bytes.Buffer
	r := sse.NewTextRenderer(&buf, true)

	// Split an OSC 52 clipboard hijack sequence across two streaming chunks.
	if err := r.Write(sse.ProjectedEvent{
		ID:           "a1",
		ResponseType: string(sdk.ResponseTypeAnswer),
		Content:      "Hello \x1b]52;c;",
	}); err != nil {
		t.Fatalf("Write chunk 1: %v", err)
	}
	if err := r.Write(sse.ProjectedEvent{
		ID:           "a1",
		ResponseType: string(sdk.ResponseTypeAnswer),
		Content:      "SGVsbG8=\x07world\rspoof",
		Done:         true,
	}); err != nil {
		t.Fatalf("Write chunk 2: %v", err)
	}
	if err := r.Write(sse.ProjectedEvent{
		ResponseType: string(sdk.ResponseTypeReferences),
		KnowledgeReferences: []format.ReferenceIndex{{
			ChunkID: "chk\x1b[31m_1",
			KBID:    "kb\x1b]0;title\x07_1",
		}},
	}); err != nil {
		t.Fatalf("Write references: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := buf.String()
	if strings.ContainsAny(out, "\x1b\x07\r") || strings.Contains(out, "SGVsbG8=") {
		t.Fatalf("TextRenderer leaked terminal escape/OSC52 payload: %q", out)
	}
	if !strings.Contains(out, "Hello world spoof") {
		t.Fatalf("TextRenderer mangled clean text: %q", out)
	}
	if !strings.Contains(out, "chunk_id=chk_1 kb_id=kb_1") {
		t.Fatalf("TextRenderer references not sanitized cleanly: %q", out)
	}
}

func TestW8_Projector_RecordsTerminalErrorAndAccumulatorsCapGrowth(t *testing.T) {
	p := sse.NewProjector(false, false, "kb1")
	p.Chat(&sdk.StreamResponse{ResponseType: sdk.ResponseTypeError, Content: "upstream boom", Done: true})
	msg, isErr := p.TerminalError()
	if !isErr || msg != "upstream boom" {
		t.Fatalf("TerminalError() = (%q, %v), want (\"upstream boom\", true)", msg, isErr)
	}

	var acc sse.Accumulator
	chunk := strings.Repeat("a", 1<<20)
	for range 20 {
		acc.Append(&sdk.StreamResponse{ResponseType: sdk.ResponseTypeAnswer, Content: chunk})
	}
	if len(acc.Result()) > sse.MaxAccumulatedBytes {
		t.Fatalf("Accumulator grew past cap: got %d > %d", len(acc.Result()), sse.MaxAccumulatedBytes)
	}

	var agentAcc sse.AgentAccumulator
	for range sse.MaxToolEvents + 100 {
		agentAcc.Append(&sdk.AgentStreamResponse{ResponseType: sdk.AgentResponseTypeToolCall, ID: "t"})
	}
	if len(agentAcc.ToolEvents) > sse.MaxToolEvents {
		t.Fatalf("AgentAccumulator ToolEvents grew past cap: got %d > %d", len(agentAcc.ToolEvents), sse.MaxToolEvents)
	}
}
