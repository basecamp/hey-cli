package cmd

import (
	"fmt"

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
		Use:   "reply <entry-id>",
		Short: "Reply to an email entry",
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

	message := c.message
	if message == "" {
		if !stdinIsTerminal() {
			var err error
			message, err = readStdin()
			if err != nil {
				return err
			}
			if message == "" {
				return fmt.Errorf("no message provided (use -m or --message to provide inline, or pipe to stdin)")
			}
		} else {
			var err error
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

	data, err := apiClient.ReplyToEntry(args[0], body)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Reply sent.%s\n", extractMutationInfo(data))
	return nil
}
