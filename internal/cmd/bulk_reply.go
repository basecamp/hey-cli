package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type bulkReplyCommand struct {
	cmd *cobra.Command
}

type bulkReplyPreviewCommand struct {
	cmd *cobra.Command
}

type bulkReplySendCommand struct {
	cmd         *cobra.Command
	message     string
	messageHTML string
	attachments []string
}

type bulkReplyUndoCommand struct {
	cmd *cobra.Command
}

type bulkReplyRecipient struct {
	ID           int64  `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	EmailAddress string `json:"email_address,omitempty"`
}

type bulkReplyEntry struct {
	ID        int64                `json:"id"`
	TopicID   int64                `json:"topic_id"`
	TopicName string               `json:"topic_name"`
	To        []bulkReplyRecipient `json:"to"`
	CC        []bulkReplyRecipient `json:"cc"`
	BCC       []bulkReplyRecipient `json:"bcc"`
}

type bulkReplyMarkdownEntry struct {
	ID        int64  `json:"id"`
	TopicID   int64  `json:"topic_id"`
	TopicName string `json:"topic_name"`
	To        string `json:"to"`
	CC        string `json:"cc"`
	BCC       string `json:"bcc"`
}

func newBulkReplyCommand() *bulkReplyCommand {
	bulkReplyCommand := &bulkReplyCommand{}
	bulkReplyCommand.cmd = &cobra.Command{
		Use:   "bulk-reply",
		Short: "Reply to multiple email threads",
		Long:  "Preview, send, and undo one reply across multiple HEY threads.",
		Annotations: map[string]string{
			"agent_notes": "Always run `hey bulk-reply preview <box-item-id>...` before sending. Send resolves the box items again, keeps HEY's name-tag content, and returns the delivery ID and undo state.",
		},
	}
	bulkReplyCommand.cmd.AddCommand(newBulkReplyPreviewCommand().cmd)
	bulkReplyCommand.cmd.AddCommand(newBulkReplySendCommand().cmd)
	bulkReplyCommand.cmd.AddCommand(newBulkReplyUndoCommand().cmd)
	return bulkReplyCommand
}

func newBulkReplyPreviewCommand() *bulkReplyPreviewCommand {
	previewCommand := &bulkReplyPreviewCommand{}
	previewCommand.cmd = &cobra.Command{
		Use:   "preview <box-item-id>...",
		Short: "Preview threads and recipients without sending",
		Annotations: map[string]string{
			"agent_notes": "Read-only. Box item IDs come from `hey box view` or `hey search`. The result contains the latest replyable entry and exact To, CC, and BCC recipients for each thread.",
		},
		Example: `  hey bulk-reply preview 12345 67890
  hey bulk-reply preview 12345 67890 --json`,
		RunE: previewCommand.run,
		Args: usageMinOneArg(),
	}
	return previewCommand
}

func (c *bulkReplyPreviewCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	postingIDs, err := parsePositiveUniqueIDs("posting", args)
	if err != nil {
		return err
	}
	draft, err := sdk.BulkReplies().Draft(cmd.Context(), postingIDs)
	if err != nil {
		if hey.AsError(err).Code != hey.CodeNotFound {
			return apierr.FromSDK(err)
		}
		draft = &generated.BulkReplyDraft{}
	}
	entries := makeBulkReplyEntries(draft)
	skipped := max(len(postingIDs)-len(entries), 0)

	if writer.IsStyled() {
		printBulkReplyPreview(cmd, entries, draftContent(draft), skipped)
		return nil
	}
	var data any = entries
	if writer.EffectiveFormat() == output.FormatMarkdown {
		data = makeBulkReplyMarkdownEntries(entries)
	}
	return writeOK(data,
		output.WithSummary(bulkReplyPreviewSummary(len(entries), skipped)),
		output.WithMeta("posting_ids", postingIDs),
		output.WithMeta("replyable_count", len(entries)),
		output.WithMeta("skipped_count", skipped),
		output.WithMeta("prefilled_content", draftContent(draft)),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "send",
			Command:     "hey bulk-reply send <box-item-id>... -m <message>",
			Description: "Send one reply to the previewed threads",
		}),
	)
}

func newBulkReplySendCommand() *bulkReplySendCommand {
	sendCommand := &bulkReplySendCommand{}
	sendCommand.cmd = &cobra.Command{
		Use:   "send <box-item-id>...",
		Short: "Send one reply to multiple threads",
		Annotations: map[string]string{
			"agent_notes": "Mutating. Preview first. Accepts a message via -m, stdin, or $EDITOR and repeatable --attach files. HEY's server-provided name-tag content is preserved.",
		},
		Example: `  hey bulk-reply send 12345 67890 -m "Thanks for the update."
  echo "Thanks for the update." | hey bulk-reply send 12345 67890
  hey bulk-reply send 12345 67890 -m "The report is attached." --attach ./report.pdf`,
		RunE: sendCommand.run,
		Args: usageMinOneArg(),
	}
	sendCommand.cmd.Flags().StringVarP(&sendCommand.message, "message", "m", "", "Reply message as Markdown (or opens $EDITOR)")
	sendCommand.cmd.Flags().StringVar(&sendCommand.messageHTML, "message-html", "", "Reply message as raw HTML instead of Markdown")
	sendCommand.cmd.Flags().StringArrayVar(&sendCommand.attachments, "attach", nil, "File to attach (repeatable)")
	sendCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html")
	return sendCommand
}

func (c *bulkReplySendCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	postingIDs, err := parsePositiveUniqueIDs("posting", args)
	if err != nil {
		return err
	}
	message := c.messageHTML
	if message == "" {
		markdownMessage, readErr := c.readMessage()
		if readErr != nil {
			return readErr
		}
		message = htmlutil.FromMarkdown(markdownMessage)
	}
	attachments, err := prepareAttachments(c.attachments)
	if err != nil {
		return err
	}
	defer closePreparedAttachments(attachments)

	ctx := cmd.Context()
	draft, err := sdk.BulkReplies().Draft(ctx, postingIDs)
	if err != nil {
		if hey.AsError(err).Code == hey.CodeNotFound {
			return apierr.ErrUsage("no replyable threads resolved; no reply was sent")
		}
		return apierr.FromSDK(err)
	}
	entries := makeBulkReplyEntries(draft)
	if len(entries) == 0 {
		return apierr.ErrUsage("no replyable threads resolved; no reply was sent")
	}
	entryIDs := make([]int64, len(entries))
	for i, entry := range entries {
		entryIDs[i] = entry.ID
	}

	content := htmlutil.PrependHTML(draftContent(draft), message)
	content, err = attachPreparedFiles(ctx, content, attachments)
	if err != nil {
		return err
	}
	delivery, err := sdk.BulkReplies().Send(ctx, entryIDs, content)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if delivery == nil {
		return apierr.ErrAPI(0, "HEY returned an empty bulk reply delivery")
	}

	skipped := max(len(postingIDs)-len(entries), 0)
	summary := bulkReplyDeliverySummary(delivery, len(c.attachments), skipped)
	if writer.IsStyled() {
		printBulkReplyDelivery(cmd, delivery, summary)
		return nil
	}
	switch writer.EffectiveFormat() {
	case output.FormatIDs:
		fmt.Fprintln(cmd.OutOrStdout(), delivery.Id)
		return nil
	case output.FormatCount:
		fmt.Fprintln(cmd.OutOrStdout(), delivery.EntriesCount)
		return nil
	case output.FormatAuto, output.FormatJSON, output.FormatStyled, output.FormatQuiet, output.FormatMarkdown, output.FormatHTML:
	}

	options := []output.ResponseOption{
		output.WithSummary(summary),
		output.WithMeta("posting_ids", postingIDs),
		output.WithMeta("skipped_count", skipped),
	}
	if delivery.Delayed && delivery.Id > 0 {
		options = append(options, output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "undo",
			Command:     fmt.Sprintf("hey bulk-reply undo %d", delivery.Id),
			Description: "Recall this bulk reply before HEY sends it",
		}))
	}
	return writeOK(delivery, options...)
}

func (c *bulkReplySendCommand) readMessage() (string, error) {
	return readBulkReplyMessage(c.message, len(c.attachments), stdinIsTerminal(), readStdin, editor.Open)
}

func readBulkReplyMessage(message string, attachmentCount int, stdinTerminal bool, readInput func() (string, error), openEditor func(string) (string, error)) (string, error) {
	inputSource := "inline"
	if message == "" && !stdinTerminal {
		inputSource = "stdin"
		var err error
		message, err = readInput()
		if err != nil {
			return "", err
		}
	} else if message == "" && attachmentCount == 0 {
		inputSource = "editor"
		var err error
		message, err = openEditor("")
		if err != nil {
			return "", apierr.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
		}
	}

	message = strings.TrimSpace(message)
	if message == "" && attachmentCount == 0 {
		if inputSource == "stdin" {
			return "", apierr.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
		}
		return "", apierr.ErrUsage("empty message, aborting")
	}
	return message, nil
}

func newBulkReplyUndoCommand() *bulkReplyUndoCommand {
	undoCommand := &bulkReplyUndoCommand{}
	undoCommand.cmd = &cobra.Command{
		Use:   "undo <bulk-reply-id>",
		Short: "Recall a delayed bulk reply",
		Annotations: map[string]string{
			"agent_notes": "Mutating. Works only while HEY's undo window remains open. The bulk reply ID is returned by `hey bulk-reply send`.",
		},
		Example: `  hey bulk-reply undo 98765`,
		RunE:    undoCommand.run,
		Args:    usageExactOneArg(),
	}
	return undoCommand
}

func (c *bulkReplyUndoCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	ids, err := parsePositiveUniqueIDs("bulk reply", args)
	if err != nil {
		return err
	}
	if err := sdk.BulkReplies().Undo(cmd.Context(), ids[0]); err != nil {
		return apierr.FromSDK(err)
	}
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Bulk reply recalled.")
		return nil
	}
	switch writer.EffectiveFormat() {
	case output.FormatIDs:
		fmt.Fprintln(cmd.OutOrStdout(), ids[0])
		return nil
	case output.FormatCount:
		fmt.Fprintln(cmd.OutOrStdout(), 1)
		return nil
	case output.FormatAuto, output.FormatJSON, output.FormatStyled, output.FormatQuiet, output.FormatMarkdown, output.FormatHTML:
	}
	return writeOK(map[string]any{"id": ids[0], "undone": true}, output.WithSummary("Bulk reply recalled"))
}

func parsePositiveUniqueIDs(kind string, values []string) ([]int64, error) {
	ids := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, apierr.ErrUsage(fmt.Sprintf("invalid %s ID: %s (must be a positive integer)", kind, value))
		}
		if _, exists := seen[id]; exists {
			return nil, apierr.ErrUsage(fmt.Sprintf("duplicate %s ID: %d", kind, id))
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func makeBulkReplyEntries(draft *generated.BulkReplyDraft) []bulkReplyEntry {
	if draft == nil {
		return []bulkReplyEntry{}
	}
	entries := make([]bulkReplyEntry, 0, len(draft.Entries))
	for _, entry := range draft.Entries {
		entries = append(entries, bulkReplyEntry{
			ID:        entry.Id,
			TopicID:   entry.TopicId,
			TopicName: entry.TopicName,
			To:        makeBulkReplyRecipients(entry.Addressed.Directly),
			CC:        makeBulkReplyRecipients(entry.Addressed.Copied),
			BCC:       makeBulkReplyRecipients(entry.Addressed.Blindcopied),
		})
	}
	return entries
}

func makeBulkReplyRecipients(contacts []generated.Contact) []bulkReplyRecipient {
	recipients := make([]bulkReplyRecipient, 0, len(contacts))
	for _, contact := range contacts {
		recipients = append(recipients, bulkReplyRecipient{
			ID:           contact.Id,
			Name:         contact.Name,
			EmailAddress: contact.EmailAddress,
		})
	}
	return recipients
}

func makeBulkReplyMarkdownEntries(entries []bulkReplyEntry) []bulkReplyMarkdownEntry {
	formatted := make([]bulkReplyMarkdownEntry, 0, len(entries))
	for _, entry := range entries {
		formatted = append(formatted, bulkReplyMarkdownEntry{
			ID:        entry.ID,
			TopicID:   entry.TopicID,
			TopicName: markdownSafeText(entry.TopicName),
			To:        markdownSafeText(formatBulkReplyRecipients(entry.To)),
			CC:        markdownSafeText(formatBulkReplyRecipients(entry.CC)),
			BCC:       markdownSafeText(formatBulkReplyRecipients(entry.BCC)),
		})
	}
	return formatted
}

func draftContent(draft *generated.BulkReplyDraft) string {
	if draft == nil {
		return ""
	}
	return draft.Content
}

func printBulkReplyPreview(cmd *cobra.Command, entries []bulkReplyEntry, content string, skipped int) {
	if len(entries) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No replyable threads found.")
	} else {
		for i, entry := range entries {
			if i > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Entry: %d\n", entry.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Thread: %d\n", entry.TopicID)
			fmt.Fprintf(cmd.OutOrStdout(), "Subject: %s\n", terminal.SanitizeLine(entry.TopicName))
			fmt.Fprintf(cmd.OutOrStdout(), "To: %s\n", formatBulkReplyRecipients(entry.To))
			fmt.Fprintf(cmd.OutOrStdout(), "CC: %s\n", formatBulkReplyRecipients(entry.CC))
			fmt.Fprintf(cmd.OutOrStdout(), "BCC: %s\n", formatBulkReplyRecipients(entry.BCC))
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\n%s.\n", bulkReplyPreviewSummary(len(entries), skipped))
	if text := htmlutil.ToText(content); text != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "HEY will preserve this name tag: %s\n", terminal.SanitizeLine(text))
	}
}

func formatBulkReplyRecipients(recipients []bulkReplyRecipient) string {
	formatted := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		name := terminal.SanitizeLine(recipient.Name)
		email := terminal.SanitizeLine(recipient.EmailAddress)
		switch {
		case name != "" && email != "":
			formatted = append(formatted, fmt.Sprintf("%s <%s>", name, email))
		case email != "":
			formatted = append(formatted, email)
		case name != "":
			formatted = append(formatted, name)
		}
	}
	return strings.Join(formatted, ", ")
}

func bulkReplyPreviewSummary(replyable, skipped int) string {
	summary := fmt.Sprintf("%d replyable %s", replyable, threadNoun(replyable))
	if skipped > 0 {
		noun := "postings"
		if skipped == 1 {
			noun = "posting"
		}
		summary += fmt.Sprintf("; %d %s skipped", skipped, noun)
	}
	return summary
}

func bulkReplyDeliverySummary(delivery *generated.BulkReplyDelivery, attachmentCount, skipped int) string {
	count := int(delivery.EntriesCount)
	noun := "replies"
	if count == 1 {
		noun = "reply"
	}
	summary := fmt.Sprintf("%d %s sent", count, noun)
	if delivery.Delayed {
		summary = fmt.Sprintf("%d %s queued with undo available", count, noun)
	}
	if attachmentCount > 0 {
		attachmentNoun := "attachments"
		if attachmentCount == 1 {
			attachmentNoun = "attachment"
		}
		summary += fmt.Sprintf(" with %d %s", attachmentCount, attachmentNoun)
	}
	if skipped > 0 {
		summary += fmt.Sprintf("; %d skipped", skipped)
	}
	return summary
}

func printBulkReplyDelivery(cmd *cobra.Command, delivery *generated.BulkReplyDelivery, summary string) {
	fmt.Fprintf(cmd.OutOrStdout(), "%s.\n", summary)
	fmt.Fprintf(cmd.OutOrStdout(), "Delivery ID: %d\n", delivery.Id)
	fmt.Fprintf(cmd.OutOrStdout(), "Delayed: %t\n", delivery.Delayed)
	if delivery.UndoSendUrl != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Undo URL: %s\n", terminal.SanitizeLine(delivery.UndoSendUrl))
		fmt.Fprintf(cmd.OutOrStdout(), "Undo: hey bulk-reply undo %d\n", delivery.Id)
	}
}
