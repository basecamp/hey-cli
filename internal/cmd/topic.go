package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
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
		Args: cobra.ExactArgs(1),
	}

	return topicCommand
}

func (c *topicCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	topicID, err := strconv.Atoi(args[0])
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid topic ID: %s", args[0]))
	}

	entries, err := apiClient.GetTopicEntries(topicID)
	if err != nil {
		return err
	}

	if writer.IsStyled() {
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

	return writer.OK(entries,
		output.WithSummary(fmt.Sprintf("%d entries in topic %d", len(entries), topicID)),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "reply",
				Command:     fmt.Sprintf("hey reply %d", topicID),
				Description: "Reply to this thread",
			},
		),
	)
}
