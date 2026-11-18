package client

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Usir233/GENYEE/docreader/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultMessageEnvelopeSlack = 1 << 20
	defaultReadRPCTimeout       = 5 * time.Minute
	defaultControlRPCTimeout    = 30 * time.Second
)

var errClientNotConnected = status.Error(codes.Unavailable, "docreader client is not connected")

func getMaxFileSize() int {
	for _, key := range []string{"DOCREADER_GRPC_MAX_FILE_SIZE_MB", "MAX_FILE_SIZE_MB"} {
		if sizeStr := strings.TrimSpace(os.Getenv(key)); sizeStr != "" {
			if size, err := strconv.Atoi(sizeStr); err == nil && size > 0 && size <= 4096 {
				return size * 1024 * 1024
			}
		}
	}
	return 50 * 1024 * 1024
}

func maskAddrForLog(addr string) string {
	s := strings.TrimSpace(addr)
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.User != nil {
			u.User = url.UserPassword("***", "***")
			return u.String()
		}
	}
	if at := strings.LastIndex(s, "@"); at >= 0 {
		return "***@" + s[at+1:]
	}
	return s
}

// Logger writes structured diagnostic lines for the standalone DocReader client.
var Logger = log.New(os.Stdout, "[DocReader] ", log.LstdFlags|log.Lmicroseconds)

// ImageRefInfo represents an image reference from a converted document.
type ImageRefInfo struct {
	Filename    string
	OriginalRef string
	MimeType    string
	StorageKey  string
}

// Client represents a DocReader service client.
type Client struct {
	conn *grpc.ClientConn
	proto.DocReaderClient
	debug atomic.Bool
}

// NewClient constructs a DocReader client using TLS and token settings loaded
// from the process environment.
func NewClient(addr string) (*Client, error) {
	authConfig := LoadAuthConfigFromEnv()
	return NewClientWithAuth(addr, authConfig)
}

// NewClientWithAuth constructs a DocReader client connected to addr using the
// provided AuthConfig.
func NewClientWithAuth(addr string, authConfig *AuthConfig) (*Client, error) {
	trimmedAddr := strings.TrimSpace(addr)
	if trimmedAddr == "" {
		return nil, fmt.Errorf("docreader address must not be empty")
	}
	Logger.Printf("INFO: Creating new DocReader client connecting to %s", maskAddrForLog(trimmedAddr))

	// Reserve the same bounded envelope allowance as the App and Python server.
	// The raw file cap is checked separately in Read and ReadStream.
	maxMsgSize := getMaxFileSize() + defaultMessageEnvelopeSlack

	if authConfig == nil {
		authConfig = &AuthConfig{}
	}

	opts, err := authConfig.BuildDialOptionsForAddr(trimmedAddr, maxMsgSize)
	if err != nil {
		Logger.Printf("ERROR: Failed to build dial options: %v", err)
		return nil, err
	}

	dialTarget := "dns:///" + trimmedAddr
	if strings.HasPrefix(trimmedAddr, "dns:///") ||
		strings.HasPrefix(trimmedAddr, "passthrough:///") ||
		strings.HasPrefix(trimmedAddr, "unix:") {
		dialTarget = trimmedAddr
	}

	startTime := time.Now()
	conn, err := grpc.NewClient(dialTarget, opts...)
	if err != nil {
		Logger.Printf("ERROR: Failed to connect to DocReader service: %v", err)
		return nil, err
	}
	Logger.Printf("INFO: Successfully connected to DocReader service in %v", time.Since(startTime))

	return &Client{
		conn:            conn,
		DocReaderClient: proto.NewDocReaderClient(conn),
	}, nil
}

func validatePayloadSize(size int, kind string) error {
	maxFileSize := getMaxFileSize()
	if size > maxFileSize {
		return status.Errorf(codes.ResourceExhausted,
			"docreader %s size %d exceeds maximum %d", kind, size, maxFileSize)
	}
	return nil
}

func validateFileContentSize(req *proto.ReadRequest) error {
	if req == nil {
		return nil
	}
	return validatePayloadSize(len(req.GetFileContent()), "file content")
}

func ensureRPCDeadline(ctx context.Context, fallback time.Duration) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "context must not be nil")
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}, nil
	}
	derived, cancel := context.WithTimeout(ctx, fallback)
	return derived, cancel, nil
}

func (c *Client) checkReady() error {
	if c == nil || c.DocReaderClient == nil {
		return errClientNotConnected
	}
	return nil
}

// Read enforces the raw-file limit independently of protobuf framing and bounds
// deadline-less contexts.
func (c *Client) Read(
	ctx context.Context, req *proto.ReadRequest, opts ...grpc.CallOption,
) (*proto.ReadResponse, error) {
	if err := c.checkReady(); err != nil {
		return nil, err
	}
	if err := validateFileContentSize(req); err != nil {
		return nil, err
	}
	rpcCtx, cancel, err := ensureRPCDeadline(ctx, defaultReadRPCTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.DocReaderClient.Read(rpcCtx, req, opts...)
}

type managedReadStream struct {
	grpc.ServerStreamingClient[proto.ReadStreamResponse]
	cancelOnce sync.Once
	cancel     context.CancelFunc
}

func (s *managedReadStream) Recv() (*proto.ReadStreamResponse, error) {
	frame, err := s.ServerStreamingClient.Recv()
	if err != nil {
		s.cancelOnce.Do(s.cancel)
		return nil, err
	}
	return frame, nil
}

func (s *managedReadStream) Close() error {
	s.cancelOnce.Do(s.cancel)
	return nil
}

// ReadStream applies the same file budget as Read and returns a stream that
// also implements io.Closer for deterministic early-abort cancellation.
func (c *Client) ReadStream(
	ctx context.Context, req *proto.ReadRequest, opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[proto.ReadStreamResponse], error) {
	if err := c.checkReady(); err != nil {
		return nil, err
	}
	if err := validateFileContentSize(req); err != nil {
		return nil, err
	}
	boundedCtx, timeoutCancel, err := ensureRPCDeadline(ctx, defaultReadRPCTimeout)
	if err != nil {
		return nil, err
	}
	streamCtx, streamCancel := context.WithCancel(boundedCtx)
	cleanup := func() {
		streamCancel()
		timeoutCancel()
	}
	stream, err := c.DocReaderClient.ReadStream(streamCtx, req, opts...)
	if err != nil {
		cleanup()
		return nil, err
	}
	return &managedReadStream{
		ServerStreamingClient: stream,
		cancel:                cleanup,
	}, nil
}

// ListEngines queries available parser engines with a bounded control deadline.
func (c *Client) ListEngines(
	ctx context.Context, req *proto.ListEnginesRequest, opts ...grpc.CallOption,
) (*proto.ListEnginesResponse, error) {
	if err := c.checkReady(); err != nil {
		return nil, err
	}
	rpcCtx, cancel, err := ensureRPCDeadline(ctx, defaultControlRPCTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.DocReaderClient.ListEngines(rpcCtx, req, opts...)
}

// PreprocessImage straightens and crops a photographed page while enforcing
// the configured raw-file budget and default RPC deadline.
func (c *Client) PreprocessImage(
	ctx context.Context, req *proto.PreprocessImageRequest, opts ...grpc.CallOption,
) (*proto.PreprocessImageResponse, error) {
	if err := c.checkReady(); err != nil {
		return nil, err
	}
	if req != nil {
		if err := validatePayloadSize(len(req.GetImage()), "preprocess image"); err != nil {
			return nil, err
		}
	}
	rpcCtx, cancel, err := ensureRPCDeadline(ctx, defaultReadRPCTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.DocReaderClient.PreprocessImage(rpcCtx, req, opts...)
}

// GetInfo queries the DocReader server version and commit identity.
func (c *Client) GetInfo(
	ctx context.Context, req *proto.GetInfoRequest, opts ...grpc.CallOption,
) (*proto.GetInfoResponse, error) {
	if err := c.checkReady(); err != nil {
		return nil, err
	}
	rpcCtx, cancel, err := ensureRPCDeadline(ctx, defaultControlRPCTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.DocReaderClient.GetInfo(rpcCtx, req, opts...)
}

// Close closes the underlying gRPC client connection.
func (c *Client) Close() error {
	Logger.Printf("INFO: Closing DocReader client connection")
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// SetDebug toggles verbose DEBUG-level client logging.
func (c *Client) SetDebug(debug bool) {
	c.debug.Store(debug)
}

// Log writes a formatted message to Logger, filtering DEBUG messages unless
// SetDebug(true) is active.
func (c *Client) Log(level string, format string, args ...interface{}) {
	if level == "DEBUG" && !c.debug.Load() {
		return
	}
	Logger.Printf("%s: %s", level, fmt.Sprintf(format, args...))
}

// GetImageRefsFromResponse extracts image references from a ReadResponse.
func GetImageRefsFromResponse(resp *proto.ReadResponse) []ImageRefInfo {
	if resp == nil || len(resp.ImageRefs) == 0 {
		return nil
	}

	refs := make([]ImageRefInfo, 0, len(resp.ImageRefs))
	for _, ref := range resp.ImageRefs {
		refs = append(refs, ImageRefInfo{
			Filename:    ref.Filename,
			OriginalRef: ref.OriginalRef,
			MimeType:    ref.MimeType,
			StorageKey:  ref.StorageKey,
		})
	}
	return refs
}
