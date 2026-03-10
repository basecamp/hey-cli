package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type draftsCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newDraftsCommand() *draftsCommand {
	draftsCommand := &draftsCommand{}
	draftsCommand.cmd = &cobra.Command{
		Use:   "drafts",
		Short: "List drafts",
		Annotations: map[string]string{
			"agent_notes": "Returns saved draft messages with IDs, summaries, and subjects.",
		},
		Example: `  hey drafts
  hey drafts --limit 10
  hey drafts --json`,
		RunE: draftsCommand.run,
	}

	draftsCommand.cmd.Flags().IntVar(&draftsCommand.limit, "limit", 0, "Maximum number of drafts to show")
	draftsCommand.cmd.Flags().BoolVar(&draftsCommand.all, "all", false, "Fetch all results (override --limit)")

	return draftsCommand
}

func (c *draftsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	result, err := sdk.Entries().ListDrafts(ctx, nil)
	if err != nil {
		return convertSDKError(err)
	}

	var drafts []generated.DraftMessage
	if result != nil {
		drafts = *result
	}
	total := len(drafts)
	if c.limit > 0 && !c.all && len(drafts) > c.limit {
		drafts = drafts[:c.limit]
	}
	notice := output.TruncationNotice(len(drafts), total)

	if writer.IsStyled() {
		if len(drafts) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No drafts.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Summary", "Subject", "Date"})
		for _, d := range drafts {
			table.addRow([]string{fmt.Sprintf("%d", d.Id), truncate(d.Summary, 60), d.Subject, formatDate(d.UpdatedAt)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(drafts,
		output.WithSummary(fmt.Sprintf("%d drafts", len(drafts))),
		output.WithNotice(notice),
	)
}
