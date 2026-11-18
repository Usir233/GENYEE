package common

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParseLLMJsonResponse(t *testing.T) {
	type payload struct {
		Key string `json:"key"`
	}

	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{"direct json", `{"key":"value"}`, "value", false},
		{"fenced json", "```json\n{\"key\":\"fenced\"}\n```", "fenced", false},
		{"fenced no lang", "```\n{\"key\":\"plain\"}\n```", "plain", false},
		{"prose around fence", "Here you go:\n```json\n{\"key\":\"wrapped\"}\n```\nThanks", "wrapped", false},
		{"prose around bare json", `Sure: {"key":"bare"} hope that helps`, "bare", false},
		// Trailing bracket-like prose must not truncate the payload.
		{"trailing citation", `{"key":"cited"} — see section [1].`, "cited", false},
		// Braces inside a string literal must not close the object early.
		{"braces in string", `Result: {"key":"a}b"} done`, "a}b", false},
		{"not json", "just some text", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got payload
			err := ParseLLMJsonResponse(tt.content, &got)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.Key != tt.want {
				t.Errorf("Key = %q, want %q", got.Key, tt.want)
			}
		})
	}
}

func TestCleanInvalidUTF8(t *testing.T) {
	input := "valid " + string([]byte{0xef, 0xbc, 0x2e}) + "\x00tail"

	got := CleanInvalidUTF8(input)
	if !utf8.ValidString(got) {
		t.Fatalf("CleanInvalidUTF8 returned invalid UTF-8: % x", []byte(got))
	}
	if want := "valid .tail"; got != want {
		t.Fatalf("CleanInvalidUTF8() = %q, want %q", got, want)
	}
}

func BenchmarkParseLLMJsonResponse_Fenced(b *testing.B) {
	const content = "```json\n{\"name\":\"Acme\",\"slug\":\"entity/acme\",\"aliases\":[\"A\",\"B\"]}\n```"
	type payload struct {
		Name string `json:"name"`
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var p payload
		_ = ParseLLMJsonResponse(content, &p)
	}
}

type scoredTestItem struct {
	id    string
	score float64
}

func (s scoredTestItem) GetScore() float64 { return s.score }

func TestDeduplicateWithScoreNaNAndTypedNilPipelineLog(t *testing.T) {
	got := DeduplicateWithScore(func(it scoredTestItem) string { return it.id },
		scoredTestItem{id: "a", score: math.NaN()},
		scoredTestItem{id: "a", score: 0.9},
		scoredTestItem{id: "b", score: math.NaN()},
		scoredTestItem{id: "c", score: 0.5},
	)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got[0].id != "a" || got[0].score != 0.9 {
		t.Fatalf("expected finite 0.9 score to replace NaN and sort first, got %+v", got)
	}
	if got[1].id != "c" || got[1].score != 0.5 {
		t.Fatalf("expected second item to be c (0.5), got %+v", got)
	}
	if got[2].id != "b" || !math.IsNaN(got[2].score) {
		t.Fatalf("expected NaN item sorted last, got %+v", got)
	}

	var nilURL *url.URL
	logLine := PipelineLog("FETCH", "check", map[string]interface{}{"url": nilURL})
	if logLine == "" {
		t.Fatal("expected non-empty PipelineLog output for typed-nil Stringer")
	}
}

func TestPipelineLogMasksSensitiveFieldsAndTruncatesRawMessage(t *testing.T) {
	line := PipelineLog("AUTH", "verify", map[string]interface{}{
		"api_key": "sk-pipeline-secret-value-123456",
		"dsn":     "postgres://admin:PipeDBPass99@pg.internal:5432/app",
		"raw":     json.RawMessage(`"` + strings.Repeat("r", 600) + `"`),
		"safe":    "visible-value",
	})
	for _, secret := range []string{"sk-pipeline-secret-value-123456", "PipeDBPass99"} {
		if strings.Contains(line, secret) {
			t.Fatalf("PipelineLog leaked secret %q in %q", secret, line)
		}
	}
	if !strings.Contains(line, "visible-value") {
		t.Fatalf("PipelineLog dropped safe field: %q", line)
	}
	if len(line) > 550 {
		t.Fatalf("PipelineLog failed to truncate oversized json.RawMessage (len=%d): %q", len(line), line)
	}
}

func TestWithDeadlockRetryHonorsContextCancellationAndNilCallback(t *testing.T) {
	if err := WithDeadlockRetry(context.Background(), nil); err != nil {
		t.Fatalf("expected nil callback to return nil, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()

	calls := 0
	err := WithDeadlockRetry(ctx, func() error {
		calls++
		return errors.New("Error 1213: Deadlock found when trying to get lock")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded during retry wait, got %v (calls=%d)", err, calls)
	}
}
