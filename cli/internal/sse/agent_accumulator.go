package sse

import (
	"strings"

	sdk "github.com/Usir233/GENYEE/client"
)

// AgentToolEvent captures one tool_call / tool_result event from an
// agent SSE stream. Kind is the SDK event type (typed, not bare string)
// so consumers can compare against sdk.AgentResponseTypeToolCall /
// sdk.AgentResponseTypeToolResult without retyping the constants.
// NOTE: Kind is the event kind, NOT the function name; the function
// name typically lives in Data for tool_call events.
type AgentToolEvent struct {
	ID     string                `json:"id"`
	Kind   sdk.AgentResponseType `json:"kind,omitempty"`
	Result string                `json:"result,omitempty"`
	Data   map[string]any        `json:"data,omitempty"`
}

// AgentAccumulator buffers an AgentQAStream callback sequence. Distinct
// from Accumulator (KnowledgeQAStream) because the agent event model is
// wider - events include thinking / reflection / tool_call / tool_result
// / answer / references / error / complete. Done is scoped to an individual
// event stream (thinking, reflection, answer, etc.); only response_type=complete
// terminates the whole agent stream.
//
// Zero value is ready to use. Not safe for concurrent Append calls - the
// SDK callback contract is sequential on a single goroutine.
//
// API mirrors sse.Accumulator: private builders + accessor methods so the
// "Append is idempotent post-Done" invariant cannot be broken by external
// mutation. References and ToolEvents stay exported as plain slices since
// they carry no such invariant.
type AgentAccumulator struct {
	answer     strings.Builder
	thinking   strings.Builder
	References []*sdk.SearchResult
	ToolEvents []AgentToolEvent
	done       bool
}

// Answer returns the accumulated `answer`-event content.
func (a *AgentAccumulator) Answer() string { return a.answer.String() }

// Thinking returns the accumulated `thinking` / `reflection` content
// surfaced by the agent during its reasoning pass.
func (a *AgentAccumulator) Thinking() string { return a.thinking.String() }

// Done reports whether the stream emitted a terminal frame.
func (a *AgentAccumulator) Done() bool { return a.done }

// MaxToolEvents caps the number of tool_call / tool_result events retained in
// memory by AgentAccumulator during a single stream.
const MaxToolEvents = 4096

// Append consumes one AgentStreamResponse event. Idempotent post-Done so
// callers do not need to special-case late events.
func (a *AgentAccumulator) Append(r *sdk.AgentStreamResponse) {
	if r == nil || a.done {
		return
	}
	switch r.ResponseType {
	case sdk.AgentResponseTypeAnswer:
		appendBoundedUTF8(&a.answer, r.Content, MaxAccumulatedBytes)
	case sdk.AgentResponseTypeThinking, sdk.AgentResponseTypeReflection:
		appendBoundedUTF8(&a.thinking, r.Content, MaxAccumulatedBytes)
	case sdk.AgentResponseTypeToolCall, sdk.AgentResponseTypeToolResult:
		if len(a.ToolEvents) < MaxToolEvents {
			res := r.Content
			if len(res) > 1<<20 {
				res = res[:1<<20]
			}
			a.ToolEvents = append(a.ToolEvents, AgentToolEvent{
				ID:     r.ID,
				Kind:   r.ResponseType,
				Result: res,
				Data:   r.Data,
			})
		}
	}
	// References can arrive on a dedicated `references` event OR
	// piggyback on another event's KnowledgeReferences field; always
	// capture the latest, matching sse.Accumulator's "always replace"
	// semantic for the parallel KnowledgeQAStream contract.
	if r.KnowledgeReferences != nil {
		a.References = r.KnowledgeReferences
	}
	// Thinking / reflection / answer streams each emit their own Done=true
	// marker before later events arrive. Only the explicit complete frame is
	// terminal for the whole agent run.
	if r.ResponseType == sdk.AgentResponseTypeComplete {
		a.done = true
	}
}
