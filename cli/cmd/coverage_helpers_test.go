package cmd

import (
	"github.com/spf13/cobra"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
)

// eachLeafCommand invokes fn for every runnable command (leaves plus parent
// commands that expose an explicit AgentHelp contract such as `kb config`) in
// the tree rooted at root, skipping cobra's generated `completion`/`help`
// subtrees and hidden commands.
func eachLeafCommand(root *cobra.Command, fn func(*cobra.Command)) {
	exempt := map[string]bool{"completion": true, "help": true}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		realKids := 0
		for _, k := range c.Commands() {
			if exempt[k.Name()] || k.Hidden {
				continue
			}
			realKids++
			walk(k)
		}
		if c == root || exempt[c.Name()] {
			return
		}
		if realKids > 0 {
			if _, ok := cmdutil.AgentHelpFor(c); !ok {
				return // pure group command — not runnable on its own
			}
		}
		fn(c)
	}
	walk(root)
}
