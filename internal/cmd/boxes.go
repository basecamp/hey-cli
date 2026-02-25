package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hey-cli/internal/models"
)

type boxesCommand struct {
	cmd *cobra.Command
}

func newBoxesCommand() *boxesCommand {
	boxesCommand := &boxesCommand{}
	boxesCommand.cmd = &cobra.Command{
		Use:   "boxes",
		Short: "List mailboxes",
		RunE:  boxesCommand.run,
	}

	return boxesCommand
}

func (c *boxesCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if jsonOutput {
		data, err := apiClient.Get("/boxes.json")
		if err != nil {
			return err
		}
		return printRawJSON(data)
	}

	var boxes []models.Box
	if err := apiClient.GetJSON("/boxes.json", &boxes); err != nil {
		return err
	}

	table := newTable()
	table.addRow([]string{"ID", "Kind", "Name"})
	for _, b := range boxes {
		table.addRow([]string{fmt.Sprintf("%d", b.ID), b.Kind, b.Name})
	}
	table.print()
	return nil
}
