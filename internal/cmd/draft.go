package cmd

import (
	"context"
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

// draftOutput is what hey draft show answers with: the draft's editable state, its body
// as Markdown the way every email body leaves this CLI.
type draftOutput struct {
	ID                  int64             `json:"id"`
	Subject             string            `json:"subject,omitempty"`
	Body                htmlutil.Markdown `json:"body"`
	To                  []string          `json:"to,omitempty"`
	CC                  []string          `json:"cc,omitempty"`
	BCC                 []string          `json:"bcc,omitempty"`
	IsReply             bool              `json:"is_reply,omitempty"`
	ScheduledDeliveryAt *time.Time        `json:"scheduled_delivery_at,omitempty"`
	UpdatedAt           *time.Time        `json:"updated_at,omitempty"`
}

func draftOutputFor(id int64, edit *generated.MessageEditState) draftOutput {
	out := draftOutput{
		ID:      id,
		Subject: edit.Subject,
		Body:    htmlutil.ToMarkdown(edit.Content),
		To:      addressEmails(edit.Addressed.Directly),
		CC:      addressEmails(edit.Addressed.Copied),
		BCC:     addressEmails(edit.Addressed.Blindcopied),
		IsReply: edit.IsReply,
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
func draftContentFrom(ctx context.Context, edit *generated.MessageEditState) hey.DraftContent {
	content := hey.DraftContent{
		Subject: edit.Subject,
		Content: edit.Content,
		To:      addressEmails(edit.Addressed.Directly),
		CC:      addressEmails(edit.Addressed.Copied),
		BCC:     addressEmails(edit.Addressed.Blindcopied),
	}
	if !edit.ScheduledDeliveryAt.IsZero() {
		// HEY serves the moment in UTC but schedules by a date and an hour read in
		// the identity's time zone, so the moment is said back in that zone — the
		// host's own clock would move the delivery by the difference between them.
		at := edit.ScheduledDeliveryAt.In(draftScheduleLocation(ctx))
		content.Schedule = &hey.DraftSchedule{Date: at.Format("2006-01-02"), Hour: at.Hour()}
	}
	return content
}

// draftScheduleLocation answers the clock a draft's schedule is read in: the identity's
// time zone, which is HEY's own for every scheduled delivery. The host's local clock
// stands in only when the identity does not name a zone this host knows.
func draftScheduleLocation(ctx context.Context) *time.Location {
	identity, err := sdk.Identity().GetIdentity(ctx)
	if err != nil || identity == nil || identity.TimeZoneName == "" {
		return time.Local
	}
	location, err := time.LoadLocation(identity.TimeZoneName)
	if err != nil {
		return time.Local
	}
	return location
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
			"agent_notes": "Draft IDs come from `hey draft list` or from saving with `hey compose --draft`. The body is Markdown.",
		},
		Example: `  hey draft show 12345
  hey draft show 12345 --json`,
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

// --- edit ---

type draftEditCommand struct {
	cmd         *cobra.Command
	subject     string
	to          string
	cc          string
	bcc         string
	message     string
	messageHTML string
}

func newDraftEditCommand() *draftEditCommand {
	editCommand := &draftEditCommand{}
	editCommand.cmd = &cobra.Command{
		Use:   "edit <draft-id>",
		Short: "Change a draft",
		Annotations: map[string]string{
			"agent_notes": "Each flag replaces its field and an omitted flag keeps what the draft has — --to/--cc/--bcc replace that whole recipient kind (an explicit empty value clears it). With no field flags the body opens in $EDITOR as Markdown. A scheduled delivery is preserved.",
		},
		Example: `  hey draft edit 12345 --subject "Quarterly planning (v2)"
  hey draft edit 12345 --to maria@example.com --cc finance@example.com
  hey draft edit 12345 -m "Rewritten agenda: budget first, hiring second."
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
	editCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html")
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
	content := draftContentFrom(ctx, edit)

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
	cmd  *cobra.Command
	on   string
	hour int
}

func newDraftSendCommand() *draftSendCommand {
	sendCommand := &draftSendCommand{}
	sendCommand.cmd = &cobra.Command{
		Use:   "send <draft-id>",
		Short: "Deliver a draft",
		Annotations: map[string]string{
			"agent_notes": "Sends the draft as it stands — recipients are required, added with `hey draft edit --to`. Delivery goes through HEY's undo window. --on with --hour schedules it instead (to the hour, in the account's time zone); the draft stays listed until it goes out.",
		},
		Example: `  hey draft send 12345
  hey draft send 12345 --on tomorrow --hour 9
  hey draft send 12345 --on 2026-09-01 --hour 14`,
		RunE: sendCommand.run,
		Args: usageExactOneArg(),
	}
	sendCommand.cmd.Flags().StringVar(&sendCommand.on, "on", "", "Schedule delivery for this date (YYYY-MM-DD, today or tomorrow)")
	sendCommand.cmd.Flags().IntVar(&sendCommand.hour, "hour", -1, "Hour of --on to deliver at (0-23)")
	sendCommand.cmd.MarkFlagsRequiredTogether("on", "hour")
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
	if c.on != "" {
		if c.hour < 0 || c.hour > 23 {
			return apierr.ErrUsage("--hour must be between 0 and 23")
		}
		if dateErr := validateScheduleDate(c.on); dateErr != nil {
			return dateErr
		}
	}
	ctx := cmd.Context()

	edit, err := sdk.Messages().GetEdit(ctx, draftID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if edit == nil {
		return apierr.ErrNotFound("draft", args[0])
	}
	content := draftContentFrom(ctx, edit)
	if len(content.To)+len(content.CC)+len(content.BCC) == 0 {
		return apierr.ErrUsageHint("the draft has no recipients", fmt.Sprintf("hey draft edit %d --to <email>", draftID))
	}

	if c.on != "" {
		content.Schedule = &hey.DraftSchedule{Date: c.on, Hour: c.hour}
		if err := sdk.Messages().UpdateDraft(ctx, draftID, content); err != nil {
			return apierr.FromSDK(err)
		}
		return writeMutationLine(cmd, fmt.Sprintf("Draft %d scheduled for delivery.", draftID),
			"Draft scheduled for delivery", map[string]any{"id": draftID})
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

// validateScheduleDate holds --on to what it advertises — YYYY-MM-DD, today or
// tomorrow — so a typo is a usage error before anything is fetched or written.
func validateScheduleDate(on string) error {
	if on == "today" || on == "tomorrow" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", on); err != nil {
		return apierr.ErrUsage(fmt.Sprintf("invalid --on date %q: use YYYY-MM-DD, today or tomorrow", on))
	}
	return nil
}
