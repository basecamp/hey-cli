package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
)

type composeCommand struct {
	cmd         *cobra.Command
	from        string
	to          string
	cc          string
	bcc         string
	subject     string
	message     string
	messageHTML string
	threadID    string
	attachments []string
	draft       bool
	noNameTag   bool
}

func newComposeCommand() *composeCommand {
	composeCommand := &composeCommand{}
	composeCommand.cmd = &cobra.Command{
		Use:   "compose",
		Short: "Write and send a new email",
		Long: `Write and send a new email, or reply to a thread with --thread-id.

--thread-id answers the thread's latest emailed message, as hey reply does: a note or
share notice posted after it is internal and never emailed, and a thread holding nothing
else is refused.

A --to, --cc or --bcc address HEY would drop without saying so — one with no domain,
or a top-level domain HEY does not know — is refused before anything is sent.

A send answers the new message's id and the topic_id of the thread it is on (for hey
thread read or hey reply), with its subject and delayed, which is true while Undo Send
holds the delivery back. Whatever HEY's answer leaves out is left out here too; nothing
is guessed in its place. --draft saves instead of sending and answers the draft's id.`,
		Args: recipientsChecked(nil),
		Annotations: map[string]string{
			"agent_notes": "--from selects a configured sender email or ID from account senders; --account must agree. --from is only for new messages. Starts a new thread with --to (optionally --cc/--bcc), which requires --subject, or replies to an existing one with --thread-id, which does not; --thread-id answers the thread's latest emailed message, never a note (kind \"comment\") or share notice (kind \"access_notice\"), and a thread with no emailed message is refused as not_found. Repeatable --attach files are uploaded before sending and can be sent without body text. The body is Markdown; use --message-html to send raw HTML instead. --draft saves instead of sending — recipients become optional — and answers the draft ID for hey draft show/edit/send/delete. A new message ends with the sender's HEY name tag, as one composed in HEY does; --no-name-tag leaves it out. A send answers the new message's id and the topic_id of its thread (for hey thread read), with subject and delayed (true while Undo Send holds it back) — only the fields HEY's answer carries, never guessed.",
		},
		Example: `  hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"
  hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Kitchen remodel timeline" -m "Cabinets land the week of the 14th."
  hey compose --to alice@example.com --subject "Q3 revenue report" -m "The numbers are attached." --attach ./report.pdf
  hey compose --thread-id 12345 -m "Confirmed — see you then." --attach ./diagram.png
  hey compose --to alice@example.com --subject "Sprint recap" -m "We **shipped** the pagination fix."
  hey compose --to alice@example.com --subject "Newsletter draft" --message-html "<h1>March</h1><p>What we shipped.</p>"
  echo "Notes from the offsite" | hey compose --to bob@example.com --subject "Offsite recap"
  hey compose --subject "Board update" -m "Numbers to follow." --draft  # save a draft; add recipients later`,
		RunE: composeCommand.run,
	}

	composeCommand.cmd.Flags().StringVar(&composeCommand.from, "from", "", "Configured sender email or ID (see hey account senders)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.to, "to", "", "Recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.cc, "cc", "", "CC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.bcc, "bcc", "", "BCC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.subject, "subject", "", "Message subject (required for a new message)")
	composeCommand.cmd.Flags().StringVarP(&composeCommand.message, "message", "m", "", "Message body as Markdown (or opens $EDITOR)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.messageHTML, "message-html", "", "Message body as raw HTML instead of Markdown")
	composeCommand.cmd.Flags().StringVar(&composeCommand.threadID, "thread-id", "", "Reply to this thread's latest emailed message instead of starting a new one")
	composeCommand.cmd.Flags().StringArrayVar(&composeCommand.attachments, "attach", nil, "File to attach (repeatable)")
	composeCommand.cmd.Flags().BoolVar(&composeCommand.draft, "draft", false, "Save as a draft instead of sending")
	composeCommand.cmd.Flags().BoolVar(&composeCommand.noNameTag, "no-name-tag", false, "Leave the sender's HEY name tag off a new message")
	composeCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html")

	return composeCommand
}

func (c *composeCommand) run(cmd *cobra.Command, args []string) error {
	if cmd.Flags().Changed("from") && (strings.TrimSpace(c.from) == "" || c.threadID != "") {
		return apierr.ErrUsage("--from requires a sender email or ID and is only supported for new messages")
	}
	if err := requireAuth(); err != nil {
		return err
	}

	// A reply carries the thread's subject, so only a new message needs one.
	if c.subject == "" && c.threadID == "" {
		return apierr.ErrUsageHint("--subject is required", "hey compose --to <email> --subject <subject> -m <message>")
	}

	ctx := cmd.Context()
	summary := sentWithAttachmentsSummary("Message sent", len(c.attachments))
	var senderClient *hey.Client
	var sender generated.Sender
	if cmd.Flags().Changed("from") {
		var err error
		senderClient, sender, err = selectedSender(ctx, c.from, 0)
		if err != nil {
			return err
		}
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

	if c.threadID != "" {
		topicID, parseErr := strconv.ParseInt(c.threadID, 10, 64)
		if parseErr != nil {
			return apierr.ErrUsage(fmt.Sprintf("invalid thread ID: %s", c.threadID))
		}
		target, resolveErr := resolveThreadReply(ctx, topicID)
		if resolveErr != nil {
			return resolveErr
		}
		if !replyHasRecipients(target.Addressed) {
			return apierr.ErrUsage("could not determine thread recipients; use hey reply with --to, --cc or --bcc")
		}
		replySDK := target.client
		messageWithAttachments, attachErr := attachFilesWithClient(ctx, replySDK, message, c.attachments)
		if attachErr != nil {
			return attachErr
		}
		if c.draft {
			draftID, draftErr := replySDK.Entries().CreateReplyDraft(ctx, target.EntryID, target.ActingSenderID, target.Subject, messageWithAttachments,
				target.Addressed.To, target.Addressed.CC, target.Addressed.BCC)
			if draftErr != nil {
				return apierr.FromSDK(draftErr)
			}
			return writeDraftSaved(cmd, draftID, len(c.attachments))
		}
		sent, err := replySDK.Entries().CreateReply(ctx, target.EntryID, target.ActingSenderID, target.Subject, messageWithAttachments,
			target.Addressed.To, target.Addressed.CC, target.Addressed.BCC)
		if err != nil {
			return apierr.FromSDK(err)
		}
		if err := confirmSent(ctx, replySDK, sent); err != nil {
			return err
		}
		return writeMessageSent(cmd, messageSent{line: summary, summary: summary, thread: topicID}, sent)
	}

	to := parseAddresses(c.to)
	cc := parseAddresses(c.cc)
	bcc := parseAddresses(c.bcc)
	// A draft needs nobody on it yet; only a send does.
	if len(to)+len(cc)+len(bcc) == 0 && !c.draft {
		return apierr.ErrUsage("a message needs at least one recipient (to, cc or bcc)")
	}
	if cmd.Flags().Changed("from") {
		return c.composeFrom(cmd, senderClient, sender, message, to, cc, bcc)
	}
	messageWithAttachments, attachErr := attachFiles(ctx, message, c.attachments)
	if attachErr != nil {
		return attachErr
	}
	if !c.noNameTag {
		var tagErr error
		if messageWithAttachments, tagErr = appendSenderNameTag(ctx, messageWithAttachments); tagErr != nil {
			return tagErr
		}
	}
	if c.draft {
		draftID, draftErr := sdk.Messages().CreateDraft(ctx, hey.DraftContent{
			Subject: c.subject, Content: messageWithAttachments, To: to, CC: cc, BCC: bcc,
		})
		if draftErr != nil {
			return apierr.FromSDK(draftErr)
		}
		return writeDraftSaved(cmd, draftID, len(c.attachments))
	}
	sent, err := sdk.Messages().Create(ctx, c.subject, messageWithAttachments, to, cc, bcc)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if err := confirmSent(ctx, sdk, sent); err != nil {
		return err
	}
	return writeMessageSent(cmd, messageSent{line: summary, summary: summary}, sent)
}

// appendSenderNameTag ends a new message — attachments included — with the sender's name
// tag the way HEY's own compose form does. HEY applies the tag in the form it prefills, not on the message it
// saves, so a message written here has to carry its own — otherwise a draft or a send from
// the CLI goes out unsigned while the same message written in HEY would not. The tag is the
// one HEY serves for the sender the message is filed under; a sender without one leaves the
// message alone.
func appendSenderNameTag(ctx context.Context, message string) (string, error) {
	senderID, err := sdk.DefaultSenderID(ctx)
	if err != nil {
		return "", apierr.FromSDK(err)
	}
	identity, err := rootSDK.Identity().GetIdentity(ctx)
	if err != nil {
		return "", apierr.FromSDK(err)
	}
	if identity == nil {
		return message, nil
	}
	for _, sender := range identity.Senders {
		if sender.Id == senderID && sender.NameTag != "" {
			return message + "<br>" + sender.NameTag, nil
		}
	}
	return message, nil
}

// writeDraftSaved confirms a saved draft, naming the id every draft verb takes.
func writeDraftSaved(cmd *cobra.Command, draftID int64, attachments int) error {
	summary := sentWithAttachmentsSummary("Draft saved", attachments)
	return writeMutationLine(cmd, fmt.Sprintf("%s (id %d).", summary, draftID), summary,
		map[string]any{"id": draftID},
		output.WithBreadcrumbs(
			output.Breadcrumb{Action: "show", Command: fmt.Sprintf("hey draft show %d", draftID), Description: "Read the draft back"},
			output.Breadcrumb{Action: "edit", Command: fmt.Sprintf("hey draft edit %d", draftID), Description: "Change it"},
			output.Breadcrumb{Action: "send", Command: fmt.Sprintf("hey draft send %d", draftID), Description: "Deliver it"},
			output.Breadcrumb{Action: "delete", Command: fmt.Sprintf("hey draft delete %d", draftID), Description: "Trash it"},
		),
	)
}

// recipientsChecked refuses a --to, --cc or --bcc address HEY would drop without
// saying so (see mail.InvalidAddress). It runs as the command's arguments are checked,
// which cobra does before the root's PersistentPreRunE resolves an account over the
// network, so a bad address is refused before any request, upload or editor.
func recipientsChecked(args cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, positional []string) error {
		if args != nil {
			if err := args(cmd, positional); err != nil {
				return err
			}
		}
		var lists [][]string
		for _, name := range []string{"to", "cc", "bcc"} {
			flag := cmd.Flags().Lookup(name)
			if flag == nil || !flag.Changed {
				continue
			}
			values := []string{flag.Value.String()}
			if slice, ok := flag.Value.(pflag.SliceValue); ok {
				values = slice.GetSlice()
			}
			for _, value := range values {
				lists = append(lists, parseAddresses(value))
			}
		}
		if address := mail.InvalidAddress(lists...); address != "" {
			return apierr.ErrUsage("not a valid email address: " + address)
		}
		return nil
	}
}

func parseAddresses(s string) []string {
	return mail.SplitAddresses(s)
}
