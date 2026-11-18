package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

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

type failingReadCloser struct {
	io.Reader
	closed   bool
	closeErr error
}

func (f *failingReadCloser) Close() error {
	f.closed = true
	return f.closeErr
}

func TestW4POC02_DrainImageBuildResponseBoundedAndChecksStreamErrors(t *testing.T) {
	okStream := &failingReadCloser{
		Reader: strings.NewReader(`{"stream":"Step 1/3 : FROM python:3.11-slim\n"}` + "\n" +
			`{"stream":"Successfully tagged genyee-poc/template:v2\n"}` + "\n"),
	}
	if err := drainImageBuildResponse(okStream, 1024); err != nil {
		t.Fatalf("unexpected error on healthy build stream: %v", err)
	}
	if !okStream.closed {
		t.Fatal("expected build response body to be closed")
	}

	daemonErrStream := &failingReadCloser{
		Reader: strings.NewReader(`{"stream":"Step 1/3\n"}` + "\n" +
			`{"errorDetail":{"message":"apt-get failed"},"error":"apt-get failed"}` + "\n"),
	}
	err := drainImageBuildResponse(daemonErrStream, 1024)
	if err == nil || !strings.Contains(err.Error(), "apt-get failed") {
		t.Fatalf("expected daemon build error to surface, got %v", err)
	}
	if !daemonErrStream.closed {
		t.Fatal("expected errored build response body to be closed")
	}

	oversizedStream := &failingReadCloser{
		Reader: strings.NewReader(`{"stream":"` + strings.Repeat("a", 256) + `"}`),
	}
	if err := drainImageBuildResponse(oversizedStream, 64); err == nil {
		t.Fatal("expected oversized build log stream to fail")
	}

	closeFailStream := &failingReadCloser{
		Reader:   strings.NewReader(`{"stream":"done\n"}`),
		closeErr: errors.New("socket close error"),
	}
	err = drainImageBuildResponse(closeFailStream, 1024)
	if err == nil || !strings.Contains(err.Error(), "socket close error") {
		t.Fatalf("expected Close error to propagate, got %v", err)
	}
}

func writeDockerMuxStdoutFrame(w io.Writer, payload []byte) error {
	var hdr [8]byte
	hdr[0] = 1 // stdout stream
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func TestW4POC02_CollectExecStreamRaceFreeOnCancelAndBounded(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			if err := writeDockerMuxStdoutFrame(serverConn, []byte("chunk-of-output\n")); err != nil {
				return
			}
		}
	}()

	time.AfterFunc(15*time.Millisecond, cancel)
	_, _, err := collectExecStream(ctx, clientConn, clientConn.Close, 1<<20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled on cancelled exec stream, got %v", err)
	}
	_ = serverConn.Close()
	<-writerDone

	// Verify bounded muxed output enforcement.
	var muxed bytes.Buffer
	if werr := writeDockerMuxStdoutFrame(&muxed, []byte(strings.Repeat("z", 128))); werr != nil {
		t.Fatal(werr)
	}
	if _, _, err := collectExecStream(context.Background(), &muxed, func() error { return nil }, 32); err == nil {
		t.Fatal("expected collectExecStream to reject output exceeding maxBytes")
	}
}

func TestW4POC02_ReadTarRegBoundedCapsHeaderFlood(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := 0; i < maxTarEntries+5; i++ {
		if err := tw.WriteHeader(&tar.Header{
			Name:     "dir/",
			Mode:     0o755,
			Typeflag: tar.TypeDir,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readTarRegBounded(&buf, 1024); err == nil || !strings.Contains(err.Error(), "exceeds entry limit") {
		t.Fatalf("expected header flood error, got %v", err)
	}
}
