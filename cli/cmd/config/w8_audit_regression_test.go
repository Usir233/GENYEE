package configcmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
)

func TestW8_ConfigView_RedactsHostUserInfoCredentials(t *testing.T) {
	iostreams.SetForTest(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("GENYEE_API_KEY", "gy_testkey_123456")
	t.Setenv("GENYEE_HOST", "https://admin:super_secret_pw@api.example.com:8080")

	f := &cmdutil.Factory{
		Config: func() (*config.Config, error) {
			return &config.Config{
				CurrentProfile: "prod",
				Profiles: map[string]config.Profile{
					"prod": {Host: "https://user:prof_secret@api.example.com"},
				},
			}, nil
		},
	}

	d := resolveView(&cobra.Command{Use: "view"}, f)
	if strings.Contains(d.Host, "super_secret_pw") || strings.Contains(d.Host, "prof_secret") {
		t.Fatalf("resolveView leaked URL userinfo password in host: %q", d.Host)
	}
	if !strings.Contains(d.Host, "api.example.com:8080") {
		t.Fatalf("resolveView lost host authority: %q", d.Host)
	}
}
