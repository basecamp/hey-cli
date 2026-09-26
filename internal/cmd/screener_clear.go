package cmd

import (
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type screenerClearCommand struct {
	cmd *cobra.Command
}

func newScreenerClearCommand() *screenerClearCommand {
	clearCommand := &screenerClearCommand{}
	clearCommand.cmd = &cobra.Command{
		Use:   "clear",
		Short: "Trash everything waiting in the Screener",
		Long:  "Clear The Screener by moving everything waiting there to Trash (a shared thread loses your access instead). Nobody is approved or denied — each sender is screened again the next time they write.",
		Annotations: map[string]string{
			"agent_notes": "Clears the whole queue for the account, not one sender, and takes no confirmation: everything waiting goes to Trash, or loses your access if it is a shared thread. It approves and denies no one, so senders reappear in The Screener on their next email. Confirm with the user first. HEY queues the work, so `hey screener list` may still show the queue for a moment afterwards.",
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
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd,
		"Screener cleared. What was waiting goes to Trash; each sender is screened again on their next email.",
		"Screener cleared",
		map[string]any{"action": "clear", "queued": true},
		output.WithBreadcrumbs(output.Breadcrumb{Action: "list", Command: "hey screener list", Description: "Check the queue"}),
	)
}
