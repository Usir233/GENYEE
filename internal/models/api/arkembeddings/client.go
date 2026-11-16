// Package arkembeddings implements Volcengine Ark's multimodal embedding
// shape: an input array of typed parts answering a single `data` object.
//
// The response is one fused vector for the whole input, not one per element —
// the reference's own example returns
//
//	"data": { "embedding": [...], "object": "embedding" }
//
// for a request carrying a video, an image and a text. So a batch of N texts
// is N requests, which the vendor declares as a batch size of one rather than
// this package looping.
//
// It is the only embedding API Ark still documents. Its retired text
// endpoint (/api/v3/embeddings) was the plain OpenAI shape, which is why rows
// naming a text model resolve to that protocol instead.
//
// https://docs.volcengine.com/docs/ark/multimodal-vectorization-api
package arkembeddings

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

// Client talks Ark's multimodal embedding to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

type response struct {
	Data struct {
		Embedding []float32 `json:"embedding"`
		Object    string    `json:"object"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// BuildRequestBody is the golden-test entry point.
func (c *Client) BuildRequestBody(texts []string, _ api.EmbedInputType) map[string]any {
	input := make([]any, 0, len(texts))
	for _, text := range texts {
		input = append(input, map[string]any{"type": "text", "text": text})
	}
	return c.body(input)
}

// BuildImageRequestBody is the golden-test entry point for images. The
// reference takes an image as a URL or as
// "data:image/{图片格式};base64,{图片Base64编码}" under image_url.url.
func (c *Client) BuildImageRequestBody(images []api.EmbedImage, _ api.EmbedInputType) map[string]any {
	input := make([]any, 0, len(images))
	for _, img := range images {
		input = append(input, map[string]any{
			"type": "image_url", "image_url": map[string]any{"url": img.DataURI()},
		})
	}
	return c.body(input)
}

func (c *Client) body(input []any) map[string]any {
	body := map[string]any{
		"model": c.cfg.Endpoint.Model,
		"input": input,
	}
	if c.cfg.Settings.SendEncodingFormat {
		body["encoding_format"] = "float"
	}
	if c.cfg.Settings.DimensionsField != "" && c.cfg.Dimensions > 0 {
		body[c.cfg.Settings.DimensionsField] = c.cfg.Dimensions
	}
	for k, v := range c.cfg.Settings.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body
}

// Embed vectorizes one input. The vendor fuses everything it is sent into a
// single vector, so the caller must hand over exactly one text; the shared
// batching layer guarantees that through the declared batch size.
func (c *Client) Embed(
	ctx context.Context, texts []string, kind api.EmbedInputType,
) ([][]float32, error) {
	if len(texts) != 1 {
		return nil, fmt.Errorf(
			"ark embedding fuses its input into one vector, so it takes one text per request, got %d",
			len(texts),
		)
	}
	return c.post(ctx, c.BuildRequestBody(texts, kind))
}

// AcceptsImages is always true: the image part is part of the schema.
func (c *Client) AcceptsImages() bool { return true }

// EmbedImages vectorizes one image, for the same reason Embed takes one text.
func (c *Client) EmbedImages(
	ctx context.Context, images []api.EmbedImage, kind api.EmbedInputType,
) ([][]float32, error) {
	if len(images) != 1 {
		return nil, fmt.Errorf(
			"ark embedding fuses its input into one vector, so it takes one image per request, got %d",
			len(images),
		)
	}
	return c.post(ctx, c.BuildImageRequestBody(images, kind))
}

func (c *Client) post(ctx context.Context, body map[string]any) ([][]float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var decoded response
	url := c.cfg.Endpoint.Resolve(c.cfg.Settings.Path)
	err := c.cfg.Endpoint.PostJSONWithRetry(ctx, url, body, &decoded, c.cfg.Retry, "embedding")
	if err != nil {
		return nil, redactArkEmbedError(err)
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return nil, fmt.Errorf("ark embedding error %s: %s", decoded.Error.Code, decoded.Error.Message)
	}
	return validateArkVector(decoded.Data.Embedding)
}

func validateArkVector(vec []float32) ([][]float32, error) {
	if len(vec) == 0 {
		return nil, fmt.Errorf("ark embedding returned no vector")
	}
	out := [][]float32{vec}
	if err := api.ValidateEmbeddingVectors(out); err != nil {
		return nil, err
	}
	return out, nil
}

var (
	arkEmbedUserinfoRE    = regexp.MustCompile(`(https?://[^/\s:@]+):([^@\s/]+)@`)
	arkEmbedQuerySecretRE = regexp.MustCompile(`(?i)([?&](?:key|api_key|apikey|access_token|token|secret|password|sig|signature)=)[^&\s"']+`)
)

type redactedArkEmbedErr struct {
	msg   string
	cause error
}

func (e *redactedArkEmbedErr) Error() string        { return e.msg }
func (e *redactedArkEmbedErr) Is(target error) bool { return errors.Is(e.cause, target) }
func (e *redactedArkEmbedErr) As(target any) bool {
	switch target.(type) {
	case **api.HTTPError, **api.TransportError:
		return errors.As(e.cause, target)
	default:
		return false
	}
}

func redactArkEmbedError(err error) error {
	if err == nil {
		return nil
	}
	raw := err.Error()
	out := arkEmbedUserinfoRE.ReplaceAllString(raw, "${1}:[REDACTED]@")
	out = arkEmbedQuerySecretRE.ReplaceAllString(out, "${1}[REDACTED]")
	if out == raw {
		return err
	}
	return &redactedArkEmbedErr{msg: out, cause: err}
}
