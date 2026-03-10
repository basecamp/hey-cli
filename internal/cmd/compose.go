package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/output"
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
		Annotations: map[string]string{
			"agent_notes": "Creates a new email. Requires --subject. Use --to for new threads or --thread-id for existing ones.",
		},
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

	ctx := cmd.Context()
	var result any

	if c.threadID != "" {
		topicID, err := strconv.ParseInt(c.threadID, 10, 64)
		if err != nil {
			return output.ErrUsage(fmt.Sprintf("invalid thread ID: %s", c.threadID))
		}
		resp, err := sdk.Messages().CreateTopicMessage(ctx, topicID, generated.CreateTopicMessageJSONRequestBody{
			Content: message,
		})
		if err != nil {
			return convertSDKError(err)
		}
		result = resp
	} else {
		to := []string{}
		if c.to != "" {
			for _, addr := range strings.Split(c.to, ",") {
				addr = strings.TrimSpace(addr)
				if addr != "" {
					to = append(to, addr)
				}
			}
		}
		resp, err := sdk.Messages().Create(ctx, generated.CreateMessageJSONRequestBody{
			Subject: c.subject,
			Content: message,
			To:      to,
		})
		if err != nil {
			return convertSDKError(err)
		}
		result = resp
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Message sent.%s\n", extractMutationInfoFromResult(result))
		return nil
	}

	normalized, err := normalizeAny(result)
	if err != nil {
		return writeOK(nil, output.WithSummary("Message sent"))
	}
	return writeOK(normalized, output.WithSummary("Message sent"))
}
