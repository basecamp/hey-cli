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
		RunE:  replyCommand.run,
		Args:  cobra.ExactArgs(1),
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
		var err error
		message, err = editor.Open("")
		if err != nil {
			return fmt.Errorf("could not open editor: %w", err)
		}
		if message == "" {
			return fmt.Errorf("empty message, aborting")
		}
	}

	path := fmt.Sprintf("/entries/%s/replies", args[0])
	body := map[string]interface{}{"body": message}

	data, err := apiClient.PostJSON(path, body)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Println("Reply sent.")
	return nil
}
