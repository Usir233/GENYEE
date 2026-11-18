// Package tencentlkeap implements Tencent RunRerank through its official SDK.
package tencentlkeap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	lkeap "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/lkeap/v20240522"
)

// SDK defaults follow the managed rerank service.
const (
	// LKEAPRerankEndpoint 腾讯云知识引擎原子能力 Rerank API 域名
	LKEAPRerankEndpoint = "lkeap.tencentcloudapi.com"
	// LKEAPDefaultRegion RunRerank 支持的地域，默认广州
	LKEAPDefaultRegion = "ap-guangzhou"
	// LKEAPDefaultRerankModel 默认 rerank 模型名
	LKEAPDefaultRerankModel = "lke-reranker-base"
)

var defaultLKEAPRequestTimeout = 60 * time.Second

// Config contains resolved connection parameters for the SDK client.
type Config struct {
	SecretID, SecretKey, Region, Model, BaseURL, Endpoint, Scheme string
	Client                                                        *http.Client
}

type transportErrKey struct{}

type transportErrHolder struct {
	mu  sync.Mutex
	err error
}

func (h *transportErrHolder) set(err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
}

func (h *transportErrHolder) get() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// httpClientRoundTripper adapts an *http.Client (including its CheckRedirect
// policy and Timeout) to http.RoundTripper so Tencent Cloud SDK's
// WithHttpTransport honors SSRF redirect validation, request deadlines, and
// bounded response reads.
type httpClientRoundTripper struct {
	client   *http.Client
	maxBytes int64
}

func (rt httpClientRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	limit := rt.maxBytes
	if limit <= 0 {
		limit = api.MaxResponseBytes
	}
	holder, _ := req.Context().Value(transportErrKey{}).(*transportErrHolder)

	cloned := req.Clone(req.Context())
	cloned.RequestURI = ""
	resp, err := rt.client.Do(cloned)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if holder != nil {
			holder.set(err)
		}
		return nil, err
	}
	if resp.ContentLength > limit {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		overErr := fmt.Errorf("%w (%d bytes > %d cap)", api.ErrResponseTooLarge, resp.ContentLength, limit)
		if holder != nil {
			holder.set(overErr)
		}
		return nil, overErr
	}
	if resp.Body != nil {
		resp.Body = &cappedReadCloser{
			rc:        resp.Body,
			remaining: limit,
			limit:     limit,
			holder:    holder,
		}
	}
	return resp, nil
}

type cappedReadCloser struct {
	rc        io.ReadCloser
	remaining int64
	limit     int64
	holder    *transportErrHolder
}

func (c *cappedReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.remaining < 0 {
		err := fmt.Errorf("%w (%d bytes)", api.ErrResponseTooLarge, c.limit)
		if c.holder != nil {
			c.holder.set(err)
		}
		return 0, err
	}
	maxRead := c.remaining + 1
	if int64(len(p)) > maxRead {
		p = p[:maxRead]
	}
	n, err := c.rc.Read(p)
	c.remaining -= int64(n)
	if c.remaining < 0 {
		overErr := fmt.Errorf("%w (%d bytes)", api.ErrResponseTooLarge, c.limit)
		if c.holder != nil {
			c.holder.set(overErr)
		}
		return n, overErr
	}
	return n, err
}

func (c *cappedReadCloser) Close() error {
	return c.rc.Close()
}

func resolveEndpointAndScheme(config Config) (string, string, error) {
	if rawURL := strings.TrimSpace(config.BaseURL); rawURL != "" {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", fmt.Errorf("invalid LKEAP base URL")
		}
		if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
			return "", "", fmt.Errorf("unsupported LKEAP base URL scheme %q", parsed.Scheme)
		}
		cleanPath := strings.TrimRight(parsed.Path, "/")
		return parsed.Host + cleanPath, strings.ToUpper(parsed.Scheme), nil
	}

	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		endpoint = LKEAPRerankEndpoint
	}
	if strings.Contains(endpoint, "://") ||
		strings.ContainsAny(endpoint, "@?#") ||
		strings.IndexFunc(endpoint, unicode.IsSpace) >= 0 {
		return "", "", fmt.Errorf("invalid LKEAP endpoint")
	}
	scheme := strings.ToUpper(strings.TrimSpace(config.Scheme))
	if scheme == "" {
		scheme = "HTTPS"
	}
	if scheme != "HTTPS" && scheme != "HTTP" {
		return "", "", fmt.Errorf("unsupported LKEAP scheme %q", scheme)
	}
	return endpoint, scheme, nil
}

// lkeapClient calls Tencent Cloud's RunRerank action through the official
// SDK, which owns the TC3 signing this API requires. It implements
// api.Reranker and nothing else: the per-request ceilings (60 documents,
// 2000 characters for Query plus Docs together) are declared on the vendor
// and enforced by protocolReranker, like every other vendor's.
type lkeapClient struct {
	modelName      string
	secretKey      string
	defaultTimeout time.Duration
	client         *lkeap.Client
}

// New creates an authenticated rerank protocol client.
func New(config Config) (api.Reranker, error) {
	secretID, secretKey := strings.TrimSpace(config.SecretID), strings.TrimSpace(config.SecretKey)
	if secretID == "" || secretKey == "" {
		return nil, fmt.Errorf("secret_id and secret_key are required for LKEAP rerank (set API Key and Secret Key)")
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = LKEAPDefaultRegion
	}
	endpoint, scheme, err := resolveEndpointAndScheme(config)
	if err != nil {
		return nil, err
	}
	httpClient := config.Client
	if httpClient == nil {
		httpClient = api.HTTPClient
	}

	credential := common.NewCredential(secretID, secretKey)
	cpf := profile.NewClientProfile()
	cpf.DisableRegionBreaker = true
	cpf.HttpProfile.Endpoint = endpoint
	cpf.HttpProfile.Scheme = scheme
	if httpClient != nil && httpClient.Timeout > 0 {
		cpf.HttpProfile.ReqTimeout = 0
	}

	client, err := lkeap.NewClient(credential, region, cpf)
	if err != nil {
		return nil, redactStringInError(fmt.Errorf("create LKEAP client: %w", err), secretKey)
	}
	if httpClient != nil {
		client.WithHttpTransport(httpClientRoundTripper{
			client:   httpClient,
			maxBytes: api.MaxResponseBytes,
		})
	}

	modelName := strings.TrimSpace(config.Model)
	if modelName == "" {
		modelName = LKEAPDefaultRerankModel
	}
	var fallbackTimeout time.Duration
	if httpClient == nil || httpClient.Timeout <= 0 {
		fallbackTimeout = defaultLKEAPRequestTimeout
	}
	return &lkeapClient{
		modelName:      modelName,
		secretKey:      secretKey,
		defaultTimeout: fallbackTimeout,
		client:         client,
	}, nil
}

// Rerank scores one batch. RunRerank answers a bare score list in input
// order rather than an indexed, sorted result set.
func (r *lkeapClient) Rerank(
	ctx context.Context, query string, documents []string,
) ([]api.RerankResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(documents) == 0 {
		return []api.RerankResult{}, nil
	}
	if r.defaultTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = api.WithLLMTimeout(ctx, r.defaultTimeout)
		defer cancel()
	}

	req := lkeap.NewRunRerankRequest()
	req.Query = common.StringPtr(query)
	req.Docs = common.StringPtrs(documents)
	req.Model = common.StringPtr(r.modelName)

	holder := &transportErrHolder{}
	callCtx := context.WithValue(ctx, transportErrKey{}, holder)
	resp, err := r.client.RunRerankWithContext(callCtx, req)
	if err != nil {
		if transportErr := holder.get(); transportErr != nil {
			if errors.Is(transportErr, api.ErrResponseTooLarge) {
				return nil, redactStringInError(fmt.Errorf("LKEAP RunRerank: %w", transportErr), r.secretKey)
			}
			return nil, redactStringInError(fmt.Errorf("LKEAP RunRerank: %w: %w", err, transportErr), r.secretKey)
		}
		return nil, redactStringInError(fmt.Errorf("LKEAP RunRerank: %w", err), r.secretKey)
	}
	if resp == nil || resp.Response == nil || len(resp.Response.ScoreList) == 0 {
		return nil, fmt.Errorf("LKEAP rerank API returned empty score list")
	}
	return resultsFromScores(documents, resp.Response.ScoreList)
}

func redactStringInError(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, secret, "[REDACTED]"))
}

func resultsFromScores(documents []string, scores []*float64) ([]api.RerankResult, error) {
	if len(scores) != len(documents) {
		return nil, fmt.Errorf("LKEAP rerank score count mismatch: got %d scores for %d documents",
			len(scores), len(documents))
	}

	results := make([]api.RerankResult, len(documents))
	for i, score := range scores {
		if score == nil {
			return nil, fmt.Errorf("LKEAP rerank score is nil at index %d", i)
		}
		if math.IsNaN(*score) || math.IsInf(*score, 0) {
			return nil, fmt.Errorf("LKEAP rerank score is non-finite (%v) at index %d", *score, i)
		}
		results[i] = api.RerankResult{
			Index: i,
			Text:  documents[i],
			Score: *score,
		}
	}
	return results, nil
}
