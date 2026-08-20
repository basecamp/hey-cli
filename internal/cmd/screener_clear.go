package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type screenerClearCommand struct {
	cmd *cobra.Command
}

func newScreenerClearCommand() *screenerClearCommand {
	clearCommand := &screenerClearCommand{}
	clearCommand.cmd = &cobra.Command{
		Use:   "clear",
		Short: "Empty the Screener without deciding",
		Long:  "Drop everyone waiting in The Screener. Nobody is screened in or out — each sender is asked about again the next time they write.",
		Annotations: map[string]string{
			"agent_notes": "Clears the whole queue for the account, not one sender, and takes no confirmation. It decides nothing: senders reappear in The Screener on their next email, so what they already sent stays hidden until then. HEY queues the work, so `hey screener list` may still show the queue for a moment afterwards.",
		},
		Example: `  hey screener clear
  hey screener clear --json`,
		RunE: clearCommand.run,
		Args: cobra.NoArgs,
	}
	return clearCommand
}

func (c *screenerClearCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if err := sdk.Clearances().Punt(cmd.Context()); err != nil {
		return convertSDKError(err)
	}
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Screener cleared. Everyone waiting will be asked about again on their next email.")
		return nil
	}
	return writeOK(map[string]any{"action": "clear", "queued": true},
		output.WithSummary("Screener cleared"),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "list", Command: "hey screener list", Description: "Check the queue"}),
	)
}
