package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

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
		Short: "Write and send a new email",
		Annotations: map[string]string{
			"agent_notes": "Starts a new thread with --to (optionally --cc/--bcc), which requires --subject, or replies to an existing one with --thread-id, which does not. Repeatable --attach files are uploaded before sending and can be sent without body text.",
		},
		Example: `  hey compose --to alice@example.com --subject "Hello" -m "Hi there"
  hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Hello" -m "Hi"
  hey compose --to alice@example.com --subject "Report" -m "Attached." --attach ./report.pdf
  hey compose --thread-id 12345 -m "Thread reply" --attach ./diagram.png
  echo "Long message" | hey compose --to bob@example.com --subject "Report"`,
		RunE: composeCommand.run,
	}

	composeCommand.cmd.Flags().StringVar(&composeCommand.to, "to", "", "Recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.cc, "cc", "", "CC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.bcc, "bcc", "", "BCC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.subject, "subject", "", "Message subject (required for a new message)")
	composeCommand.cmd.Flags().StringVarP(&composeCommand.message, "message", "m", "", "Message body (or opens $EDITOR)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.threadID, "thread-id", "", "Reply to this thread instead of starting a new one")
	composeCommand.cmd.Flags().StringArrayVar(&composeCommand.attachments, "attach", nil, "File to attach (repeatable)")

	return composeCommand
}

func (c *composeCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	// A reply carries the thread's subject, so only a new message needs one.
	if c.subject == "" && c.threadID == "" {
		return output.ErrUsageHint("--subject is required", "hey compose --to <email> --subject <subject> -m <message>")
	}

	message := c.message
	if message == "" && !stdinIsTerminal() {
		var err error
		message, err = readStdin()
		if err != nil {
			return err
		}
		if message == "" && len(c.attachments) == 0 {
			return output.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
		}
	} else if message == "" && len(c.attachments) == 0 {
		var err error
		message, err = editor.Open("")
		if err != nil {
			return output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
		}
		if message == "" {
			return output.ErrUsage("empty message, aborting")
		}
	}

	ctx := cmd.Context()

	if c.threadID != "" {
		topicID, parseErr := strconv.ParseInt(c.threadID, 10, 64)
		if parseErr != nil {
			return output.ErrUsage(fmt.Sprintf("invalid thread ID: %s", c.threadID))
		}
		target, resolveErr := resolveThreadReply(ctx, topicID)
		if resolveErr != nil {
			return resolveErr
		}
		replySDK, resolveErr := clientForResourceAccount(ctx, target.AccountID)
		if resolveErr != nil {
			return resolveErr
		}
		messageWithAttachments, attachErr := attachFilesWithClient(ctx, replySDK, message, c.attachments)
		if attachErr != nil {
			return attachErr
		}
		if err := replySDK.Entries().CreateReply(ctx, target.EntryID, messageWithAttachments,
			target.Addressed.To, target.Addressed.CC, target.Addressed.BCC); err != nil {
			return convertSDKError(err)
		}
	} else {
		to := parseAddresses(c.to)
		cc := parseAddresses(c.cc)
		bcc := parseAddresses(c.bcc)
		if len(to)+len(cc)+len(bcc) == 0 {
			return output.ErrUsage("a message needs at least one recipient (to, cc or bcc)")
		}
		messageWithAttachments, attachErr := attachFiles(ctx, message, c.attachments)
		if attachErr != nil {
			return attachErr
		}
		if err := sdk.Messages().Create(ctx, c.subject, messageWithAttachments, to, cc, bcc); err != nil {
			return convertSDKError(err)
		}
	}

	summary := sentWithAttachmentsSummary("Message sent", len(c.attachments))
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
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
