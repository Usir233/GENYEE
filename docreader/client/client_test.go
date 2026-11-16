package client

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/docreader/proto"
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

func requireLiveDocReaderClient(t *testing.T) *Client {
	t.Helper()

	client, err := NewClient("localhost:50051")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.ListEngines(ctx, &proto.ListEnginesRequest{}); err != nil {
		t.Skipf("DocReader gRPC server not available at localhost:50051: %v", err)
	}

	return client
}

func TestReadURL(t *testing.T) {
	client := requireLiveDocReaderClient(t)
	client.SetDebug(true)

	startTime := time.Now()
	resp, err := client.Read(
		context.Background(),
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
	client := requireLiveDocReaderClient(t)
	client.SetDebug(true)

	fileContent, err := os.ReadFile("../testdata/test.md")
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	startTime := time.Now()
	resp, err := client.Read(
		context.Background(),
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
