package auth

import (
	"context"
	"errors"
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
		t.Run(operation, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			iostreams.SetForTest(t)
			require.NoError(t, config.Save(&config.Config{CurrentProfile: "prod", Profiles: map[string]config.Profile{
				"prod":    {Host: "https://prod.invalid", TokenRef: "mem://prod/access"},
				"staging": {Host: "https://staging.invalid", TokenRef: "mem://staging/access"},
			}}))
			store := secrets.NewMemStore()
			require.NoError(t, store.Set("prod", "access", "prod-token"))
			require.NoError(t, store.Set("staging", "access", "old-token"))
			f := cmdutil.New()
			f.ProfileOverride = "staging"
			f.Secrets = func() (secrets.Store, error) { return store, nil }
			format := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
			if operation == "login" {
				require.NoError(t, runLogin(context.Background(), &LoginOptions{Email: "user@example.invalid", Password: "fixture"}, format, f, &fakeLoginService{resp: &sdk.LoginResponse{Success: true, Token: "new-token", RefreshToken: "new-refresh"}}))
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
		})
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
		t.Run(key, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			out, _ := iostreams.SetForTest(t)
			profile := config.Profile{Host: "https://example.invalid", TokenRef: "mem://prod/access", RefreshRef: "mem://prod/refresh", APIKeyRef: "mem://prod/api_key"}
			cfg := &config.Config{CurrentProfile: "prod", Profiles: map[string]config.Profile{"prod": profile}}
			require.NoError(t, config.Save(cfg))
			store := &deleteFailureStore{MemStore: secrets.NewMemStore(), failKey: key}
			err := runLogout(&LogoutOptions{Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, newLogoutFactory(t, cfg, store))
			var typed *cmdutil.Error
			require.ErrorAs(t, err, &typed)
			require.Equal(t, cmdutil.CodeLocalKeychainDenied, typed.Code)
			require.Empty(t, out.String(), "failed cleanup cannot claim success")
			saved, loadErr := config.Load()
			require.NoError(t, loadErr)
			require.Equal(t, profile, saved.Profiles["prod"], "keep references so cleanup can be retried")
		})
	}
}
