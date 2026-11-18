// Package volcengineknowledge implements Volcengine Knowledge Service rerank through its official SDK.
package volcengineknowledge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/volcengine/vikingdb-go-sdk/knowledge"
	knowledgemodel "github.com/volcengine/vikingdb-go-sdk/knowledge/model"
)

// SDK defaults follow the managed rerank service.
const (
	DefaultModel  = "doubao-seed-rerank"
	DefaultRegion = "cn-beijing"
	// DefaultInstruction is the console's default instruction, verbatim: "如需对齐控制台效果，请使用
	// 相同指令" (https://docs.volcengine.com/docs/vector_database_vikingdb/Rerank).
	DefaultInstruction = "Whether the document answers the query " +
		"or matches the content retrieval intent"
)

var (
	volcUserinfoRE    = regexp.MustCompile(`(https?://[^/\s:@]+):([^@\s/]+)@`)
	volcQuerySecretRE = regexp.MustCompile(
		`(?i)([?&](?:key|api_key|apikey|access_token|token|secret|password|sig|signature)=)[^&\s"']+`,
	)
)

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

type cappedRoundTripper struct {
	base     http.RoundTripper
	maxBytes int64
}

func (rt *cappedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	limit := rt.maxBytes
	if limit <= 0 {
		limit = api.MaxResponseBytes
	}
	holder, _ := req.Context().Value(transportErrKey{}).(*transportErrHolder)
	if holder != nil {
		if prev := holder.get(); errors.Is(prev, api.ErrResponseTooLarge) {
			return nil, prev
		}
	}

	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
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

// volcengineClient calls the managed Knowledge Service rerank through the
// vikingdb SDK, which owns the AK/SK signing. It implements api.Reranker and
// nothing else: the 200-document ceiling and the batch concurrency are
// declared on the vendor and enforced by protocolReranker.
type volcengineClient struct {
	modelName   string
	instruction string
	secretKey   string
	client      *knowledge.Client
}

// Config contains resolved connection parameters for the SDK client.
type Config struct {
	AccessKey, SecretKey, BaseURL, Model, Region, Instruction string
	Client                                                    *http.Client
}

func validateBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid Volcengine rerank base URL")
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("unsupported Volcengine rerank base URL scheme %q", parsed.Scheme)
	}
	if err := secutils.ValidateURLForSSRF(trimmed); err != nil {
		return "", redactVolcError(fmt.Errorf("volcengine rerank base URL SSRF check failed: %w", err), "")
	}
	return strings.TrimRight(trimmed, "/"), nil
}

// New creates an authenticated rerank protocol client.
func New(config Config) (api.Reranker, error) {
	accessKey, secretKey := strings.TrimSpace(config.AccessKey), strings.TrimSpace(config.SecretKey)
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("access key and secret key are required for Volcengine rerank")
	}
	baseURL, err := validateBaseURL(config.BaseURL)
	if err != nil {
		return nil, redactVolcError(err, secretKey)
	}
	modelName := strings.TrimSpace(config.Model)
	if modelName == "" {
		modelName = DefaultModel
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = DefaultRegion
	}
	instruction := strings.TrimSpace(config.Instruction)
	if instruction == "" {
		instruction = DefaultInstruction
	}
	httpClient := config.Client
	if httpClient == nil {
		httpClient = api.HTTPClient
	}
	wrappedHTTP := *httpClient
	wrappedHTTP.Transport = &cappedRoundTripper{
		base:     httpClient.Transport,
		maxBytes: api.MaxResponseBytes,
	}

	opts := []knowledge.ClientOption{
		knowledge.WithRegion(region),
		knowledge.WithTimeout(30 * time.Second),
		knowledge.WithHTTPClient(&wrappedHTTP),
		knowledge.WithMaxRetries(1),
	}
	if baseURL != "" {
		opts = append(opts, knowledge.WithEndpoint(baseURL))
	}

	client, err := knowledge.New(knowledge.AuthIAM(accessKey, secretKey), opts...)
	if err != nil {
		return nil, redactVolcError(fmt.Errorf("create Volcengine rerank client: %w", err), secretKey)
	}
	return &volcengineClient{
		modelName:   modelName,
		instruction: instruction,
		secretKey:   secretKey,
		client:      client,
	}, nil
}

// Rerank scores one batch. Every document is paired with the same query and
// instruction, and the reply is a score list in input order.
func (r *volcengineClient) Rerank(
	ctx context.Context, query string, documents []string,
) ([]api.RerankResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(documents) == 0 {
		return []api.RerankResult{}, nil
	}

	data := make([]knowledgemodel.RerankDataItem, len(documents))
	for i := range documents {
		data[i] = knowledgemodel.RerankDataItem{Query: query, Content: &documents[i]}
	}
	request := knowledgemodel.RerankRequest{
		Datas:             data,
		RerankModel:       &r.modelName,
		RerankInstruction: &r.instruction,
	}

	holder := &transportErrHolder{}
	callCtx := context.WithValue(ctx, transportErrKey{}, holder)
	response, err := r.client.Rerank(callCtx, request)
	if err != nil {
		if transportErr := holder.get(); errors.Is(transportErr, api.ErrResponseTooLarge) {
			return nil, redactVolcError(fmt.Errorf("call Volcengine rerank: %w", transportErr), r.secretKey)
		}
		return nil, redactVolcError(fmt.Errorf("call Volcengine rerank: %w", err), r.secretKey)
	}
	if response == nil || response.Data == nil {
		return nil, fmt.Errorf("volcengine rerank returned an empty response")
	}
	if response.Code != 0 {
		return nil, redactVolcError(
			fmt.Errorf("volcengine rerank API error %d: %s", response.Code, response.Message),
			r.secretKey,
		)
	}
	if len(response.Data.Scores) != len(documents) {
		return nil, fmt.Errorf(
			"volcengine rerank score count mismatch: got %d scores for %d documents",
			len(response.Data.Scores), len(documents),
		)
	}

	results := make([]api.RerankResult, len(documents))
	for i, score := range response.Data.Scores {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			return nil, fmt.Errorf("volcengine rerank score is non-finite (%v) at index %d", score, i)
		}
		results[i] = api.RerankResult{Index: i, Score: score, Text: documents[i]}
	}
	return results, nil
}

type redactedVolcErr struct {
	msg   string
	cause error
}

func (e *redactedVolcErr) Error() string        { return e.msg }
func (e *redactedVolcErr) Is(target error) bool { return errors.Is(e.cause, target) }

func redactVolcError(err error, secret string) error {
	if err == nil {
		return nil
	}
	raw := err.Error()
	out := volcUserinfoRE.ReplaceAllString(raw, "${1}:[REDACTED]@")
	out = volcQuerySecretRE.ReplaceAllString(out, "${1}[REDACTED]")
	if secret != "" {
		out = strings.ReplaceAll(out, secret, "[REDACTED]")
	}
	if out == raw {
		return err
	}
	return &redactedVolcErr{msg: out, cause: err}
}
