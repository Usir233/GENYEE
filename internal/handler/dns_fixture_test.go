package handler

import (
	"fmt"
	"os"
	"testing"

	"github.com/Usir233/GENYEE/internal/testutil/dnstest"
)

// Keep public-endpoint validation deterministic without bypassing SSRF checks.
func TestMain(m *testing.M) {
	restore, err := dnstest.Install(map[string][]string{
		"example.com": {"93.184.216.34"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	restore()
	os.Exit(code)
}
