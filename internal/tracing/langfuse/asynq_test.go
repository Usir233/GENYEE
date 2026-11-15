package langfuse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/hibiken/asynq"
)

// dummyPayload is a minimal payload that embeds TracingContext, mirroring
// how real asynq payloads opt into trace propagation.
type dummyPayload struct {
	types.TracingContext
	KnowledgeID string `json:"knowledge_id"`
}

// TestInjectTracing_DisabledIsZero verifies InjectTracing is a no-op when
// Langfuse is disabled: no panics, no trace fields written.
func TestInjectTracing_DisabledIsZero(t *testing.T) {
	_, _ = Init(Config{Enabled: false})

	p := &dummyPayload{KnowledgeID: "k1"}
	InjectTracing(context.Background(), p)
	if p.LangfuseTraceparent != "" || p.LangfuseTraceID != "" {
		t.Fatalf("expected no tracing fields on disabled manager, got %+v", p.TracingContext)
	}
}

// TestInjectTracing_PopulatesTraceparent checks that when a trace is active
// on the context, a W3C traceparent is stamped onto the payload (so the
// asynq worker can resume the same trace).
func TestInjectTracing_PopulatesTraceparent(t *testing.T) {
	m, _ := newTestManager(t)

	ctx, trace := m.StartTrace(context.Background(), TraceOptions{Name: "parent"})
	p := &dummyPayload{KnowledgeID: "k1"}
	InjectTracing(ctx, p)

	if p.LangfuseTraceparent == "" {
		t.Fatal("expected LangfuseTraceparent to be populated")
	}
	// The traceparent carries the trace id; trace.ID is the OTel trace id.
	if !strings.HasPrefix(p.LangfuseTraceparent, "00-"+trace.ID) {
		t.Errorf("traceparent %q does not carry trace id %s", p.LangfuseTraceparent, trace.ID)
	}
	if p.LangfuseTraceID != trace.ID {
		t.Errorf("LangfuseTraceID = %q, want %q", p.LangfuseTraceID, trace.ID)
	}
}

// TestAsynqMiddleware_TraceparentPropagation is the cross-process correlation
// core test: InjectTracing stamps a traceparent onto the payload; the worker
// middleware re-extracts it, and the worker span inherits the upstream trace
// id — stitching the HTTP trace and the async job into one LiteFuse tree.
func TestAsynqMiddleware_TraceparentPropagation(t *testing.T) {
	m, exp := newTestManager(t)

	// Upstream caller opens a span and injects a traceparent onto the payload.
	upstreamCtx, upstreamSpan := m.Tracer().Start(context.Background(), "upstream-http")
	remoteTraceID := upstreamSpan.SpanContext().TraceID()
	payload := &dummyPayload{KnowledgeID: "k1"}
	InjectTracing(upstreamCtx, payload)
	if payload.LangfuseTraceparent == "" {
		t.Fatal("InjectTracing did not stamp a traceparent")
	}
	raw, _ := json.Marshal(payload)

	mw := AsynqMiddleware()(asynq.HandlerFunc(func(context.Context, *asynq.Task) error { return nil }))
	if err := mw.ProcessTask(context.Background(), asynq.NewTask("test:type", raw)); err != nil {
		t.Fatalf("handler err: %v", err)
	}

	for _, s := range exp.GetSpans() {
		if s.Name != "asynq.test:type" {
			continue
		}
		if s.SpanContext.TraceID() != remoteTraceID {
			t.Errorf("worker span trace id = %s, want upstream %s (traceparent not propagated)",
				s.SpanContext.TraceID(), remoteTraceID)
		}
		return
	}
	t.Fatal("asynq worker span not exported")
}

// TestAsynqMiddleware_StandaloneTrace asserts that when the payload carries
// NO upstream traceparent (e.g. a scheduled job), the middleware opens a
// standalone trace named after the task type.
func TestAsynqMiddleware_StandaloneTrace(t *testing.T) {
	_, exp := newTestManager(t)

	payload := &dummyPayload{KnowledgeID: "kX"}
	raw, _ := json.Marshal(payload)

	mw := AsynqMiddleware()(asynq.HandlerFunc(func(context.Context, *asynq.Task) error { return nil }))
	if err := mw.ProcessTask(context.Background(), asynq.NewTask("scheduled:ping", raw)); err != nil {
		t.Fatalf("handler err: %v", err)
	}

	// The standalone run opens a root trace span ("asynq.scheduled:ping",
	// type=trace) plus a worker span with the same name (type=span).
	var sawRoot, sawSpan bool
	for _, s := range exp.GetSpans() {
		if s.Name != "asynq.scheduled:ping" {
			continue
		}
		switch spanType(s) {
		case obsTypeTrace:
			sawRoot = true
		case obsTypeSpan:
			sawSpan = true
		}
	}
	if !sawRoot {
		t.Error("standalone run should open a root trace span named asynq.scheduled:ping")
	}
	if !sawSpan {
		t.Error("standalone run should open a worker span")
	}
}

func TestAsynqSpanInputUTF8SafeAndRedactsSecrets(t *testing.T) {
	_, exp := newTestManager(t)

	// Construct a >1024-byte JSON payload whose 1024th byte lands inside a 3-byte CJK rune
	// and embeds sensitive credentials.
	prefix := `{"api_key":"sk-asynq-secret-1234567890","password":"myTopSecretPassword","text":"`
	// Pad ASCII so byte 1024 lands mid-rune inside "中文测试".
	padLen := (1023 - len(prefix))
	raw := []byte(prefix + strings.Repeat("a", padLen) + strings.Repeat("中文测试", 40) + `"}`)
	if len(raw) <= 1024 {
		t.Fatalf("expected payload > 1024 bytes, got %d", len(raw))
	}

	mw := AsynqMiddleware()(asynq.HandlerFunc(func(context.Context, *asynq.Task) error { return nil }))
	if err := mw.ProcessTask(context.Background(), asynq.NewTask("knowledge:ingest", raw)); err != nil {
		t.Fatalf("handler err: %v", err)
	}

	// Also verify direct spanInputFromPayload preview validity before JSON marshaling
	// (json.Marshal replaces invalid UTF-8 with U+FFFD, masking broken rune slicing unless checked directly).
	direct := spanInputFromPayload(raw)
	m, ok := direct.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map preview for >1024 payload, got %T", direct)
	}
	previewStr, _ := m["preview"].(string)
	if !utf8.ValidString(previewStr) || strings.ContainsRune(previewStr, utf8.RuneError) {
		t.Fatalf("spanInputFromPayload split multi-byte UTF-8 rune: %q", previewStr)
	}
	for _, secret := range []string{"sk-asynq-secret-1234567890", "myTopSecretPassword"} {
		if strings.Contains(previewStr, secret) {
			t.Fatalf("spanInputFromPayload leaked secret %q in preview: %q", secret, previewStr)
		}
	}

	// Also check short payload <=1024 bytes redacts secrets.
	short := []byte(`{"knowledge_id":"k1","secret_key":"super_secret_val"}`)
	shortOut, _ := spanInputFromPayload(short).(string)
	if strings.Contains(shortOut, "super_secret_val") {
		t.Fatalf("short spanInputFromPayload leaked secret_key: %q", shortOut)
	}

	var sawWorkerSpan bool
	for _, s := range exp.GetSpans() {
		if s.Name == "asynq.knowledge:ingest" && spanType(s) == obsTypeSpan {
			sawWorkerSpan = true
			inp := spanAttr(s.Attributes, attrObsInput)
			if strings.Contains(inp, "sk-asynq-secret-1234567890") || strings.Contains(inp, "myTopSecretPassword") {
				t.Fatalf("exported asynq span input leaked secret: %s", inp)
			}
		}
	}
	if !sawWorkerSpan {
		t.Fatal("asynq worker span not exported")
	}
}
