package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type calendarsCommand struct {
	cmd *cobra.Command
}

func newCalendarsCommand() *calendarsCommand {
	calendarsCommand := &calendarsCommand{}
	calendarsCommand.cmd = &cobra.Command{
		Use:   "calendars",
		Short: "List calendars",
		Example: `  hey calendars
  hey calendars --json`,
		RunE: calendarsCommand.run,
	}

	return calendarsCommand
}

func (c *calendarsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	calendars, err := apiClient.ListCalendars()
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		table := newTable()
		table.addRow([]string{"ID", "Name", "Kind", "Owned"})
		for _, cal := range calendars {
			owned := "no"
			if cal.Owned {
				owned = "yes"
			}
			table.addRow([]string{fmt.Sprintf("%d", cal.ID), cal.Name, cal.Kind, owned})
		}
		table.print()
		return nil
	}

	return writer.OK(calendars,
		output.WithSummary(fmt.Sprintf("%d calendars", len(calendars))),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey recordings <calendar-id>",
			Description: "List recordings for a calendar",
		}),
	)
}
