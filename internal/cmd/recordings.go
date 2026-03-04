package cmd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type recordingsCommand struct {
	cmd      *cobra.Command
	startsOn string
	endsOn   string
	limit    int
}

func newRecordingsCommand() *recordingsCommand {
	recordingsCommand := &recordingsCommand{}
	recordingsCommand.cmd = &cobra.Command{
		Use:   "recordings <calendar-id>",
		Short: "List recordings (events, todos, etc.) for a calendar",
		Example: `  hey recordings 123
  hey recordings 123 --starts-on 2024-01-01 --ends-on 2024-01-31
  hey recordings 123 --limit 5 --json`,
		RunE: recordingsCommand.run,
		Args: cobra.ExactArgs(1),
	}

	recordingsCommand.cmd.Flags().StringVar(&recordingsCommand.startsOn, "starts-on", "", "Start date (YYYY-MM-DD, defaults to today)")
	recordingsCommand.cmd.Flags().StringVar(&recordingsCommand.endsOn, "ends-on", "", "End date (YYYY-MM-DD, defaults to 30 days from starts-on)")
	recordingsCommand.cmd.Flags().IntVar(&recordingsCommand.limit, "limit", 0, "Maximum number of recordings per type to show")

	return recordingsCommand
}

func (c *recordingsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	calendarID, err := strconv.Atoi(args[0])
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

	resp, err := apiClient.GetCalendarRecordings(calendarID, startsOn, endsOn)
	if err != nil {
		return err
	}

	if c.limit > 0 {
		for key, recordings := range resp {
			if len(recordings) > c.limit {
				resp[key] = recordings[:c.limit]
			}
		}
	}

	if writer.IsStyled() {
		for recType, recordings := range resp {
			if len(recordings) == 0 {
				continue
			}
			fmt.Printf("\n%s:\n", recType)
			table := newTable()
			table.addRow([]string{"ID", "Title", "Starts", "Ends"})
			for _, r := range recordings {
				starts := ""
				if len(r.StartsAt) >= 16 {
					starts = r.StartsAt[:16]
				}
				ends := ""
				if len(r.EndsAt) >= 16 {
					ends = r.EndsAt[:16]
				}
				table.addRow([]string{fmt.Sprintf("%d", r.ID), r.Title, starts, ends})
			}
			table.print()
		}
		return nil
	}

	return writer.OK(resp,
		output.WithSummary(fmt.Sprintf("Recordings for calendar %d (%s to %s)", calendarID, startsOn, endsOn)),
	)
}
