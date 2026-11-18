// Package dashscopeembeddings implements Alibaba Model Studio's native
// multimodal embedding shape: input.contents, parameters.dimension, and
// results under output.embeddings.
//
// Only the multimodal models use it. DashScope's text embeddings are served
// on the OpenAI-compatible endpoint, which is why the vendor's default
// protocol is the OpenAI one and the multimodal entries override it.
//
// https://help.aliyun.com/zh/model-studio/multimodal-embedding-api-reference
package dashscopeembeddings

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/Usir233/GENYEE/internal/models/api"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint   api.Endpoint
	Settings   api.EmbeddingsSettings
	Dimensions int
	Retry      api.RetryPolicy
}

// Client talks DashScope's native multimodal embedding to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

type response struct {
	Output struct {
		Embeddings []struct {
			// The position field is `index`. DashScope's *text* embedding API
			// one page over calls it `text_index`, and decoding that name
			// here yields 0 for every element, collapsing a whole batch onto
			// slot 0 (upstream issue #3484).
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
			Type      string    `json:"type"`
		} `json:"embeddings"`
	} `json:"output"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BuildRequestBody is the golden-test entry point.
func (c *Client) BuildRequestBody(texts []string, _ api.EmbedInputType) map[string]any {
	contents := make([]any, 0, len(texts))
	for _, text := range texts {
		contents = append(contents, map[string]any{"text": text})
	}
	return c.body(contents)
}

// BuildImageRequestBody is the golden-test entry point for images. Each
// content is {"image": URL or "data:image/{format};base64,{data}"}.
func (c *Client) BuildImageRequestBody(images []api.EmbedImage, _ api.EmbedInputType) map[string]any {
	contents := make([]any, 0, len(images))
	for _, img := range images {
		contents = append(contents, map[string]any{"image": img.DataURI()})
	}
	return c.body(contents)
}

func (c *Client) body(contents []any) map[string]any {
	body := map[string]any{
		"model": c.cfg.Endpoint.Model,
		"input": map[string]any{"contents": contents},
	}
	// The width lives under parameters, not at the top level, and the field
	// is singular: `dimension`.
	if c.cfg.Settings.DimensionsField != "" && c.cfg.Dimensions > 0 {
		body["parameters"] = map[string]any{c.cfg.Settings.DimensionsField: c.cfg.Dimensions}
	}
	for k, v := range c.cfg.Settings.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body
}

// Embed vectorizes one batch.
func (c *Client) Embed(
	ctx context.Context, texts []string, kind api.EmbedInputType,
) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return c.post(ctx, c.BuildRequestBody(texts, kind), len(texts))
}

// AcceptsImages is always true: the image part is part of the schema.
func (c *Client) AcceptsImages() bool { return true }

// EmbedImages vectorizes one batch of images, each its own vector.
func (c *Client) EmbedImages(
	ctx context.Context, images []api.EmbedImage, kind api.EmbedInputType,
) ([][]float32, error) {
	if len(images) == 0 {
		return nil, nil
	}
	return c.post(ctx, c.BuildImageRequestBody(images, kind), len(images))
}

func (c *Client) post(ctx context.Context, body map[string]any, want int) ([][]float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var decoded response
	url := c.cfg.Endpoint.Resolve(c.cfg.Settings.Path)
	err := c.cfg.Endpoint.PostJSONWithRetry(ctx, url, body, &decoded, c.cfg.Retry, "embedding")
	if err != nil {
		return nil, redactDashScopeEmbedError(err)
	}
	// DashScope reports some failures in the body with a 200.
	if decoded.Code != "" {
		return nil, fmt.Errorf("DashScope embedding error %s: %s", decoded.Code, decoded.Message)
	}
	return api.PlaceEmbeddings(want, len(decoded.Output.Embeddings), func(i int) (int, []float32) {
		return decoded.Output.Embeddings[i].Index, decoded.Output.Embeddings[i].Embedding
	})
}

var (
	dashscopeEmbedUserinfoRE    = regexp.MustCompile(`(https?://[^/\s:@]+):([^@\s/]+)@`)
	dashscopeEmbedQuerySecretRE = regexp.MustCompile(
		`(?i)([?&](?:key|api_key|apikey|access_token|token|secret|password|sig|signature)=)[^&\s"']+`,
	)
)

type redactedDashScopeEmbedErr struct {
	msg   string
	cause error
}

func (e *redactedDashScopeEmbedErr) Error() string        { return e.msg }
func (e *redactedDashScopeEmbedErr) Is(target error) bool { return errors.Is(e.cause, target) }
func (e *redactedDashScopeEmbedErr) As(target any) bool {
	switch target.(type) {
	case **api.HTTPError, **api.TransportError:
		return errors.As(e.cause, target)
	default:
		return false
	}
}

func redactDashScopeEmbedError(err error) error {
	if err == nil {
		return nil
	}
	raw := err.Error()
	out := dashscopeEmbedUserinfoRE.ReplaceAllString(raw, "${1}:[REDACTED]@")
	out = dashscopeEmbedQuerySecretRE.ReplaceAllString(out, "${1}[REDACTED]")
	if out == raw {
		return err
	}
	return &redactedDashScopeEmbedErr{msg: out, cause: err}
}
