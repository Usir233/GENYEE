package tencentlkeap

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRerankRejectsNilScoreInWireResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"ScoreList":[0.92,null],"RequestId":"req-1"}}`))
	}))
	defer server.Close()

	client, err := New(Config{
		SecretID:  "AKID-test",
		SecretKey: "SK-test",
		BaseURL:   server.URL,
		Client:    server.Client(),
	})
	require.NoError(t, err)

	_, err = client.Rerank(context.Background(), "query", []string{"doc0", "doc1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score is nil at index 1")
}

func TestResultsFromScoresRejectsNilAndNonFiniteScores(t *testing.T) {
	valid := 0.75
	nan := math.NaN()
	posInf := math.Inf(1)
	negInf := math.Inf(-1)

	for _, tc := range []struct {
		name   string
		scores []*float64
		want   string
	}{
		{name: "nil score", scores: []*float64{nil, &valid}, want: "score is nil at index 0"},
		{name: "NaN score", scores: []*float64{&valid, &nan}, want: "non-finite"},
		{name: "+Inf score", scores: []*float64{&posInf, &valid}, want: "non-finite"},
		{name: "-Inf score", scores: []*float64{&valid, &negInf}, want: "non-finite"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resultsFromScores([]string{"a", "b"}, tc.scores)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestNewHonorsBaseURLAndCustomPath(t *testing.T) {
	var seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"ScoreList":[0.61],"RequestId":"req-2"}}`))
	}))
	defer server.Close()

	client, err := New(Config{
		SecretID:  "AKID-test",
		SecretKey: "SK-test",
		BaseURL:   server.URL + "/gateway/lkeap/",
		Client:    server.Client(),
	})
	require.NoError(t, err)

	got, err := client.Rerank(context.Background(), "query", []string{"doc0"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.InDelta(t, 0.61, got[0].Score, 1e-6)
	assert.Equal(t, "/gateway/lkeap", seenPath)
}

func TestNewRejectsMalformedBaseURL(t *testing.T) {
	for _, raw := range []string{
		"ftp://example.com",
		"not-a-url",
		"https://user:pass@example.com",
		"https://example.com?x=1",
		"https://example.com#frag",
	} {
		_, err := New(Config{
			SecretID:  "AKID-test",
			SecretKey: "SK-test",
			BaseURL:   raw,
		})
		require.Error(t, err, "baseURL %q should fail", raw)
	}
}

func TestRerankDefaultClientEnforcesRequestDeadline(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	prev := defaultLKEAPRequestTimeout
	defaultLKEAPRequestTimeout = 60 * time.Millisecond
	t.Cleanup(func() { defaultLKEAPRequestTimeout = prev })

	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	}))
	defer server.Close()
	defer close(unblock)

	client, err := New(Config{
		SecretID:  "AKID-test",
		SecretKey: "SK-test",
		BaseURL:   server.URL,
	})
	require.NoError(t, err)

	start := time.Now()
	_, err = client.Rerank(context.Background(), "query", []string{"doc0"})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
}
