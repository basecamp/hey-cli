package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/output"
)

type composeCommand struct {
	cmd     *cobra.Command
	to      string
	subject string
	message string
	topicID string
}

func newComposeCommand() *composeCommand {
	composeCommand := &composeCommand{}
	composeCommand.cmd = &cobra.Command{
		Use:   "compose",
		Short: "Compose a new message",
		Example: `  hey compose --to alice@hey.com --subject "Hello" -m "Hi there"
  hey compose --subject "Update" --topic-id 12345 -m "Thread reply"
  echo "Long message" | hey compose --to bob@hey.com --subject "Report"`,
		RunE: composeCommand.run,
	}

	composeCommand.cmd.Flags().StringVar(&composeCommand.to, "to", "", "Recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.subject, "subject", "", "Message subject (required)")
	composeCommand.cmd.Flags().StringVarP(&composeCommand.message, "message", "m", "", "Message body (or opens $EDITOR)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.topicID, "topic-id", "", "Topic ID to post message to")

	return composeCommand
}

func (c *composeCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if c.subject == "" {
		return output.ErrUsageHint("--subject is required", "hey compose --to <email> --subject <subject> -m <message>")
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
				return output.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
			}
		} else {
			var err error
			message, err = editor.Open("")
			if err != nil {
				return output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
			}
			if message == "" {
				return output.ErrUsage("empty message, aborting")
			}
		}
	}

	body := map[string]any{
		"subject": c.subject,
		"body":    message,
	}
	if c.to != "" {
		body["to"] = c.to
	}

	var topicID *int
	if c.topicID != "" {
		id, err := strconv.Atoi(c.topicID)
		if err != nil {
			return output.ErrUsage(fmt.Sprintf("invalid topic ID: %s", c.topicID))
		}
		topicID = &id
	}

	data, err := apiClient.CreateMessage(topicID, body)
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		fmt.Printf("Message sent.%s\n", extractMutationInfo(data))
		return nil
	}

	normalized, err := output.NormalizeJSONNumbers(data)
	if err != nil {
		return writer.OK(nil, output.WithSummary("Message sent"))
	}
	return writer.OK(normalized, output.WithSummary("Message sent"))
}
