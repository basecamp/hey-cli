package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"hey-cli/internal/editor"
)

type composeCommand struct {
	cmd      *cobra.Command
	to       string
	subject  string
	message  string
	threadID string
}

func newComposeCommand() *composeCommand {
	composeCommand := &composeCommand{}
	composeCommand.cmd = &cobra.Command{
		Use:   "compose",
		Short: "Compose a new message",
		Example: `  hey compose --to alice@hey.com --subject "Hello" -m "Hi there"
  hey compose --subject "Update" --thread-id 12345 -m "Thread reply"
  echo "Long message" | hey compose --to bob@hey.com --subject "Report"`,
		RunE: composeCommand.run,
	}

	composeCommand.cmd.Flags().StringVar(&composeCommand.to, "to", "", "Recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.subject, "subject", "", "Message subject (required)")
	composeCommand.cmd.Flags().StringVarP(&composeCommand.message, "message", "m", "", "Message body (or opens $EDITOR)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.threadID, "thread-id", "", "Thread ID to post message to")

	return composeCommand
}

func (c *composeCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if c.subject == "" {
		return fmt.Errorf("--subject is required")
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

	body := map[string]interface{}{
		"subject": c.subject,
		"body":    message,
	}
	if c.to != "" {
		body["to"] = c.to
	}

	var threadID *int
	if c.threadID != "" {
		id, err := strconv.Atoi(c.threadID)
		if err != nil {
			return fmt.Errorf("invalid thread ID: %s", c.threadID)
		}
		threadID = &id
	}

	data, err := apiClient.CreateMessage(threadID, body)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Message sent.%s\n", extractMutationInfo(data))
	return nil
}
