package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/secrets"
	sdk "github.com/Usir233/GENYEE/client"
	"github.com/stretchr/testify/require"
)

func TestAuthMutationKeepsOneShotProfileOverride(t *testing.T) {
	for _, operation := range []string{"login", "logout"} {
		t.Run(
			operation,
			func(t *testing.T) {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				iostreams.SetForTest(t)
				require.NoError(
					t,
					config.Save(&config.Config{CurrentProfile: "prod", Profiles: map[string]config.Profile{
						"prod":    {Host: "https://prod.invalid", TokenRef: "mem://prod/access"},
						"staging": {Host: "https://staging.invalid", TokenRef: "mem://staging/access"},
					}}),
				)
				store := secrets.NewMemStore()
				require.NoError(t, store.Set("prod", "access", "prod-token"))
				require.NoError(t, store.Set("staging", "access", "old-token"))
				f := cmdutil.New()
				f.ProfileOverride = "staging"
				f.Secrets = func() (secrets.Store, error) { return store, nil }
				format := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
				if operation == "login" {
					require.NoError(
						t,
						runLogin(
							context.Background(),
							&LoginOptions{Email: "user@example.invalid", Password: "fixture"},
							format,
							f,
							&fakeLoginService{
								resp: &sdk.LoginResponse{
									Success:      true,
									Token:        "new-token",
									RefreshToken: "new-refresh",
								},
							},
						),
					)
				} else {
					require.NoError(t, runLogout(&LogoutOptions{Yes: true}, format, f))
				}
				saved, err := config.Load()
				require.NoError(t, err)
				require.Equal(t, "prod", saved.CurrentProfile, "--profile must not change the persisted default")
				prod, err := store.Get("prod", "access")
				require.NoError(t, err)
				require.Equal(t, "prod-token", prod)
				if operation == "login" {
					token, err := store.Get("staging", "access")
					require.NoError(t, err)
					require.Equal(t, "new-token", token)
				} else {
					_, err := store.Get("staging", "access")
					require.ErrorIs(t, err, secrets.ErrNotFound)
				}
			},
		)
	}
}

type deleteFailureStore struct {
	*secrets.MemStore
	failKey string
}

func (s *deleteFailureStore) Delete(profile, key string) error {
	if key == s.failKey {
		return errors.New("keychain is locked")
	}
	return s.MemStore.Delete(profile, key)
}

func TestLogoutPreservesReferencesWhenCredentialDeletionFails(t *testing.T) {
	for _, key := range []string{"access", "refresh", "api_key"} {
		t.Run(
			key,
			func(t *testing.T) {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				out, _ := iostreams.SetForTest(t)
				profile := config.Profile{
					Host:       "https://example.invalid",
					TokenRef:   "mem://prod/access",
					RefreshRef: "mem://prod/refresh",
					APIKeyRef:  "mem://prod/api_key",
				}
				cfg := &config.Config{CurrentProfile: "prod", Profiles: map[string]config.Profile{"prod": profile}}
				require.NoError(t, config.Save(cfg))
				store := &deleteFailureStore{MemStore: secrets.NewMemStore(), failKey: key}
				err := runLogout(
					&LogoutOptions{Yes: true},
					&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON},
					newLogoutFactory(t, cfg, store),
				)
				var typed *cmdutil.Error
				require.ErrorAs(t, err, &typed)
				require.Equal(t, cmdutil.CodeLocalKeychainDenied, typed.Code)
				require.Empty(t, out.String(), "failed cleanup cannot claim success")
				saved, loadErr := config.Load()
				require.NoError(t, loadErr)
				require.Equal(t, profile, saved.Profiles["prod"], "keep references so cleanup can be retried")
			},
		)
	}
}

func TestLoginModeSwitchAndEmptyRefreshPurgeStaleSecrets(t *testing.T) {
	xdgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)
	iostreams.SetForTest(t)

	fileStore, err := secrets.NewFileStore()
	require.NoError(t, err)
	require.NoError(t, config.Save(&config.Config{
		CurrentProfile: "prod",
		Profiles: map[string]config.Profile{
			"prod": {Host: "https://prod.invalid"},
		},
	}))

	restoreValidator := stubAPIKeyValidator(func(_ context.Context, _, _ string) (*sdk.AuthUser, error) {
		return &sdk.AuthUser{ID: "u1", Email: "u@example.invalid", TenantID: 1}, nil
	})
	defer restoreValidator()

	f := cmdutil.New()
	f.Secrets = func() (secrets.Store, error) { return fileStore, nil }
	jsonFmt := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}

	// 1. Password login with refresh token stores access + refresh.
	require.NoError(t, runLogin(context.Background(),
		&LoginOptions{Email: "u@example.invalid", Password: "pw"},
		jsonFmt, f,
		&fakeLoginService{resp: &sdk.LoginResponse{Success: true, Token: "jwt-1", RefreshToken: "rfr-1"}},
	))
	_, err = fileStore.Get("prod", "access")
	require.NoError(t, err)
	_, err = fileStore.Get("prod", "refresh")
	require.NoError(t, err)

	// 2. Password re-login with empty RefreshToken purges stale refresh secret + clears RefreshRef.
	require.NoError(t, runLogin(context.Background(),
		&LoginOptions{Email: "u@example.invalid", Password: "pw"},
		jsonFmt, f,
		&fakeLoginService{resp: &sdk.LoginResponse{Success: true, Token: "jwt-2", RefreshToken: ""}},
	))
	_, err = fileStore.Get("prod", "refresh")
	require.ErrorIs(t, err, secrets.ErrNotFound, "empty RefreshToken must delete prior refresh secret")
	cfgAfterEmptyRfr, err := config.Load()
	require.NoError(t, err)
	require.Empty(t, cfgAfterEmptyRfr.Profiles["prod"].RefreshRef, "empty RefreshToken must clear RefreshRef")

	// 3. Switching to API-key login purges stale access + refresh secrets.
	require.NoError(t, fileStore.Set("prod", "refresh", "stale-rfr"))
	require.NoError(t, runLogin(context.Background(),
		&LoginOptions{WithToken: true, StdinReader: strings.NewReader("sk-new\n")},
		jsonFmt, f,
		nil,
	))
	_, err = fileStore.Get("prod", "access")
	require.ErrorIs(t, err, secrets.ErrNotFound, "API-key switch must delete stale access token")
	_, err = fileStore.Get("prod", "refresh")
	require.ErrorIs(t, err, secrets.ErrNotFound, "API-key switch must delete stale refresh token")
	gotKey, err := fileStore.Get("prod", "api_key")
	require.NoError(t, err)
	require.Equal(t, "sk-new", gotKey)

	// 4. Switching back to JWT login purges stale api_key secret.
	require.NoError(t, runLogin(context.Background(),
		&LoginOptions{Email: "u@example.invalid", Password: "pw"},
		jsonFmt, f,
		&fakeLoginService{resp: &sdk.LoginResponse{Success: true, Token: "jwt-3", RefreshToken: "rfr-3"}},
	))
	_, err = fileStore.Get("prod", "api_key")
	require.ErrorIs(t, err, secrets.ErrNotFound, "JWT switch must delete stale api_key")

	// 5. Logout deletes orphaned secret files even when refs were manually cleared, and removes empty profile dir.
	cfgEdited, err := config.Load()
	require.NoError(t, err)
	p := cfgEdited.Profiles["prod"]
	p.TokenRef = ""
	p.RefreshRef = ""
	p.APIKeyRef = "env:GENYEE_API_KEY"
	cfgEdited.Profiles["prod"] = p
	require.NoError(t, config.Save(cfgEdited))
	require.NoError(t, fileStore.Set("prod", "api_key", "env-untouched"))

	// Clear the env-backed api_key manually after checking env: skip behavior.
	require.NoError(t, runLogout(&LogoutOptions{Yes: true}, jsonFmt, f))
	_, err = fileStore.Get("prod", "access")
	require.ErrorIs(
		t,
		err,
		secrets.ErrNotFound,
		"logout must purge orphaned access secret even when TokenRef was empty",
	)
	_, err = fileStore.Get("prod", "refresh")
	require.ErrorIs(
		t,
		err,
		secrets.ErrNotFound,
		"logout must purge orphaned refresh secret even when RefreshRef was empty",
	)
	envUntouched, err := fileStore.Get("prod", "api_key")
	require.NoError(t, err)
	require.Equal(t, "env-untouched", envUntouched, "env: ref must not delete store slot")

	require.NoError(t, fileStore.Delete("prod", "api_key"))
	_, statErr := os.Stat(filepath.Join(xdgRoot, "genyee", "secrets", "prod"))
	require.True(t, os.IsNotExist(statErr), "FileStore.Delete must remove empty <root>/<profile> directory")
}
