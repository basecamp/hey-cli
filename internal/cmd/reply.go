package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/output"
)

type replyCommand struct {
	cmd     *cobra.Command
	message string
}

func newReplyCommand() *replyCommand {
	replyCommand := &replyCommand{}
	replyCommand.cmd = &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Reply to a thread",
		Annotations: map[string]string{
			"agent_notes": "Replies to the latest entry in a thread. Accepts message via -m, stdin, or $EDITOR.",
		},
		Example: `  hey reply 12345 -m "Thanks!"
  echo "Detailed reply" | hey reply 12345`,
		RunE: replyCommand.run,
		Args: usageExactOneArg(),
	}

	replyCommand.cmd.Flags().StringVarP(&replyCommand.message, "message", "m", "", "Reply message (or opens $EDITOR)")

	return replyCommand
}

func (c *replyCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.Atoi(args[0])
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	entries, err := apiClient.GetTopicEntries(threadID)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return output.ErrNotFound("entries for thread", args[0])
	}

	latestEntryID := entries[len(entries)-1].ID

	message := c.message
	if message == "" {
		if !stdinIsTerminal() {
			message, err = readStdin()
			if err != nil {
				return err
			}
			if message == "" {
				return output.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
			}
		} else {
			message, err = editor.Open("")
			if err != nil {
				return output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
			}
			if message == "" {
				return output.ErrUsage("empty message, aborting")
			}
		}
	}

	body := map[string]any{"body": message}

	data, err := apiClient.ReplyToEntry(fmt.Sprintf("%d", latestEntryID), body)
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Reply sent.%s\n", extractMutationInfo(data))
		return nil
	}

	normalized, err := output.NormalizeJSONNumbers(data)
	if err != nil {
		return writeOK(nil, output.WithSummary("Reply sent"))
	}
	return writeOK(normalized,
		output.WithSummary("Reply sent"),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     fmt.Sprintf("hey threads %d", threadID),
			Description: "View the full thread",
		}),
	)
}
