package cmd

import (
	"github.com/spf13/cobra"
)

type localScreenerApproveCommand struct {
	cmd *cobra.Command
}

func newLocalScreenerApproveCommand() *localScreenerApproveCommand {
	approveCommand := &localScreenerApproveCommand{}
	approveCommand.cmd = &cobra.Command{
		Use:   "approve <sender>",
		Short: "Screen a sender in",
		Long:  "Screen a sender in through the HEY app, so their email reaches you. Undo with hey local screener deny.",
		Example: `  hey local screener approve 42
  hey local screener approve 42 --json`,
		RunE: approveCommand.run,
		Args: usageExactOneArg(),
	}
	return approveCommand
}

func (c *localScreenerApproveCommand) run(cmd *cobra.Command, args []string) error {
	return decideLocally(cmd, args[0], "in")
}
