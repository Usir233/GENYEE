package file

import (
	"bytes"
	"context"
	"hash/crc64"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/tencentyun/cos-go-sdk-v5"
)

type lifecycleTransport struct {
	objects  map[string][]byte
	requests []string
}

func (t *lifecycleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.Host + req.URL.Path
	t.requests = append(t.requests, req.Method+" "+key)
	var body []byte
	switch req.Method {
	case http.MethodPut:
		body, _ = io.ReadAll(req.Body)
		t.objects[key] = body
		body = nil
	case http.MethodGet:
		body = t.objects[key]
	case http.MethodDelete:
		delete(t.objects, key)
	}
	headers := http.Header{"Etag": []string{`"test"`}}
	headers.Set("X-Cos-Hash-Crc64ecma", strconv.FormatUint(crc64.Checksum(t.objects[key], crc64.MakeTable(crc64.ECMA)), 10))
	return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(bytes.NewReader(body)), Request: req, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}, nil
}

func TestCOSTemporaryBytesUseResolvableResourcePaths(t *testing.T) {
	raw, err := NewCosFileServiceWithTempBucket("main-1250000000", "ap-shanghai", "ak", "sk", "genyee", "temporary-1250000000", "ap-beijing")
	require.NoError(t, err)
	svc := raw.(*cosFileService)
	transport := &lifecycleTransport{objects: map[string][]byte{}}
	endpoint, err := url.Parse(svc.tempBucketURL)
	require.NoError(t, err)
	svc.tempClient = cos.NewClient(&cos.BaseURL{BucketURL: endpoint}, &http.Client{Transport: transport})
	ref, err := svc.SaveBytes(context.Background(), []byte("temporary image"), 7, "image.png", true)
	require.NoError(t, err)
	require.Equal(t, "cos", types.ParseProviderScheme(ref), "catalog registration requires a provider path, not an HTTPS URL")
	reader, err := svc.GetFile(context.Background(), ref)
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, []byte("temporary image"), data)
	require.NoError(t, svc.DeleteFile(context.Background(), ref))
	require.Empty(t, transport.objects)
	require.Len(t, transport.requests, 3)
	for _, request := range transport.requests {
		require.Contains(t, request, "temporary-1250000000.cos.ap-beijing.myqcloud.com/")
	}
}

func TestOBSProxyBytesKeepProviderStorageIdentity(t *testing.T) {
	transport := &lifecycleTransport{objects: map[string][]byte{}}
	options := obsS3Options("https://obs.example.com", "region", "ak", "sk")
	options.HTTPClient = &http.Client{Transport: transport}
	options.Retryer = aws.NopRetryer{}
	svc := &obsFileService{client: s3.New(options), bucketName: "bucket", pathPrefix: "genyee", proxyDomain: "https://cdn.example.com"}
	ref, err := svc.SaveBytes(context.Background(), []byte("image"), 7, "image.png", false)
	require.NoError(t, err)
	require.Equal(t, "obs", types.ParseProviderScheme(ref), "the proxy URL is a presentation URL, not a catalog storage identity")
	public, err := svc.GetFileURL(context.Background(), ref)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(public, "https://cdn.example.com/genyee/7/"))
	require.NoError(t, svc.DeleteFile(context.Background(), ref))
	require.Empty(t, transport.objects)
}
