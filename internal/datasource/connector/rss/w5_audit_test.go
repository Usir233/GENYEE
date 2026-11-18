package rss

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/datasource"
)

func TestW5RSSAudit_NilIncrementalConfigAndContextCancellation(t *testing.T) {
	conn := NewConnector()
	_, _, incErr := conn.FetchIncremental(context.Background(), nil, nil)
	if !errors.Is(incErr, datasource.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig on nil FetchIncremental config, got %v", incErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var articleCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>CancelFeed</title>
<item><guid>1</guid><title>One</title><link>http://%s/a1</link><description>Summary 1</description></item>
<item><guid>2</guid><title>Two</title><link>http://%s/a2</link><description>Summary 2</description></item>
<item><guid>3</guid><title>Three</title><link>http://%s/a3</link><description>Summary 3</description></item>
</channel></rss>`, r.Host, r.Host, r.Host)
		case "/a1":
			articleCalls.Add(1)
			cancel()
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/a2", "/a3":
			articleCalls.Add(1)
			_, _ = w.Write([]byte(
				"<html><body><article><p>Should not be fetched after cancel</p></article></body></html>",
			))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := makeConfig(srv.URL+"/feed.xml", "")
	items, err := conn.FetchAll(ctx, cfg, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled on mid-sync cancel, got items=%d err=%v", len(items), err)
	}
	if calls := articleCalls.Load(); calls != 1 {
		t.Fatalf("expected sync to stop immediately after cancelled article fetch (1 call), got %d", calls)
	}
}

func TestW5RSSAudit_BlocksUnsafeItemLinksUserinfoAndHeaderSecretLeak(t *testing.T) {
	feedSecret := "rss-feed-secret-pw-w5"
	if _, err := parseConfig(makeConfig("https://user:"+feedSecret+"@example.com/feed.xml", "")); err == nil {
		t.Fatal("expected parseConfig to reject embedded userinfo in feed_urls")
	} else if strings.Contains(err.Error(), feedSecret) {
		t.Fatalf("parseConfig leaked feed password in error: %v", err)
	}

	hdrSecret := "rss-bearer-secret-w5"
	badHeaderCfg := makeConfig("https://example.com/feed.xml", "Authorization: Bearer "+hdrSecret+"\x00bad")
	if _, err := parseConfig(badHeaderCfg); err == nil {
		t.Fatal("expected parseConfig to reject control characters in auth_headers")
	} else if strings.Contains(err.Error(), hdrSecret) {
		t.Fatalf("parseConfig leaked auth header secret in error: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = fmt.Fprint(w, `<rss version="2.0"><channel><title>UnsafeLinks</title>
<item><guid>ssrf</guid><title>Metadata</title><link>http://169.254.169.254/latest/meta-data/</link></item>
<item><guid>js</guid><title>Script</title><link>javascript:alert(1)</link></item>
<item><guid>userinfo</guid><title>User</title><link>http://user:secret@127.0.0.1/private</link></item>
</channel></rss>`)
	}))
	defer srv.Close()

	items, err := NewConnector().FetchAll(context.Background(), makeConfig(srv.URL+"/feed.xml", ""), nil)
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	for _, it := range items {
		if it.URL != "" {
			t.Fatalf(
				"expected unsafe RSS item link to be stripped from FetchedItem.URL, got %q (title=%q)",
				it.URL, it.Title,
			)
		}
	}
}
