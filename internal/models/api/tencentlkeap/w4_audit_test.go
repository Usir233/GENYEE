package tencentlkeap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRerankCapsOversizedResponseAndHandlesNilContextAndEmptyBatch(t *testing.T) {
	// 1. Oversized Content-Length / body must fail with api.ErrResponseTooLarge
	// rather than reading unbounded bytes into memory.
	bodyBytes := bytes.Repeat([]byte("a"), 2048)
	customClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: api.MaxResponseBytes + 1024,
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				Body:          io.NopCloser(bytes.NewReader(bodyBytes)),
				Request:       req,
			}, nil
		}),
	}
	c, err := New(Config{
		SecretID:  "AKID-test",
		SecretKey: "SK-super-secret-value",
		Endpoint:  "lkeap.tencentcloudapi.com",
		Client:    customClient,
	})
	require.NoError(t, err)

	_, err = c.Rerank(context.Background(), "query", []string{"doc0"})
	require.Error(t, err)
	assert.ErrorIs(t, err, api.ErrResponseTooLarge)

	// 2. Chunked stream exceeding limit (unknown ContentLength = -1) must also fail with ErrResponseTooLarge.
	holder := &transportErrHolder{}
	capped := &cappedReadCloser{
		rc:        io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 512))),
		remaining: 128,
		limit:     128,
		holder:    holder,
	}
	_, streamErr := io.ReadAll(capped)
	require.Error(t, streamErr)
	assert.ErrorIs(t, streamErr, api.ErrResponseTooLarge)
	assert.ErrorIs(t, holder.get(), api.ErrResponseTooLarge)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"ScoreList":[0.91],"RequestId":"req-ok"}}`))
	}))
	defer server.Close()

	okClient, err := New(Config{
		SecretID:  "AKID-test",
		SecretKey: "SK-super-secret-value",
		BaseURL:   server.URL,
		Client:    server.Client(),
	})
	require.NoError(t, err)

	// Empty document slice must return empty result without failing on empty ScoreList.
	emptyOut, err := okClient.Rerank(context.Background(), "query", nil)
	require.NoError(t, err)
	assert.Empty(t, emptyOut)

	// Nil context must not panic inside context.WithValue.
	var nilCtx context.Context
	got, err := okClient.Rerank(nilCtx, "query", []string{"doc0"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.InDelta(t, 0.91, got[0].Score, 1e-6)
}

func TestNewRedactsCredentialsAndValidatesEndpointAndScheme(t *testing.T) {
	// 1. Embedded credentials in invalid BaseURL must NEVER appear in the error message.
	const leakedPass = "my-ultra-secret-password-98765"
	_, err := New(Config{
		SecretID:  "AKID-test",
		SecretKey: "SK-test",
		BaseURL:   "https://admin:" + leakedPass + "@example.com/rerank",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), leakedPass, "BaseURL password leaked in error string")

	// 2. Invalid Scheme or malformed Endpoint when BaseURL is empty must be rejected.
	for _, badCfg := range []Config{
		{SecretID: "AKID", SecretKey: "SK", Scheme: "FTP"},
		{SecretID: "AKID", SecretKey: "SK", Scheme: "gopher"},
		{SecretID: "AKID", SecretKey: "SK", Endpoint: "https://evil.example.com/path"},
		{SecretID: "AKID", SecretKey: "SK", Endpoint: "user:pass@evil.example.com"},
		{SecretID: "AKID", SecretKey: "SK", Endpoint: "evil.example.com?foo=bar"},
		{SecretID: "AKID", SecretKey: "SK", Endpoint: "evil.example.com#frag"},
		{SecretID: "AKID", SecretKey: "SK", Endpoint: "evil .example.com"},
	} {
		_, err := New(badCfg)
		require.Error(t, err, "config %+v should be rejected", badCfg)
	}

	// 3. SecretKey leaked by an upstream transport/proxy error must be redacted.
	const secretKey = "SK-do-not-leak-me-12345"
	leakingClient := &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("proxy auth failed for key " + secretKey)
		}),
	}
	c, err := New(Config{
		SecretID:  "AKID-test",
		SecretKey: secretKey,
		Client:    leakingClient,
	})
	require.NoError(t, err)
	_, err = c.Rerank(context.Background(), "q", []string{"doc"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), secretKey), "SecretKey leaked in Rerank error: %v", err)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
