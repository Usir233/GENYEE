package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEndpointRequestIdentity(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	// One endpoint is shared by different sessions. Static model headers must
	// neither merge them nor leak one session into a call without a session.
	ep := Endpoint{
		Auth: BearerAuth("test-key"),
		Headers: map[string]string{
			"X-Conversation-ID": "static-id", "User-Agent": "custom-client",
			"X-Custom": "preserved",
		},
	}
	for _, sessionID := range []string{"session-a", "session-b", ""} {
		t.Run("session="+sessionID, func(t *testing.T) {
			ctx := types.WithSessionID(context.Background(), sessionID)
			for _, stream := range []bool{false, true} {
				req, _, err := ep.NewRequest(ctx, "http://127.0.0.1/v1/chat/completions", map[string]any{}, stream)
				require.NoError(t, err)
				require.Equal(t, sessionID, req.Header.Get("X-Conversation-ID"))
				require.Equal(t, "GENYEE/1.0", req.UserAgent())
				require.Equal(t, "preserved", req.Header.Get("X-Custom"))
				require.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
				if sessionID == "" {
					require.NotContains(t, req.Header, "X-Conversation-Id")
				}
			}
			require.Equal(t, "static-id", ep.Headers["X-Conversation-ID"])
		})
	}
}

func TestDoKeepsHeadersOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, nil)
	require.NoError(t, err)
	_, err = Endpoint{Client: srv.Client()}.Do(req)

	var httpErr *HTTPError
	require.True(t, errors.As(err, &httpErr))
	require.Equal(t, http.StatusTooManyRequests, httpErr.StatusCode)
	require.Equal(t, 12*time.Second, httpErr.RetryAfter())
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"absent", "", 0},
		{"seconds", "30", 30 * time.Second},
		{"seconds with spaces", " 5 ", 5 * time.Second},
		{"zero", "0", 0},
		{"negative", "-3", 0},
		{"http date", now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{"http date in the past", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"garbage", "tomorrow", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseRetryAfter(tc.value, now))
		})
	}
}

func TestRetryAfterOnNilError(t *testing.T) {
	var e *HTTPError
	require.Zero(t, e.RetryAfter())
	require.Zero(t, (&HTTPError{StatusCode: 503}).RetryAfter())
}

type failingRoundTripper struct {
	err error
}

func (f failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, f.err
}

func TestEndpointResolveDeterministicAndRedactsSecretsInLogsAndErrors(t *testing.T) {
	ep := Endpoint{
		BaseURL: "https://api.example.com/",
		Query: map[string]string{
			"zeta":  "3",
			"alpha": "1",
			"mid":   "2",
		},
	}
	const wantResolved = "https://api.example.com/v1/embeddings?alpha=1&mid=2&zeta=3"
	for i := 0; i < 30; i++ {
		require.Equal(t, wantResolved, ep.Resolve("/v1/embeddings"))
	}

	var logBuf bytes.Buffer
	logger.SetOutput(&logBuf)
	defer logger.SetOutput(os.Stdout)

	const (
		urlPass = "UrlPassSecret888"
		urlKey  = "AIzaQuerySecret777"
		hdrKey  = "sk-header-secret-token-999"
	)
	LogRequest(
		context.Background(),
		"https://admin:"+urlPass+"@llm.example.com/v1beta/models/gemini:generateContent?key="+urlKey+"&alt=sse",
		"gemini-pro",
		[]byte(`{"hello":"world"}`),
		true,
	)
	logged := logBuf.String()
	require.NotContains(t, logged, urlPass)
	require.NotContains(t, logged, urlKey)
	require.Contains(t, logged, "alt=sse")

	// TransportError redacts URL secrets and outbound auth credentials.
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://llm.example.com/v1?key="+urlKey,
		strings.NewReader(`{}`),
	)
	require.NoError(t, err)
	BearerAuth(hdrKey)(req, nil)

	_, err = Endpoint{
		Client: &http.Client{
			Transport: failingRoundTripper{
				err: errors.New("dial failed for https://admin:" + urlPass + "@llm.example.com/v1?key=" + urlKey + " auth=" + hdrKey),
			},
		},
	}.Do(req)
	require.Error(t, err)
	require.NotContains(t, err.Error(), urlPass)
	require.NotContains(t, err.Error(), urlKey)
	require.NotContains(t, err.Error(), hdrKey)

	// HTTPError redacts outbound API key if upstream echoes it back.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key ` + r.Header.Get("Authorization") + `"}`))
	}))
	defer srv.Close()

	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, nil)
	require.NoError(t, err)
	BearerAuth(hdrKey)(httpReq, nil)
	_, err = Endpoint{Client: srv.Client()}.Do(httpReq)
	require.Error(t, err)
	require.NotContains(t, err.Error(), hdrKey)
}

