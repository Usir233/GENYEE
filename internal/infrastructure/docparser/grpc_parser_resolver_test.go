package docparser

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/docreader/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/resolver"
)

func TestGRPCDocumentReaderDoesNotChangeGlobalResolver(t *testing.T) {
	previous := resolver.GetDefaultScheme()
	resolver.SetDefaultScheme("passthrough")
	t.Cleanup(func() { resolver.SetDefaultScheme(previous) })

	reader, err := NewGRPCDocumentReader("127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if got := resolver.GetDefaultScheme(); got != "passthrough" {
		t.Fatalf("DocReader changed global gRPC resolver to %q", got)
	}
}

type authCaptureDocReaderServer struct {
	proto.UnimplementedDocReaderServer
	authHeaderCh chan string
}

func (s *authCaptureDocReaderServer) ListEngines(ctx context.Context, _ *proto.ListEnginesRequest) (*proto.ListEnginesResponse, error) {
	var authVal string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("authorization"); len(vals) > 0 {
			authVal = vals[0]
		}
	}
	select {
	case s.authHeaderCh <- authVal:
	default:
	}
	return &proto.ListEnginesResponse{}, nil
}

func TestProbeGRPCDocumentReaderBlocksLoopbackSSRFByDefault(t *testing.T) {
	t.Setenv("DOCREADER_ADDR", "docreader:50051")
	t.Setenv("GRPC_AUTH_TOKEN", "super-secret-grpc-token")

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()

	srv := grpc.NewServer()
	capture := &authCaptureDocReaderServer{authHeaderCh: make(chan string, 1)}
	proto.RegisterDocReaderServer(srv, capture)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	probe, err := NewProbeGRPCDocumentReader(lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = probe.ListEngines(ctx, nil)
	if err == nil {
		t.Fatal("expected SSRFSafeGRPCDialer to block loopback target when not configured as DOCREADER_ADDR")
	}
	select {
	case hdr := <-capture.authHeaderCh:
		t.Fatalf("server should never have been reached, got authorization=%q", hdr)
	default:
	}
}

func TestConfiguredDocReaderSendsTokenOnlyToConfiguredEndpoint(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()

	t.Setenv("DOCREADER_ADDR", lis.Addr().String())
	t.Setenv("GRPC_AUTH_TOKEN", "configured-sidecar-token")
	t.Setenv("GRPC_TLS_ENABLED", "false")

	srv := grpc.NewServer()
	capture := &authCaptureDocReaderServer{authHeaderCh: make(chan string, 1)}
	proto.RegisterDocReaderServer(srv, capture)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	reader, err := NewGRPCDocumentReader(lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := reader.ListEngines(ctx, nil); err != nil {
		t.Fatalf("expected configured DOCREADER_ADDR to connect: %v", err)
	}
	select {
	case hdr := <-capture.authHeaderCh:
		if !strings.Contains(hdr, "configured-sidecar-token") {
			t.Fatalf("expected configured sidecar to receive token, got %q", hdr)
		}
	case <-time.After(time.Second):
		t.Fatal("expected incoming ListEngines metadata")
	}
}

// Regression: a hostname-based DOCREADER_ADDR (e.g. "docreader:50051" in
// docker compose) must stay reachable. With the dns:/// resolver the dialer
// only saw the resolved private IP, failed the trusted-address match and the
// SSRF guard blocked the operator's own docreader.
func TestGRPCDocumentReaderReachesHostnameConfiguredDocReader(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	_, port, _ := net.SplitHostPort(lis.Addr().String())
	addr := "localhost:" + port
	t.Setenv("DOCREADER_ADDR", addr)

	srv := grpc.NewServer()
	capture := &authCaptureDocReaderServer{authHeaderCh: make(chan string, 1)}
	proto.RegisterDocReaderServer(srv, capture)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	reader, err := NewGRPCDocumentReader(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := reader.ListEngines(ctx, nil); err != nil {
		t.Fatalf("operator-configured hostname docreader must be reachable, got: %v", err)
	}
}
