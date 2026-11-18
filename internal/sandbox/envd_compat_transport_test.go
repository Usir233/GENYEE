package sandbox

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// recordingRoundTripper captures the request the shim produced.
type recordingRoundTripper struct {
	request *http.Request
	body    []byte
}

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.request = req
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		r.body = body
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func TestEnvdCompatTransportAuthenticatesDataPlaneCalls(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := NewEnvdCompatTransport(recorder, DefaultSandboxExecUser)

	request := httptest.NewRequest(
		http.MethodPost,
		"https://49983-sbx.example.com/filesystem.Filesystem/MakeDir",
		strings.NewReader("{}"),
	)
	resp, err := transport.RoundTrip(request)
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Equal(t,
		"Basic "+base64.StdEncoding.EncodeToString([]byte(DefaultSandboxExecUser+":")),
		recorder.request.Header.Get("Authorization"),
	)
}

// A caller that already authenticated must win: the shim fills a gap, it does
// not override credentials.
func TestEnvdCompatTransportKeepsExistingAuthorization(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := NewEnvdCompatTransport(recorder, "user")

	request := httptest.NewRequest(
		http.MethodGet,
		"https://49983-sbx.example.com/files?path=/workspace/a.txt",
		nil,
	)
	request.Header.Set("Authorization", "Basic preset")
	resp, err := transport.RoundTrip(request)
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Equal(t, "Basic preset", recorder.request.Header.Get("Authorization"))
}

// Control-plane traffic shares the same transport, so the shim must leave it
// untouched - an unexpected Authorization header there would replace the API
// key the SDK sends.
func TestEnvdCompatTransportIgnoresControlPlaneCalls(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := NewEnvdCompatTransport(recorder, "user")

	request := httptest.NewRequest(http.MethodGet, "https://api.e2b.app/v2/sandboxes", nil)
	resp, err := transport.RoundTrip(request)
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Empty(t, recorder.request.Header.Get("Authorization"))
}

func TestEnvdCompatTransportRewritesUploadAsMultipart(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := NewEnvdCompatTransport(recorder, "user")

	payload := []byte("print('hi')\n")
	request := httptest.NewRequest(
		http.MethodPost,
		"https://49983-sbx.example.com/files?path=/workspace/script.py&username=user",
		bytes.NewReader(payload),
	)
	request.Header.Set("Content-Type", "application/octet-stream")
	resp, err := transport.RoundTrip(request)
	require.NoError(t, err)
	_ = resp.Body.Close()

	contentType := recorder.request.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	require.EqualValues(t, len(recorder.body), recorder.request.ContentLength)

	reader := multipart.NewReader(bytes.NewReader(recorder.body), params["boundary"])
	part, err := reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, envdUploadFormField, part.FormName())
	require.Equal(t, "script.py", part.FileName())
	content, err := io.ReadAll(part)
	require.NoError(t, err)
	require.Equal(t, payload, content)
}

// Uploads that already are multipart must pass through byte-for-byte.
func TestEnvdCompatTransportPreservesMultipartUploads(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := NewEnvdCompatTransport(recorder, "user")

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile(envdUploadFormField, "already.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("body"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	original := form.Bytes()

	request := httptest.NewRequest(
		http.MethodPost,
		"https://49983-sbx.example.com/files?path=/workspace/already.txt",
		bytes.NewReader(original),
	)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := transport.RoundTrip(request)
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Equal(t, writer.FormDataContentType(), recorder.request.Header.Get("Content-Type"))
	require.Equal(t, original, recorder.body)
}

// A download shares the /files route with uploads; only the POST body is
// rewritten.
func TestEnvdCompatTransportLeavesDownloadsAlone(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := NewEnvdCompatTransport(recorder, "user")

	request := httptest.NewRequest(
		http.MethodGet,
		"https://49983-sbx.example.com/files?path=/workspace/a.txt",
		nil,
	)
	resp, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Empty(t, recorder.request.Header.Get("Content-Type"))
	require.Empty(t, recorder.body)
}

type zeroTrackingReadCloser struct {
	remaining int64
	readBytes int64
	closed    bool
}

func (z *zeroTrackingReadCloser) Read(p []byte) (int, error) {
	if z.remaining <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > z.remaining {
		n = z.remaining
	}
	clear(p[:n])
	z.remaining -= n
	z.readBytes += n
	return int(n), nil
}

func (z *zeroTrackingReadCloser) Close() error {
	z.closed = true
	return nil
}

func TestEnvdCompatTransportRejectsOversizedUploadAndClosesBody(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := NewEnvdCompatTransport(recorder, "user")

	const overCap = (64 << 20) + (1 << 20)
	body := &zeroTrackingReadCloser{remaining: overCap}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://49983-sbx.example.com/files?path=/workspace/huge.bin&username=user",
		body,
	)
	request.Header.Set("Content-Type", "application/octet-stream")

	resp, err := transport.RoundTrip(request)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err, "oversized upload must be rejected instead of buffered unbounded into RAM")
	require.Nil(t, recorder.request, "oversized upload must not reach upstream transport")
	require.True(t, body.closed, "request body must still be closed on oversize rejection")
	require.LessOrEqual(t, body.readBytes, int64(64<<20)+1,
		"shim must stop reading immediately past the max upload ceiling")
}
