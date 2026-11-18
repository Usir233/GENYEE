package docparser

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/Usir233/GENYEE/docreader/proto"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type streamCancelProbeServer struct {
	pb.UnimplementedDocReaderServer
	streamCanceled chan struct{}
}

func (s *streamCancelProbeServer) ReadStream(
	_ *pb.ReadRequest, stream grpc.ServerStreamingServer[pb.ReadStreamResponse],
) error {
	if err := stream.Send(&pb.ReadStreamResponse{
		Payload: &pb.ReadStreamResponse_Meta{
			Meta: &pb.ReadStreamMeta{MarkdownContent: "![](img1.png)"},
		},
	}); err != nil {
		return err
	}
	// Exceeds GENYEE_MINERU_MAX_IMAGE_BYTES=8 configured by the test.
	if err := stream.Send(&pb.ReadStreamResponse{
		Payload: &pb.ReadStreamResponse_Image{
			Image: &pb.ImageRef{Filename: "img1.png", ImageData: []byte("0123456789abcdef")},
		},
	}); err != nil {
		return err
	}
	<-stream.Context().Done()
	close(s.streamCanceled)
	return stream.Context().Err()
}

func TestGRPCDocumentReader_ReadStreamCancelsServerStreamOnEarlyAbort(t *testing.T) {
	t.Setenv(mineruMaxImageBytesEnv, "8")

	probe := &streamCancelProbeServer{streamCanceled: make(chan struct{})}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() })

	srv := grpc.NewServer()
	pb.RegisterDocReaderServer(srv, probe)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	reader := &GRPCDocumentReader{
		conn:   conn,
		client: pb.NewDocReaderClient(conn),
		addr:   lis.Addr().String(),
	}

	parentCtx, parentCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer parentCancel()

	_, err = reader.Read(parentCtx, &types.ReadRequest{FileName: "large_images.pdf"})
	require.Error(t, err)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))

	select {
	case <-probe.streamCanceled:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("server stream remained open after early ResourceExhausted abort while parent ctx was still active")
	}
}
