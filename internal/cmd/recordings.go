package cmd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type recordingsCommand struct {
	cmd      *cobra.Command
	startsOn string
	endsOn   string
	limit    int
	all      bool
}

func newRecordingsCommand() *recordingsCommand {
	recordingsCommand := &recordingsCommand{}
	recordingsCommand.cmd = &cobra.Command{
		Use:   "recordings <calendar-id>",
		Short: "List recordings (events, todos, etc.) for a calendar",
		Annotations: map[string]string{
			"agent_notes": "Returns recordings grouped by type for a calendar. Defaults to today + 30 days.",
		},
		Example: `  hey recordings 123
  hey recordings 123 --starts-on 2024-01-01 --ends-on 2024-01-31
  hey recordings 123 --limit 5 --json`,
		RunE: recordingsCommand.run,
		Args: usageExactOneArg(),
	}

	recordingsCommand.cmd.Flags().StringVar(&recordingsCommand.startsOn, "starts-on", "", "Start date (YYYY-MM-DD, defaults to today)")
	recordingsCommand.cmd.Flags().StringVar(&recordingsCommand.endsOn, "ends-on", "", "End date (YYYY-MM-DD, defaults to 30 days from starts-on)")
	recordingsCommand.cmd.Flags().IntVar(&recordingsCommand.limit, "limit", 0, "Maximum number of recordings per type to show")
	recordingsCommand.cmd.Flags().BoolVar(&recordingsCommand.all, "all", false, "Fetch all results (override --limit)")

	return recordingsCommand
}

func (c *recordingsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	calendarID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid calendar ID: %s", args[0]))
	}

	startsOn := c.startsOn
	if startsOn == "" {
		startsOn = time.Now().Format("2006-01-02")
	}
	endsOn := c.endsOn
	if endsOn == "" {
		var start time.Time
		start, err = time.Parse("2006-01-02", startsOn)
		if err != nil {
			return output.ErrUsage(fmt.Sprintf("invalid starts-on date: %s", startsOn))
		}
		endsOn = start.AddDate(0, 0, 30).Format("2006-01-02")
	}

	ctx := cmd.Context()
	resp, err := sdk.Calendars().GetRecordings(ctx, calendarID, &generated.GetCalendarRecordingsParams{
		StartsOn: startsOn,
		EndsOn:   endsOn,
	})
	if err != nil {
		return convertSDKError(err)
	}

	if resp == nil {
		resp = &generated.CalendarRecordingsResponse{}
	}
	var total, shown int
	for _, recordings := range *resp {
		total += len(recordings)
	}
	if c.limit > 0 && !c.all {
		for key, recordings := range *resp {
			if len(recordings) > c.limit {
				(*resp)[key] = recordings[:c.limit]
			}
		}
	}
	for _, recordings := range *resp {
		shown += len(recordings)
	}
	notice := output.TruncationNotice(shown, total)

	if writer.IsStyled() {
		for recType, recordings := range *resp {
			if len(recordings) == 0 {
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s:\n", recType)
			table := newTable(cmd.OutOrStdout())
			table.addRow([]string{"ID", "Title", "Starts", "Ends"})
			for _, r := range recordings {
				table.addRow([]string{fmt.Sprintf("%d", r.Id), r.Title, formatTimestamp(r.StartsAt), formatTimestamp(r.EndsAt)})
			}
			table.print()
		}
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(resp,
		output.WithSummary(fmt.Sprintf("Recordings for calendar %d (%s to %s)", calendarID, startsOn, endsOn)),
		output.WithNotice(notice),
	)
}
