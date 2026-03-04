package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type topicCommand struct {
	cmd *cobra.Command
}

func newThreadsCommand() *topicCommand {
	threadsCommand := &topicCommand{}
	threadsCommand.cmd = &cobra.Command{
		Use:   "threads <id>",
		Short: "Read an email thread",
		Example: `  hey threads 12345
  hey threads 12345 --json`,
		RunE: threadsCommand.run,
		Args: usageExactArgs(1),
	}

	return threadsCommand
}

func (c *topicCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid thread ID: %s", args[0])
	}

	entries, err := apiClient.GetTopicEntries(threadID)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printJSON(entries)
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
		if htmlOutput && e.BodyHTML != "" {
			fmt.Println()
			fmt.Println(e.BodyHTML)
		} else if e.Body != "" {
			fmt.Println()
			fmt.Println(e.Body)
		}
		fmt.Println()
	}

	return nil
}
