package client

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/Usir233/GENYEE/docreader/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type captureAuthServer struct {
	pb.UnimplementedDocReaderServer
	expectedHeader string
}

func (s captureAuthServer) GetInfo(ctx context.Context, _ *pb.GetInfoRequest) (*pb.GetInfoResponse, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}
	vals := md.Get("authorization")
	if len(vals) != 1 || vals[0] != s.expectedHeader {
		return nil, status.Errorf(codes.Unauthenticated, "got authorization=%q, want %q", vals, s.expectedHeader)
	}
	return &pb.GetInfoResponse{Version: "test"}, nil
}

func TestLoadAuthConfigFromEnvTrimsTokenWhitespace(t *testing.T) {
	t.Setenv("GRPC_AUTH_TOKEN", "  \tsecret-token-from-k8s\r\n")
	cfg := LoadAuthConfigFromEnv()
	if cfg.AuthToken != "secret-token-from-k8s" {
		t.Fatalf("expected trimmed AuthToken, got %q", cfg.AuthToken)
	}

	t.Setenv("GRPC_AUTH_TOKEN", " \t\r\n ")
	emptyCfg := LoadAuthConfigFromEnv()
	if emptyCfg.AuthToken != "" {
		t.Fatalf("expected whitespace-only GRPC_AUTH_TOKEN to become empty, got %q", emptyCfg.AuthToken)
	}
}

func TestClientSendsTrimmedEnvAuthTokenOverGRPC(t *testing.T) {
	t.Setenv("DOCREADER_ADDR", "")
	t.Setenv("GRPC_TLS_ENABLED", "false")
	t.Setenv("GRPC_AUTH_TOKEN", "  mounted-secret-with-newline\n")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterDocReaderServer(server, captureAuthServer{
		expectedHeader: "Bearer mounted-secret-with-newline",
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	cli, err := NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := cli.GetInfo(ctx, &pb.GetInfoRequest{})
	if err != nil {
		t.Fatalf("GetInfo with whitespace-padded GRPC_AUTH_TOKEN failed: %v", err)
	}
	if resp.GetVersion() != "test" {
		t.Fatalf("unexpected version %q", resp.GetVersion())
	}
}

func TestEffectiveForAddrStripsGlobalTokenOnUntrustedEndpoints(t *testing.T) {
	t.Setenv("DOCREADER_ADDR", "docreader:50051")

	base := &AuthConfig{
		TLSEnabled: false,
		AuthToken:  "global-docreader-secret",
	}

	if got := base.EffectiveForAddr("docreader:50051"); got.AuthToken != "global-docreader-secret" {
		t.Fatalf("expected configured DOCREADER_ADDR to retain AuthToken, got %q", got.AuthToken)
	}
	if got := base.EffectiveForAddr("203.0.113.10:50051"); got.AuthToken != "" {
		t.Fatalf("expected untrusted external plaintext endpoint to strip AuthToken, got %q", got.AuthToken)
	}
	if got := base.EffectiveForAddr("127.0.0.1:50051"); got.AuthToken != "" {
		t.Fatalf("expected non-matching endpoint to strip AuthToken when DOCREADER_ADDR is set, got %q", got.AuthToken)
	}
}

func TestEffectiveForAddrAllowsLoopbackWhenDocReaderAddrUnset(t *testing.T) {
	t.Setenv("DOCREADER_ADDR", "")

	base := &AuthConfig{
		TLSEnabled: false,
		AuthToken:  "local-dev-token",
	}

	if got := base.EffectiveForAddr("127.0.0.1:50051"); got.AuthToken != "local-dev-token" {
		t.Fatalf("expected loopback to retain token when DOCREADER_ADDR is unset, got %q", got.AuthToken)
	}
	if got := base.EffectiveForAddr("localhost:50051"); got.AuthToken != "local-dev-token" {
		t.Fatalf("expected localhost to retain token when DOCREADER_ADDR is unset, got %q", got.AuthToken)
	}
	if got := base.EffectiveForAddr("198.51.100.25:50051"); got.AuthToken != "" {
		t.Fatalf("expected external endpoint to strip token when DOCREADER_ADDR is unset, got %q", got.AuthToken)
	}
}

func TestLoadAuthConfigFromEnvParsesTLSEnabledCaseAndWhitespace(t *testing.T) {
	for _, truthy := range []string{"true", "TRUE", " 1\n", "yes", "on"} {
		t.Setenv("GRPC_TLS_ENABLED", truthy)
		t.Setenv("GRPC_TLS_CERT", "  /etc/certs/tls.crt\n")
		t.Setenv("GRPC_TLS_KEY", " /etc/certs/tls.key\t")
		t.Setenv("GRPC_TLS_CA", " /etc/certs/ca.crt\r\n")
		t.Setenv("GRPC_TLS_SERVER_NAME", " docreader.internal \n")
		cfg := LoadAuthConfigFromEnv()
		if !cfg.TLSEnabled {
			t.Fatalf("expected GRPC_TLS_ENABLED=%q to enable TLS", truthy)
		}
		if cfg.CertFile != "/etc/certs/tls.crt" || cfg.KeyFile != "/etc/certs/tls.key" ||
			cfg.CAFile != "/etc/certs/ca.crt" || cfg.ServerName != "docreader.internal" {
			t.Fatalf("expected trimmed TLS paths/server name, got %+v", cfg)
		}
	}
}

func TestIsTrustedDocReaderAddrNormalizesSchemesFQDNsAndRejectsUserinfo(t *testing.T) {
	t.Setenv("DOCREADER_ADDR", "")
	for _, trusted := range []string{
		"dns:///127.0.0.1:50051",
		"passthrough:///localhost:50051",
		"localhost.:50051",
		"[::1]:50051",
	} {
		if !IsTrustedDocReaderAddr(trusted) {
			t.Fatalf("expected %q to be trusted loopback when DOCREADER_ADDR is unset", trusted)
		}
	}
	for _, untrusted := range []string{
		"http://user:secret@127.0.0.1:50051",
		"dns:///203.0.113.5:50051",
		"",
	} {
		if IsTrustedDocReaderAddr(untrusted) {
			t.Fatalf("expected %q to be untrusted", untrusted)
		}
	}

	t.Setenv("DOCREADER_ADDR", "docreader.svc.cluster.local.:50051")
	if !IsTrustedDocReaderAddr("dns:///docreader.svc.cluster.local:50051") {
		t.Fatal("expected FQDN trailing dot before port and dns:/// scheme to normalize identically")
	}
}

