package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type eventCommand struct {
	cmd *cobra.Command
}

func newEventCommand() *eventCommand {
	eventCommand := &eventCommand{}
	eventCommand.cmd = &cobra.Command{
		Use:   "event",
		Short: "Manage calendar events",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: list. Lists events from the personal calendar by default, or from --calendar ID.",
		},
	}

	eventCommand.cmd.AddCommand(newEventListCommand().cmd)

	return eventCommand
}

// list

type eventListCommand struct {
	cmd        *cobra.Command
	limit      int
	all        bool
	calendarID int64
}

func newEventListCommand() *eventListCommand {
	eventListCommand := &eventListCommand{}
	eventListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List calendar events",
		Example: `  hey event list
  hey event list --limit 10
  hey event list --calendar 123
  hey event list --ids-only`,
		RunE: eventListCommand.run,
	}

	eventListCommand.cmd.Flags().IntVar(&eventListCommand.limit, "limit", 0, "Maximum number of events to show")
	eventListCommand.cmd.Flags().BoolVar(&eventListCommand.all, "all", false, "Fetch all results (override --limit)")
	eventListCommand.cmd.Flags().Int64Var(&eventListCommand.calendarID, "calendar", 0, "Calendar ID (defaults to personal calendar)")

	return eventListCommand
}

func (c *eventListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()

	var events []generated.Recording
	if c.calendarID != 0 {
		resp, err := sdk.Calendars().GetRecordings(ctx, c.calendarID, nil)
		if err != nil {
			return convertSDKError(err)
		}
		events = filterRecordingsByType(resp, "Calendar::Event")
	} else {
		resp, err := listPersonalRecordings(ctx)
		if err != nil {
			return err
		}
		events = filterRecordingsByType(resp, "Calendar::Event")
	}

	total := len(events)
	if c.limit > 0 && !c.all && len(events) > c.limit {
		events = events[:c.limit]
	}
	notice := output.TruncationNotice(len(events), total)

	if writer.IsStyled() {
		if len(events) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No events.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Title", "Starts", "Ends"})
		for _, e := range events {
			table.addRow([]string{fmt.Sprintf("%d", e.Id), e.Title, formatTimestamp(e.StartsAt), formatTimestamp(e.EndsAt)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(events,
		output.WithSummary(fmt.Sprintf("%d events", len(events))),
		output.WithNotice(notice),
	)
}
