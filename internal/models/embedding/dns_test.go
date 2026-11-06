package embedding

import (
	"fmt"
	"os"
	"testing"

	"github.com/Usir233/GENYEE/internal/testutil/dnstest"
)

func TestMain(m *testing.M) {
	// Credential validation constructs a client but never contacts the vendor.
	restore, err := dnstest.Install(map[string][]string{
		"weknora.weixin.qq.com": {"93.184.216.34"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	restore()
	os.Exit(code)
}
