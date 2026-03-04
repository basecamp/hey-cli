package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

type recordingsCommand struct {
	cmd   *cobra.Command
	from  string
	to    string
	limit int
}

func newRecordingsCommand() *recordingsCommand {
	recordingsCommand := &recordingsCommand{}
	recordingsCommand.cmd = &cobra.Command{
		Use:   "recordings <calendar-id>",
		Short: "List recordings (events, todos, etc.) for a calendar",
		Example: `  hey recordings 123
  hey recordings 123 --from 2024-01-01 --to 2024-01-31
  hey recordings 123 --limit 5 --json`,
		RunE: recordingsCommand.run,
		Args: cobra.ExactArgs(1),
	}

	recordingsCommand.cmd.Flags().StringVar(&recordingsCommand.from, "from", "", "Start date (YYYY-MM-DD)")
	recordingsCommand.cmd.Flags().StringVar(&recordingsCommand.to, "to", "", "End date (YYYY-MM-DD)")
	recordingsCommand.cmd.Flags().IntVar(&recordingsCommand.limit, "limit", 0, "Maximum number of recordings per type to show")

	return recordingsCommand
}

func (c *recordingsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	calendarID, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid calendar ID: %s", args[0])
	}

	resp, err := apiClient.GetCalendarRecordings(calendarID, c.from, c.to)
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

	if jsonOutput {
		return printJSON(resp)
	}

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
