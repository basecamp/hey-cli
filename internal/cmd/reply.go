package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"hey-cli/internal/editor"
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
		Example: `  hey reply 12345 -m "Thanks!"
  echo "Detailed reply" | hey reply 12345`,
		RunE: replyCommand.run,
		Args: usageExactArgs(1),
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
		return fmt.Errorf("invalid thread ID: %s", args[0])
	}

	entries, err := apiClient.GetTopicEntries(threadID)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("no entries found for thread %d", threadID)
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
				return fmt.Errorf("no message provided (use -m or --message to provide inline, or pipe to stdin)")
			}
		} else {
			message, err = editor.Open("")
			if err != nil {
				return fmt.Errorf("could not open editor: %w", err)
			}
			if message == "" {
				return fmt.Errorf("empty message, aborting")
			}
		}
	}

	body := map[string]interface{}{"body": message}

	data, err := apiClient.ReplyToEntry(fmt.Sprintf("%d", latestEntryID), body)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Reply sent.%s\n", extractMutationInfo(data))
	return nil
}
