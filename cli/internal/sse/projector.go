package sse

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/cli/internal/format"
	"github.com/Usir233/GENYEE/cli/internal/text"
	sdk "github.com/Usir233/GENYEE/client"
)

// ProjectedEvent is the bounded event representation shared by
// buffered JSON, human-readable text, and MCP tool results. It deliberately
// mirrors the SDK field names so consumers can move between projected JSON and
// raw NDJSON without learning a second event vocabulary.
type ProjectedEvent struct {
	ID                  string                  `json:"id,omitempty"`
	ResponseType        string                  `json:"response_type"`
	Content             string                  `json:"content,omitempty"`
	Done                bool                    `json:"done"`
	KnowledgeReferences []format.ReferenceIndex `json:"knowledge_references,omitempty"`
	SessionID           string                  `json:"session_id,omitempty"`
	AssistantMessageID  string                  `json:"assistant_message_id,omitempty"`
	ToolCalls           []sdk.LLMToolCall       `json:"tool_calls,omitempty"`
	Data                map[string]any          `json:"data,omitempty"`
}

// Projector observes every SDK frame for lifecycle state, then returns the
// presentation event selected by the output contract. Default output keeps
// answer frames only. Reference enables bounded citation events; verbose
// enables execution and lifecycle events.
type Projector struct {
	verbose            bool
	reference          bool
	fallbackKBID       string
	seen               bool
	done               bool
	hasTermErr         bool
	termErr            string
	sessionID          string
	assistantMessageID string
}

func NewProjector(verbose, reference bool, fallbackKBID string) *Projector {
	return &Projector{verbose: verbose, reference: reference, fallbackKBID: fallbackKBID}
}

func (p *Projector) Seen() bool                 { return p.seen }
func (p *Projector) Done() bool                 { return p.done }
func (p *Projector) SessionID() string          { return p.sessionID }
func (p *Projector) AssistantMessageID() string { return p.assistantMessageID }

// TerminalError reports whether the stream terminated via an error event
// (response_type="error" with done=true) and returns its message.
func (p *Projector) TerminalError() (string, bool) { return p.termErr, p.hasTermErr }

// Chat consumes one KnowledgeQAStream frame.
func (p *Projector) Chat(r *sdk.StreamResponse) (ProjectedEvent, bool) {
	if r == nil || p.done {
		return ProjectedEvent{}, false
	}
	p.seen = true
	if p.sessionID == "" && r.SessionID != "" {
		p.sessionID = r.SessionID
	}
	if p.assistantMessageID == "" && r.AssistantMessageID != "" {
		p.assistantMessageID = r.AssistantMessageID
	}
	if r.ResponseType == sdk.ResponseTypeComplete {
		p.done = true
	}
	if r.ResponseType == sdk.ResponseTypeError && r.Done {
		p.done = true
		p.hasTermErr = true
		p.termErr = strings.TrimSpace(r.Content)
		if p.termErr == "" {
			p.termErr = "stream error"
		}
	}

	responseType := string(r.ResponseType)
	isAnswer := r.ResponseType == sdk.ResponseTypeAnswer || (r.ResponseType == "" && r.Content != "")
	isReference := r.ResponseType == sdk.ResponseTypeReferences
	include := isAnswer || (p.verbose && !isReference) || (p.reference && isReference)
	if !include && p.reference && len(r.KnowledgeReferences) > 0 {
		return p.chatReferenceEvent(r), true
	}
	if !include {
		return ProjectedEvent{}, false
	}
	if responseType == "" {
		responseType = string(sdk.ResponseTypeAnswer)
	}
	event := ProjectedEvent{
		ID:                 r.ID,
		ResponseType:       responseType,
		Content:            r.Content,
		Done:               r.Done,
		SessionID:          r.SessionID,
		AssistantMessageID: r.AssistantMessageID,
		ToolCalls:          r.ToolCalls,
		Data:               r.Data,
	}
	if p.reference {
		event.KnowledgeReferences = format.IndexReferences(r.KnowledgeReferences, p.fallbackKBID)
	}
	return event, true
}

// Agent consumes one AgentQAStream frame.
func (p *Projector) Agent(r *sdk.AgentStreamResponse) (ProjectedEvent, bool) {
	if r == nil || p.done {
		return ProjectedEvent{}, false
	}
	p.seen = true
	if r.ResponseType == sdk.AgentResponseTypeComplete {
		p.done = true
	}
	if r.ResponseType == sdk.AgentResponseTypeError && r.Done {
		p.done = true
		p.hasTermErr = true
		p.termErr = strings.TrimSpace(r.Content)
		if p.termErr == "" {
			p.termErr = "agent stream error"
		}
	}

	isAnswer := r.ResponseType == sdk.AgentResponseTypeAnswer
	isReference := r.ResponseType == sdk.AgentResponseTypeReferences
	include := isAnswer || (p.verbose && !isReference) || (p.reference && isReference)
	if !include && p.reference && len(r.KnowledgeReferences) > 0 {
		return p.agentReferenceEvent(r), true
	}
	if !include {
		return ProjectedEvent{}, false
	}
	event := ProjectedEvent{
		ID:           r.ID,
		ResponseType: string(r.ResponseType),
		Content:      r.Content,
		Done:         r.Done,
		Data:         r.Data,
	}
	if p.reference {
		event.KnowledgeReferences = format.IndexReferences(r.KnowledgeReferences, p.fallbackKBID)
	}
	return event, true
}

func (p *Projector) chatReferenceEvent(r *sdk.StreamResponse) ProjectedEvent {
	return ProjectedEvent{
		ID:                  r.ID,
		ResponseType:        string(sdk.ResponseTypeReferences),
		Done:                r.Done,
		KnowledgeReferences: format.IndexReferences(r.KnowledgeReferences, p.fallbackKBID),
		SessionID:           r.SessionID,
		AssistantMessageID:  r.AssistantMessageID,
	}
}

func (p *Projector) agentReferenceEvent(r *sdk.AgentStreamResponse) ProjectedEvent {
	return ProjectedEvent{
		ID:                  r.ID,
		ResponseType:        string(sdk.AgentResponseTypeReferences),
		Done:                r.Done,
		KnowledgeReferences: format.IndexReferences(r.KnowledgeReferences, p.fallbackKBID),
	}
}

type ansiStreamState uint8

const (
	ansiStateNormal ansiStreamState = iota
	ansiStateEsc
	ansiStateNF
	ansiStateCSI
	ansiStateControlStr
	ansiStateControlStrEsc
)

const (
	maxCSISeqBytes     = 256
	maxControlStrBytes = 8192
)

// TextRenderer writes projected events as a human-readable stream while
// stripping ANSI/OSC/DCS/C0/C1/Bidi terminal escape sequences across chunk
// boundaries so untrusted SSE frames cannot hijack terminals or clipboards.
type TextRenderer struct {
	w         io.Writer
	verbose   bool
	lastKey   string
	atBOL     bool
	state     ansiStreamState
	seqBytes  int
	pendingCR bool
	utf8Buf   [utf8.UTFMax]byte
	utf8Len   int
}

func NewTextRenderer(w io.Writer, verbose bool) *TextRenderer {
	return &TextRenderer{w: w, verbose: verbose, atBOL: true}
}

func (r *TextRenderer) Write(ev ProjectedEvent) error {
	switch ev.ResponseType {
	case string(sdk.ResponseTypeAnswer):
		return r.writeContentBlock(ev, "answer")
	case string(sdk.ResponseTypeThinking), string(sdk.ResponseTypeReflection):
		return r.writeContentBlock(ev, text.SanitizeInline(ev.ResponseType))
	case string(sdk.ResponseTypeReferences):
		return r.writeReferences(ev)
	default:
		return r.writeStructured(ev)
	}
}

func (r *TextRenderer) Close() error {
	if err := r.flushStreamState(); err != nil {
		return err
	}
	if !r.atBOL {
		return r.writeRaw("\n")
	}
	return nil
}

func (r *TextRenderer) writeContentBlock(ev ProjectedEvent, label string) error {
	key := ev.ResponseType + "\x00" + ev.ID
	if key != r.lastKey {
		if err := r.flushStreamState(); err != nil {
			return err
		}
		if err := r.ensureBOL(); err != nil {
			return err
		}
		if r.verbose {
			if err := r.writeRaw("[" + text.SanitizeInline(label) + "]\n"); err != nil {
				return err
			}
		}
		r.lastKey = key
	}
	if ev.Content != "" {
		if err := r.writeSanitizedChunk(ev.Content); err != nil {
			return err
		}
	}
	if ev.Done {
		if err := r.flushStreamState(); err != nil {
			return err
		}
		r.lastKey = ""
		return r.ensureBOL()
	}
	return nil
}

func (r *TextRenderer) writeReferences(ev ProjectedEvent) error {
	if err := r.flushStreamState(); err != nil {
		return err
	}
	if err := r.ensureBOL(); err != nil {
		return err
	}
	if err := r.writeRaw("[references]\n"); err != nil {
		return err
	}
	for _, ref := range ev.KnowledgeReferences {
		line := "- chunk_id=" + text.SanitizeInline(ref.ChunkID)
		if ref.KBID != "" {
			line += " kb_id=" + text.SanitizeInline(ref.KBID)
		}
		if ref.ParentChunkID != "" {
			line += " parent_chunk_id=" + text.SanitizeInline(ref.ParentChunkID)
		}
		if err := r.writeRaw(line + "\n"); err != nil {
			return err
		}
	}
	r.lastKey = ""
	return nil
}

func (r *TextRenderer) writeStructured(ev ProjectedEvent) error {
	if err := r.flushStreamState(); err != nil {
		return err
	}
	if err := r.ensureBOL(); err != nil {
		return err
	}
	header := "[" + text.SanitizeInline(ev.ResponseType)
	if ev.ID != "" {
		header += " " + text.SanitizeInline(ev.ID)
	}
	header += "]"
	if err := r.writeRaw(header); err != nil {
		return err
	}
	if ev.Content != "" {
		if err := r.writeRaw(" " + text.SanitizeInline(ev.Content)); err != nil {
			return err
		}
	}
	if len(ev.ToolCalls) > 0 {
		b, _ := json.Marshal(ev.ToolCalls)
		if err := r.writeRaw(" " + text.SanitizeInline(string(b))); err != nil {
			return err
		}
	}
	if len(ev.Data) > 0 {
		b, _ := json.Marshal(ev.Data)
		if err := r.writeRaw(" " + text.SanitizeInline(string(b))); err != nil {
			return err
		}
	}
	if err := r.writeRaw("\n"); err != nil {
		return err
	}
	r.lastKey = ""
	return nil
}

func (r *TextRenderer) ensureBOL() error {
	if r.atBOL {
		return nil
	}
	return r.writeRaw("\n")
}

func (r *TextRenderer) flushStreamState() error {
	var out strings.Builder
	if r.pendingCR {
		out.WriteByte(' ')
		r.pendingCR = false
	}
	if r.utf8Len > 0 {
		out.WriteRune('\uFFFD')
		r.utf8Len = 0
	}
	r.state = ansiStateNormal
	r.seqBytes = 0
	return r.writeRaw(out.String())
}

func (r *TextRenderer) writeSanitizedChunk(chunk string) error {
	if chunk == "" {
		return nil
	}
	var buf []byte
	if r.utf8Len > 0 {
		buf = make([]byte, 0, r.utf8Len+len(chunk))
		buf = append(buf, r.utf8Buf[:r.utf8Len]...)
		buf = append(buf, chunk...)
		r.utf8Len = 0
	} else {
		buf = []byte(chunk)
	}

	var out strings.Builder
	out.Grow(len(buf))
	for len(buf) > 0 {
		if !utf8.FullRune(buf) {
			copy(r.utf8Buf[:], buf)
			r.utf8Len = len(buf)
			break
		}
		rn, width := utf8.DecodeRune(buf)
		if rn == utf8.RuneError && width == 1 {
			rn = '\uFFFD'
		}
		buf = buf[width:]
		r.consumeRune(&out, rn)
	}
	return r.writeRaw(out.String())
}

func (r *TextRenderer) consumeRune(out *strings.Builder, rn rune) {
	switch r.state {
	case ansiStateEsc:
		r.seqBytes = 0
		switch {
		case rn == '[':
			r.state = ansiStateCSI
		case rn == ']' || rn == 'P' || rn == 'X' || rn == '^' || rn == '_':
			r.state = ansiStateControlStr
		case rn >= 0x20 && rn <= 0x2f:
			r.state = ansiStateNF
		case rn >= 0x30 && rn <= 0x7e:
			r.state = ansiStateNormal
		default:
			r.state = ansiStateNormal
		}
		return
	case ansiStateNF:
		r.seqBytes++
		if rn >= 0x20 && rn <= 0x2f && r.seqBytes < maxCSISeqBytes {
			return
		}
		r.state = ansiStateNormal
		r.seqBytes = 0
		return
	case ansiStateCSI:
		r.seqBytes++
		if rn >= 0x20 && rn <= 0x3f && r.seqBytes < maxCSISeqBytes {
			return
		}
		r.state = ansiStateNormal
		r.seqBytes = 0
		return
	case ansiStateControlStr:
		if rn == 0x07 || rn == 0x9c {
			r.state = ansiStateNormal
			r.seqBytes = 0
			return
		}
		if rn == 0x1b {
			r.state = ansiStateControlStrEsc
			return
		}
		r.seqBytes++
		if r.seqBytes >= maxControlStrBytes {
			r.state = ansiStateNormal
			r.seqBytes = 0
		}
		return
	case ansiStateControlStrEsc:
		if rn == '\\' {
			r.state = ansiStateNormal
			r.seqBytes = 0
			return
		}
		r.state = ansiStateEsc
		r.consumeRune(out, rn)
		return
	}

	if rn == 0x1b {
		if r.pendingCR {
			out.WriteByte(' ')
			r.pendingCR = false
		}
		r.state = ansiStateEsc
		r.seqBytes = 0
		return
	}
	if rn == 0x9b {
		if r.pendingCR {
			out.WriteByte(' ')
			r.pendingCR = false
		}
		r.state = ansiStateCSI
		r.seqBytes = 0
		return
	}
	if rn == 0x90 || rn == 0x98 || rn == 0x9d || rn == 0x9e || rn == 0x9f {
		if r.pendingCR {
			out.WriteByte(' ')
			r.pendingCR = false
		}
		r.state = ansiStateControlStr
		r.seqBytes = 0
		return
	}
	if rn == '\r' {
		if r.pendingCR {
			out.WriteByte(' ')
		}
		r.pendingCR = true
		return
	}
	if rn == '\n' {
		r.pendingCR = false
		out.WriteByte('\n')
		return
	}
	if r.pendingCR {
		out.WriteByte(' ')
		r.pendingCR = false
	}
	if rn == '\t' {
		out.WriteByte('\t')
		return
	}
	if rn < 0x20 || rn == 0x7f || (rn >= 0x80 && rn <= 0x9f) || (rn >= 0x202a && rn <= 0x202e) || (rn >= 0x2066 && rn <= 0x2069) {
		return
	}
	out.WriteRune(rn)
}

func (r *TextRenderer) writeRaw(s string) error {
	if s == "" {
		return nil
	}
	if _, err := fmt.Fprint(r.w, s); err != nil {
		return err
	}
	r.atBOL = strings.HasSuffix(s, "\n")
	return nil
}
