package vlm

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/types"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

const (
	maxVLMImageBytes      = 20 << 20
	maxVLMTotalImageBytes = 64 << 20
	maxVLMPixels          = 40_000_000
	maxVLMImageSide       = 32_768
)

const (
	// defaultTimeout is the fallback HTTP timeout for a single VLM request.
	// Dense scanned-PDF OCR (full-page text + layout extraction) can take well
	// over a minute on slow endpoints, so this is intentionally generous and
	// can be raised further via VLM_HTTP_TIMEOUT_SECONDS.
	defaultTimeout = 180 * time.Second
	// defaultMaxToks is the fallback completion budget for a single VLM
	// request. Reasoning vision models spend part of it on hidden thinking
	// tokens, so a dense scanned page can stop at finish_reason=length; raise
	// it via VLM_MAX_TOKENS.
	defaultMaxToks = 5000
	defaultTemp    = 0.1
)

// vlmHTTPTimeout returns the HTTP client timeout for VLM requests, read from
// the VLM_HTTP_TIMEOUT_SECONDS env var when set (and positive), falling back to
// defaultTimeout otherwise. Shared by all OpenAI-compatible VLM backends.
func vlmHTTPTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("VLM_HTTP_TIMEOUT_SECONDS")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultTimeout
}

// vlmMaxTokens returns the completion budget for a single VLM request, read
// from the VLM_MAX_TOKENS env var when set (and positive), falling back to
// defaultMaxToks otherwise. The budget covers thinking tokens as well, so a
// reasoning vision model can exhaust the default on a dense page and fail OCR
// with finish_reason=length; this makes the cap adjustable without a rebuild.
func vlmMaxTokens() int {
	if v := strings.TrimSpace(os.Getenv("VLM_MAX_TOKENS")); v != "" {
		if tokens, err := strconv.Atoi(v); err == nil && tokens > 0 {
			return tokens
		}
	}
	return defaultMaxToks
}

// RemoteAPIVLM implements VLM on top of the catalog-driven chat client: a
// vision call is a chat call whose user turn carries images, so the vendor
// dialect (max_tokens field, sampling restrictions, protocol) is resolved
// exactly like it is for chat models.
type RemoteAPIVLM struct {
	modelName   string
	modelID     string
	chat        chat.Chat
	temperature float64
}

// NewRemoteAPIVLM creates a remote-API backed VLM instance.
func NewRemoteAPIVLM(config *Config) (*RemoteAPIVLM, error) {
	if config == nil {
		return nil, fmt.Errorf("vlm config is nil")
	}
	if err := validateVLMBaseURL(config.BaseURL); err != nil {
		return nil, err
	}

	temp := float64(defaultTemp)
	extra := make(map[string]string, len(config.Extra))
	for k, v := range config.Extra {
		if s, ok := v.(string); ok {
			extra[k] = s
		}
	}
	if raw, ok := extra["temperature"]; ok {
		if f, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 {
			temp = f
		}
	}

	client, err := chat.NewRemoteChat(&chat.ChatConfig{
		Source:        types.ModelSourceRemote,
		BaseURL:       config.BaseURL,
		ModelName:     config.ModelName,
		APIKey:        config.APIKey,
		ModelID:       config.ModelID,
		Provider:      config.Provider,
		ExtraConfig:   extra,
		CustomHeaders: config.CustomHeaders,
		Spec:          config.Spec,
	})
	if err != nil {
		return nil, err
	}
	return &RemoteAPIVLM{
		modelName:   config.ModelName,
		modelID:     config.ModelID,
		chat:        client,
		temperature: temp,
	}, nil
}

// Predict sends images with a text prompt through the chat client.
func (v *RemoteAPIVLM) Predict(ctx context.Context, imgBytesList [][]byte, prompt string) (string, error) {
	return v.PredictWithOptions(ctx, imgBytesList, prompt, nil)
}

// PredictWithOptions sends the same request, carrying a per-call thinking
// switch into the chat client. The switch rides on Options.Thinking, which the
// protocol layer turns into whatever key this vendor uses, so a model that only
// understands chat_template_kwargs still gets that spelling rather than a
// bare enable_thinking the server would ignore.
func (v *RemoteAPIVLM) PredictWithOptions(
	ctx context.Context, imgBytesList [][]byte, prompt string, opts *PredictOptions,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateVLMImages(imgBytesList); err != nil {
		return "", err
	}
	parts := []chat.MessageContentPart{{Type: "text", Text: prompt}}
	totalImageSize := 0
	for _, imgBytes := range imgBytesList {
		if len(imgBytes) == 0 {
			continue
		}
		totalImageSize += len(imgBytes)
		dataURI := fmt.Sprintf("data:%s;base64,%s",
			detectImageMIME(imgBytes), base64.StdEncoding.EncodeToString(imgBytes))
		parts = append(parts, chat.MessageContentPart{
			Type: "image_url", ImageURL: &chat.ImageURL{URL: dataURI, Detail: "auto"},
		})
	}
	logger.Infof(ctx, "[VLM] Calling chat protocol, model=%s, numImages=%d, totalImageSize=%d",
		v.modelName, len(imgBytesList), totalImageSize)

	maxTokens := vlmMaxTokens()
	chatOpts := &chat.ChatOptions{
		Temperature: v.temperature,
		MaxTokens:   maxTokens,
	}
	// Only an explicit preference is forwarded. The protocol layer skips the
	// thinking fields entirely when nobody asked for a level, which is what
	// leaves an always-on reasoning model running as its vendor intends.
	if opts != nil && opts.Thinking != nil {
		chatOpts.Thinking = opts.Thinking
	}

	ctx, cancel := context.WithTimeout(ctx, vlmHTTPTimeout())
	defer cancel()
	resp, err := v.chat.Chat(ctx, []chat.Message{{Role: "user", MultiContent: parts}}, chatOpts)
	if err != nil {
		return "", fmt.Errorf("VLM request: %w", err)
	}
	content := resp.Content
	if resp.FinishReason == "length" {
		// Non-empty partial output is still incomplete and must not be indexed
		// as successful OCR. Returning an error also marks the model trace failed.
		return "", fmt.Errorf("%w at %d tokens (finish_reason=length)", ErrTruncatedCompletion, maxTokens)
	}
	logger.Infof(ctx, "[VLM] response received, len=%d", len(content))
	return content, nil
}

func (v *RemoteAPIVLM) GetModelName() string { return v.modelName }
func (v *RemoteAPIVLM) GetModelID() string   { return v.modelID }

// detectImageMIME returns the MIME type for the given image bytes.
func detectImageMIME(data []byte) string {
	ct := http.DetectContentType(data)
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	return "image/png"
}

func validateVLMImages(imgBytesList [][]byte) error {
	total := 0
	for i, imgBytes := range imgBytesList {
		if len(imgBytes) == 0 {
			continue
		}
		if len(imgBytes) > maxVLMImageBytes {
			return fmt.Errorf("VLM image %d size %d bytes exceeds %d byte cap", i, len(imgBytes), maxVLMImageBytes)
		}
		total += len(imgBytes)
		if total > maxVLMTotalImageBytes {
			return fmt.Errorf("VLM total image payload %d bytes exceeds %d byte cap", total, maxVLMTotalImageBytes)
		}
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(imgBytes)); err == nil {
			if cfg.Width <= 0 || cfg.Height <= 0 {
				return fmt.Errorf("invalid VLM image dimensions %dx%d", cfg.Width, cfg.Height)
			}
			if cfg.Width > maxVLMImageSide || cfg.Height > maxVLMImageSide || int64(cfg.Width)*int64(cfg.Height) > maxVLMPixels {
				return fmt.Errorf("VLM image dimensions %dx%d exceed maximum %d pixels", cfg.Width, cfg.Height, maxVLMPixels)
			}
		}
	}
	return nil
}
