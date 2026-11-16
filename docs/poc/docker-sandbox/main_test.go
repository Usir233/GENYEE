package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func TestW7POC01_RunExecutesDefersInsteadOfExitingProcess(t *testing.T) {
	cleaned := false
	code := runWithSteps(func() (int, func()) {
		return 2, func() { cleaned = true }
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 when failures > 0, got %d", code)
	}
	if !cleaned {
		t.Fatal("deferred container cleanup did not run before returning non-zero exit code")
	}
}

func TestW7POC01_CreateSandboxRemovesContainerOnStartFailure(t *testing.T) {
	var removedID string
	var removedForce bool

	id, err := createAndStartContainer(
		context.Background(),
		func(context.Context) (string, error) {
			return "cid-partial-1234567890", nil
		},
		func(context.Context, string) error {
			return errors.New("oci runtime start failed")
		},
		func(_ context.Context, cid string, force bool) {
			removedID = cid
			removedForce = force
		},
	)
	if err == nil {
		t.Fatal("expected error when ContainerStart fails")
	}
	if id != "" {
		t.Fatalf("expected empty container ID after rollback, got %q", id)
	}
	if removedID != "cid-partial-1234567890" || !removedForce {
		t.Fatalf("expected forced removal of partial container, got id=%q force=%v", removedID, removedForce)
	}
}

func TestW7POC01_InspectHelpersNilSafeOnError(t *testing.T) {
	var zero client.ContainerInspectResult
	if got := inspectStatus(zero); got != "" {
		t.Fatalf("inspectStatus on nil container = %q, want empty", got)
	}
	if got := inspectLabel(zero, "genyee.session"); got != "" {
		t.Fatalf("inspectLabel on nil container = %q, want empty", got)
	}
	if got := inspectStartedAt(zero); got != "" {
		t.Fatalf("inspectStartedAt on nil container = %q, want empty", got)
	}

	populated := client.ContainerInspectResult{
		Container: container.InspectResponse{
			State:  &container.State{Status: "paused", StartedAt: "2026-10-29T00:00:00Z"},
			Config: &container.Config{Labels: map[string]string{"genyee.session": "s-1"}},
		},
	}
	if got := inspectStatus(populated); got != "paused" {
		t.Fatalf("inspectStatus = %q, want paused", got)
	}
	if got := inspectLabel(populated, "genyee.session"); got != "s-1" {
		t.Fatalf("inspectLabel = %q, want s-1", got)
	}
	if got := inspectStartedAt(populated); got != "2026-10-29T00:00:00Z" {
		t.Fatalf("inspectStartedAt = %q", got)
	}
}

func TestW7POC01_ArchiveDestValidationAndBoundedTarRead(t *testing.T) {
	for _, invalid := range []string{"", "relative/file.txt", "/", "/workspace/dir/", "/workspace/../etc/passwd"} {
		if _, _, err := cleanArchiveDest(invalid); err == nil {
			t.Errorf("cleanArchiveDest(%q) succeeded, want error", invalid)
		}
	}
	dir, base, err := cleanArchiveDest("/workspace/input/nested/attachment.txt")
	if err != nil {
		t.Fatalf("cleanArchiveDest valid path error: %v", err)
	}
	if dir != "/workspace/input/nested" || base != "attachment.txt" {
		t.Fatalf("cleanArchiveDest = (%q, %q), want (/workspace/input/nested, attachment.txt)", dir, base)
	}

	makeTar := func(payload string) *bytes.Buffer {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := tw.WriteHeader(&tar.Header{
			Name: "payload.txt", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		return &buf
	}

	got, err := readTarRegBounded(makeTar("hello\n"), 64)
	if err != nil || string(got) != "hello\n" {
		t.Fatalf("readTarRegBounded small = (%q, %v)", got, err)
	}

	if _, err := readTarRegBounded(makeTar(strings.Repeat("x", 128)), 32); err == nil {
		t.Fatal("readTarRegBounded accepted archive entry exceeding maxBytes")
	}
}
