// Package profilecmd holds the `genyee profile` command tree
// (list / add / remove / use).
//
// Package name `profilecmd` (not `profile`) to keep the pattern consistent
// with other cmd subpackages.
// The cobra Use: string is "profile" - this is what users type.
package profilecmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
)

func loadProfileConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		if errors.Is(err, config.ErrCorrupt) {
			return nil, cmdutil.Wrapf(cmdutil.CodeLocalConfigCorrupt, err, "config malformed")
		}
		return nil, cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "load config")
	}
	return cfg, nil
}

// NewCmd builds the `genyee profile` parent command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage CLI profiles (named connection targets)",
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdAdd(f))
	cmd.AddCommand(NewCmdRemove(f))
	cmd.AddCommand(NewCmdUse(f))
	return cmd
}
