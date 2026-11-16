package auth

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/secrets"
)

type LogoutOptions struct {
	All    bool // --all: clear every profile
	Yes    bool // sourced from the global -y/--yes persistent flag
	DryRun bool
}

// authLogoutFields enumerates the fields surfaced for `--format json` discovery
// on `auth logout`. The result is the list of profile names that were
// logged out.
var authLogoutFields = []string{"removed"}

// logoutResult is the typed payload emitted under data.
type logoutResult struct {
	Removed []string `json:"removed"`
}

// NewCmdLogout builds `genyee auth logout`. Clears stored credentials
// (keyring + file fallback) and removes the profile entry from config.yaml.
// No server-side revocation - local-only credential clear.
func NewCmdLogout(f *cmdutil.Factory) *cobra.Command {
	opts := &LogoutOptions{}
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials for a profile",
		Long: `Clear keyring + file-fallback secrets for one profile (or all of
them with --all), keeping the profile registered for a later login.
If credential deletion fails, its references are kept so logout can be retried.

Note: this does NOT revoke the credential server-side - for API keys, you
must rotate them in the server UI; for JWT, the token will continue to be
accepted until it expires.`,
		Example: `  genyee auth logout                       # active profile
  genyee --profile staging auth logout     # specific profile
  genyee auth logout --all`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			// Pure-local validation runs before the dry-run gate so --dry-run
			// rejects identically to the live path. Same typed errors as
			// runLogout (kept there for direct-call callers).
			cfg, cfgErr := f.Config()
			if cfgErr != nil {
				return cfgErr
			}
			if len(cfg.Profiles) == 0 {
				if active, kind := cmdutil.EnvCredential(); active {
					return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
						"authenticated via "+kind+" (stateless env credential) — nothing is stored to log out; unset "+kind+" to drop it")
				}
				return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no profiles configured; nothing to log out")
			}
			if _, err := pickLogoutTargets(opts, cfg); err != nil {
				return err
			}
			if opts.DryRun {
				planArgs := map[string]any{"profile": cfg.CurrentProfile}
				if opts.All {
					planArgs = map[string]any{"all": true}
				}
				if handled, err := cmdutil.HandleDryRun(c, true, cmdutil.DryRunPlan{
					Action: "auth.logout",
					Args:   planArgs,
				}); handled {
					return err
				}
			}
			return runLogout(opts, fopts, f)
		},
	}
	cmd.Flags().BoolVar(&opts.All, "all", false, "Log out of every configured profile")
	cmdutil.AddFormatFlag(cmd, authLogoutFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetRisk(cmd, "auth.logout")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor: "clear stored credentials for the active profile (or --all); the profile itself stays registered (host preserved) so `auth login` can re-auth it — use `profile remove` to delete the profile entirely",
		Examples: []string{
			"genyee auth logout",
			"genyee --profile staging auth logout",
			"genyee auth logout --all",
		},
		Output: "envelope.data is {removed: [profile names cleared]}",
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"auth logout clears local credentials for this profile; server-side session continues until expiry.",
		},
	})
	return cmd
}

func runLogout(opts *LogoutOptions, fopts *cmdutil.FormatOptions, f *cmdutil.Factory) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	if len(cfg.Profiles) == 0 {
		if active, kind := cmdutil.EnvCredential(); active {
			return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
				"authenticated via "+kind+" (stateless env credential) — nothing is stored to log out; unset "+kind+" to drop it")
		}
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no profiles configured; nothing to log out")
	}

	targets, err := pickLogoutTargets(opts, cfg)
	if err != nil {
		return err
	}

	// Destructive-write protocol: confirm before clearing credentials.
	scope := fmt.Sprintf("%d profile(s) [%s]", len(targets), strings.Join(targets, ", "))
	retryCmd := []string{"genyee", "auth", "logout", "-y"}
	if opts.All {
		retryCmd = []string{"genyee", "auth", "logout", "--all", "-y"}
	}
	if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, fopts.WantsJSON(), "delete", "auth credentials", scope, "auth.logout", retryCmd); err != nil {
		return err
	}

	store, err := f.Secrets()
	if err != nil {
		return err
	}
	for _, name := range targets {
		if err := clearProfileSecrets(store, cfg.Profiles[name], name); err != nil {
			return err
		}
	}
	for _, name := range targets {
		// Keep the profile registered (its host stays) — only clear the
		// credential refs so it reads as logged-out and can be re-authed with
		// `auth login`. Deleting the profile entirely is `profile remove`'s job;
		// that clean logout(who)/remove(what) split matches gh / lark.
		p := cfg.Profiles[name]
		p.APIKeyRef = ""
		p.TokenRef = ""
		p.RefreshRef = ""
		cfg.Profiles[name] = p
	}
	if err := f.SaveConfig(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, logoutResult{Removed: targets}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Logged out of %d profile(s): %s\n", len(targets), strings.Join(targets, ", "))
	return nil
}

// pickLogoutTargets resolves the set of profiles to clear from flags + config.
func pickLogoutTargets(opts *LogoutOptions, cfg *config.Config) ([]string, error) {
	if opts.All {
		names := make([]string, 0, len(cfg.Profiles))
		for n := range cfg.Profiles {
			if err := cmdutil.ValidateProfileName(n); err != nil {
				return nil, cmdutil.Wrapf(cmdutil.CodeLocalConfigCorrupt, err, "invalid profile %q in config", n)
			}
			names = append(names, n)
		}
		sort.Strings(names)
		return names, nil
	}
	name := cfg.CurrentProfile
	if name == "" {
		return nil, cmdutil.NewError(cmdutil.CodeInputMissingFlag,
			"no active profile set; use the global --profile <profile> or --all")
	}
	if err := cmdutil.ValidateProfileName(name); err != nil {
		return nil, cmdutil.Wrapf(cmdutil.CodeLocalConfigCorrupt, err, "invalid profile %q in config", name)
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return nil, cmdutil.NewError(cmdutil.CodeLocalProfileNotFound,
			fmt.Sprintf("profile %q not found in config", name))
	}
	return []string{name}, nil
}

// clearProfileSecrets leaves failures visible so configuration references are
// retained until every stored credential slot has been deleted. Slots backed by
// an external `env:` reference are skipped; empty refs still call Delete so
// orphaned secrets left behind by manual edits or partial switches are purged.
func clearProfileSecrets(store secrets.Store, c config.Profile, name string) error {
	for _, slot := range []struct{ key, ref string }{{"access", c.TokenRef}, {"refresh", c.RefreshRef}, {"api_key", c.APIKeyRef}} {
		if strings.HasPrefix(slot.ref, "env:") {
			continue
		}
		if err := store.Delete(name, slot.key); err != nil {
			return cmdutil.Wrapf(cmdutil.CodeLocalKeychainDenied, err, "delete %s credential for profile %q", slot.key, name)
		}
	}
	return nil
}
