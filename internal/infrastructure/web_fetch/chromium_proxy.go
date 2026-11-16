package web_fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/utils"
)

const (
	defaultChromiumConcurrency = 3
	maxChromiumConcurrency     = 32
)

var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// allowedChromiumEnvKeys is the minimal allowlist of host environment variables
// permitted inside headless Chromium. Application secrets (SYSTEM_AES_KEY,
// JWT_SECRET, DB_PASSWORD, API keys, proxy vars, dynamic linker vars, etc.)
// are explicitly blanked out so child processes cannot exfiltrate them.
var allowedChromiumEnvKeys = map[string]bool{
	"PATH":                true,
	"HOME":                true,
	"TMPDIR":              true,
	"TEMP":                true,
	"TMP":                 true,
	"LANG":                true,
	"LC_ALL":              true,
	"LC_CTYPE":            true,
	"USER":                true,
	"LOGNAME":             true,
	"DISPLAY":             true,
	"XDG_RUNTIME_DIR":     true,
	"XDG_CONFIG_HOME":     true,
	"XDG_CACHE_HOME":      true,
	"FONTCONFIG_PATH":     true,
	"FONTCONFIG_FILE":     true,
	"NSS_DEFAULT_DB_TYPE": true,
	"SYSTEMROOT":          true,
	"WINDIR":              true,
	"LOCALAPPDATA":        true,
	"APPDATA":             true,
	"USERPROFILE":         true,
	"PROGRAMFILES":        true,
	"PROGRAMFILES(X86)":   true,
}

var blockedProxyPorts = map[string]bool{
	"22":    true,
	"23":    true,
	"25":    true,
	"445":   true,
	"3389":  true,
	"5432":  true,
	"3306":  true,
	"6379":  true,
	"27017": true,
	"9200":  true,
	"2379":  true,
	"2380":  true,
	"8500":  true,
	"4001":  true,
}

var blockedFetchHostnames = map[string]bool{
	"localhost":                            true,
	"0.0.0.0":                              true,
	"metadata.google.internal":             true,
	"metadata.tencentyun.com":              true,
	"metadata.aws.internal":                true,
	"host.docker.internal":                 true,
	"gateway.docker.internal":              true,
	"kubernetes.docker.internal":           true,
	"kubernetes":                           true,
	"kubernetes.default":                   true,
	"kubernetes.default.svc":               true,
	"kubernetes.default.svc.cluster.local": true,
}

var blockedFetchHostSuffixes = []string{
	".localhost",
	".local",
	".internal",
	".corp",
	".lan",
	".home",
	".localdomain",
	".svc.cluster.local",
	".pod.cluster.local",
}

func canonicalizeFetchHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "" {
		return "", fmt.Errorf("missing target hostname")
	}
	if strings.HasPrefix(host, ".") || strings.Contains(host, "..") {
		return "", fmt.Errorf("invalid hostname %q", raw)
	}
	host = strings.TrimRight(host, ".")
	if host == "" {
		return "", fmt.Errorf("missing target hostname")
	}
	for _, r := range host {
		if r <= 32 || r == 127 {
			return "", fmt.Errorf("invalid control character in hostname")
		}
	}
	return host, nil
}

func isRestrictedFetchHostname(hostCanon string) bool {
	if blockedFetchHostnames[hostCanon] {
		return true
	}
	for _, suffix := range blockedFetchHostSuffixes {
		if strings.HasSuffix(hostCanon, suffix) {
			return true
		}
	}
	return false
}

// ChromiumConcurrencyLimit returns the bounded global limit for concurrent
// headless Chromium renders, configured via GENYEE_WEB_FETCH_CHROMIUM_CONCURRENCY.
func ChromiumConcurrencyLimit() int {
	raw := strings.TrimSpace(os.Getenv("GENYEE_WEB_FETCH_CHROMIUM_CONCURRENCY"))
	if raw == "" {
		return defaultChromiumConcurrency
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultChromiumConcurrency
	}
	if n > maxChromiumConcurrency {
		return maxChromiumConcurrency
	}
	return n
}

type chromiumConcurrencyGate struct {
	mu     sync.Mutex
	active int
	notify chan struct{}
}

var globalChromiumGate = &chromiumConcurrencyGate{
	notify: make(chan struct{}),
}

func acquireChromiumSlot(ctx context.Context) (func(), error) {
	return globalChromiumGate.acquire(ctx, ChromiumConcurrencyLimit)
}

func (g *chromiumConcurrencyGate) acquire(ctx context.Context, limitFn func() int) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.mu.Lock()
		limit := limitFn()
		if limit <= 0 {
			limit = defaultChromiumConcurrency
		}
		if g.active < limit {
			g.active++
			var once sync.Once
			release := func() {
				once.Do(func() {
					g.mu.Lock()
					g.active--
					close(g.notify)
					g.notify = make(chan struct{})
					g.mu.Unlock()
				})
			}
			g.mu.Unlock()
			return release, nil
		}
		waitCh := g.notify
		g.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-waitCh:
		}
	}
}

// buildChromiumEnv builds an environment slice for chromedp.Env(...) that
// preserves only minimal OS runtime variables and blanks every non-allowlisted
// parent environment variable so Go's os/exec deduplication strips secrets
// (SYSTEM_AES_KEY, JWT_SECRET, DB_PASSWORD, etc.) before ForkExec.
func buildChromiumEnv(environ []string) []string {
	out := make([]string, 0, len(environ)+3)
	hasPath := false
	hasHome := false
	hasTmp := false

	for _, entry := range environ {
		key, val, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		upperKey := strings.ToUpper(key)
		if allowedChromiumEnvKeys[upperKey] && !strings.HasPrefix(upperKey, "LD_") && !strings.HasPrefix(upperKey, "DYLD_") {
			out = append(out, key+"="+val)
			switch upperKey {
			case "PATH":
				hasPath = val != ""
			case "HOME":
				hasHome = val != ""
			case "TMPDIR", "TEMP", "TMP":
				if val != "" {
					hasTmp = true
				}
			}
			continue
		}
		// Explicitly override non-allowlisted parent variables with an empty
		// value because chromedp.ExecAllocator prepends os.Environ() before
		// initEnv; os/exec deduplicates keys using the last occurrence.
		out = append(out, key+"=")
	}

	if !hasPath {
		out = append(out, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}
	if !hasHome {
		out = append(out, "HOME="+os.TempDir())
	}
	if !hasTmp {
		out = append(out, "TMPDIR="+os.TempDir())
	}
	return out
}

type chromiumSSRFProxy struct {
	listener    net.Listener
	server      *http.Server
	transport   *http.Transport
	validateURL func(context.Context, string) error
	resolveIPs  func(context.Context, string) ([]net.IP, error)
	dialContext func(context.Context, string, string) (net.Conn, error)
	pinned      map[string]net.IP

	closeOnce sync.Once
	connsMu   sync.Mutex
	conns     map[net.Conn]struct{}
}

type chromiumProxyOption func(*chromiumSSRFProxy)

func startChromiumSSRFProxy(target pinnedTarget, opts ...chromiumProxyOption) (*chromiumSSRFProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start chromium SSRF proxy: %w", err)
	}
	p := &chromiumSSRFProxy{
		listener:    ln,
		validateURL: utils.ValidateURLForSSRFContext,
		resolveIPs:  lookupPublicDNS,
		pinned:      make(map[string]net.IP),
		conns:       make(map[net.Conn]struct{}),
	}
	if target.Host != "" && target.IP != nil {
		if canon, canonErr := canonicalizeFetchHost(target.Host); canonErr == nil {
			p.pinned[canon] = target.IP
		} else {
			p.pinned[strings.ToLower(target.Host)] = target.IP
		}
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	p.transport = &http.Transport{
		Proxy:                 nil, // Never chain to ambient HTTP_PROXY/HTTPS_PROXY
		DialContext:           p.dialTarget,
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 15 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
	}
	p.server = &http.Server{
		Handler:           http.HandlerFunc(p.serveHTTP),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = p.server.Serve(ln)
	}()
	return p, nil
}

func (p *chromiumSSRFProxy) Addr() string {
	if p == nil || p.listener == nil {
		return ""
	}
	return p.listener.Addr().String()
}

func (p *chromiumSSRFProxy) ProxyURL() string {
	return "http://" + p.Addr()
}

func (p *chromiumSSRFProxy) Close() error {
	if p == nil {
		return nil
	}
	var closeErr error
	p.closeOnce.Do(func() {
		if p.transport != nil {
			p.transport.CloseIdleConnections()
		}
		if p.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			closeErr = p.server.Shutdown(ctx)
			_ = p.server.Close()
		}
		if p.listener != nil {
			_ = p.listener.Close()
		}
		p.connsMu.Lock()
		for c := range p.conns {
			_ = c.Close()
		}
		p.conns = nil
		p.connsMu.Unlock()
	})
	return closeErr
}

func (p *chromiumSSRFProxy) trackConn(c net.Conn) {
	if c == nil {
		return
	}
	p.connsMu.Lock()
	if p.conns != nil {
		p.conns[c] = struct{}{}
	} else {
		_ = c.Close()
	}
	p.connsMu.Unlock()
}

func (p *chromiumSSRFProxy) untrackConn(c net.Conn) {
	if c == nil {
		return
	}
	p.connsMu.Lock()
	if p.conns != nil {
		delete(p.conns, c)
	}
	p.connsMu.Unlock()
}

func (p *chromiumSSRFProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

func (p *chromiumSSRFProxy) validateTargetURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid proxy target URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("blocked scheme %q", parsed.Scheme)
	}
	if parsed.User != nil {
		return fmt.Errorf("credentials in target URL are forbidden")
	}
	host, err := canonicalizeFetchHost(parsed.Hostname())
	if err != nil {
		return err
	}
	if !utils.IsSSRFWhitelistedContext(ctx, host) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if port := parsed.Port(); port != "" && blockedProxyPorts[port] {
			return fmt.Errorf("connection blocked: port %s is restricted", port)
		}
		if err := utils.CheckSSRFWhitelistOnly(host); err != nil {
			return err
		}
		if net.ParseIP(host) != nil {
			return fmt.Errorf("direct IP literal %s is forbidden", host)
		}
		if isRestrictedFetchHostname(host) {
			return fmt.Errorf("connection blocked: restricted hostname %s", host)
		}
	}
	if p.validateURL != nil {
		if err := p.validateURL(ctx, rawURL); err != nil {
			return err
		}
	}
	return nil
}

func (p *chromiumSSRFProxy) dialTarget(ctx context.Context, network, address string) (net.Conn, error) {
	rawHost, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid target address %q: %w", address, err)
	}
	port = strings.TrimSpace(port)
	if rawHost == "" || port == "" {
		return nil, fmt.Errorf("empty host or port in %q", address)
	}
	host, err := canonicalizeFetchHost(rawHost)
	if err != nil {
		return nil, fmt.Errorf("connection blocked: %w", err)
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return nil, fmt.Errorf("invalid target port %q", port)
	}

	whitelisted := utils.IsSSRFWhitelistedContext(ctx, host)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !whitelisted {
		if blockedProxyPorts[port] {
			return nil, fmt.Errorf("connection blocked: port %s is restricted", port)
		}
		if err := utils.CheckDialWhitelistOnly(host); err != nil {
			return nil, fmt.Errorf("connection blocked: %w", err)
		}
		if net.ParseIP(host) != nil {
			return nil, fmt.Errorf("connection blocked: direct IP literal %s is forbidden", host)
		}
		if isRestrictedFetchHostname(host) {
			return nil, fmt.Errorf("connection blocked: restricted hostname %s", host)
		}
	}

	var ips []net.IP
	if pinnedIP, ok := p.pinned[host]; ok && pinnedIP != nil {
		ips = []net.IP{pinnedIP}
	} else if parsedIP := net.ParseIP(host); parsedIP != nil {
		ips = []net.IP{parsedIP}
	} else {
		resolved, err := p.resolveIPs(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("DNS resolution failed for %s: %w", host, err)
		}
		ips = resolved
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("DNS resolution returned no addresses for %s", host)
	}

	if !whitelisted {
		for _, ip := range ips {
			if !utils.IsPublicIP(ip) {
				return nil, fmt.Errorf("connection blocked: %s resolves to restricted IP %s", host, ip.String())
			}
		}
	}

	dial := p.dialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}).DialContext
	}
	var lastErr error
	for _, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, dialErr := dial(dialCtx, network, net.JoinHostPort(ip.String(), port))
		cancel()
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("failed to connect to %s: %w", host, lastErr)
}

func (p *chromiumSSRFProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL == nil || !strings.EqualFold(r.URL.Scheme, "http") || r.URL.Hostname() == "" {
		http.Error(w, "proxy requires absolute http target URL", http.StatusBadRequest)
		return
	}
	targetURL := r.URL.String()
	if err := p.validateTargetURL(r.Context(), targetURL); err != nil {
		http.Error(w, "blocked by SSRF policy", http.StatusForbidden)
		return
	}

	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	outReq.Host = outReq.URL.Host
	stripHopHeaders(outReq.Header)

	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		status := http.StatusBadGateway
		if isProxySSRFBlockError(err) {
			status = http.StatusForbidden
		}
		http.Error(w, "upstream request rejected", status)
		return
	}
	defer resp.Body.Close()

	stripHopHeaders(resp.Header)
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxAgentBodySize*2))
}

func (p *chromiumSSRFProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	authority := strings.TrimSpace(r.Host)
	if authority == "" && r.URL != nil {
		authority = strings.TrimSpace(r.URL.Host)
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil || host == "" || port == "" {
		http.Error(w, "invalid CONNECT authority", http.StatusBadRequest)
		return
	}

	syntheticURL := "https://" + net.JoinHostPort(host, port)
	if err := p.validateTargetURL(r.Context(), syntheticURL); err != nil {
		http.Error(w, "CONNECT target blocked by SSRF policy", http.StatusForbidden)
		return
	}

	upstreamConn, err := p.dialTarget(r.Context(), "tcp", net.JoinHostPort(host, port))
	if err != nil {
		status := http.StatusBadGateway
		if isProxySSRFBlockError(err) {
			status = http.StatusForbidden
		}
		http.Error(w, "CONNECT target rejected", status)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstreamConn.Close()
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		_ = upstreamConn.Close()
		return
	}

	p.trackConn(clientConn)
	p.trackConn(upstreamConn)
	defer func() {
		p.untrackConn(clientConn)
		p.untrackConn(upstreamConn)
		_ = clientConn.Close()
		_ = upstreamConn.Close()
	}()

	if _, err := io.WriteString(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if clientBuf != nil && clientBuf.Reader.Buffered() > 0 {
		buffered := make([]byte, clientBuf.Reader.Buffered())
		n, _ := clientBuf.Read(buffered)
		if n > 0 {
			if _, err := upstreamConn.Write(buffered[:n]); err != nil {
				return
			}
		}
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstreamConn, clientConn)
		closeWrite(upstreamConn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, upstreamConn)
		closeWrite(clientConn)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func closeWrite(c net.Conn) {
	type closeWriter interface {
		CloseWrite() error
	}
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

func stripHopHeaders(h http.Header) {
	if h == nil {
		return
	}
	if connHdr := h.Get("Connection"); connHdr != "" {
		for _, field := range strings.Split(connHdr, ",") {
			if field = strings.TrimSpace(field); field != "" {
				h.Del(field)
			}
		}
	}
	for _, k := range hopByHopHeaders {
		h.Del(k)
	}
}

func isProxySSRFBlockError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, utils.ErrSSRFRedirectBlocked) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection blocked") ||
		strings.Contains(msg, "restricted") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "whitelist") ||
		strings.Contains(msg, "ssrf") ||
		strings.Contains(msg, "direct ip")
}
