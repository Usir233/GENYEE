package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

const (
	embedWebhookTimeout       = 5 * time.Second
	maxConcurrentEmbedWebhook = 32
)

var embedWebhookSem = make(chan struct{}, maxConcurrentEmbedWebhook)

// ErrEmbedWebhookURLInvalid is returned when a webhook URL fails format or SSRF checks.
var ErrEmbedWebhookURLInvalid = errors.New("invalid embed webhook URL")

func newEmbedWebhookHTTPClient() *http.Client {
	return secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
		Timeout:      embedWebhookTimeout,
		MaxRedirects: 5,
	})
}

// ValidateEmbedWebhookURL checks an optional outbound webhook URL. Empty is allowed.
func ValidateEmbedWebhookURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: webhook URL must be a valid http(s) URL", ErrEmbedWebhookURLInvalid)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%w: webhook URL must use http or https", ErrEmbedWebhookURLInvalid)
	}
	if err := secutils.ValidateURLForSSRF(trimmed); err != nil {
		if hint := secutils.FormatSSRFError("Webhook URL", trimmed, err); hint != "" {
			return fmt.Errorf("%w: %s", ErrEmbedWebhookURLInvalid, hint)
		}
		return fmt.Errorf("%w: %v", ErrEmbedWebhookURLInvalid, err)
	}
	return nil
}

// DispatchEmbedWebhook POSTs an event to the channel webhook URL (best-effort, async).
func DispatchEmbedWebhook(ch *types.EmbedChannel, eventType, sessionID string, payload map[string]any) {
	if ch == nil {
		return
	}
	url := strings.TrimSpace(ch.WebhookURL)
	if url == "" {
		return
	}
	if err := ValidateEmbedWebhookURL(url); err != nil {
		logger.Warnf(context.Background(), "[embed_webhook] skip dispatch %s: %v", eventType, err)
		return
	}
	body := make(map[string]any, len(payload)+4)
	for k, v := range payload {
		body[k] = v
	}
	body["type"] = eventType
	body["channel_id"] = ch.ID
	body["session_id"] = sessionID
	body["timestamp"] = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	secret := ""
	if rawSecret := strings.TrimSpace(ch.WebhookSecret); rawSecret != "" {
		decrypted, decErr := secutils.DecryptStoredSecret(rawSecret)
		if decErr != nil {
			logger.Warnf(context.Background(), "[embed_webhook] skip dispatch %s: decrypt webhook secret failed: %v", eventType, decErr)
			return
		}
		secret = strings.TrimSpace(decrypted)
	}
	select {
	case embedWebhookSem <- struct{}{}:
	default:
		logger.Warnf(context.Background(), "[embed_webhook] drop dispatch %s: webhook queue full", eventType)
		return
	}
	go func() {
		defer func() { <-embedWebhookSem }()
		ctx, cancel := context.WithTimeout(context.Background(), embedWebhookTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "GENYEE-Embed-Webhook/1.0")
		if secret != "" {
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write(raw)
			sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-Genyee-Signature", sig)
		}
		resp, err := newEmbedWebhookHTTPClient().Do(req)
		if err != nil {
			logger.Warnf(context.Background(), "[embed_webhook] dispatch %s failed: %v", eventType, err)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode >= 300 {
			logger.Warnf(context.Background(), "[embed_webhook] dispatch %s HTTP %d", eventType, resp.StatusCode)
		}
	}()
}

// SignEmbedWebhookBody returns the hex HMAC signature for tests.
func SignEmbedWebhookBody(secret string, raw []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}
