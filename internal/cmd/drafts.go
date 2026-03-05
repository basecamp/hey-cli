package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

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
			"agent_notes": "Returns saved draft messages with IDs, summaries, and kind.",
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

	drafts, err := apiClient.ListDrafts()
	if err != nil {
		return err
	}

	total := len(drafts)
	if c.limit > 0 && !c.all && len(drafts) > c.limit {
		drafts = drafts[:c.limit]
	}
	notice := output.TruncationNotice(len(drafts), total)

	if writer.IsStyled() {
		if len(drafts) == 0 {
			fmt.Println("No drafts.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Summary", "Kind", "Date"})
		for _, d := range drafts {
			date := ""
			if len(d.UpdatedAt) >= 10 {
				date = d.UpdatedAt[:10]
			}
			table.addRow([]string{fmt.Sprintf("%d", d.ID), truncate(d.Summary, 60), d.Kind, date})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writer.OK(drafts, statsOption(),
		output.WithSummary(fmt.Sprintf("%d drafts", len(drafts))),
		output.WithNotice(notice),
	)
}
