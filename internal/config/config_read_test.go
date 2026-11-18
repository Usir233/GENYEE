package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigParsesExpandedYAML(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{"valid expansion", "127.0.0.9", true},
		{"malformed expansion", "[", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			previousDir := resolvedConfigDir
			t.Cleanup(func() { resolvedConfigDir = previousDir })
			t.Setenv("GENYEE_TEST_CONFIG_HOST", tc.value)
			t.Setenv("SERVER_HOST", "")
			path := filepath.Join(t.TempDir(), "config.yaml")
			contents := "server:\n  port: 8080\n  host: ${GENYEE_TEST_CONFIG_HOST}\n" +
				"knowledge_base:\n  chunk_size: 512\n  chunk_overlap: 80\n"
			require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
			viper.AddConfigPath(filepath.Dir(path))

			cfg, err := LoadConfig()
			if !tc.valid {
				var parseErr viper.ConfigParseError
				require.ErrorAs(t, err, &parseErr)
				require.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cfg.Server)
			require.Equal(t, tc.value, cfg.Server.Host)
		})
	}
}

func TestLoadConfigExpandsDefaultPlaceholdersAndGenyeeHomeSearchPath(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	previousDir := resolvedConfigDir
	t.Cleanup(func() { resolvedConfigDir = previousDir })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GENYEE_UNSET_HOST", "")
	require.NoError(t, os.Unsetenv("GENYEE_UNSET_HOST"))
	t.Setenv("GENYEE_UNSET_COLON_PORT", "")
	require.NoError(t, os.Unsetenv("GENYEE_UNSET_COLON_PORT"))
	t.Setenv("GENYEE_SET_CHUNKS", "768")
	t.Setenv("SERVER_HOST", "")
	t.Setenv("SERVER_PORT", "")

	// Place config.yaml inside $HOME/.genyee and run from an empty temp cwd to
	// verify the canonical ~/.genyee search path and ${VAR:-default}/${VAR:default}.
	emptyCwd := t.TempDir()
	t.Chdir(emptyCwd)
	dotGenyee := filepath.Join(home, ".genyee")
	require.NoError(t, os.MkdirAll(dotGenyee, 0o700))
	contents := "server:\n" +
		"  port: ${GENYEE_UNSET_COLON_PORT:9191}\n" +
		"  host: ${GENYEE_UNSET_HOST:-127.0.0.5}\n" +
		"frontend_base_url: ${GENYEE_UNSET_FE_URL:-}\n" +
		"knowledge_base:\n" +
		"  chunk_size: ${GENYEE_SET_CHUNKS:-512}\n" +
		"  chunk_overlap: 80\n"
	require.NoError(t, os.WriteFile(filepath.Join(dotGenyee, "config.yaml"), []byte(contents), 0o600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.5", cfg.Server.Host)
	require.Equal(t, 9191, cfg.Server.Port)
	require.Equal(t, 768, cfg.KnowledgeBase.ChunkSize)
	require.Empty(t, cfg.FrontendBaseURL)
}
