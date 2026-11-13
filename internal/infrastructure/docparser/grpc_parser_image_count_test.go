package docparser

import (
	"context"
	"fmt"
	"io"
	"testing"

	pb "github.com/Usir233/GENYEE/docreader/proto"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type imageCountDocReaderClient struct {
	pb.DocReaderClient
	frames []*pb.ReadStreamResponse
}

func (c imageCountDocReaderClient) ReadStream(context.Context, *pb.ReadRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.ReadStreamResponse], error) {
	return &imageCountFrames{frames: c.frames}, nil
}

type imageCountFrames struct {
	grpc.ClientStream
	frames []*pb.ReadStreamResponse
}

func (s *imageCountFrames) Recv() (*pb.ReadStreamResponse, error) {
	if len(s.frames) == 0 {
		return nil, io.EOF
	}
	frame := s.frames[0]
	s.frames = s.frames[1:]
	return frame, nil
}

func TestGRPCStreamImageCountOnlyAllocatesReceivedFrames(t *testing.T) {
	// Keep the before-fix reproducer safely bounded. The wire field accepts
	// uint32, but exercising its maximum would allocate hundreds of GB.
	for _, advertised := range []uint32{0, 1, 100000} {
		for _, received := range []int{0, 4} {
			t.Run(fmt.Sprintf("advertised=%d/received=%d", advertised, received), func(t *testing.T) {
				frames := []*pb.ReadStreamResponse{{Payload: &pb.ReadStreamResponse_Meta{Meta: &pb.ReadStreamMeta{
					MarkdownContent: "fixture", ImageCount: advertised,
				}}}}
				for i := 0; i < received; i++ {
					frames = append(frames, &pb.ReadStreamResponse{Payload: &pb.ReadStreamResponse_Image{Image: &pb.ImageRef{
						Filename: fmt.Sprintf("image%d.png", i), ImageData: []byte{byte(i)},
					}}})
				}
				reader := &GRPCDocumentReader{client: imageCountDocReaderClient{frames: frames}}
				got, err := reader.Read(context.Background(), &types.ReadRequest{FileName: "fixture.pdf"})
				require.NoError(t, err)
				require.Equal(t, "fixture", got.MarkdownContent)
				require.Len(t, got.ImageRefs, received, "the declared count is best-effort, not an image limit")
				require.LessOrEqual(t, cap(got.ImageRefs), 2*received, "metadata alone must not allocate unreceived image slots")
				for i, ref := range got.ImageRefs {
					require.Equal(t, fmt.Sprintf("image%d.png", i), ref.Filename)
					require.Equal(t, []byte{byte(i)}, ref.ImageData)
				}
			})
		}
	}
}
