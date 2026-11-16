// Package pineconerank implements Pinecone Inference's standalone rerank API.
// https://docs.pinecone.io/guides/search/rerank-results
package pineconerank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/Usir233/GENYEE/internal/models/api"
)

// Config holds the Pinecone endpoint and rerank request settings.
type Config struct {
	Endpoint api.Endpoint
	Settings api.RerankSettings
}

// Client calls Pinecone's standalone rerank API.
type Client struct{ cfg Config }

// New creates a Pinecone rerank client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

func (c *Client) url() string {
	if c.cfg.Endpoint.URL != "" {
		return c.cfg.Endpoint.Resolve("")
	}
	path := c.cfg.Settings.Path
	if path == "" {
		path = "/rerank"
	}
	if strings.HasSuffix(strings.TrimRight(c.cfg.Endpoint.BaseURL, "/"), path) {
		path = ""
	}
	return c.cfg.Endpoint.Resolve(path)
}

type document struct {
	Text string `json:"text"`
}

type request struct {
	Model           string         `json:"model"`
	Query           string         `json:"query"`
	Documents       []document     `json:"documents"`
	TopN            int            `json:"top_n"`
	RankFields      []string       `json:"rank_fields"`
	ReturnDocuments bool           `json:"return_documents"`
	Parameters      map[string]any `json:"parameters,omitempty"`
}

// BuildRequestBody constructs the native Pinecone body. Asking for every
// candidate and suppressing echoed documents preserves GENYEE's index mapping
// while avoiding unnecessary response bytes.
func (c *Client) BuildRequestBody(query string, documents []string) (map[string]any, error) {
	docs := make([]document, len(documents))
	for i, text := range documents {
		docs[i] = document{Text: text}
	}
	body := request{
		Model: c.cfg.Endpoint.Model, Query: query, Documents: docs,
		TopN: len(documents), RankFields: []string{"text"},
	}
	if c.cfg.Settings.Truncate != "" {
		body.Parameters = map[string]any{"truncate": c.cfg.Settings.Truncate}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	for key, value := range c.cfg.Settings.ExtraBody {
		if _, exists := out[key]; !exists {
			out[key] = value
		}
	}
	return out, nil
}

type response struct {
	Data *[]result `json:"data"`
}

type result struct {
	Index *int     `json:"index"`
	Score *float64 `json:"score"`
}

// Rerank returns scores for the supplied documents in Pinecone's ranked order.
func (c *Client) Rerank(
	ctx context.Context, query string, documents []string,
) ([]api.RerankResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(documents) == 0 {
		return nil, nil
	}
	body, err := c.BuildRequestBody(query, documents)
	if err != nil {
		return nil, err
	}
	var decoded response
	if err := c.cfg.Endpoint.PostJSON(ctx, c.url(), body, &decoded); err != nil {
		return nil, redactPineconeError(err)
	}
	if decoded.Data == nil {
		return nil, fmt.Errorf("pinecone rerank response has no data array")
	}
	if len(*decoded.Data) != len(documents) {
		return nil, fmt.Errorf(
			"pinecone rerank returned %d scores for %d documents", len(*decoded.Data), len(documents),
		)
	}
	out := make([]api.RerankResult, 0, len(*decoded.Data))
	seen := make([]bool, len(documents))
	for _, item := range *decoded.Data {
		if item.Index == nil || item.Score == nil {
			return nil, fmt.Errorf("pinecone rerank result is missing index or score")
		}
		if *item.Index < 0 || *item.Index >= len(documents) {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", *item.Index, len(documents))
		}
		if math.IsNaN(*item.Score) || math.IsInf(*item.Score, 0) {
			return nil, fmt.Errorf("pinecone rerank score is non-finite (%v) at index %d", *item.Score, *item.Index)
		}
		if seen[*item.Index] {
			return nil, fmt.Errorf("pinecone rerank returned duplicate index %d", *item.Index)
		}
		seen[*item.Index] = true
		out = append(out, api.RerankResult{
			Index: *item.Index, Score: *item.Score, Text: documents[*item.Index],
		})
	}
	return out, nil
}

var (
	pineconeUserinfoRE    = regexp.MustCompile(`(https?://[^/\s:@]+):([^@\s/]+)@`)
	pineconeQuerySecretRE = regexp.MustCompile(`(?i)([?&](?:key|api_key|apikey|access_token|token|secret|password|sig|signature)=)[^&\s"']+`)
)

type redactedPineconeErr struct {
	msg   string
	cause error
}

func (e *redactedPineconeErr) Error() string        { return e.msg }
func (e *redactedPineconeErr) Is(target error) bool { return errors.Is(e.cause, target) }
func (e *redactedPineconeErr) As(target any) bool {
	switch target.(type) {
	case **api.HTTPError, **api.TransportError:
		return errors.As(e.cause, target)
	default:
		return false
	}
}

func redactPineconeError(err error) error {
	if err == nil {
		return nil
	}
	raw := err.Error()
	out := pineconeUserinfoRE.ReplaceAllString(raw, "${1}:[REDACTED]@")
	out = pineconeQuerySecretRE.ReplaceAllString(out, "${1}[REDACTED]")
	if out == raw {
		return err
	}
	return &redactedPineconeErr{msg: out, cause: err}
}
