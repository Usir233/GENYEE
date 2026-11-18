package client

import (
	"context"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/Usir233/GENYEE/docreader/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type clientEnvelopeServer struct {
	pb.UnimplementedDocReaderServer
	replyBytes int
}

func (s clientEnvelopeServer) Read(_ context.Context, _ *pb.ReadRequest) (*pb.ReadResponse, error) {
	text := "parsed"
	if s.replyBytes > 0 {
		text = strings.Repeat("r", s.replyBytes)
	}
	return &pb.ReadResponse{MarkdownContent: text}, nil
}

func (s clientEnvelopeServer) ReadStream(
	_ *pb.ReadRequest, stream grpc.ServerStreamingServer[pb.ReadStreamResponse],
) error {
	result, _ := s.Read(context.Background(), nil)
	return stream.Send(&pb.ReadStreamResponse{Payload: &pb.ReadStreamResponse_Meta{Meta: &pb.ReadStreamMeta{
		MarkdownContent: result.MarkdownContent,
	}}})
}

func TestClientFileEnvelope(t *testing.T) {
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	const limit = 1 << 20
	for _, tt := range []struct {
		name          string
		input, output int
	}{
		{"file at cap", limit, 0}, {"reply at cap", 16, limit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer(grpc.MaxRecvMsgSize(2*limit), grpc.MaxSendMsgSize(2*limit))
			pb.RegisterDocReaderServer(server, clientEnvelopeServer{replyBytes: tt.output})
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			client, err := NewClientWithAuth(listener.Addr().String(), &AuthConfig{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			req := &pb.ReadRequest{
				FileContent: []byte(strings.Repeat("a", tt.input)), FileName: "report.txt", FileType: "txt",
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			unary, err := client.Read(ctx, req)
			if err != nil {
				t.Fatalf("legal unary payload rejected: %v", err)
			}
			want := "parsed"
			if tt.output > 0 {
				want = strings.Repeat("r", tt.output)
			}
			if unary.MarkdownContent != want {
				t.Fatal("unary response bytes changed")
			}
			stream, err := client.ReadStream(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := stream.Recv()
			if err != nil {
				t.Fatalf("legal streaming payload rejected: %v", err)
			}
			if frame.GetMeta().MarkdownContent != want {
				t.Fatal("stream response bytes changed")
			}
		})
	}
}

type fileLimitClientProbe struct {
	pb.DocReaderClient
	calls atomic.Int32
}

func (p *fileLimitClientProbe) Read(context.Context, *pb.ReadRequest, ...grpc.CallOption) (*pb.ReadResponse, error) {
	p.calls.Add(1)
	return nil, nil
}

func (p *fileLimitClientProbe) ReadStream(
	context.Context, *pb.ReadRequest, ...grpc.CallOption,
) (grpc.ServerStreamingClient[pb.ReadStreamResponse], error) {
	p.calls.Add(1)
	return nil, nil
}

func TestClientRejectsFileBeyondRawCapBeforeRPC(t *testing.T) {
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	remote := &fileLimitClientProbe{}
	client := &Client{DocReaderClient: remote}
	req := &pb.ReadRequest{FileContent: make([]byte, (1<<20)+1)}
	_, err := client.Read(context.Background(), req)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("Read oversized error=%v", err)
	}
	_, err = client.ReadStream(context.Background(), req)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("ReadStream oversized error=%v", err)
	}
	if remote.calls.Load() != 0 {
		t.Fatal("oversized file reached the RPC boundary")
	}

	_, err = client.PreprocessImage(context.Background(), &pb.PreprocessImageRequest{Image: make([]byte, (1<<20)+1)})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("PreprocessImage oversized error=%v", err)
	}
	if remote.calls.Load() != 0 {
		t.Fatal("oversized PreprocessImage reached the RPC boundary")
	}
}

type streamCancelProbeServer struct {
	pb.UnimplementedDocReaderServer
	hasDeadline   atomic.Bool
	streamDoneErr chan error
}

func (s *streamCancelProbeServer) GetInfo(ctx context.Context, _ *pb.GetInfoRequest) (*pb.GetInfoResponse, error) {
	_, ok := ctx.Deadline()
	s.hasDeadline.Store(ok)
	return &pb.GetInfoResponse{Version: "v1"}, nil
}

func (s *streamCancelProbeServer) ReadStream(
	_ *pb.ReadRequest, stream grpc.ServerStreamingServer[pb.ReadStreamResponse],
) error {
	if err := stream.Send(&pb.ReadStreamResponse{
		Payload: &pb.ReadStreamResponse_Meta{Meta: &pb.ReadStreamMeta{MarkdownContent: "head"}},
	}); err != nil {
		return err
	}
	<-stream.Context().Done()
	err := stream.Context().Err()
	select {
	case s.streamDoneErr <- err:
	default:
	}
	return err
}

func TestClientInjectsDefaultDeadlinesNilGuardsAndClosesStreamEarly(t *testing.T) {
	var uninit *Client
	if _, err := uninit.Read(context.Background(), &pb.ReadRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable on nil Client.Read, got %v", err)
	}
	if _, err := uninit.GetInfo(context.Background(), &pb.GetInfoRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable on nil Client.GetInfo, got %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srvImpl := &streamCancelProbeServer{streamDoneErr: make(chan error, 1)}
	server := grpc.NewServer()
	pb.RegisterDocReaderServer(server, srvImpl)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	cli, err := NewClientWithAuth(listener.Addr().String(), &AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	if _, err := cli.GetInfo(context.Background(), &pb.GetInfoRequest{}); err != nil {
		t.Fatalf("GetInfo failed: %v", err)
	}
	if !srvImpl.hasDeadline.Load() {
		t.Fatal("expected Client.GetInfo(context.Background()) to apply a default RPC deadline")
	}

	stream, err := cli.ReadStream(context.Background(), &pb.ReadRequest{
		FileContent: []byte("hello"),
		FileName:    "hello.txt",
		FileType:    "txt",
	})
	if err != nil {
		t.Fatalf("ReadStream failed: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("ReadStream first frame failed: %v", err)
	}
	closer, ok := stream.(io.Closer)
	if !ok {
		t.Fatal("expected ReadStream client to implement io.Closer for deterministic early abort")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("stream.Close() returned error: %v", err)
	}
	select {
	case doneErr := <-srvImpl.streamDoneErr:
		if doneErr == nil {
			t.Fatal("expected server stream context cancellation error after stream.Close()")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server stream was not cancelled promptly after client stream.Close()")
	}
}
