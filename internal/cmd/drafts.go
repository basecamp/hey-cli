package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hey-cli/internal/models"
)

type draftsCommand struct {
	cmd *cobra.Command
}

func newDraftsCommand() *draftsCommand {
	draftsCommand := &draftsCommand{}
	draftsCommand.cmd = &cobra.Command{
		Use:   "drafts",
		Short: "List draft entries",
		RunE:  draftsCommand.run,
	}

	return draftsCommand
}

func (c *draftsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if jsonOutput {
		data, err := apiClient.Get("/entries/drafts.json")
		if err != nil {
			return err
		}
		return printRawJSON(data)
	}

	var drafts []models.Draft
	if err := apiClient.GetJSON("/entries/drafts.json", &drafts); err != nil {
		return err
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
