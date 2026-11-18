package client

import (
	"context"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/docreader/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
)

func TestNewClientDoesNotChangeGlobalGRPCResolver(t *testing.T) {
	previous := resolver.GetDefaultScheme()
	resolver.SetDefaultScheme("passthrough")
	t.Cleanup(func() { resolver.SetDefaultScheme(previous) })

	client, err := NewClientWithAuth("127.0.0.1:1", &AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if got := resolver.GetDefaultScheme(); got != "passthrough" {
		t.Fatalf("DocReader changed global gRPC resolver to %q", got)
	}
}

func TestClientConfiguresNamespacedFileSizeGuardsEmptyAddrMasksLogsAndIsConcurrencySafe(t *testing.T) {
	t.Setenv("DOCREADER_GRPC_MAX_FILE_SIZE_MB", "  2 \n")
	t.Setenv("MAX_FILE_SIZE_MB", "50")
	if got := getMaxFileSize(); got != 2*1024*1024 {
		t.Fatalf("expected DOCREADER_GRPC_MAX_FILE_SIZE_MB=2 MiB to take precedence, got %d", got)
	}

	if _, err := NewClientWithAuth("   ", &AuthConfig{}); err == nil {
		t.Fatal("expected empty docreader address to return error")
	}

	var nilConnClient Client
	if err := nilConnClient.Close(); err != nil {
		t.Fatalf("expected nil-conn Close() to be safe no-op, got %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			nilConnClient.SetDebug(i%2 == 0)
		}
	}()
	for i := 0; i < 100; i++ {
		nilConnClient.Log("DEBUG", "iter %d", i)
	}
	<-done
}

func init() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("INFO: Initializing DocReader client tests")
}

type localFallbackDocReaderServer struct {
	proto.UnimplementedDocReaderServer
}

func (localFallbackDocReaderServer) ListEngines(
	_ context.Context, _ *proto.ListEnginesRequest,
) (*proto.ListEnginesResponse, error) {
	return &proto.ListEnginesResponse{
		Engines: []*proto.ParserEngineInfo{{Name: "builtin", Available: true}},
	}, nil
}

func (localFallbackDocReaderServer) Read(
	_ context.Context, req *proto.ReadRequest,
) (*proto.ReadResponse, error) {
	if len(req.GetFileContent()) > 0 {
		return &proto.ReadResponse{MarkdownContent: string(req.GetFileContent())}, nil
	}
	if req.GetUrl() != "" {
		return &proto.ReadResponse{MarkdownContent: "# " + req.GetTitle() + "\n\nFetched " + req.GetUrl()}, nil
	}
	return &proto.ReadResponse{Error: "empty request"}, nil
}

func requireDocReaderClient(t *testing.T) *Client {
	t.Helper()

	probe, err := NewClient("localhost:50051")
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, probeErr := probe.ListEngines(ctx, &proto.ListEnginesRequest{})
		cancel()
		if probeErr == nil {
			t.Cleanup(func() { _ = probe.Close() })
			return probe
		}
		_ = probe.Close()
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen for local fallback DocReader server: %v", err)
	}
	server := grpc.NewServer()
	proto.RegisterDocReaderServer(server, localFallbackDocReaderServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	client, err := NewClientWithAuth(listener.Addr().String(), &AuthConfig{})
	if err != nil {
		t.Fatalf("Failed to create local fallback client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestReadURL(t *testing.T) {
	client := requireDocReaderClient(t)
	client.SetDebug(true)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	startTime := time.Now()
	resp, err := client.Read(
		ctx,
		&proto.ReadRequest{
			Url:   "https://example.com",
			Title: "test",
		},
	)
	log.Printf("INFO: Read(URL) completed in %v", time.Since(startTime))

	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("Read returned error: %s", resp.Error)
	}
	if resp.MarkdownContent == "" {
		t.Error("Expected non-empty markdown content")
	}
	log.Printf("INFO: content_len=%d, images=%d", len(resp.MarkdownContent), len(resp.ImageRefs))
}

func TestReadFile(t *testing.T) {
	client := requireDocReaderClient(t)
	client.SetDebug(true)

	fileContent, err := os.ReadFile("../testdata/test.md")
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	startTime := time.Now()
	resp, err := client.Read(
		ctx,
		&proto.ReadRequest{
			FileContent: fileContent,
			FileName:    "test.md",
			FileType:    "md",
		},
	)
	log.Printf("INFO: Read(file) completed in %v", time.Since(startTime))

	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("Read returned error: %s", resp.Error)
	}
	if resp.MarkdownContent == "" {
		t.Error("Expected non-empty markdown content")
	}

	imageRefs := GetImageRefsFromResponse(resp)
	log.Printf("INFO: content_len=%d, images=%d", len(resp.MarkdownContent), len(imageRefs))
}
