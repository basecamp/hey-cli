package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hey-cli/internal/models"
)

type recordingsCommand struct {
	cmd  *cobra.Command
	from string
	to   string
}

func newRecordingsCommand() *recordingsCommand {
	recordingsCommand := &recordingsCommand{}
	recordingsCommand.cmd = &cobra.Command{
		Use:   "recordings <calendar-id>",
		Short: "List recordings (events, todos, etc.) for a calendar",
		RunE:  recordingsCommand.run,
		Args:  cobra.ExactArgs(1),
	}

	recordingsCommand.cmd.Flags().StringVar(&recordingsCommand.from, "from", "", "Start date (YYYY-MM-DD)")
	recordingsCommand.cmd.Flags().StringVar(&recordingsCommand.to, "to", "", "End date (YYYY-MM-DD)")

	return recordingsCommand
}

func (c *recordingsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	path := fmt.Sprintf("/calendar/calendars/%s/recordings.json", args[0])
	sep := "?"
	if c.from != "" {
		path += sep + "from=" + c.from
		sep = "&"
	}
	if c.to != "" {
		path += sep + "to=" + c.to
	}

	if jsonOutput {
		data, err := apiClient.Get(path)
		if err != nil {
			return err
		}
		return printRawJSON(data)
	}

	var resp models.RecordingsResponse
	if err := apiClient.GetJSON(path, &resp); err != nil {
		return err
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
