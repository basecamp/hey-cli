package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
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
		Annotations: map[string]string{
			"agent_notes": "Returns all calendars with IDs. Pipe IDs to hey recordings <id>.",
		},
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

	ctx := cmd.Context()
	payload, err := sdk.Calendars().List(ctx)
	if err != nil {
		return apierr.FromSDK(err)
	}

	calendars := unwrapCalendars(payload)

	if writer.IsStyled() {
		// The account is named only when there is more than one, since it is what tells two
		// calendars of the same name apart — a reader with a work account and a personal one has
		// two called "Maybe" and nothing else to go on. With one account it would be their own
		// address written down the column.
		accounts := spansAccounts(calendars)

		table := newTable(cmd.OutOrStdout())
		header := []string{"ID", "Name", "Kind", "Owned"}
		if accounts {
			header = append(header, "Account")
		}
		table.addRow(header)

		for _, cal := range calendars {
			owned := "no"
			if cal.Owned {
				owned = "yes"
			}
			row := []string{fmt.Sprintf("%d", cal.Id), cal.Name, cal.Kind, owned}
			if accounts {
				row = append(row, cal.OwnerEmailAddress)
			}
			table.addRow(row)
		}
		table.print()
		return nil
	}

	return writeOK(calendars,
		output.WithSummary(fmt.Sprintf("%d calendars", len(calendars))),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey recordings <calendar-id>",
			Description: "List recordings for a calendar",
		}),
	)
}

// spansAccounts is whether the list covers more than one HEY account, which is the only time
// naming them tells the reader anything.
func spansAccounts(calendars []generated.Calendar) bool {
	seen := ""
	for _, calendar := range calendars {
		if calendar.OwnerEmailAddress == "" {
			continue
		}
		if seen != "" && calendar.OwnerEmailAddress != seen {
			return true
		}
		seen = calendar.OwnerEmailAddress
	}
	return false
}
