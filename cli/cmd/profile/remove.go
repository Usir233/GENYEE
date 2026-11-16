package profilecmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/prompt"
	"github.com/Usir233/GENYEE/cli/internal/secrets"
)

type RemoveOptions struct {
	Yes    bool // sourced from the global -y/--yes persistent flag (matches `kb delete`)
	DryRun bool
}

// profileRemoveFields enumerates the fields surfaced for `--format json` discovery on
// `profile remove`. The result reports the disposition of the removed entry.
var profileRemoveFields = []string{
	"name", "removed", "was_current",
}

// removeResult is the typed payload emitted under data on success.
type removeResult struct {
	Name       string `json:"name"`
	Removed    bool   `json:"removed"`
	WasCurrent bool   `json:"was_current"`
}

// NewCmdRemove builds `genyee profile remove`. Drops the entry from
// config.yaml and best-effort clears keyring references. Removing a
// non-current profile is low-friction (no prompt). Removing the *current*
// profile triggers the destructive-write confirmation protocol (exit 10),
// because subsequent commands will have no default connection target.
func NewCmdRemove(f *cmdutil.Factory) *cobra.Command {
	opts := &RemoveOptions{}
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a profile (drops entry, clears keyring refs)",
		Long: `Deletes the named profile from config.yaml after clearing its stored
credentials (matches ` + "`genyee auth logout`" + `).

Removing the current profile also clears CurrentProfile - subsequent commands
will error until you select another with ` + "`genyee profile use <name>`" + ` or pick
one up via the global ` + "`--profile`" + ` flag. Because that change is observable in
every later command, removing the current profile requires explicit -y/--yes
in scripted / --format json invocations (exit code 10; see cli/README.md).`,
		Example: `  genyee profile remove staging              # remove non-current → no prompt
  genyee profile remove production -y        # remove current → confirm`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			// Pure-local existence check runs before the dry-run gate so
			// --dry-run rejects unknown profile names identically to the live
			// path. Same notFoundError as runRemove (kept there for direct
			// callers).
			cfg, cfgErr := loadProfileConfig()
			if cfgErr != nil {
				return cfgErr
			}
			if _, exists := cfg.Profiles[args[0]]; !exists {
				return notFoundError(args[0], cfg)
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "profile.remove",
				Args: map[string]any{
					"name": args[0],
				},
			}); handled {
				return err
			}
			store, err := f.Secrets()
			if err != nil {
				return err
			}
			return runRemove(opts, fopts, args[0], store, f.Prompter())
		},
	}
	cmdutil.AddFormatFlag(cmd, profileRemoveFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetRisk(cmd, "profile.remove")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "remove a named profile and its stored credentials",
		RequiredFlags: []string{"<name> (positional)"},
		Output:        "envelope.data is {name, removed:true, was_current}",
		Examples: []string{
			"genyee profile remove staging",
			"genyee profile remove production -y",
		},
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"profile remove deletes local credentials + config; the server-side token is not revoked (use 'auth logout' on server instead).",
		},
	})
	return cmd
}

func runRemove(opts *RemoveOptions, fopts *cmdutil.FormatOptions, name string, store secrets.Store, p prompt.Prompter) error {
	cfg, err := loadProfileConfig()
	if err != nil {
		return err
	}
	ctx, exists := cfg.Profiles[name]
	if !exists {
		return notFoundError(name, cfg)
	}
	wasCurrent := name == cfg.CurrentProfile

	jsonOut := fopts.WantsJSON()
	// Confirmation only fires for removing the current profile - non-current
	// remove uses the same low-friction policy as `auth logout`.
	if wasCurrent {
		if err := cmdutil.ConfirmDestructive(p, opts.Yes, jsonOut, "remove", "current profile", name, "profile.remove", []string{"genyee", "profile", "remove", name, "-y"}); err != nil {
			return err
		}
	}

	// Delete credentials first so failures leave references available for retry.
	if err := clearProfileSecrets(store, ctx, name); err != nil {
		return err
	}
	delete(cfg.Profiles, name)
	if wasCurrent {
		cfg.CurrentProfile = ""
	}
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}

	result := removeResult{Name: name, Removed: true, WasCurrent: wasCurrent}
	if jsonOut {
		return fopts.Emit(iostreams.IO.Out, result, nil)
	}
	if wasCurrent {
		fmt.Fprintf(iostreams.IO.Out, "✓ Removed profile %s (current profile cleared - run `genyee profile use <name>` to pick another)\n", name)
	} else {
		fmt.Fprintf(iostreams.IO.Out, "✓ Removed profile %s\n", name)
	}
	return nil
}

// clearProfileSecrets reports failed cleanup before the profile is removed,
// keeping the references available for a later retry. Non-env slots are deleted
// unconditionally even when their config ref is empty so orphaned secrets do
// not survive profile removal.
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
