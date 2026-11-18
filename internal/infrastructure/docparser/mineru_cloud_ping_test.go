package docparser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func TestPingMinerUCloud_ContextPropagationBoundsAndEnglishMessages(t *testing.T) {
	allowLoopbackSSRF(t)

	ok, msg := PingMinerUCloud("   ")
	assert.False(t, ok)
	assert.NotEmpty(t, msg)
	assert.False(t, containsHan(msg), "expected English message for empty API key, got %q", msg)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	var hitCancelledServer bool
	cancelledServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitCancelledServer = true
		w.WriteHeader(http.StatusOK)
	}))
	defer cancelledServer.Close()

	ok, msg = PingMinerUCloudContext(cancelledCtx, cancelledServer.URL, "sk-test")
	assert.False(t, ok)
	assert.False(t, hitCancelledServer, "cancelled context must prevent outbound HTTP probe")
	assert.False(t, containsHan(msg), "expected English cancellation message, got %q", msg)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer sk-valid":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0}`))
		case "Bearer sk-500":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("upstream maintenance"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":401}`))
		}
	}))
	defer authServer.Close()

	ok, msg = PingMinerUCloudContext(context.Background(), authServer.URL, "sk-valid")
	assert.True(t, ok, msg)
	assert.Empty(t, msg)

	ok, msg = PingMinerUCloudContext(context.Background(), authServer.URL, "sk-invalid")
	assert.False(t, ok)
	assert.False(t, containsHan(msg), "expected English auth failure message, got %q", msg)
	assert.Contains(t, strings.ToLower(msg), "api key")

	ok, msg = PingMinerUCloudContext(context.Background(), authServer.URL, "sk-500")
	assert.False(t, ok, "5xx response must not report MinerU Cloud as healthy")
	assert.Contains(t, msg, "503")
	assert.False(t, containsHan(msg), "expected English 5xx failure message, got %q", msg)
}

func TestMinerUCloudReader_UploadFileIncludesBoundedErrorPreview(t *testing.T) {
	allowLoopbackSSRF(t)

	oversizedErr := strings.Repeat("upload-denied-", 512)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(oversizedErr))
	}))
	defer server.Close()

	reader := NewMinerUCloudReader(map[string]string{"mineru_api_key": "sk-test"})
	err := reader.uploadFile(context.Background(), server.URL+"/upload/doc.pdf", []byte("%PDF-1.4"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "upload-denied-")
	assert.LessOrEqual(t, len(err.Error()), maxErrorPreviewBytes+128)
}

func TestPingPaddleOCRVL_ContextPropagationAndEnglishMessages(t *testing.T) {
	allowLoopbackSSRF(t)

	ok, msg := PingPaddleOCRVL("")
	assert.False(t, ok)
	assert.False(t, containsHan(msg), "expected English message, got %q", msg)

	ok, msg = PingPaddleOCRVLCloud("")
	assert.False(t, ok)
	assert.False(t, containsHan(msg), "expected English message, got %q", msg)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	var hitServer bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitServer = true
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	ok, msg = PingPaddleOCRVLContext(cancelledCtx, server.URL)
	assert.False(t, ok)
	assert.False(t, hitServer, "cancelled context must prevent outbound HTTP probe")
	assert.False(t, containsHan(msg), "expected English message, got %q", msg)
}
