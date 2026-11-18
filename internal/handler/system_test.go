package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usir233/GENYEE/docreader/proto"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type spyGlobalDocumentReader struct {
	reconnectCalls int
	lastAddr       string
}

func (s *spyGlobalDocumentReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return &types.ReadResult{}, nil
}

func (s *spyGlobalDocumentReader) ListEngines(context.Context, map[string]string) ([]types.ParserEngineInfo, error) {
	return nil, nil
}

func (s *spyGlobalDocumentReader) PreprocessImage(context.Context, []byte) ([]byte, bool, error) {
	return nil, true, nil
}

func (s *spyGlobalDocumentReader) Reconnect(addr string) error {
	s.reconnectCalls++
	s.lastAddr = addr
	return nil
}
func (s *spyGlobalDocumentReader) IsConnected() bool { return true }

type stubProbeDocReaderServer struct {
	proto.UnimplementedDocReaderServer
	lastAuthHeader string
}

func (s *stubProbeDocReaderServer) ListEngines(
	ctx context.Context, _ *proto.ListEnginesRequest,
) (*proto.ListEnginesResponse, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("authorization"); len(vals) > 0 {
			s.lastAuthHeader = vals[0]
		}
	}
	return &proto.ListEnginesResponse{
		Engines: []*proto.ParserEngineInfo{
			{Name: "custom_probe_engine", Available: true},
		},
	}, nil
}

func TestReconnectDocReaderDoesNotMutateGlobalReaderOrLeakTokenForExternalAddr(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DOCREADER_ADDR", "docreader-internal:50051")
	t.Setenv("GRPC_AUTH_TOKEN", "platform-global-docreader-secret")
	t.Setenv("GRPC_TLS_ENABLED", "false")

	secutils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { secutils.SetSSRFWhitelistFromRaw("") })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()

	grpcSrv := grpc.NewServer()
	probeSrv := &stubProbeDocReaderServer{}
	proto.RegisterDocReaderServer(grpcSrv, probeSrv)
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	globalSpy := &spyGlobalDocumentReader{}
	h := &SystemHandler{documentReader: globalSpy}

	body, err := json.Marshal(map[string]string{"addr": lis.Addr().String()})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/system/docreader/reconnect", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.ReconnectDocReader(c)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"code":0`)
	assert.Equal(
		t, 0, globalSpy.reconnectCalls,
		"non-configured external addr must NOT replace global documentReader singleton",
	)
	assert.Empty(t, probeSrv.lastAuthHeader, "non-configured external addr must NOT receive global GRPC_AUTH_TOKEN")
}

func TestReconnectDocReaderRejectsSSRFMetadataAddr(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DOCREADER_ADDR", "docreader-internal:50051")

	globalSpy := &spyGlobalDocumentReader{}
	h := &SystemHandler{documentReader: globalSpy}

	body, _ := json.Marshal(map[string]string{"addr": "169.254.169.254:50051"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/system/docreader/reconnect", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.ReconnectDocReader(c)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, globalSpy.reconnectCalls)
}
