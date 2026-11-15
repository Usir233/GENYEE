// Package langfuse implements GENYEE's Langfuse/LiteFuse LLM tracing client on
// top of the OpenTelemetry Go SDK. It records chat/embedding/rerank/VLM/ASR
// generations plus the surrounding agent/HTTP/asynq spans as OTLP/HTTP spans
// to a Langfuse v3+ or LiteFuse backend (POST /api/public/otel/v1/traces).
//
// The integration is fully opt-in: when disabled (the default), every public
// entry point is a cheap no-op, so callers can wire them unconditionally. A
// W3C traceparent propagated from upstream callers (e.g. sop3) is inherited so
// GENYEE's trace shares the upstream trace id — correlating runs across
// services in the LiteFuse UI.
package langfuse

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	defaultLangfuseFlushAt        = 15
	defaultLangfuseFlushInterval  = 3 * time.Second
	defaultLangfuseQueueSize      = 2048
	defaultLangfuseRequestTimeout = 10 * time.Second
	maxLangfuseQueueSize          = 65536
	maxLangfuseFlushAt            = 4096
)

// Config holds the runtime configuration for the Langfuse client.
//
// In practice users enable Langfuse purely through environment variables —
// Host / PublicKey / SecretKey — which matches every other Langfuse SDK and
// keeps GENYEE's YAML config free of secrets.
type Config struct {
	// Enabled is the master switch. If false the entire package is a no-op.
	Enabled bool
	// Host is the Langfuse base URL, e.g. https://cloud.langfuse.com or
	// https://us.cloud.langfuse.com or a self-hosted address.
	Host string
	// PublicKey / SecretKey are the project credentials used for Basic Auth.
	PublicKey string
	SecretKey string
	// FlushAt flushes the queued events once the buffer reaches this size.
	FlushAt int
	// FlushInterval is the maximum time between automatic flushes.
	FlushInterval time.Duration
	// QueueSize bounds the in-memory buffer to avoid unbounded growth if the
	// Langfuse endpoint is unreachable.
	QueueSize int
	// RequestTimeout is the HTTP timeout for a single ingestion batch.
	RequestTimeout time.Duration
	// Release / Environment are attached to every trace for filtering in the
	// Langfuse UI (e.g. release="v0.4.2", environment="production").
	Release     string
	Environment string
	// SampleRate (0..1) controls trace sampling. 0 means "use 1.0".
	SampleRate float64
	// Debug enables verbose logging of batch send errors.
	Debug bool

	// testExporter, when non-nil, replaces the real OTLP exporter. Tests use
	// it to inject an in-memory span exporter (tracetest.InMemoryExporter) so
	// they can assert on exported spans deterministically without an HTTP
	// server. Init registers it with a SimpleSpanProcessor (synchronous
	// export on span End) when set. Unexported — production never sets it.
	testExporter sdktrace.SpanExporter
}

// LoadConfigFromEnv builds a Config by reading the LANGFUSE_* environment
// variables, mirroring the official Python / JS SDK conventions.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Host:           strings.TrimSpace(os.Getenv("LANGFUSE_HOST")),
		PublicKey:      strings.TrimSpace(os.Getenv("LANGFUSE_PUBLIC_KEY")),
		SecretKey:      strings.TrimSpace(os.Getenv("LANGFUSE_SECRET_KEY")),
		Release:        strings.TrimSpace(os.Getenv("LANGFUSE_RELEASE")),
		Environment:    strings.TrimSpace(os.Getenv("LANGFUSE_ENVIRONMENT")),
		FlushAt:        15,
		FlushInterval:  3 * time.Second,
		QueueSize:      2048,
		RequestTimeout: 10 * time.Second,
		SampleRate:     1.0,
	}

	if v := strings.TrimSpace(os.Getenv("LANGFUSE_ENABLED")); v != "" {
		cfg.Enabled = parseBool(v) && cfg.Host != ""
	} else if cfg.Host != "" && cfg.PublicKey != "" && cfg.SecretKey != "" {
		// Auto-enable when host and credentials are present.
		cfg.Enabled = true
	}

	if v := strings.TrimSpace(os.Getenv("LANGFUSE_FLUSH_AT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FlushAt = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_FLUSH_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.FlushInterval = d
		} else if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FlushInterval = time.Duration(n) * time.Second
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_QUEUE_SIZE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.QueueSize = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_REQUEST_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.RequestTimeout = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_SAMPLE_RATE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			cfg.SampleRate = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_DEBUG")); v != "" {
		cfg.Debug = parseBool(v)
	}

	if cfg.SampleRate == 0 {
		cfg.SampleRate = 1.0
	}

	return normalizeConfig(cfg)
}

func normalizeConfig(cfg Config) Config {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.PublicKey = strings.TrimSpace(cfg.PublicKey)
	cfg.SecretKey = strings.TrimSpace(cfg.SecretKey)
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultLangfuseQueueSize
	} else if cfg.QueueSize > maxLangfuseQueueSize {
		cfg.QueueSize = maxLangfuseQueueSize
	}
	if cfg.FlushAt <= 0 {
		cfg.FlushAt = defaultLangfuseFlushAt
	} else if cfg.FlushAt > maxLangfuseFlushAt {
		cfg.FlushAt = maxLangfuseFlushAt
	}
	if cfg.FlushAt > cfg.QueueSize {
		cfg.FlushAt = cfg.QueueSize
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultLangfuseFlushInterval
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultLangfuseRequestTimeout
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 1.0
	}
	return cfg
}

// Validate verifies required fields are present when Langfuse / standard OTLP is enabled.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	host := strings.TrimSpace(c.Host)
	if host == "" {
		return fmt.Errorf("langfuse: host is required when enabled")
	}
	u, err := url.Parse(host)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("langfuse: host must be a valid http(s) URL")
	}
	if math.IsNaN(c.SampleRate) || math.IsInf(c.SampleRate, 0) || c.SampleRate < 0 || c.SampleRate > 1 {
		return fmt.Errorf("langfuse: sample_rate must be between 0 and 1")
	}
	// Allow keyless standard OpenTelemetry / Jaeger collector connections.
	// If credentials are provided, both public_key and secret_key are required.
	if (c.PublicKey != "" && c.SecretKey == "") || (c.PublicKey == "" && c.SecretKey != "") {
		return fmt.Errorf("langfuse: both public_key and secret_key are required when credentials are provided")
	}
	return nil
}

var (
	reURLUserInfo       = regexp.MustCompile(`(?i)(https?://)([^/\s@]+)@`)
	reQuerySecretParam  = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|apikey|key|access[_-]?token|refresh[_-]?token|id[_-]?token|token|client[_-]?secret|secret[_-]?key|secret|password|passwd|pwd|auth|authorization|sig|signature|x-amz-signature|x-amz-security-token)=)[^&\s"']+`)
	reAuthHeaderScheme  = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{6,}`)
	reHeaderKeySecret   = regexp.MustCompile(`(?i)((?:x-api-key|api-key|authorization|proxy-authorization)\s*[:=]\s*)[^\s"',;]+`)
	reProviderKeyToken  = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|pk-lf-[A-Za-z0-9_-]{6,}|sk-lf-[A-Za-z0-9_-]{6,}|lf_[A-Za-z0-9_-]{8,}|AIza[0-9A-Za-z_-]{16,})\b`)
	reJSONSecretKeyPair = regexp.MustCompile(`(?i)("(?:api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|id[_-]?token|client[_-]?secret|secret[_-]?key|secret|password|passwd|pwd|authorization|auth[_-]?token|private[_-]?key|token)"\s*:\s*)"(?:\\.|[^"\\])*"`)
)

func isSensitiveTraceKey(rawKey string) bool {
	k := strings.ToLower(strings.TrimSpace(rawKey))
	if k == "" {
		return false
	}
	switch k {
	case "api_key", "apikey", "api-key", "x-api-key", "key",
		"token", "access_token", "access-token", "refresh_token", "refresh-token", "id_token",
		"secret", "secret_key", "secret-key", "client_secret", "client-secret", "private_key",
		"password", "passwd", "pwd", "auth", "authorization", "cookie",
		"sig", "signature", "x-amz-signature", "x-amz-security-token", "x-goog-signature":
		return true
	}
	return strings.HasSuffix(k, "_secret") ||
		strings.HasSuffix(k, "-secret") ||
		strings.HasSuffix(k, "_token") ||
		strings.HasSuffix(k, "-token") ||
		strings.HasSuffix(k, "_password") ||
		strings.HasSuffix(k, "-password") ||
		strings.HasSuffix(k, "_api_key") ||
		strings.HasSuffix(k, "_apikey")
}

func redactHTTPQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	parts := strings.Split(rawQuery, "&")
	for i, part := range parts {
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			if reProviderKeyToken.MatchString(part) {
				parts[i] = "[REDACTED]"
			}
			continue
		}
		rawKey := part[:eq]
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			key = rawKey
		}
		val := part[eq+1:]
		if isSensitiveTraceKey(key) || reProviderKeyToken.MatchString(val) {
			parts[i] = rawKey + "=[REDACTED]"
		}
	}
	joined := strings.Join(parts, "&")
	const maxQueryBytes = 2048
	if len(joined) > maxQueryBytes {
		return trimUTF8Bytes([]byte(joined)[:maxQueryBytes]) + "..."
	}
	return joined
}

func redactLangfuseText(s string) string {
	if s == "" {
		return ""
	}
	out := reURLUserInfo.ReplaceAllString(s, "${1}[REDACTED]@")
	out = reQuerySecretParam.ReplaceAllString(out, "${1}[REDACTED]")
	out = reAuthHeaderScheme.ReplaceAllString(out, "${1} [REDACTED]")
	out = reHeaderKeySecret.ReplaceAllString(out, "${1}[REDACTED]")
	out = reJSONSecretKeyPair.ReplaceAllString(out, `${1}"[REDACTED]"`)
	out = reProviderKeyToken.ReplaceAllString(out, "[REDACTED]")
	return out
}

// trimUTF8Bytes converts b to a valid UTF-8 string while dropping any
// incomplete multi-byte rune at the end of a byte-sliced buffer.
func trimUTF8Bytes(b []byte) string {
	for len(b) > 0 && !utf8.Valid(b) {
		r, size := utf8.DecodeLastRune(b)
		if r == utf8.RuneError && size == 1 {
			// Check whether the trailing 1..3 bytes are a valid prefix of a multi-byte UTF-8 rune.
			b = b[:len(b)-1]
			continue
		}
		break
	}
	if !utf8.Valid(b) {
		return strings.ToValidUTF8(string(b), "")
	}
	return string(b)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	}
	return false
}
