package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hey-cli/internal/models"
)

type topicCommand struct {
	cmd *cobra.Command
}

func newTopicCommand() *topicCommand {
	topicCommand := &topicCommand{}
	topicCommand.cmd = &cobra.Command{
		Use:   "topic <id>",
		Short: "Read an email thread",
		Example: `  hey topic 12345
  hey topic 12345 --json`,
		RunE: topicCommand.run,
		Args:  cobra.ExactArgs(1),
	}

	return topicCommand
}

func (c *topicCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	path := fmt.Sprintf("/topics/%s/entries.json", args[0])

	if jsonOutput {
		data, err := apiClient.Get(path)
		if err != nil {
			return err
		}
		return printRawJSON(data)
	}

	var entries []models.Entry
	if err := apiClient.GetJSON(path, &entries); err != nil {
		return err
	}

	for i, e := range entries {
		if i > 0 {
			fmt.Println(strings.Repeat("─", 60))
		}
		from := e.Creator.Name
		if from == "" {
			from = e.Creator.EmailAddress
		}
		if e.AlternativeSenderName != "" {
			from = e.AlternativeSenderName
		}
		date := ""
		if len(e.CreatedAt) >= 16 {
			date = e.CreatedAt[:16]
		}
		fmt.Printf("From: %s  [%s]  #%d\n", from, date, e.ID)
		if e.Summary != "" {
			fmt.Println(e.Summary)
		}
		if e.Body != "" {
			fmt.Println()
			fmt.Println(e.Body)
		}
		fmt.Println()
	}

	return nil
}
