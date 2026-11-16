package compat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/cli/internal/compat"
	sdk "github.com/Usir233/GENYEE/client"
)

type nilSystemInfoProbe struct{}

func (nilSystemInfoProbe) GetSystemInfo(context.Context) (*sdk.SystemInfo, error) {
	return nil, nil
}

func TestW8_Probe_NilResponseDoesNotPanic(t *testing.T) {
	_, err := compat.Probe(context.Background(), nilSystemInfoProbe{})
	if err == nil {
		t.Fatal("expected error when GetSystemInfo returns (nil, nil)")
	}
}

func TestW8_LoadCache_FutureProbedAtIsNotFresh(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	future := time.Now().Add(365 * 24 * time.Hour)
	if err := compat.SaveCache(&compat.Info{
		ServerVersion: "1.0.0",
		ProbedAt:      future,
	}); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	info, fresh, err := compat.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if fresh {
		t.Fatal("future-dated ProbedAt must not be treated as fresh")
	}
}

func TestW8_Compat_RejectsNegativeSemverAndSanitizesHint(t *testing.T) {
	if level, _ := compat.Compat("-1.2.0", "1.2.0"); level != compat.OK {
		t.Fatalf("negative major semver should fail-open as unparseable OK, got %v", level)
	}
	if level, _ := compat.Compat("1.-2.0", "1.2.0"); level != compat.OK {
		t.Fatalf("negative minor semver should fail-open as unparseable OK, got %v", level)
	}
	level, hint := compat.Compat("1.0.0-\x1b]52;c;SGVsbG8=\x07pwn", "2.0.0")
	if level != compat.HardError {
		t.Fatalf("expected HardError, got %v", level)
	}
	if strings.Contains(hint, "\x1b") || strings.Contains(hint, "\x07") {
		t.Fatalf("Compat hint leaked raw terminal escape sequence: %q", hint)
	}
}
