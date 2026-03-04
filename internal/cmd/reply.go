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
		Use:   "reply <topic-id>",
		Short: "Reply to an email topic",
		Example: `  hey reply 12345 -m "Thanks!"
  echo "Detailed reply" | hey reply 12345`,
		RunE: replyCommand.run,
		Args: cobra.ExactArgs(1),
	}

	replyCommand.cmd.Flags().StringVarP(&replyCommand.message, "message", "m", "", "Reply message (or opens $EDITOR)")

	return replyCommand
}

func (c *replyCommand) run(cmd *cobra.Command, args []string) error {
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
	if len(entries) == 0 {
		return output.ErrNotFound("entries for topic", args[0])
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
		fmt.Printf("Reply sent.%s\n", extractMutationInfo(data))
		return nil
	}

	normalized, err := output.NormalizeJSONNumbers(data)
	if err != nil {
		return writer.OK(nil, output.WithSummary("Reply sent"))
	}
	return writer.OK(normalized,
		output.WithSummary("Reply sent"),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     fmt.Sprintf("hey topic %d", topicID),
			Description: "View the full thread",
		}),
	)
}
