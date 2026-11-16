package client

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Usir233/GENYEE/docreader/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

func NewClient(addr string) (*Client, error) {
	authConfig := LoadAuthConfigFromEnv()
	return NewClientWithAuth(addr, authConfig)
}

func NewClientWithAuth(addr string, authConfig *AuthConfig) (*Client, error) {
	trimmedAddr := strings.TrimSpace(addr)
	if trimmedAddr == "" {
		return nil, fmt.Errorf("docreader address must not be empty")
	}
	Logger.Printf("INFO: Creating new DocReader client connecting to %s", maskAddrForLog(trimmedAddr))

	// Reserve the same bounded envelope allowance as the App and Python server.
	// The raw file cap is checked separately in Read and ReadStream.
	maxMsgSize := getMaxFileSize() + (1 << 20)

	if authConfig == nil {
		authConfig = &AuthConfig{}
	}

	opts, err := authConfig.BuildDialOptionsForAddr(trimmedAddr, maxMsgSize)
	if err != nil {
		Logger.Printf("ERROR: Failed to build dial options: %v", err)
		return nil, err
	}

	dialTarget := "dns:///" + trimmedAddr
	if strings.HasPrefix(trimmedAddr, "dns:///") || strings.HasPrefix(trimmedAddr, "passthrough:///") {
		dialTarget = trimmedAddr
	}

	startTime := time.Now()
	conn, err := grpc.Dial(dialTarget, opts...)
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

func validateFileContentSize(req *proto.ReadRequest) error {
	if req == nil {
		return nil
	}
	maxFileSize := getMaxFileSize()
	if size := len(req.GetFileContent()); size > maxFileSize {
		return status.Errorf(codes.ResourceExhausted,
			"docreader file content size %d exceeds maximum %d", size, maxFileSize)
	}
	return nil
}

// Read enforces the raw-file limit independently of protobuf framing.
func (c *Client) Read(
	ctx context.Context, req *proto.ReadRequest, opts ...grpc.CallOption,
) (*proto.ReadResponse, error) {
	if err := validateFileContentSize(req); err != nil {
		return nil, err
	}
	return c.DocReaderClient.Read(ctx, req, opts...)
}

// ReadStream applies the same file budget as Read, including on older servers.
func (c *Client) ReadStream(
	ctx context.Context, req *proto.ReadRequest, opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[proto.ReadStreamResponse], error) {
	if err := validateFileContentSize(req); err != nil {
		return nil, err
	}
	return c.DocReaderClient.ReadStream(ctx, req, opts...)
}

func (c *Client) Close() error {
	Logger.Printf("INFO: Closing DocReader client connection")
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) SetDebug(debug bool) {
	c.debug.Store(debug)
}

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
