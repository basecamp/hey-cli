package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type draftAttachmentOutput struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	ByteSize    *int64 `json:"byte_size,omitempty"`
}

// draftOutput is what hey draft show answers with outside --html: the draft's editable
// state, its body as Markdown the way every email body leaves this CLI, and safe
// metadata for the downloadable attachments in that complete stored body.
type draftOutput struct {
	ID                  int64                   `json:"id"`
	Subject             string                  `json:"subject,omitempty"`
	Body                htmlutil.Markdown       `json:"body"`
	Attachments         []draftAttachmentOutput `json:"attachments"`
	To                  []string                `json:"to,omitempty"`
	CC                  []string                `json:"cc,omitempty"`
	BCC                 []string                `json:"bcc,omitempty"`
	IsReply             bool                    `json:"is_reply,omitempty"`
	ScheduledDeliveryAt *time.Time              `json:"scheduled_delivery_at,omitempty"`
	UpdatedAt           *time.Time              `json:"updated_at,omitempty"`
}

func draftOutputFor(id int64, edit *generated.MessageEditState) draftOutput {
	out := draftOutput{
		ID:          id,
		Subject:     edit.Subject,
		Body:        htmlutil.ToMarkdown(edit.Content),
		Attachments: draftAttachments(edit.Content),
		To:          addressEmails(edit.Addressed.Directly),
		CC:          addressEmails(edit.Addressed.Copied),
		BCC:         addressEmails(edit.Addressed.Blindcopied),
		IsReply:     edit.IsReply,
	}
	if !edit.ScheduledDeliveryAt.IsZero() {
		at := edit.ScheduledDeliveryAt
		out.ScheduledDeliveryAt = &at
	}
	if !edit.UpdatedAt.IsZero() {
		at := edit.UpdatedAt
		out.UpdatedAt = &at
	}
	return out
}

func draftAttachments(content string) []draftAttachmentOutput {
	attachments := htmlutil.ExtractAttachments(content)
	out := make([]draftAttachmentOutput, len(attachments))
	for index, attachment := range attachments {
		out[index] = draftAttachmentOutput{
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
			ByteSize:    attachment.ByteSize,
		}
	}
	return out
}

func addressEmails(contacts []generated.Contact) []string {
	var emails []string
	for _, contact := range contacts {
		if contact.EmailAddress != "" {
			emails = append(emails, contact.EmailAddress)
		}
	}
	return emails
}

func parseDraftID(arg string) (int64, error) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id <= 0 {
		return 0, apierr.ErrUsage(fmt.Sprintf("invalid draft ID: %s", arg))
	}
	return id, nil
}

// draftContentFrom is the edit state as the full DraftContent an update resends. HEY
// revises a draft from the whole request, so every verb that writes one starts here.
func draftContentFrom(edit *generated.MessageEditState) hey.DraftContent {
	content := hey.DraftContent{
		Subject: edit.Subject,
		Content: edit.Content,
		To:      addressEmails(edit.Addressed.Directly),
		CC:      addressEmails(edit.Addressed.Copied),
		BCC:     addressEmails(edit.Addressed.Blindcopied),
	}
	if !edit.ScheduledDeliveryAt.IsZero() {
		// HEY's API reads a schedule's date and hour in UTC (ApiRequest sets
		// Time.zone to UTC for every JSON request), so the served UTC moment is
		// said back as its own UTC date and hour — the exact round-trip, whatever
		// clock the host or the identity keeps. Proven live: an identity-zone
		// conversion here moved a 09:00Z delivery to 04:00Z on the first edit.
		at := edit.ScheduledDeliveryAt.UTC()
		content.Schedule = &hey.DraftSchedule{Date: at.Format("2006-01-02"), Hour: at.Hour()}
	}
	return content
}

// preservableSchedule refuses an edit that could not keep the draft's scheduled
// delivery where it is. HEY's API expresses a schedule as a whole UTC hour, so an
// instant between hours — set from a HEY app on a fractional-offset clock like
// India's or Adelaide's — would be silently moved by the resend. Refusing is the
// honest answer until the API can name an exact instant.
func preservableSchedule(edit *generated.MessageEditState) error {
	at := edit.ScheduledDeliveryAt.UTC()
	if at.IsZero() || (at.Minute() == 0 && at.Second() == 0) {
		return nil
	}
	return apierr.ErrUsage(fmt.Sprintf(
		"this draft is scheduled for %s, between the whole hours HEY's API can express — editing here would move its delivery; adjust it in a HEY app first",
		at.Format(time.RFC3339)))
}

// --- show ---

type draftShowCommand struct {
	cmd *cobra.Command
}

func newDraftShowCommand() *draftShowCommand {
	showCommand := &draftShowCommand{}
	showCommand.cmd = &cobra.Command{
		Use:   "show <draft-id>",
		Short: "Read a draft back",
		Annotations: map[string]string{
			"agent_notes": "Draft IDs come from `hey draft list` or from saving with `hey compose --draft`. Structured and styled output lists each downloadable attachment's filename and available type/size metadata without exposing its internal URL or signed ID. The body is Markdown by default; --html writes the complete stored HTML fragment instead, including attachment markup, and must be redirected to a file or pipe.",
		},
		Example: `  hey draft show 12345
  hey draft show 12345 --json
  hey draft show 12345 --jq '.data.attachments'
  hey draft show 12345 --html > draft.html`,
		RunE: showCommand.run,
		Args: usageExactOneArg(),
	}
	return showCommand
}

func (c *draftShowCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	draftID, err := parseDraftID(args[0])
	if err != nil {
		return err
	}

	edit, err := sdk.Messages().GetEdit(cmd.Context(), draftID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if edit == nil {
		return apierr.ErrNotFound("draft", args[0])
	}
	if writer.EffectiveFormat() == output.FormatHTML {
		return writeHTMLFragment(cmd.OutOrStdout(), edit.Content)
	}
	out := draftOutputFor(draftID, edit)

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Draft %d: %s\n", out.ID, terminal.SanitizeLine(out.Subject))
		for _, kind := range []struct {
			label  string
			emails []string
		}{{"To", out.To}, {"CC", out.CC}, {"BCC", out.BCC}} {
			if len(kind.emails) > 0 {
				fmt.Fprintf(w, "%s: %s\n", kind.label, terminal.SanitizeLine(strings.Join(kind.emails, ", ")))
			}
		}
		if out.ScheduledDeliveryAt != nil {
			fmt.Fprintf(w, "Scheduled: %s\n", out.ScheduledDeliveryAt.Local().Format("2006-01-02 15:04"))
		}
		if len(out.Attachments) > 0 {
			fmt.Fprintln(w, "Attachments:")
			for _, attachment := range out.Attachments {
				fmt.Fprintf(w, "  %s\n", formatDraftAttachment(attachment))
			}
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, markdown.Render(out.Body, stdoutWidth()))
		return nil
	}
	return writeOK(out,
		output.WithSummary(fmt.Sprintf("Draft %d", out.ID)),
		output.WithBreadcrumbs(
			output.Breadcrumb{Action: "edit", Command: fmt.Sprintf("hey draft edit %d", out.ID), Description: "Change the draft"},
			output.Breadcrumb{Action: "send", Command: fmt.Sprintf("hey draft send %d", out.ID), Description: "Deliver it"},
		),
	)
}

func formatDraftAttachment(attachment draftAttachmentOutput) string {
	filename := terminal.SanitizeLine(attachment.Filename)
	var details []string
	if attachment.ContentType != "" {
		details = append(details, terminal.SanitizeLine(attachment.ContentType))
	}
	if attachment.ByteSize != nil {
		details = append(details, formatByteSize(*attachment.ByteSize))
	}
	if len(details) == 0 {
		return filename
	}
	return fmt.Sprintf("%s (%s)", filename, strings.Join(details, ", "))
}

// --- edit ---

type draftEditCommand struct {
	cmd             *cobra.Command
	subject         string
	to              string
	cc              string
	bcc             string
	message         string
	messageHTML     string
	messageHTMLFile string
}

func newDraftEditCommand() *draftEditCommand {
	editCommand := &draftEditCommand{}
	editCommand.cmd = &cobra.Command{
		Use:   "edit <draft-id>",
		Short: "Change a draft",
		Annotations: map[string]string{
			"agent_notes": "Each flag replaces its field and an omitted flag keeps what the draft has — --to/--cc/--bcc replace that whole recipient kind (an explicit empty value clears it). --message-html-file reads a complete raw HTML replacement verbatim from a local file. With no field flags the body opens in $EDITOR as Markdown. A scheduled delivery is preserved.",
		},
		Example: `  hey draft edit 12345 --subject "Quarterly planning (v2)"
  hey draft edit 12345 --to maria@example.com --cc finance@example.com
  hey draft edit 12345 -m "Rewritten agenda: budget first, hiring second."
  hey draft edit 12345 --message-html-file ./revised-message.html
  hey draft edit 12345    # open the body in $EDITOR`,
		RunE: editCommand.run,
		Args: usageExactOneArg(),
	}
	editCommand.cmd.Flags().StringVar(&editCommand.subject, "subject", "", "Replace the subject")
	editCommand.cmd.Flags().StringVar(&editCommand.to, "to", "", "Replace the To recipients (comma separated; empty clears)")
	editCommand.cmd.Flags().StringVar(&editCommand.cc, "cc", "", "Replace the CC recipients (comma separated; empty clears)")
	editCommand.cmd.Flags().StringVar(&editCommand.bcc, "bcc", "", "Replace the BCC recipients (comma separated; empty clears)")
	editCommand.cmd.Flags().StringVarP(&editCommand.message, "message", "m", "", "Replace the body with this Markdown")
	editCommand.cmd.Flags().StringVar(&editCommand.messageHTML, "message-html", "", "Replace the body with raw HTML instead of Markdown")
	editCommand.cmd.Flags().StringVar(&editCommand.messageHTMLFile, "message-html-file", "", "Replace the body with raw HTML read from this file")
	editCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html", "message-html-file")
	return editCommand
}

func (c *draftEditCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	draftID, err := parseDraftID(args[0])
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	edit, err := sdk.Messages().GetEdit(ctx, draftID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if edit == nil {
		return apierr.ErrNotFound("draft", args[0])
	}
	if scheduleErr := preservableSchedule(edit); scheduleErr != nil {
		return scheduleErr
	}
	content := draftContentFrom(edit)

	flags := cmd.Flags()
	fieldFlagged := false
	if flags.Changed("subject") {
		content.Subject = c.subject
		fieldFlagged = true
	}
	if flags.Changed("to") {
		content.To = parseAddresses(c.to)
		fieldFlagged = true
	}
	if flags.Changed("cc") {
		content.CC = parseAddresses(c.cc)
		fieldFlagged = true
	}
	if flags.Changed("bcc") {
		content.BCC = parseAddresses(c.bcc)
		fieldFlagged = true
	}
	switch {
	case flags.Changed("message-html-file"):
		messageHTML, readErr := readMessageHTMLFile(c.messageHTMLFile)
		if readErr != nil {
			return readErr
		}
		content.Content = messageHTML
	case flags.Changed("message-html"):
		content.Content = c.messageHTML
	case flags.Changed("message"):
		content.Content = htmlutil.FromMarkdown(c.message)
	case !fieldFlagged:
		// No field named: the edit is the body, in $EDITOR, prefilled as Markdown.
		body, editorErr := editor.Open(htmlutil.ToMarkdown(edit.Content).String())
		if editorErr != nil {
			return apierr.ErrAPI(0, fmt.Sprintf("could not open editor: %v", editorErr))
		}
		if body == "" {
			return apierr.ErrUsage("empty body, aborting — delete the draft with `hey draft delete` instead")
		}
		content.Content = htmlutil.FromMarkdown(body)
	}

	if err := sdk.Messages().UpdateDraft(ctx, draftID, content); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd, fmt.Sprintf("Draft %d updated.", draftID), "Draft updated",
		map[string]any{"id": draftID})
}

// --- send ---

type draftSendCommand struct {
	cmd *cobra.Command
}

func newDraftSendCommand() *draftSendCommand {
	sendCommand := &draftSendCommand{}
	sendCommand.cmd = &cobra.Command{
		Use:   "send <draft-id>",
		Short: "Deliver a draft",
		Annotations: map[string]string{
			"agent_notes": "Sends the draft as it stands — recipients are required, added with `hey draft edit --to`. Delivery goes through HEY's undo window. Scheduling a delivery is done in a HEY app for now; the API cannot yet name an exact instant.",
		},
		Example: `  hey draft send 12345`,
		RunE:    sendCommand.run,
		Args:    usageExactOneArg(),
	}
	return sendCommand
}

func (c *draftSendCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	draftID, err := parseDraftID(args[0])
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	edit, err := sdk.Messages().GetEdit(ctx, draftID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if edit == nil {
		return apierr.ErrNotFound("draft", args[0])
	}
	content := draftContentFrom(edit)
	if len(content.To)+len(content.CC)+len(content.BCC) == 0 {
		return apierr.ErrUsageHint("the draft has no recipients", fmt.Sprintf("hey draft edit %d --to <email>", draftID))
	}

	content.Schedule = nil
	if err := sdk.Messages().SendDraft(ctx, draftID, content); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd, fmt.Sprintf("Draft %d sent.", draftID), "Draft sent",
		map[string]any{"id": draftID})
}

// --- delete ---

type draftDeleteCommand struct {
	cmd *cobra.Command
}

func newDraftDeleteCommand() *draftDeleteCommand {
	deleteCommand := &draftDeleteCommand{}
	deleteCommand.cmd = &cobra.Command{
		Use:   "delete <draft-id>...",
		Short: "Trash drafts",
		Annotations: map[string]string{
			"agent_notes": "Takes draft IDs from `hey draft list`. Trashes the draft; there is no undo here.",
		},
		Example: `  hey draft delete 12345
  hey draft delete 12345 67890`,
		RunE: deleteCommand.run,
		Args: usageMinOneArg(),
	}
	return deleteCommand
}

func (c *draftDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	ids := make([]int64, 0, len(args))
	for _, arg := range args {
		id, err := parseDraftID(arg)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}

	for _, id := range ids {
		if err := sdk.Entries().DeleteDraft(cmd.Context(), id); err != nil {
			return apierr.FromSDK(err)
		}
	}
	return writeMutation(cmd, fmt.Sprintf("%d %s deleted", len(ids), draftNoun(len(ids))), nil)
}

func draftNoun(count int) string {
	if count == 1 {
		return "draft"
	}
	return "drafts"
}
