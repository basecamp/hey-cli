package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
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
	draft       bool
}

func newComposeCommand() *composeCommand {
	composeCommand := &composeCommand{}
	composeCommand.cmd = &cobra.Command{
		Use:   "compose",
		Short: "Write and send a new email",
		Annotations: map[string]string{
			"agent_notes": "Starts a new thread with --to (optionally --cc/--bcc), which requires --subject, or replies to an existing one with --thread-id, which does not. Repeatable --attach files are uploaded before sending and can be sent without body text. The body is Markdown; use --message-html to send raw HTML instead. --draft saves instead of sending — recipients become optional — and answers the draft ID for hey draft show/edit/send/delete.",
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

	composeCommand.cmd.Flags().StringVar(&composeCommand.to, "to", "", "Recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.cc, "cc", "", "CC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.bcc, "bcc", "", "BCC recipient email address(es)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.subject, "subject", "", "Message subject (required for a new message)")
	composeCommand.cmd.Flags().StringVarP(&composeCommand.message, "message", "m", "", "Message body as Markdown (or opens $EDITOR)")
	composeCommand.cmd.Flags().StringVar(&composeCommand.messageHTML, "message-html", "", "Message body as raw HTML instead of Markdown")
	composeCommand.cmd.Flags().StringVar(&composeCommand.threadID, "thread-id", "", "Reply to this thread instead of starting a new one")
	composeCommand.cmd.Flags().StringArrayVar(&composeCommand.attachments, "attach", nil, "File to attach (repeatable)")
	composeCommand.cmd.Flags().BoolVar(&composeCommand.draft, "draft", false, "Save as a draft instead of sending")
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

	// A send answers where the message went, so the response HEY gives is kept rather
	// than reduced to an error: sendClient, response and sent below are what
	// composeHandle and verifyComposedMessage work from.
	var (
		sendClient = sdk
		response   *hey.Response
		sent       composeSent
	)

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
		if c.draft {
			draftID, draftErr := replySDK.Entries().CreateReplyDraft(ctx, target.EntryID, target.ActingSenderID, target.Subject, messageWithAttachments,
				target.Addressed.To, target.Addressed.CC, target.Addressed.BCC)
			if draftErr != nil {
				return apierr.FromSDK(draftErr)
			}
			return writeDraftSaved(cmd, draftID, len(c.attachments))
		}
		if len(target.Addressed.To)+len(target.Addressed.CC)+len(target.Addressed.BCC) == 0 {
			return apierr.ErrUsage("a reply needs at least one recipient (to, cc or bcc); HEY saves an unaddressed reply as a draft")
		}
		sendClient = replySDK
		sent = composeSent{
			Subject: target.Subject, Content: messageWithAttachments,
			To: target.Addressed.To, CC: target.Addressed.CC, BCC: target.Addressed.BCC,
		}
		var sendErr error
		response, sendErr = sendReply(ctx, replySDK, target.EntryID, target.ActingSenderID,
			sent.Subject, sent.Content, sent.To, sent.CC, sent.BCC)
		if sendErr != nil {
			return apierr.FromSDK(sendErr)
		}
	} else {
		to := parseAddresses(c.to)
		cc := parseAddresses(c.cc)
		bcc := parseAddresses(c.bcc)
		// A draft needs nobody on it yet; only a send does.
		if len(to)+len(cc)+len(bcc) == 0 && !c.draft {
			return apierr.ErrUsage("a message needs at least one recipient (to, cc or bcc)")
		}
		messageWithAttachments, attachErr := attachFiles(ctx, message, c.attachments)
		if attachErr != nil {
			return attachErr
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
		sent = composeSent{Subject: c.subject, Content: messageWithAttachments, To: to, CC: cc, BCC: bcc}
		var sendErr error
		response, sendErr = sendMessage(ctx, sdk, sent.Subject, sent.Content, sent.To, sent.CC, sent.BCC)
		if sendErr != nil {
			return apierr.FromSDK(sendErr)
		}
	}

	// HEY accepted the request. Which message it made is a separate question, and one a
	// caller that has to prove what it sent cannot do without: a send that names nothing
	// is reported as ambiguous rather than as a success, so nobody reads "sent" off a
	// response there is no way back from — and nobody retries a send that may already
	// have gone out.
	handle, handleErr := handleFromResponse(response.StatusCode, response.Headers, response.Data)
	if handleErr != nil {
		return apierr.ErrAmbiguousOutcome(
			fmt.Sprintf("the message may have been sent, but the response named no message to read back: %v", handleErr),
			"Check the thread before sending again — a retry may deliver it twice.")
	}

	verification, readback := verifyComposedMessage(ctx, sendClient, handle.MessageID, sent)
	result := composeResultFor(handle, verification, readback)

	summary := sentWithAttachmentsSummary("Message sent", len(c.attachments))
	if result.TopicID != 0 {
		return writeMutation(cmd, summary, result, output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "read",
				Command:     fmt.Sprintf("hey thread read %d", result.TopicID),
				Description: "Read the thread this message landed in",
			},
		))
	}
	return writeMutation(cmd, summary, result)
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
