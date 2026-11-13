package web_fetch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFetchURLContentBlocksRedirectToLoopback(t *testing.T) {
	internalHit := make(chan struct{}, 1)
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHit <- struct{}{}
		fmt.Fprint(w, "<html><body>GENYEE_INTERNAL_CANARY_178193</body></html>")
	}))
	defer internal.Close()

	if _, err := FetchURLContent(context.Background(), internal.URL); err == nil {
		t.Fatal("direct loopback URL was not rejected")
	}

	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer attacker.Close()

	got, err := FetchURLContent(context.Background(), attacker.URL+"/entry")
	if err == nil {
		if strings.Contains(got, "GENYEE_INTERNAL_CANARY_178193") {
			t.Fatalf("redirect followed to loopback, got %q", got)
		}
		t.Fatal("expected redirect to loopback to be blocked")
	}
	select {
	case <-internalHit:
		t.Fatal("internal loopback server was reached via redirect")
	default:
	}
}

func TestChromiumSSRFProxyBlocksRawIPLiteralsAndDNSRebinding(t *testing.T) {
	internalHit := make(chan string, 10)
	internalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHit <- r.URL.Path
		fmt.Fprint(w, "INTERNAL_SECRET")
	}))
	defer internalSrv.Close()

	publicSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body>PUBLIC_OK</body></html>")
	}))
	defer publicSrv.Close()
	publicListenerAddr := publicSrv.Listener.Addr().String()

	pinnedURL, _ := url.Parse("http://spa.public.example/app")
	rebindFlipped := false
	proxy, err := startChromiumSSRFProxy(
		pinnedTarget{
			URL:  pinnedURL,
			Host: "spa.public.example",
			Port: "80",
			IP:   net.ParseIP("93.184.216.34"),
		},
		func(p *chromiumSSRFProxy) {
			p.validateURL = func(_ context.Context, raw string) error {
				u, err := url.Parse(raw)
				if err != nil {
					return err
				}
				h := strings.ToLower(u.Hostname())
				if h == "spa.public.example" || h == "rebind.evil.example" {
					return nil
				}
				return fmt.Errorf("rejected by test validator: %s", raw)
			}
			p.resolveIPs = func(_ context.Context, host string) ([]net.IP, error) {
				switch strings.ToLower(host) {
				case "spa.public.example":
					if rebindFlipped {
						return []net.IP{net.ParseIP("127.0.0.1")}, nil
					}
					return []net.IP{net.ParseIP("93.184.216.34")}, nil
				case "rebind.evil.example":
					return []net.IP{net.ParseIP("127.0.0.1")}, nil
				default:
					return nil, fmt.Errorf("unexpected lookup for %s", host)
				}
			}
			p.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				host, _, _ := net.SplitHostPort(address)
				if host == "93.184.216.34" {
					return (&net.Dialer{}).DialContext(ctx, network, publicListenerAddr)
				}
				return nil, fmt.Errorf("unexpected dial to %s", address)
			}
		},
	)
	if err != nil {
		t.Fatalf("startChromiumSSRFProxy failed: %v", err)
	}
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.ProxyURL())
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	proxyClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	// 1. Blocked HTTP proxy targets (raw IPv4/IPv6/mapped-IPv6/metadata/localhost/DNS-rebind).
	blockedHTTPURLs := []string{
		internalSrv.URL + "/steal",
		"http://127.0.0.1/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1:8080/internal",
		"http://[::1]:8080/internal",
		"http://[::ffff:127.0.0.1]:8080/internal",
		"http://localhost/internal",
		"http://rebind.evil.example/secret",
	}
	for _, badURL := range blockedHTTPURLs {
		resp, reqErr := proxyClient.Get(badURL)
		if reqErr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden for %s, got %d", badURL, resp.StatusCode)
			}
		}
	}

	// 2. Blocked HTTPS CONNECT targets.
	blockedConnectTargets := []string{
		strings.TrimPrefix(internalSrv.URL, "http://"),
		"127.0.0.1:443",
		"169.254.169.254:443",
		"10.0.0.1:443",
		"[::1]:443",
		"[::ffff:127.0.0.1]:443",
		"localhost:443",
		"rebind.evil.example:443",
		"spa.public.example:6379",
	}
	for _, authority := range blockedConnectTargets {
		conn, dialErr := net.DialTimeout("tcp", proxy.Addr(), 2*time.Second)
		if dialErr != nil {
			t.Fatalf("dial proxy for %s: %v", authority, dialErr)
		}
		_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
		resp, readErr := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
		_ = conn.Close()
		if readErr != nil {
			t.Fatalf("read CONNECT response for %s: %v", authority, readErr)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected CONNECT %s to be rejected with 403, got %d", authority, resp.StatusCode)
		}
	}

	// 3. Pinned public target succeeds even after DNS flips to 127.0.0.1.
	rebindFlipped = true
	resp, err := proxyClient.Get("http://spa.public.example/app")
	if err != nil {
		t.Fatalf("pinned public HTTP fetch failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "PUBLIC_OK") {
		t.Fatalf("unexpected response for pinned public target: status=%d body=%q", resp.StatusCode, string(body))
	}

	select {
	case path := <-internalHit:
		t.Fatalf("internal loopback server was unexpectedly reached at %s", path)
	default:
	}
}

func TestBuildChromiumEnvStripsParentSecretsInSubprocess(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "CANARY_AES_SECRET_991122")
	t.Setenv("JWT_SECRET", "CANARY_JWT_SECRET_991122")
	t.Setenv("DB_PASSWORD", "CANARY_DB_PASS_991122")
	t.Setenv("HTTP_PROXY", "http://canary-proxy.internal:8080")
	t.Setenv("LD_PRELOAD", "/tmp/canary_preload.so")

	// Simulate exact chromedp.ExecAllocator behavior: append(os.Environ(), initEnv...).
	initEnv := buildChromiumEnv(os.Environ())
	cmd := exec.Command("env")
	cmd.Env = append(os.Environ(), initEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("env command failed: %v", err)
	}
	envText := string(out)
	for _, secret := range []string{
		"CANARY_AES_SECRET_991122",
		"CANARY_JWT_SECRET_991122",
		"CANARY_DB_PASS_991122",
		"canary-proxy.internal",
		"/tmp/canary_preload.so",
	} {
		if strings.Contains(envText, secret) {
			t.Fatalf("child process leaked parent secret %q in environment", secret)
		}
	}
	if !strings.Contains(envText, "PATH=") {
		t.Fatalf("expected PATH to remain present in child environment, got:\n%s", envText)
	}
}

func TestChromiumConcurrencyGateHonorsEnvLimitAndContextTimeout(t *testing.T) {
	t.Setenv("GENYEE_WEB_FETCH_CHROMIUM_CONCURRENCY", "2")
	if got := ChromiumConcurrencyLimit(); got != 2 {
		t.Fatalf("expected ChromiumConcurrencyLimit()=2, got %d", got)
	}

	gate := &chromiumConcurrencyGate{notify: make(chan struct{})}
	rel1, err := gate.acquire(context.Background(), ChromiumConcurrencyLimit)
	if err != nil {
		t.Fatalf("slot 1 acquire failed: %v", err)
	}
	defer rel1()

	rel2, err := gate.acquire(context.Background(), ChromiumConcurrencyLimit)
	if err != nil {
		t.Fatalf("slot 2 acquire failed: %v", err)
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := gate.acquire(shortCtx, ChromiumConcurrencyLimit); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded when concurrency gate is full, got %v", err)
	}

	rel2()
	rel3, err := gate.acquire(context.Background(), ChromiumConcurrencyLimit)
	if err != nil {
		t.Fatalf("slot 3 acquire after release failed: %v", err)
	}
	rel3()
}
