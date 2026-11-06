package service

import (
	"fmt"
	"os"
	"testing"

	"github.com/Usir233/GENYEE/internal/testutil/dnstest"
)

func TestMain(m *testing.M) {
	// These tests validate public URLs and mock the remote operations. Host
	// DNS (including proxy fake-IP replies) must not determine their outcome.
	restore, err := dnstest.Install(map[string][]string{
		"example.com": {"93.184.216.34"},
		"api.e2b.app": {"93.184.216.34"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	restore()
	os.Exit(code)
}
