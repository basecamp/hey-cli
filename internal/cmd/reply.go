package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type replyCommand struct {
	cmd               *cobra.Command
	message           string
	messageHTML       string
	attachments       []string
	to                []string
	cc                []string
	bcc               []string
	draft             bool
	replaceRecipients bool
	dryRun            bool
}

type replyPreview struct {
	ThreadID  int64            `json:"thread_id"`
	EntryID   int64            `json:"entry_id"`
	AccountID int64            `json:"account_id"`
	Subject   string           `json:"subject"`
	From      mail.ReplySender `json:"from"`
	To        []string         `json:"to"`
	CC        []string         `json:"cc"`
	BCC       []string         `json:"bcc"`
}

func newReplyCommand() *replyCommand {
	replyCommand := &replyCommand{}
	replyCommand.cmd = &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Reply to a thread",
		Long: `Reply to a thread's latest entry.

The reply is addressed the way HEY's own web app addresses one: everyone that entry was
addressed to, with whoever wrote it on the To line. Repeatable --to, --cc and --bcc
values add or move recipients on those lines; each value can also be comma-separated.
--replace-recipients uses only the explicitly supplied recipients instead. A dry run
resolves and prints the complete envelope without requiring a message or sending one.`,
		Annotations: map[string]string{
			"agent_notes": "Replies to the latest entry in a thread, addressed the way HEY addresses a reply: everyone that entry was addressed to, plus its sender on the To line, minus the acting user's own addresses. Repeatable --to/--cc/--bcc flags merge explicit recipients into that prefill; --replace-recipients uses only the explicit lists. --dry-run is read-only, needs no message, and returns the resolved sender, recipients, subject, account, thread and entry. Accepts message via -m, stdin, or $EDITOR, plus repeatable --attach files; an attachment can be sent without body text. The message is Markdown; use --message-html to send raw HTML instead. --draft saves the reply as a draft, carries the resolved recipients, and answers the draft ID for hey draft show/edit/send/delete.",
		},
		Example: `  hey reply 12345 -m "Friday works for me — I'll send an agenda."
  hey reply 12345 --to support@example.com -m "The replacement is on the way."
  hey reply 12345 --to support@example.com --replace-recipients --dry-run --json
  hey reply 12345 -m "Attached is the report." --attach ./report.pdf
  hey reply 12345 -m "Drafting a longer answer — sending tomorrow." --draft
  echo "Longer reply from a file or a heredoc" | hey reply 12345`,
		RunE: replyCommand.run,
		Args: usageExactOneArg(),
	}

	replyCommand.cmd.Flags().StringVarP(&replyCommand.message, "message", "m", "", "Reply message as Markdown (or opens $EDITOR)")
	replyCommand.cmd.Flags().StringVar(&replyCommand.messageHTML, "message-html", "", "Reply message as raw HTML instead of Markdown")
	replyCommand.cmd.Flags().StringArrayVar(&replyCommand.attachments, "attach", nil, "File to attach (repeatable)")
	replyCommand.cmd.Flags().StringArrayVar(&replyCommand.to, "to", nil, "To recipient email address (repeatable or comma-separated)")
	replyCommand.cmd.Flags().StringArrayVar(&replyCommand.cc, "cc", nil, "CC recipient email address (repeatable or comma-separated)")
	replyCommand.cmd.Flags().StringArrayVar(&replyCommand.bcc, "bcc", nil, "BCC recipient email address (repeatable or comma-separated)")
	replyCommand.cmd.Flags().BoolVar(&replyCommand.draft, "draft", false, "Save as a draft instead of sending")
	replyCommand.cmd.Flags().BoolVar(&replyCommand.replaceRecipients, "replace-recipients", false, "Replace HEY's reply recipients with the explicit To, CC and BCC lists")
	replyCommand.cmd.Flags().BoolVar(&replyCommand.dryRun, "dry-run", false, "Print the resolved reply envelope without sending")
	replyCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html")
	replyCommand.cmd.MarkFlagsMutuallyExclusive("draft", "dry-run")

	return replyCommand
}

func (c *replyCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	overrides := replyRecipients{
		To:  parseReplyAddresses(c.to),
		CC:  parseReplyAddresses(c.cc),
		BCC: parseReplyAddresses(c.bcc),
	}
	if _, err := applyReplyRecipientOverrides(replyRecipients{}, overrides, c.replaceRecipients); err != nil {
		return err
	}

	threadID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return apierr.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	ctx := cmd.Context()

	target, err := resolveThreadReply(ctx, threadID)
	if err != nil {
		return err
	}
	target.Addressed, err = applyReplyRecipientOverrides(target.Addressed, overrides, c.replaceRecipients)
	if err != nil {
		return err
	}
	if !replyHasRecipients(target.Addressed) {
		return apierr.ErrUsage("could not determine thread recipients; supply --to, --cc or --bcc")
	}
	if c.dryRun {
		return writeReplyPreview(cmd, threadID, target)
	}
	replySDK := target.client

	message := c.messageHTML
	if message == "" {
		markdownMessage := c.message
		if markdownMessage == "" && !stdinIsTerminal() {
			markdownMessage, err = readStdin()
			if err != nil {
				return err
			}
			if markdownMessage == "" && len(c.attachments) == 0 {
				return apierr.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
			}
		} else if markdownMessage == "" && len(c.attachments) == 0 {
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

	message, err = attachFilesWithClient(ctx, replySDK, message, c.attachments)
	if err != nil {
		return err
	}
	if c.draft {
		draftID, draftErr := replySDK.Entries().CreateReplyDraft(ctx, target.EntryID, target.ActingSenderID, target.Subject, message,
			target.Addressed.To, target.Addressed.CC, target.Addressed.BCC)
		if draftErr != nil {
			return apierr.FromSDK(draftErr)
		}
		return writeDraftSaved(cmd, draftID, len(c.attachments))
	}
	if err = replySDK.Entries().CreateReply(ctx, target.EntryID, target.ActingSenderID, target.Subject, message, target.Addressed.To, target.Addressed.CC, target.Addressed.BCC); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, sentWithAttachmentsSummary("Reply sent", len(c.attachments)), nil,
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     fmt.Sprintf("hey thread read %d", threadID),
			Description: "View the full thread",
		}),
	)
}

func parseReplyAddresses(values []string) []string {
	addresses := make([]string, 0, len(values))
	for _, value := range values {
		addresses = append(addresses, parseAddresses(value)...)
	}
	return addresses
}

func writeReplyPreview(cmd *cobra.Command, threadID int64, target *threadReplyTarget) error {
	from, err := replySenderForPreview(cmd.Context(), target)
	if err != nil {
		return err
	}
	preview := replyPreview{
		ThreadID:  threadID,
		EntryID:   target.EntryID,
		AccountID: target.AccountID,
		Subject:   target.Subject,
		From:      from,
		To:        nonNilAddresses(target.Addressed.To),
		CC:        nonNilAddresses(target.Addressed.CC),
		BCC:       nonNilAddresses(target.Addressed.BCC),
	}
	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Thread: %d\n", preview.ThreadID)
		fmt.Fprintf(cmd.OutOrStdout(), "Entry: %d\n", preview.EntryID)
		fmt.Fprintf(cmd.OutOrStdout(), "Account: %d\n", preview.AccountID)
		fmt.Fprintf(cmd.OutOrStdout(), "Subject: %s\n", terminal.SanitizeLine(preview.Subject))
		fmt.Fprintf(cmd.OutOrStdout(), "From: %s\n", terminal.SanitizeLine(formatReplySender(preview.From)))
		fmt.Fprintf(cmd.OutOrStdout(), "To: %s\n", terminal.SanitizeLine(strings.Join(preview.To, ", ")))
		fmt.Fprintf(cmd.OutOrStdout(), "CC: %s\n", terminal.SanitizeLine(strings.Join(preview.CC, ", ")))
		fmt.Fprintf(cmd.OutOrStdout(), "BCC: %s\n", terminal.SanitizeLine(strings.Join(preview.BCC, ", ")))
		fmt.Fprintln(cmd.OutOrStdout(), "Nothing sent.")
		return nil
	}
	return writeOK(preview, output.WithSummary("Reply preview; nothing sent"))
}

func nonNilAddresses(addresses []string) []string {
	if addresses == nil {
		return []string{}
	}
	return addresses
}

func formatReplySender(sender mail.ReplySender) string {
	name := terminal.SanitizeLine(sender.Name)
	email := terminal.SanitizeLine(sender.EmailAddress)
	switch {
	case name != "" && email != "":
		return fmt.Sprintf("%s <%s>", name, email)
	case email != "":
		return email
	default:
		return strconv.FormatInt(sender.ID, 10)
	}
}
