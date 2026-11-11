package cmd

import (
	"github.com/spf13/cobra"
)

type localScreenerDenyCommand struct {
	cmd *cobra.Command
}

func newLocalScreenerDenyCommand() *localScreenerDenyCommand {
	denyCommand := &localScreenerDenyCommand{}
	denyCommand.cmd = &cobra.Command{
		Use:   "deny <sender>",
		Short: "Screen a sender out",
		Long:  "Screen a sender out through the HEY app, so their email goes to Screened Out, kept there for 90 days, and contacts nobody. Undo with hey local screener approve.",
		Example: `  hey local screener deny 42
  hey local screener deny 42 --json`,
		RunE: denyCommand.run,
		Args: usageExactOneArg(),
	}
	return denyCommand
}

func (c *localScreenerDenyCommand) run(cmd *cobra.Command, args []string) error {
	return decideLocally(cmd, args[0], "out")
}
