package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hey-cli/internal/models"
)

type calendarsCommand struct {
	cmd *cobra.Command
}

func newCalendarsCommand() *calendarsCommand {
	calendarsCommand := &calendarsCommand{}
	calendarsCommand.cmd = &cobra.Command{
		Use:   "calendars",
		Short: "List calendars",
		RunE:  calendarsCommand.run,
	}

	return calendarsCommand
}

func (c *calendarsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if jsonOutput {
		data, err := apiClient.Get("/calendar/calendars.json")
		if err != nil {
			return err
		}
		return printRawJSON(data)
	}

	var resp models.CalendarsResponse
	if err := apiClient.GetJSON("/calendar/calendars.json", &resp); err != nil {
		return err
	}

	table := newTable()
	table.addRow([]string{"ID", "Name", "Kind", "Owned"})
	for _, cal := range resp.Calendars {
		owned := "no"
		if cal.Owned {
			owned = "yes"
		}
		table.addRow([]string{fmt.Sprintf("%d", cal.ID), cal.Name, cal.Kind, owned})
	}
	table.print()
	return nil
}
