package sandbox

import (
	"fmt"
	"os"
	"testing"

	"github.com/Usir233/GENYEE/internal/testutil/dnstest"
)

func TestMain(m *testing.M) {
	restore, err := dnstest.Install(map[string][]string{
		"api.e2b.dev":                      {"93.184.216.34"},
		"this-host-does-not-exist.invalid": nil,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	restore()
	os.Exit(code)
}
