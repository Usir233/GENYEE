package text_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/cli/internal/text"
)

func TestW8_SanitizeAndTruncate_StripOSC52CSIC1AndBidi(t *testing.T) {
	malicious := "alpha\x1b]52;c;SGVsbG8=\x07\x1b[31m\u009b2J\u202ebeta\rspoof"
	got := text.SanitizeTerminal(malicious)
	if strings.ContainsAny(got, "\x1b\x07\r\u009b\u202e") {
		t.Fatalf("SanitizeTerminal leaked terminal controls: %q", got)
	}
	if strings.Contains(got, "SGVsbG8=") {
		t.Fatalf("SanitizeTerminal leaked OSC 52 clipboard payload: %q", got)
	}
	if got != "alphabeta spoof" {
		t.Fatalf("SanitizeTerminal = %q, want %q", got, "alphabeta spoof")
	}

	trunc := text.Truncate(6, "\x1b]0;evil-title\x07abcdefg")
	if trunc != "abcde…" {
		t.Fatalf("Truncate with OSC title spoof = %q, want %q", trunc, "abcde…")
	}

	one := text.OneLine(20, "row\x1b[2K\nnext")
	if one != "row next" {
		t.Fatalf("OneLine = %q, want %q", one, "row next")
	}

	name := text.KnowledgeDisplayName("\x1b]52;c;YWJj\x07", "clean\x1b[32m-title", "id1")
	if name != "clean-title" {
		t.Fatalf("KnowledgeDisplayName = %q, want %q", name, "clean-title")
	}

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	ago := text.FuzzyAgoStr(now, "bad-date\x1b]0;spoof\x07")
	if strings.Contains(ago, "\x1b") || ago != "bad-date" {
		t.Fatalf("FuzzyAgoStr = %q, want %q", ago, "bad-date")
	}
}

func TestW8_FuzzyAgo_FarFutureAndFarPastDoNotOverflow(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	farFuture := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	gotFuture := text.FuzzyAgo(now, farFuture)
	if strings.Contains(gotFuture, "less than a minute") || !strings.Contains(gotFuture, "years from now") {
		t.Fatalf("FuzzyAgo far future overflowed: %q", gotFuture)
	}

	farPast := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	gotPast := text.FuzzyAgo(now, farPast)
	if strings.Contains(gotPast, "292 years") || !strings.Contains(gotPast, "years ago") {
		t.Fatalf("FuzzyAgo far past clamped at 292 years: %q", gotPast)
	}
}
