package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/attachments"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/output"
)

type composeCommand struct {
	cmd         *cobra.Command
	to          string
	cc          string
	bcc         string
	subject     string
	message     string
	threadID    string
	attachments []string
}

func newComposeCommand() *composeCommand {
	composeCommand := &composeCommand{}
	composeCommand.cmd = &cobra.Command{
		Use:   "compose",
		Short: "Compose a new message",
		Annotations: map[string]string{
			"agent_notes": "Creates a new email. Requires --subject. Use --to (optionally with --cc/--bcc) for new threads or --thread-id for existing ones. Attach files with repeatable -a/--attachment; works for both new messages and --thread-id replies.",
		},
		Example: `  hey compose --to alice@example.com --subject "Hello" -m "Hi there"
  hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Hello" -m "Hi"
  hey compose --to alice@example.com --subject "Q3 deck" -m "See attached" -a ~/reports/q3.pdf -a ~/charts/revenue.png
  hey compose --subject "Update" --thread-id 12345 -m "Thread reply"
  echo "Long message" | hey compose --to bob@example.com --subject "Report"`,
		RunE: composeCommand.run,
	}

	composeCommand.cmd.Flags().StringVar(&composeCommand.to, "to", "", "Recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.cc, "cc", "", "CC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.bcc, "bcc", "", "BCC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.subject, "subject", "", "Message subject (required)")
	composeCommand.cmd.Flags().StringVarP(&composeCommand.message, "message", "m", "", "Message body (or opens $EDITOR)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.threadID, "thread-id", "", "Thread ID to post message to")
	composeCommand.cmd.Flags().StringArrayVarP(&composeCommand.attachments, "attachment", "a", nil, "Path to a file to attach (repeatable)")

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

	var topicID int64
	if c.threadID != "" {
		var err error
		topicID, err = strconv.ParseInt(c.threadID, 10, 64)
		if err != nil {
			return output.ErrUsage(fmt.Sprintf("invalid thread ID: %s", c.threadID))
		}
	}

	message, err := c.attachFiles(ctx, message)
	if err != nil {
		return err
	}

	if c.threadID != "" {
		if err := sdk.Messages().CreateTopicMessage(ctx, topicID, message); err != nil {
			return convertSDKError(err)
		}
	} else {
		to := parseAddresses(c.to)
		cc := parseAddresses(c.cc)
		bcc := parseAddresses(c.bcc)
		if err := sdk.Messages().Create(ctx, c.subject, message, to, cc, bcc); err != nil {
			return convertSDKError(err)
		}
	}

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Message sent.")
		return nil
	}

	return writeOK(nil, output.WithSummary("Message sent"))
}

// attachFiles validates every attachment path, uploads each file through the
// Active Storage direct-upload flow, and appends the resulting markup to the
// message content. With no attachments it returns the content unchanged so
// existing behavior is preserved.
func (c *composeCommand) attachFiles(ctx context.Context, message string) (string, error) {
	if len(c.attachments) == 0 {
		return message, nil
	}

	// Validate everything up front so a bad path never results in a partial
	// upload or a sent message.
	for _, path := range c.attachments {
		if err := attachments.Validate(path); err != nil {
			return "", err
		}
	}

	uploader := newAttachmentUploader()
	uploaded := make([]*attachments.Attachment, 0, len(c.attachments))
	for _, path := range c.attachments {
		att, err := uploader.Upload(ctx, path)
		if err != nil {
			return "", err
		}
		uploaded = append(uploaded, att)
	}

	return attachments.AppendMarkup(message, uploaded), nil
}

func parseAddresses(s string) []string {
	if s == "" {
		return nil
	}
	var addrs []string
	for _, addr := range strings.Split(s, ",") {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			addrs = append(addrs, addr)
		}
	}
	return addrs
}
