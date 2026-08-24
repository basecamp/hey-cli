package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
)

type composeCommand struct {
	cmd         *cobra.Command
	to          string
	cc          string
	bcc         string
	subject     string
	message     string
	messageHTML string
	threadID    string
	attachments []string
}

func newComposeCommand() *composeCommand {
	composeCommand := &composeCommand{}
	composeCommand.cmd = &cobra.Command{
		Use:   "compose",
		Short: "Write and send a new email",
		Annotations: map[string]string{
			"agent_notes": "Starts a new thread with --to (optionally --cc/--bcc), which requires --subject, or replies to an existing one with --thread-id, which does not. Repeatable --attach files are uploaded before sending and can be sent without body text. The body is Markdown; use --message-html to send raw HTML instead.",
		},
		Example: `  hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"
  hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Kitchen remodel timeline" -m "Cabinets land the week of the 14th."
  hey compose --to alice@example.com --subject "Q3 revenue report" -m "The numbers are attached." --attach ./report.pdf
  hey compose --thread-id 12345 -m "Confirmed — see you then." --attach ./diagram.png
  hey compose --to alice@example.com --subject "Sprint recap" -m "We **shipped** the pagination fix."
  hey compose --to alice@example.com --subject "Newsletter draft" --message-html "<h1>March</h1><p>What we shipped.</p>"
  echo "Notes from the offsite" | hey compose --to bob@example.com --subject "Offsite recap"`,
		RunE: composeCommand.run,
	}

	composeCommand.cmd.Flags().StringVar(&composeCommand.to, "to", "", "Recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.cc, "cc", "", "CC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.bcc, "bcc", "", "BCC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.subject, "subject", "", "Message subject (required for a new message)")
	composeCommand.cmd.Flags().StringVarP(&composeCommand.message, "message", "m", "", "Message body as Markdown (or opens $EDITOR)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.messageHTML, "message-html", "", "Message body as raw HTML instead of Markdown")
	composeCommand.cmd.Flags().StringVar(&composeCommand.threadID, "thread-id", "", "Reply to this thread instead of starting a new one")
	composeCommand.cmd.Flags().StringArrayVar(&composeCommand.attachments, "attach", nil, "File to attach (repeatable)")
	composeCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html")

	return composeCommand
}

func (c *composeCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	// A reply carries the thread's subject, so only a new message needs one.
	if c.subject == "" && c.threadID == "" {
		return apierr.ErrUsageHint("--subject is required", "hey compose --to <email> --subject <subject> -m <message>")
	}

	message := c.messageHTML
	if message == "" {
		markdownMessage := c.message
		if markdownMessage == "" && !stdinIsTerminal() {
			var err error
			markdownMessage, err = readStdin()
			if err != nil {
				return err
			}
			if markdownMessage == "" && len(c.attachments) == 0 {
				return apierr.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
			}
		} else if markdownMessage == "" && len(c.attachments) == 0 {
			var err error
			markdownMessage, err = editor.Open("")
			if err != nil {
				return apierr.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
			}
			if markdownMessage == "" {
				return apierr.ErrUsage("empty message, aborting")
			}
		}
		message = htmlutil.FromMarkdown(markdownMessage)
	}

	ctx := cmd.Context()

	if c.threadID != "" {
		topicID, parseErr := strconv.ParseInt(c.threadID, 10, 64)
		if parseErr != nil {
			return apierr.ErrUsage(fmt.Sprintf("invalid thread ID: %s", c.threadID))
		}
		target, resolveErr := resolveThreadReply(ctx, topicID)
		if resolveErr != nil {
			return resolveErr
		}
		replySDK := target.client
		messageWithAttachments, attachErr := attachFilesWithClient(ctx, replySDK, message, c.attachments)
		if attachErr != nil {
			return attachErr
		}
		if err := replySDK.Entries().CreateReply(ctx, target.EntryID, messageWithAttachments,
			target.Addressed.To, target.Addressed.CC, target.Addressed.BCC); err != nil {
			return apierr.FromSDK(err)
		}
	} else {
		to := parseAddresses(c.to)
		cc := parseAddresses(c.cc)
		bcc := parseAddresses(c.bcc)
		if len(to)+len(cc)+len(bcc) == 0 {
			return apierr.ErrUsage("a message needs at least one recipient (to, cc or bcc)")
		}
		messageWithAttachments, attachErr := attachFiles(ctx, message, c.attachments)
		if attachErr != nil {
			return attachErr
		}
		if err := sdk.Messages().Create(ctx, c.subject, messageWithAttachments, to, cc, bcc); err != nil {
			return apierr.FromSDK(err)
		}
	}

	return writeMutation(cmd, sentWithAttachmentsSummary("Message sent", len(c.attachments)), nil)
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
