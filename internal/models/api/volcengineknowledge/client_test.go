package volcengineknowledge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func allowLoopback(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestNewValidatesBaseURLAndRedactsSecrets(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	for _, raw := range []string{
		"ftp://example.com",
		"file:///etc/passwd",
		"http://127.0.0.1:8080",
		"https://admin:supersecretpw@example.com",
		"https://example.com?api_key=supersecretkey",
		"https://example.com#frag",
	} {
		_, err := New(Config{
			AccessKey: "AK-test",
			SecretKey: "SK-supersecret-value",
			BaseURL:   raw,
		})
		require.Error(t, err, "baseURL %q should be rejected", raw)
		assert.NotContains(t, err.Error(), "supersecretpw")
		assert.NotContains(t, err.Error(), "supersecretkey")
		assert.NotContains(t, err.Error(), "SK-supersecret-value")
	}

	// Empty BaseURL must fall back to the SDK's default endpoint rather than failing.
	c, err := New(Config{
		AccessKey: "AK-test",
		SecretKey: "SK-test",
	})
	require.NoError(t, err)
	require.NotNil(t, c)
}

func TestRerankNilContextAndEmptyDocumentsShortCircuit(t *testing.T) {
	allowLoopback(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"scores":[0.88]}}`))
	}))
	defer srv.Close()

	c, err := New(Config{
		AccessKey: "AK-test",
		SecretKey: "SK-test",
		BaseURL:   srv.URL,
		Client:    srv.Client(),
	})
	require.NoError(t, err)

	//nolint:staticcheck // verifying defensive nil-context handling
	empty, err := c.Rerank(nil, "q", nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.Zero(t, hits.Load(), "empty document slice must not issue network RPC")

	//nolint:staticcheck // verifying defensive nil-context handling
	got, err := c.Rerank(nil, "q", []string{"doc-1"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.InDelta(t, 0.88, got[0].Score, 1e-6)
	assert.Equal(t, int32(1), hits.Load())
}

func TestRerankCapsOversizedResponsesWithoutRetrying(t *testing.T) {
	allowLoopback(t)

	t.Run("content-length header over cap", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.FormatInt(api.MaxResponseBytes+1, 10))
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c, err := New(Config{
			AccessKey: "AK-test",
			SecretKey: "SK-secret-token",
			BaseURL:   srv.URL,
			Client:    srv.Client(),
		})
		require.NoError(t, err)

		_, err = c.Rerank(context.Background(), "q", []string{"doc"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, api.ErrResponseTooLarge), "got error: %v", err)
		assert.Equal(t, int32(1), hits.Load(), "oversized reply must not be retried")
	})

	t.Run("chunked body over cap", func(t *testing.T) {
		var hits atomic.Int32
		chunk := make([]byte, 64<<10)
		for i := range chunk {
			chunk[i] = ' '
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			total := int64(0)
			for total <= api.MaxResponseBytes+int64(len(chunk)) {
				n, werr := w.Write(chunk)
				total += int64(n)
				if werr != nil {
					return
				}
			}
		}))
		defer srv.Close()

		c, err := New(Config{
			AccessKey: "AK-test",
			SecretKey: "SK-secret-token",
			BaseURL:   srv.URL,
			Client:    srv.Client(),
		})
		require.NoError(t, err)

		_, err = c.Rerank(context.Background(), "q", []string{"doc"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, api.ErrResponseTooLarge), "got error: %v", err)
		assert.Equal(t, int32(1), hits.Load(), "oversized stream must not be retried")
	})
}

func TestRerankRedactsSecretKeyInError(t *testing.T) {
	allowLoopback(t)
	const secret = "SK-ultra-secret-998877"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":401,"message":"invalid signature for ` + secret + `"}`))
	}))
	defer srv.Close()

	c, err := New(Config{
		AccessKey: "AK-test",
		SecretKey: secret,
		BaseURL:   srv.URL,
		Client:    srv.Client(),
	})
	require.NoError(t, err)

	_, err = c.Rerank(context.Background(), "q", []string{"doc"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
}
