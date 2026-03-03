package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"hey-cli/internal/models"
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
		if c.limit > 0 {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(data, &obj); err == nil {
				for key, val := range obj {
					obj[key] = json.RawMessage(limitJSONArray([]byte(val), c.limit))
				}
				if limited, err := json.Marshal(obj); err == nil {
					data = limited
				}
			}
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
		if c.limit > 0 && len(recordings) > c.limit {
			recordings = recordings[:c.limit]
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
