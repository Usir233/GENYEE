package profilecmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
)

// profileUseFields enumerates fields surfaced for `--format json` discovery on
// `profile use`.
var profileUseFields = []string{"current_profile", "previous_profile"}

// NewCmdUse builds the `genyee profile use <name>` command.
func NewCmdUse(_ *cmdutil.Factory) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the default profile for subsequent commands",
		Long: `Switches the default profile written in config.yaml. Names are case-sensitive.

The active profile is what every subsequent command uses for auth + host. The
global --profile flag (e.g. genyee --profile staging kb list) overrides for
one command without writing to disk.

AI agents: Do NOT switch the active profile unless the user explicitly asked
you to. Profile selection is a user preference; one-shot overrides should use
the global --profile flag instead, which writes nothing to disk.`,
		Example: `  genyee profile use staging               # persist switch
  genyee --profile staging kb list         # one-shot override (no disk write)
  genyee profile use staging --format json        # {current_profile, previous_profile}`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			cfg, err := loadProfileConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Profiles[args[0]]; !ok {
				return notFoundError(args[0], cfg)
			}
			if handled, err := cmdutil.HandleDryRun(c, dryRun, cmdutil.DryRunPlan{
				Action: "profile.use",
				Args:   map[string]any{"name": args[0]},
			}); handled {
				return err
			}
			return runUseWithConfig(args[0], cfg, fopts)
		},
	}
	cmdutil.AddFormatFlag(cmd, profileUseFields...)
	cmdutil.AddDryRunFlag(cmd, &dryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "switch the default profile for subsequent commands (persists to config)",
		RequiredFlags: []string{"<name> (positional)"},
		Examples:      []string{"genyee profile use staging"},
		Output:        "envelope.data confirms the now-active profile",
	})
	return cmd
}

type useResult struct {
	CurrentProfile  string `json:"current_profile"`
	PreviousProfile string `json:"previous_profile,omitempty"`
}

func runUse(name string, fopts *cmdutil.FormatOptions) error {
	cfg, err := loadProfileConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return notFoundError(name, cfg)
	}
	return runUseWithConfig(name, cfg, fopts)
}

func runUseWithConfig(name string, cfg *config.Config, fopts *cmdutil.FormatOptions) error {
	prev := cfg.CurrentProfile
	cfg.CurrentProfile = name
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}
	result := useResult{CurrentProfile: name, PreviousProfile: prev}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, result, nil)
	}
	if prev != "" && prev != name {
		_, _ = fmt.Fprintf(iostreams.IO.Out, "✓ Switched profile to %s (was %s)\n", name, prev)
	} else {
		_, _ = fmt.Fprintf(iostreams.IO.Out, "✓ Active profile: %s\n", name)
	}
	return nil
}

func notFoundError(name string, cfg *config.Config) error {
	if len(cfg.Profiles) == 0 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeLocalProfileNotFound,
			Message: fmt.Sprintf("profile not found: %s", name),
			Hint:    "no profiles registered - run `genyee auth login` first",
		}
	}
	keys := profileKeys(cfg.Profiles)
	candidate := cmdutil.SuggestOne(name, keys)
	var hint string
	if candidate != "" && candidate != name {
		hint = fmt.Sprintf("did you mean: %q?", candidate)
	} else {
		hint = fmt.Sprintf("available profiles: %v", keys)
	}
	return &cmdutil.Error{
		Code:    cmdutil.CodeLocalProfileNotFound,
		Message: fmt.Sprintf("profile not found: %s", name),
		Hint:    hint,
	}
}

func profileKeys(m map[string]config.Profile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
