// Inbound traffic-token registry.
//
// Cube and E2B sandboxes only accept requests carrying their per-sandbox
// traffic token. Cube's SDK attaches that header
// itself once Sandbox.TrafficAccessToken is set, but go-e2b stores the token
// and never sends it — so for E2B the header has to be added by the one
// component GENYEE owns on that path: the data-plane RoundTripper.
//
// Because SandboxGatewayTransportPool is shared across tenants, keying the
// registry by sandboxID alone would let a malicious tenant whose custom
// control plane returns a victim sandboxID trigger cross-tenant injection of
// the victim's e2b-traffic-access-token. Entries are therefore isolated by the
// composite key (tenantID, configID, gatewayOrigin, sandboxID), cleared when
// updated with an empty token, and bounded by a TTL.

package sandbox

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// InboundTokenHeader is the header both providers accept. Cube additionally
// accepts "cube-traffic-access-token"; sending the E2B spelling covers both.
const InboundTokenHeader = "e2b-traffic-access-token"

// DefaultInboundTokenTTL bounds how long an unrefreshed inbound token entry
// remains in memory when a sandbox expires upstream without an explicit Delete.
const DefaultInboundTokenTTL = 24 * time.Hour

// InboundTokenScope isolates inbound traffic tokens across workspaces, named
// sandbox configs, and data-plane gateway origins.
type InboundTokenScope struct {
	TenantID      uint64
	ConfigID      string
	GatewayOrigin string
}

func (s InboundTokenScope) normalized() InboundTokenScope {
	return InboundTokenScope{
		TenantID:      s.TenantID,
		ConfigID:      strings.TrimSpace(s.ConfigID),
		GatewayOrigin: strings.ToLower(strings.TrimSpace(s.GatewayOrigin)),
	}
}

// InboundTokenScopeFor derives the composite isolation scope for a client or
// transport built from cfg.
func InboundTokenScopeFor(cfg *Config) InboundTokenScope {
	if cfg == nil {
		return InboundTokenScope{}
	}
	return InboundTokenScope{
		TenantID:      cfg.ConfigTenantID,
		ConfigID:      strings.TrimSpace(cfg.ConfigID),
		GatewayOrigin: inboundGatewayOriginFor(cfg),
	}
}

func inboundGatewayOriginFor(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	gatewayURL, controlURL := gatewayEndpointFor(cfg)
	if host, port, scheme, ok := parseProxyURL(gatewayURL); ok {
		hostPort := net.JoinHostPort(strings.ToLower(host), strconv.Itoa(port))
		return fmt.Sprintf("%s://%s", strings.ToLower(scheme), hostPort)
	}
	controlOrigin := canonicalURLOrigin(controlURL)
	domain := strings.ToLower(strings.TrimSpace(sandboxDomainFor(cfg)))
	if controlOrigin == "" && domain == "" {
		return ""
	}
	return controlOrigin + "|" + domain
}

func canonicalURLOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	if port != "" {
		return scheme + "://" + net.JoinHostPort(host, port)
	}
	return scheme + "://" + host
}

type inboundTokenKey struct {
	tenantID      uint64
	configID      string
	gatewayOrigin string
	sandboxID     string
}

type inboundTokenEntry struct {
	token     string
	expiresAt time.Time
}

// InboundTokenRegistry maps (tenantID, configID, gatewayOrigin, sandboxID) to
// its inbound traffic credential.
type InboundTokenRegistry struct {
	mu     sync.RWMutex
	tokens map[inboundTokenKey]inboundTokenEntry
	ttl    time.Duration
	now    func() time.Time
	ops    atomic.Uint64
}

// NewInboundTokenRegistry returns an empty registry with DefaultInboundTokenTTL.
func NewInboundTokenRegistry() *InboundTokenRegistry {
	return NewInboundTokenRegistryWithTTL(DefaultInboundTokenTTL)
}

// NewInboundTokenRegistryWithTTL returns an empty registry with a custom entry TTL.
func NewInboundTokenRegistryWithTTL(ttl time.Duration) *InboundTokenRegistry {
	if ttl <= 0 {
		ttl = DefaultInboundTokenTTL
	}
	return &InboundTokenRegistry{
		tokens: make(map[inboundTokenKey]inboundTokenEntry),
		ttl:    ttl,
		now:    time.Now,
	}
}

// ForScope binds this registry to scope.
func (r *InboundTokenRegistry) ForScope(scope InboundTokenScope) *ScopedInboundTokenRegistry {
	if r == nil {
		return nil
	}
	return &ScopedInboundTokenRegistry{
		registry: r,
		scope:    scope.normalized(),
	}
}

func makeInboundTokenKey(scope InboundTokenScope, sandboxID string) (inboundTokenKey, bool) {
	id := strings.ToLower(strings.TrimSpace(sandboxID))
	if id == "" {
		return inboundTokenKey{}, false
	}
	norm := scope.normalized()
	return inboundTokenKey{
		tenantID:      norm.TenantID,
		configID:      norm.ConfigID,
		gatewayOrigin: norm.GatewayOrigin,
		sandboxID:     id,
	}, true
}

// PutScoped records token for (scope, sandboxID). An empty token explicitly
// deletes any previously stored token for that key so a recycled or open-traffic
// sandbox ID never reuses a stale credential.
func (r *InboundTokenRegistry) PutScoped(scope InboundTokenScope, sandboxID, token string) {
	if r == nil {
		return
	}
	key, ok := makeInboundTokenKey(scope, sandboxID)
	if !ok {
		return
	}
	trimmed := strings.TrimSpace(token)
	now := r.currentTime()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.maybeSweepLocked(now)
	if trimmed == "" {
		delete(r.tokens, key)
		return
	}
	r.tokens[key] = inboundTokenEntry{
		token:     trimmed,
		expiresAt: now.Add(r.ttl),
	}
}

// GetScoped returns the token for (scope, sandboxID), or "" when absent or expired.
func (r *InboundTokenRegistry) GetScoped(scope InboundTokenScope, sandboxID string) string {
	if r == nil {
		return ""
	}
	key, ok := makeInboundTokenKey(scope, sandboxID)
	if !ok {
		return ""
	}
	now := r.currentTime()

	r.mu.RLock()
	entry, exists := r.tokens[key]
	r.mu.RUnlock()
	if !exists {
		return ""
	}
	if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
		r.mu.Lock()
		if cur, still := r.tokens[key]; still && !cur.expiresAt.IsZero() && !now.Before(cur.expiresAt) {
			delete(r.tokens, key)
		}
		r.mu.Unlock()
		return ""
	}
	return entry.token
}

// DeleteScoped forgets (scope, sandboxID).
func (r *InboundTokenRegistry) DeleteScoped(scope InboundTokenScope, sandboxID string) {
	if r == nil {
		return
	}
	key, ok := makeInboundTokenKey(scope, sandboxID)
	if !ok {
		return
	}
	now := r.currentTime()
	r.mu.Lock()
	delete(r.tokens, key)
	r.maybeSweepLocked(now)
	r.mu.Unlock()
}

// PurgeExpired removes all expired entries from the registry.
func (r *InboundTokenRegistry) PurgeExpired() int {
	if r == nil {
		return 0
	}
	now := r.currentTime()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sweepExpiredLocked(now)
}

func (r *InboundTokenRegistry) currentTime() time.Time {
	if r != nil && r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *InboundTokenRegistry) maybeSweepLocked(now time.Time) {
	if r.ops.Add(1)%64 == 0 {
		r.sweepExpiredLocked(now)
	}
}

func (r *InboundTokenRegistry) sweepExpiredLocked(now time.Time) int {
	removed := 0
	for k, v := range r.tokens {
		if !v.expiresAt.IsZero() && !now.Before(v.expiresAt) {
			delete(r.tokens, k)
			removed++
		}
	}
	return removed
}

// Put records token in the unscoped partition (used by unit tests and helpers
// without tenant config metadata). Empty token deletes any prior unscoped value.
func (r *InboundTokenRegistry) Put(sandboxID, token string) {
	r.PutScoped(InboundTokenScope{}, sandboxID, token)
}

// Get reads from the unscoped partition.
func (r *InboundTokenRegistry) Get(sandboxID string) string {
	return r.GetScoped(InboundTokenScope{}, sandboxID)
}

// Delete removes sandboxID from the unscoped partition.
func (r *InboundTokenRegistry) Delete(sandboxID string) {
	r.DeleteScoped(InboundTokenScope{}, sandboxID)
}

// ScopedInboundTokenRegistry is a tenant/config/gateway-isolated view onto an
// InboundTokenRegistry.
type ScopedInboundTokenRegistry struct {
	registry *InboundTokenRegistry
	scope    InboundTokenScope
}

// Scope returns the normalized isolation scope of s.
func (s *ScopedInboundTokenRegistry) Scope() InboundTokenScope {
	if s == nil {
		return InboundTokenScope{}
	}
	return s.scope
}

// SameTarget reports whether s and other read and write the exact same isolated
// partition of the same registry.
func (s *ScopedInboundTokenRegistry) SameTarget(other *ScopedInboundTokenRegistry) bool {
	if s == nil || other == nil {
		return s == other
	}
	return s.registry == other.registry && s.scope == other.scope
}

// Put records token for sandboxID within s's scope, or deletes it when token is empty.
func (s *ScopedInboundTokenRegistry) Put(sandboxID, token string) {
	if s == nil || s.registry == nil {
		return
	}
	s.registry.PutScoped(s.scope, sandboxID, token)
}

// Get returns the token for sandboxID within s's scope, or "".
func (s *ScopedInboundTokenRegistry) Get(sandboxID string) string {
	if s == nil || s.registry == nil {
		return ""
	}
	return s.registry.GetScoped(s.scope, sandboxID)
}

// Delete removes sandboxID from s's scope.
func (s *ScopedInboundTokenRegistry) Delete(sandboxID string) {
	if s == nil || s.registry == nil {
		return
	}
	s.registry.DeleteScoped(s.scope, sandboxID)
}

// sandboxIDFromDataPlaneHost extracts the sandbox ID from an envd authority of
// the form "<port>-<sandboxID>.<domain>", or "" when host is not one.
func sandboxIDFromDataPlaneHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 && !strings.Contains(host[idx:], "]") {
		host = host[:idx]
	}
	label, _, found := strings.Cut(host, ".")
	if !found {
		return ""
	}
	port, sandboxID, found := strings.Cut(label, "-")
	if !found || sandboxID == "" {
		return ""
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ""
	}
	return sandboxID
}
