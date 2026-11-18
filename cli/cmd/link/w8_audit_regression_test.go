package linkcmd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
)

func TestW8_Link_RejectsNonexistentOrUnsafeProfile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	iostreams.SetForTest(t)

	// 1. Nonexistent profile name in CurrentProfile (e.g. from --profile ghost) must not write a broken project.yaml.
	fGhost := &cmdutil.Factory{
		Config: func() (*config.Config, error) {
			return &config.Config{
				CurrentProfile: "ghost",
				Profiles:       map[string]config.Profile{"default": {Host: "https://example"}},
			}, nil
		},
	}
	err := runLink(
		context.Background(),
		&Options{KB: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText},
		fGhost,
	)
	require.Error(t, err)
	var ce *cmdutil.Error
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeLocalProfileNotFound, ce.Code)

	// 2. Unsafe profile name persisted in config.yaml must be rejected.
	fUnsafe := &cmdutil.Factory{
		Config: func() (*config.Config, error) {
			return &config.Config{
				CurrentProfile: "../evil",
				Profiles:       map[string]config.Profile{"../evil": {Host: "https://example"}},
			}, nil
		},
	}
	err = runLink(
		context.Background(),
		&Options{KB: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText},
		fUnsafe,
	)
	require.Error(t, err)
}
