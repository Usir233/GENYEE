package format_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/cli/internal/format"
)

func TestW8_DashIfEmpty_SanitizesTerminalEscapes(t *testing.T) {
	got := format.DashIfEmpty("alice\x1b]52;c;SGVsbG8=\x07@example.com")
	if got != "alice@example.com" {
		t.Fatalf("DashIfEmpty = %q, want %q", got, "alice@example.com")
	}
	if onlyEsc := format.DashIfEmpty("\x1b[31m"); onlyEsc != "-" {
		t.Fatalf("DashIfEmpty(only-escape) = %q, want '-'", onlyEsc)
	}
}

func TestW8_WriteJSONFiltered_DisablesHTMLEscapingInFilterAndJQ(t *testing.T) {
	var buf bytes.Buffer
	if err := format.WriteJSONFiltered(
		&buf,
		map[string]any{"expr": "a < b & c > d"},
		[]string{"expr"},
		"",
	); err != nil {
		t.Fatalf("WriteJSONFiltered fields: %v", err)
	}
	if strings.Contains(buf.String(), `\u003c`) || !strings.HasSuffix(buf.String(), "\n") {
		t.Fatalf("WriteJSONFiltered fields escaped HTML or missed newline: %q", buf.String())
	}

	buf.Reset()
	if err := format.WriteJSONFiltered(
		&buf,
		map[string]any{"obj": map[string]string{"expr": "a < b & c > d"}},
		nil,
		".obj",
	); err != nil {
		t.Fatalf("WriteJSONFiltered jq: %v", err)
	}
	if strings.Contains(buf.String(), `\u003c`) {
		t.Fatalf("WriteJSONFiltered jq escaped HTML: %q", buf.String())
	}
}
