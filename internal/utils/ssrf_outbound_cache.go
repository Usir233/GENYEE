package utils

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// outboundSSRFValidationTTL bounds how long a successful RoundTrip/redirect
// validation for the same origin may be reused. DNS rebinding at the TCP sink
// is still blocked by SSRFSafeDialContext on every new connection.
const outboundSSRFValidationTTL = 60 * time.Second

var (
	ssrfOutboundCacheState     atomic.Pointer[outboundSSRFValidationState]
	ssrfOutboundValidateMisses atomic.Uint64 // test-only counter
)

// Requests retain one state for their entire lookup. Reloading the whitelist
// replaces the state without resetting a mutex another goroutine is using;
// late results stay in the retired state and cannot populate the new cache.
type outboundSSRFValidationState struct {
	entries sync.Map // string -> *ssrfOutboundCacheEntry
	group   singleflight.Group
}

func currentOutboundSSRFState() *outboundSSRFValidationState {
	if state := ssrfOutboundCacheState.Load(); state != nil {
		return state
	}
	ssrfOutboundCacheState.CompareAndSwap(nil, &outboundSSRFValidationState{})
	return ssrfOutboundCacheState.Load()
}

type ssrfOutboundCacheEntry struct {
	err       error
	expiresAt time.Time
}

// validateURLForSSRFForOutbound validates URLs on the hot outbound path
// (RoundTripper, redirect checks). Results are cached per origin for a short
// TTL so high-frequency clients do not repeat DNS lookups on every request.
// Handler/input boundaries should keep calling ValidateURLForSSRF directly.
func validateURLForSSRFForOutbound(rawURL string) error {
	return validateURLForSSRFForOutboundContext(context.Background(), rawURL)
}

func validateURLForSSRFForOutboundContext(ctx context.Context, rawURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rawURL == "" {
		return nil
	}

	cacheKey, ok := outboundSSRFCacheKey(rawURL)
	if !ok {
		return ValidateURLForSSRFContext(ctx, rawURL)
	}

	state := currentOutboundSSRFState()
	now := time.Now()
	if cached, ok := state.entries.Load(cacheKey); ok {
		entry := cached.(*ssrfOutboundCacheEntry)
		if now.Before(entry.expiresAt) {
			return entry.err
		}
		state.entries.CompareAndDelete(cacheKey, entry)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result := state.group.DoChan(cacheKey, func() (any, error) {
			if cached, ok := state.entries.Load(cacheKey); ok {
				entry := cached.(*ssrfOutboundCacheEntry)
				if time.Now().Before(entry.expiresAt) {
					return entry.err, entry.err
				}
			}

			ssrfOutboundValidateMisses.Add(1)
			validationErr := ValidateURLForSSRFContext(ctx, rawURL)
			// Cancellation belongs to this caller, not the origin's SSRF policy.
			if !errors.Is(validationErr, context.Canceled) && !errors.Is(validationErr, context.DeadlineExceeded) {
				state.entries.Store(cacheKey, &ssrfOutboundCacheEntry{
					err:       validationErr,
					expiresAt: time.Now().Add(outboundSSRFValidationTTL),
				})
			}
			return validationErr, validationErr
		})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case settled := <-result:
			// Another caller may have started the shared lookup and canceled
			// it. A still-active waiter retries with its own context; no
			// canceled result is cached or propagated to unrelated requests.
			if errors.Is(settled.Err, context.Canceled) || errors.Is(settled.Err, context.DeadlineExceeded) {
				continue
			}
			return settled.Err
		}
	}
}

func outboundSSRFCacheKey(rawURL string) (string, bool) {
	normalized := rawURL
	if !strings.Contains(normalized, "://") {
		normalized = "https://" + normalized
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", false
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}

	host, hostErr := normalizeSSRFHostname(parsed.Hostname())
	if hostErr != "" || host == "" {
		return "", false
	}

	port := parsed.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	var origin string
	if strings.Contains(host, ":") {
		origin = fmt.Sprintf("%s://[%s]:%s", scheme, host, port)
	} else {
		origin = fmt.Sprintf("%s://%s:%s", scheme, host, port)
	}

	return origin, true
}

func invalidateSSRFOutboundValidationCache() {
	ssrfOutboundCacheState.Store(&outboundSSRFValidationState{})
}

// ResetSSRFOutboundValidationCacheForTest clears the outbound validation cache.
// NOT for production use.
func ResetSSRFOutboundValidationCacheForTest() {
	invalidateSSRFOutboundValidationCache()
	ssrfOutboundValidateMisses.Store(0)
}

// SSRFOutboundValidationMissesForTest returns how many uncached outbound
// validations have run since the last ResetSSRFOutboundValidationCacheForTest.
func SSRFOutboundValidationMissesForTest() uint64 {
	return ssrfOutboundValidateMisses.Load()
}
