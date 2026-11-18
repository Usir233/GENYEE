package web_search

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// maxJSONResponseBytes bounds every provider response body. A search result set
// is JSON metadata over a page of hits; a larger body means a broken or
// misconfigured endpoint, not more results.
const maxJSONResponseBytes = 16 << 20

// maxErrorPreviewBytes bounds how much of a failed response is echoed into a log
// line or an error string. Without it a failing endpoint can inject an unbounded
// blob into both.
const maxErrorPreviewBytes = 4 << 10

// errResponseTooLarge marks a body over the cap. It is deterministic: the same
// request returns the same oversized body, so callers must not retry it.
var errResponseTooLarge = errors.New("response exceeds maximum size")

// readCapped reads a response body, refusing anything larger than limit instead
// of buffering it. Oversized payloads are reported as an error: a truncated body
// would otherwise be parsed as if it were the whole response. A non-positive
// limit (e.g. a provider built without one) falls back to maxJSONResponseBytes.
//
// Mirrors the cap the datasource connectors already apply to their API bodies.
func readCapped(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = maxJSONResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", errResponseTooLarge, limit)
	}
	return data, nil
}

// readErrorPreview reads just enough of a failed body to put in a log line or an
// error message. It truncates rather than refusing: a preview is diagnostic, and
// the interesting part of a large error body is its start. Read errors are
// dropped because the caller is already on the error path and is about to report
// the status code, which is the useful part.
func readErrorPreview(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, maxErrorPreviewBytes))
	return trimPartialUTF8(string(data))
}

// trimPartialUTF8 drops a trailing incomplete rune. io.LimitReader stops on a
// byte count, so a preview of a Chinese error body can end mid-rune; that would
// put invalid UTF-8 into logs and API error responses.
func trimPartialUTF8(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// errorPreviewString renders a failed response body for an error message. It is
// bounded like maxErrorPreviewBytes and never ends in the middle of a UTF-8
// rune: providers such as Zhipu, Metaso and Bocha answer with Chinese text, and
// cutting on a byte index would put invalid UTF-8 into logs and API responses.
func errorPreviewString(body []byte) string {
	detail := strings.TrimSpace(string(body))
	if len(detail) > maxErrorPreviewBytes {
		detail = detail[:maxErrorPreviewBytes]
	}
	return trimPartialUTF8(detail)
}

var (
	urlUserinfoSecretRE = regexp.MustCompile(`(https?://[^/\s:@]+):([^@\s/]+)@`)
	urlQuerySecretRE    = regexp.MustCompile(
		`(?i)([?&](?:key|api_key|apikey|access_token|token|secret|password)=)[^&\s"']+`,
	)
)

// redactURLForDisplay masks basic-auth credentials and secret query parameters
// before a URL is written to logs or surfaced in an error message.
func redactURLForDisplay(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Scheme != "" && u.Host != "" {
		if u.User != nil {
			if _, hasPass := u.User.Password(); hasPass {
				u.User = url.UserPassword(u.User.Username(), "REDACTED")
			}
		}
		return redactSecretText(u.String())
	}
	return redactSecretText(rawURL)
}

func redactSecretText(s string, secrets ...string) string {
	out := urlUserinfoSecretRE.ReplaceAllString(s, "${1}:[REDACTED]@")
	out = urlQuerySecretRE.ReplaceAllString(out, "${1}[REDACTED]")
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		out = strings.ReplaceAll(out, secret, "[REDACTED]")
		if esc := url.QueryEscape(secret); esc != "" && esc != secret {
			out = strings.ReplaceAll(out, esc, "[REDACTED]")
		}
	}
	return out
}

type redactedSearchError struct {
	msg   string
	cause error
}

func (e *redactedSearchError) Error() string        { return e.msg }
func (e *redactedSearchError) Is(target error) bool { return errors.Is(e.cause, target) }

func redactSearchError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	raw := err.Error()
	redacted := redactSecretText(raw, secrets...)
	if redacted == raw {
		return err
	}
	return &redactedSearchError{msg: redacted, cause: err}
}

type cappedReadCloser struct {
	body      io.ReadCloser
	remaining int64
	limit     int64
}

func newCappedReadCloser(body io.ReadCloser, limit int64) io.ReadCloser {
	if limit <= 0 {
		limit = maxJSONResponseBytes
	}
	return &cappedReadCloser{body: body, remaining: limit + 1, limit: limit}
}

func (c *cappedReadCloser) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, fmt.Errorf("%w (%d bytes)", errResponseTooLarge, c.limit)
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.body.Read(p)
	c.remaining -= int64(n)
	if c.remaining <= 0 {
		return 0, fmt.Errorf("%w (%d bytes)", errResponseTooLarge, c.limit)
	}
	return n, err
}

func (c *cappedReadCloser) Close() error {
	return c.body.Close()
}
