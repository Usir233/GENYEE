package file

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"
	"github.com/tencentyun/cos-go-sdk-v5"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
)

type trackedBody struct {
	io.Reader
	closed atomic.Bool
	open   *atomic.Int32
}

func newTrackedBody(content string, open *atomic.Int32) *trackedBody {
	open.Add(1)
	return &trackedBody{Reader: strings.NewReader(content), open: open}
}

func (b *trackedBody) Close() error {
	if b.closed.CompareAndSwap(false, true) {
		b.open.Add(-1)
	}
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCosFileServiceClosesAllResponseBodies(t *testing.T) {
	var openBodies atomic.Int32
	var totalResponses atomic.Int32

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		totalResponses.Add(1)
		body := ""
		if req.Method == http.MethodPut && req.Header.Get("X-Cos-Copy-Source") != "" {
			body = `<CopyObjectResult><LastModified>2026-01-01T00:00:00.000Z</LastModified><ETag>"etag"</ETag></` +
				`CopyObjectResult>`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       newTrackedBody(body, &openBodies),
			Request:    req,
		}, nil
	})

	mainURL, err := url.Parse("https://main-1250000000.cos.ap-guangzhou.myqcloud.com/")
	require.NoError(t, err)
	tempURL, err := url.Parse("https://temp-1250000000.cos.ap-guangzhou.myqcloud.com/")
	require.NoError(t, err)

	httpClient := &http.Client{Transport: &cos.AuthorizationTransport{
		SecretID:  "ak",
		SecretKey: "sk",
		Transport: transport,
	}}
	svc := &cosFileService{
		client:         cos.NewClient(&cos.BaseURL{BucketURL: mainURL}, httpClient),
		bucketURL:      mainURL.String(),
		bucketName:     "main-1250000000",
		region:         "ap-guangzhou",
		cosPathPrefix:  "prefix",
		tempClient:     cos.NewClient(&cos.BaseURL{BucketURL: tempURL}, httpClient),
		tempBucketURL:  tempURL.String(),
		tempBucketName: "temp-1250000000",
		tempRegion:     "ap-guangzhou",
	}

	ctx := context.Background()
	require.NoError(t, svc.CheckConnectivity(ctx))

	savedMain, err := svc.SaveBytes(ctx, []byte("hello"), 42, "report.txt", false)
	require.NoError(t, err)

	savedTemp, err := svc.SaveBytes(ctx, []byte("temp"), 42, "export.txt", true)
	require.NoError(t, err)

	copied, err := svc.CopyFile(ctx, savedMain, 42, "kb-1")
	require.NoError(t, err)
	require.NotEmpty(t, copied)

	require.NoError(t, svc.DeleteFile(ctx, savedTemp))

	require.GreaterOrEqual(t, totalResponses.Load(), int32(5))
	require.Zero(t, openBodies.Load(), "every COS response body must be closed")

	rawBody := newTrackedBody("redirect-body", &openBodies)
	closeCosResponse(&cos.Response{Response: &http.Response{Body: rawBody}})
	require.Zero(t, openBodies.Load(), "closeCosResponse must drain and close embedded http.Response.Body")
	closeCosResponse(nil)
}

type failingCloseFile struct {
	*os.File
}

func (f *failingCloseFile) Close() error {
	_ = f.File.Close()
	return io.ErrShortWrite
}

func TestLocalFileServiceRemovesOrphanOnCopyFailure(t *testing.T) {
	baseDir := t.TempDir()
	srcDir := filepath.Join(baseDir, "1", "source-dir")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))

	svc := NewLocalFileService(baseDir, "")
	_, err := svc.CopyFile(context.Background(), "local://1/source-dir", 1, "kb-dest")
	require.Error(t, err)

	destDir := filepath.Join(baseDir, "1", "kb-dest")
	entries, readErr := os.ReadDir(destDir)
	if readErr == nil {
		require.Empty(t, entries, "failed CopyFile must not leave partial orphan files behind")
	}

	flushFailPath := filepath.Join(baseDir, "1", "flush-fail.txt")
	rawFile, createErr := os.Create(flushFailPath)
	require.NoError(t, createErr)
	closeErr := writeLocalStreamToDest(&failingCloseFile{File: rawFile}, flushFailPath, strings.NewReader("payload"))
	require.ErrorIs(t, closeErr, io.ErrShortWrite)
	_, statErr := os.Stat(flushFailPath)
	require.True(t, os.IsNotExist(statErr), "close flush failure must remove partial file")
}

func TestTOSBucketProbeAndGetFileBindTransferContext(t *testing.T) {
	var mu sync.Mutex
	var probeHasDeadline bool
	var getCtx context.Context

	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		switch req.Method {
		case http.MethodHead:
			_, probeHasDeadline = req.Context().Deadline()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		case http.MethodGet:
			getCtx = req.Context()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Length": []string{"5"}},
				Body:       io.NopCloser(bytes.NewReader([]byte("hello"))),
				Request:    req,
			}, nil
		default:
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}
	})

	client, err := tos.NewClientV2(
		"https://tos-cn-beijing.volces.com",
		tos.WithRegion("cn-beijing"),
		tos.WithCredentials(tos.NewStaticCredentials("ak", "sk")),
		tos.WithHTTPTransport(rt),
	)
	require.NoError(t, err)

	require.NoError(t, ensureTOSBucket(client, "bucket-a"))
	mu.Lock()
	require.True(t, probeHasDeadline, "ensureTOSBucket must bound HeadBucket with a setup deadline")
	mu.Unlock()

	svc := &tosFileService{client: client, bucketName: "bucket-a"}
	rc, err := svc.GetFile(context.Background(), "tos://bucket-a/1/doc.txt")
	require.NoError(t, err)

	mu.Lock()
	captured := getCtx
	mu.Unlock()
	require.NotNil(t, captured)
	_, hasTransferDeadline := captured.Deadline()
	require.True(t, hasTransferDeadline, "TOS GetFile must attach fallback transfer deadline")
	require.NoError(t, rc.Close())
	require.ErrorIs(t, captured.Err(), context.Canceled, "closing TOS GetFile reader must cancel transfer context")
}

func TestMinioGetFileBindsTransferContext(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "minio.example.internal")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	var mu sync.Mutex
	var getCtx context.Context

	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "doc.txt") {
			getCtx = req.Context()
		}
		mu.Unlock()
		header := make(http.Header)
		body := ""
		switch req.Method {
		case http.MethodGet:
			if strings.Contains(req.URL.RawQuery, "location") {
				body = `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</` +
					`LocationConstraint>`
			} else {
				body = "hello"
				header.Set("Content-Length", "5")
				header.Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
				header.Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
			}
		case http.MethodHead:
			header.Set("Content-Length", "5")
			header.Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
			header.Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})

	mc, err := minio.New("minio.example.internal:9000", &minio.Options{
		Creds:     credentials.NewStaticV4("ak", "sk", ""),
		Secure:    false,
		Transport: rt,
	})
	require.NoError(t, err)

	svc := &minioFileService{client: mc, bucketName: "bucket-m"}
	rc, err := svc.GetFile(context.Background(), "minio://bucket-m/1/doc.txt")
	require.NoError(t, err)

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))

	mu.Lock()
	captured := getCtx
	mu.Unlock()
	require.NotNil(t, captured)
	_, hasTransferDeadline := captured.Deadline()
	require.True(t, hasTransferDeadline, "MinIO GetFile must attach fallback transfer deadline")
	require.NoError(t, rc.Close())
	require.ErrorIs(t, captured.Err(), context.Canceled, "closing MinIO GetFile reader must cancel transfer context")
}
