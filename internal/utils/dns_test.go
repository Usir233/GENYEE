package utils

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/testutil/dnstest"
)

func TestMain(m *testing.M) {
	restore, err := dnstest.Install(map[string][]string{
		"example.com":                  {"93.184.216.34"},
		"loopback.fixture.invalid":     {"127.0.0.1"},
		"benchmark.fixture.invalid":    {"198.18.0.34"},
		"mixed.fixture.invalid":        {"93.184.216.34", "127.0.0.1"},
		"unresolvable.fixture.invalid": nil,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	restore()
	os.Exit(code)
}

func TestSSRFSafeURLRejectsRestrictedDNSAnswers(t *testing.T) {
	for _, hostname := range []string{"loopback.fixture.invalid", "benchmark.fixture.invalid", "mixed.fixture.invalid"} {
		t.Run(hostname, func(t *testing.T) {
			ok, reason := isSSRFSafeURL("https://" + hostname)
			if ok || !strings.Contains(reason, "restricted") {
				t.Fatalf("DNS answer must be rejected: ok=%v reason=%q", ok, reason)
			}
		})
	}
	ok, reason := isSSRFSafeURL("https://unresolvable.fixture.invalid")
	if ok || !strings.Contains(reason, "DNS resolution failed") {
		t.Fatalf("missing DNS answer must fail closed: ok=%v reason=%q", ok, reason)
	}
}
