package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type draftsCommand struct {
	cmd   *cobra.Command
	limit int
}

func newDraftsCommand() *draftsCommand {
	draftsCommand := &draftsCommand{}
	draftsCommand.cmd = &cobra.Command{
		Use:   "drafts",
		Short: "List draft entries",
		Example: `  hey drafts
  hey drafts --limit 10
  hey drafts --json`,
		RunE: draftsCommand.run,
	}

	draftsCommand.cmd.Flags().IntVar(&draftsCommand.limit, "limit", 0, "Maximum number of drafts to show")

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

	if c.limit > 0 && len(drafts) > c.limit {
		drafts = drafts[:c.limit]
	}

	if jsonOutput {
		return printJSON(drafts)
	}

	if len(drafts) == 0 {
		fmt.Println("No drafts.")
		return nil
	}

	table := newTable()
	table.addRow([]string{"ID", "Summary", "Kind", "Date"})
	for _, d := range drafts {
		date := ""
		if len(d.UpdatedAt) >= 10 {
			date = d.UpdatedAt[:10]
		}
		table.addRow([]string{fmt.Sprintf("%d", d.ID), truncate(d.Summary, 60), d.Kind, date})
	}
	table.print()
	return nil
}
