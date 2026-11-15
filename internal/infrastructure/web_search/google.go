package web_search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/api/customsearch/v1"
	"google.golang.org/api/option"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

// GoogleProvider implements web search using Google Custom Search Engine API
type GoogleProvider struct {
	srv      *customsearch.Service
	apiKey   string
	engineID string
}

type googleAPIKeyTransport struct {
	base   http.RoundTripper
	apiKey string
}

func (t *googleAPIKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	q := cloned.URL.Query()
	q.Set("key", t.apiKey)
	cloned.URL.RawQuery = q.Encode()

	resp, err := t.base.RoundTrip(cloned)
	if err != nil {
		return nil, redactSearchError(err, t.apiKey)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		preview := redactSecretText(readErrorPreview(resp.Body), t.apiKey)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(strings.NewReader(preview))
		return resp, nil
	}
	resp.Body = newCappedReadCloser(resp.Body, maxJSONResponseBytes)
	return resp, nil
}

// NewGoogleProvider creates a new Google provider from parameters (no environment variables).
// The API endpoint is the official Google Custom Search endpoint — not tenant-configurable.
func NewGoogleProvider(params types.WebSearchProviderParameters) (interfaces.WebSearcher, error) {
	apiKey := strings.TrimSpace(params.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required for Google provider")
	}
	engineID := strings.TrimSpace(params.EngineID)
	if engineID == "" {
		return nil, fmt.Errorf("engine ID is required for Google provider")
	}

	httpClient, err := NewSearchHTTPClient(30*time.Second, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	// Official Google CSE endpoint never requires following redirects; blocking
	// redirects ensures the API key query parameter cannot be forwarded elsewhere.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	httpClient.Transport = &googleAPIKeyTransport{
		base:   httpClient.Transport,
		apiKey: apiKey,
	}

	srv, err := customsearch.NewService(context.Background(), option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, redactSearchError(err, apiKey)
	}
	return &GoogleProvider{
		srv:      srv,
		apiKey:   apiKey,
		engineID: engineID,
	}, nil
}

// Name returns the provider name
func (p *GoogleProvider) Name() string {
	return "google"
}

// Search performs a web search using Google Custom Search Engine API
func (p *GoogleProvider) Search(
	ctx context.Context,
	request types.WebSearchRequest,
) ([]*types.WebSearchResult, error) {
	query, maxResults := request.Query, request.MaxResults
	if len(query) == 0 {
		return nil, fmt.Errorf("query is empty")
	}
	logger.Infof(ctx, "[WebSearch][Google] query=%q maxResults=%d engineID=%s", query, maxResults, p.engineID)
	cseCall := p.srv.Cse.List().Context(ctx).Cx(p.engineID).Q(query)

	if maxResults > 0 {
		cseCall = cseCall.Num(int64(maxResults))
	} else {
		cseCall = cseCall.Num(5)
	}
	cseCall = cseCall.Hl("ch-zh")

	resp, err := cseCall.Do()
	if err != nil {
		safeErr := redactSearchError(err, p.apiKey)
		logger.Warnf(ctx, "[WebSearch][Google] failed: %v", safeErr)
		return nil, safeErr
	}
	results := make([]*types.WebSearchResult, 0)
	for _, item := range resp.Items {
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.Link,
			Snippet: item.Snippet,
			Source:  "google",
		}
		results = append(results, result)
	}
	logger.Infof(ctx, "[WebSearch][Google] returned %d results", len(results))
	return results, nil
}
