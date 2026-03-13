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

func newThreadsCommand() *topicCommand {
	threadsCommand := &topicCommand{}
	threadsCommand.cmd = &cobra.Command{
		Use:   "threads <id>",
		Short: "Read an email thread",
		Annotations: map[string]string{
			"agent_notes": "Returns a thread with all entries. Use entry IDs with hey reply.",
		},
		Example: `  hey threads 12345
  hey threads 12345 --json`,
		RunE: threadsCommand.run,
		Args: usageExactOneArg(),
	}

	return threadsCommand
}

func (c *topicCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	entries, err := apiClient.GetTopicEntries(threadID)
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		for i, e := range entries {
			if i > 0 {
				fmt.Fprintln(w, strings.Repeat("─", 60))
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
			fmt.Fprintf(w, "From: %s  [%s]  #%d\n", from, date, e.ID)
			if e.Summary != "" {
				fmt.Fprintln(w, e.Summary)
			}
			if htmlOutput && e.BodyHTML != "" {
				fmt.Fprintln(w)
				fmt.Fprintln(w, e.BodyHTML)
			} else if e.Body != "" {
				fmt.Fprintln(w)
				fmt.Fprintln(w, e.Body)
			}
			fmt.Fprintln(w)
		}
		return nil
	}

	return writeOK(entries,
		output.WithSummary(fmt.Sprintf("%d entries in thread %d", len(entries), threadID)),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "reply",
				Command:     fmt.Sprintf("hey reply %d", threadID),
				Description: "Reply to this thread",
			},
		),
	)
}
