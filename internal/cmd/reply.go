package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

const maxReplyVerificationPages = 100

type replyCommand struct {
	cmd             *cobra.Command
	message         string
	attachments     []string
	preview         bool
	expectedEntryID int64
}

func newReplyCommand() *replyCommand {
	replyCommand := &replyCommand{}
	replyCommand.cmd = &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Reply to a thread",
		Annotations: map[string]string{
			"agent_notes": "Run with --preview first, then pass its entry_id to --expect-entry when sending. The send stops if the thread changed after preview.",
		},
		Example: `  hey reply 12345 -m "Thanks!" --preview
  hey reply 12345 -m "Thanks!" --expect-entry 67890
  hey reply 12345 -m "Attached is the report." --attach ./report.pdf --preview
  echo "Detailed reply" | hey reply 12345 --preview`,
		RunE: replyCommand.run,
		Args: usageExactOneArg(),
	}

	replyCommand.cmd.Flags().StringVarP(&replyCommand.message, "message", "m", "", "Reply message (or opens $EDITOR)")
	replyCommand.cmd.Flags().StringArrayVar(&replyCommand.attachments, "attach", nil, "File to attach (repeatable)")
	replyCommand.cmd.Flags().BoolVar(&replyCommand.preview, "preview", false, "Preview the complete reply without sending")
	replyCommand.cmd.Flags().Int64Var(&replyCommand.expectedEntryID, "expect-entry", 0, "Send only if this is still the thread's latest entry ID")

	return replyCommand
}

func (c *replyCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || threadID <= 0 {
		return output.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	ctx := cmd.Context()
	if cmd.Flags().Changed("expect-entry") && c.expectedEntryID <= 0 {
		return output.ErrUsage("--expect-entry must be a positive entry ID")
	}
	if !c.preview && c.expectedEntryID <= 0 {
		return output.ErrUsageHint(
			"sending a reply requires --expect-entry with the entry_id from a fresh preview",
			fmt.Sprintf("Run: hey reply %d -m <message> --preview --json", threadID),
		)
	}

	message := c.message
	if message == "" && !stdinIsTerminal() {
		message, err = readStdin()
		if err != nil {
			return err
		}
		if message == "" && len(c.attachments) == 0 {
			return output.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
		}
	} else if message == "" && len(c.attachments) == 0 {
		message, err = editor.Open("")
		if err != nil {
			return output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
		}
		if message == "" {
			return output.ErrUsage("empty message, aborting")
		}
	}

	if c.preview {
		target, resolveErr := resolveThreadReply(ctx, threadID)
		if resolveErr != nil {
			return resolveErr
		}
		attachments, inspectErr := inspectReplyAttachments(c.attachments)
		if inspectErr != nil {
			return inspectErr
		}
		return writeReplyPreview(cmd, threadID, target, message, attachments)
	}

	senderID, err := sdk.DefaultSenderID(ctx)
	if err != nil {
		return convertSDKError(err)
	}
	target, err := resolveExpectedThreadReply(ctx, threadID, c.expectedEntryID)
	if err != nil {
		return err
	}
	message, err = attachFiles(ctx, message, c.attachments)
	if err != nil {
		return err
	}
	if len(c.attachments) > 0 {
		target, err = resolveExpectedThreadReply(ctx, threadID, c.expectedEntryID)
		if err != nil {
			return err
		}
	}
	if err = ensureExpectedEntryStillLatest(ctx, threadID, c.expectedEntryID); err != nil {
		return err
	}
	if err = sdk.Entries().CreateReply(ctx, target.EntryID, message, target.Addressed.To, target.Addressed.CC, target.Addressed.BCC); err != nil {
		return convertSDKError(err)
	}
	replyEntryID, err := verifyReplyCreated(ctx, threadID, target.EntryID, senderID, message, defaultReplyVerificationDelays())
	if err != nil {
		return err
	}

	summary := sentWithAttachmentsSummary("Reply sent", len(c.attachments))
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(map[string]int64{
		"thread_id": threadID,
		"entry_id":  replyEntryID,
	},
		output.WithSummary(summary),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     fmt.Sprintf("hey threads %d", threadID),
			Description: "View the full thread",
		}),
	)
}

func resolveExpectedThreadReply(ctx context.Context, threadID, expectedEntryID int64) (*threadReplyTarget, error) {
	return resolveThreadReplyAtEntry(ctx, threadID, expectedEntryID)
}

func ensureExpectedEntryStillLatest(ctx context.Context, threadID, expectedEntryID int64) error {
	topic, err := sdk.Topics().Get(ctx, threadID)
	if err != nil {
		return convertSDKError(err)
	}
	if topic == nil || topic.LatestEntry.Id <= 0 {
		return output.ErrNotFound("entries for thread", fmt.Sprintf("%d", threadID))
	}
	if topic.LatestEntry.Id != expectedEntryID {
		return output.ErrUsageHint(
			fmt.Sprintf("thread changed after preview: expected entry %d, latest entry is %d", expectedEntryID, topic.LatestEntry.Id),
			fmt.Sprintf("Run: hey reply %d -m <message> --preview --json", threadID),
		)
	}
	return nil
}

type replyPreviewAttachment struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
}

type replyPreview struct {
	ThreadID    int64                    `json:"thread_id"`
	EntryID     int64                    `json:"entry_id"`
	From        string                   `json:"from"`
	To          []string                 `json:"to"`
	CC          []string                 `json:"cc"`
	BCC         []string                 `json:"bcc"`
	Subject     string                   `json:"subject"`
	Body        string                   `json:"body"`
	Attachments []replyPreviewAttachment `json:"attachments"`
}

func inspectReplyAttachments(paths []string) ([]replyPreviewAttachment, error) {
	if len(paths) == 0 {
		return []replyPreviewAttachment{}, nil
	}
	prepared, err := prepareAttachments(paths)
	if err != nil {
		return nil, err
	}
	defer closePreparedAttachments(prepared)

	attachments := make([]replyPreviewAttachment, 0, len(prepared))
	for _, attachment := range prepared {
		attachments = append(attachments, replyPreviewAttachment{
			Path:        attachment.path,
			Filename:    attachment.filename,
			ContentType: attachment.contentType,
			ByteSize:    attachment.byteSize,
		})
	}
	return attachments, nil
}

func writeReplyPreview(cmd *cobra.Command, threadID int64, target *threadReplyTarget, message string, attachments []replyPreviewAttachment) error {
	from, err := defaultReplySenderEmail(cmd.Context())
	if err != nil {
		return err
	}
	subject := strings.TrimSpace(target.Subject)
	if subject == "" {
		return output.ErrAPI(0, "could not determine thread subject")
	}
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}

	preview := replyPreview{
		ThreadID:    threadID,
		EntryID:     target.EntryID,
		From:        from,
		To:          target.Addressed.To,
		CC:          target.Addressed.CC,
		BCC:         target.Addressed.BCC,
		Subject:     subject,
		Body:        message,
		Attachments: attachments,
	}

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Thread: %d\n", preview.ThreadID)
		fmt.Fprintf(w, "Reply target: entry %d\n", preview.EntryID)
		fmt.Fprintf(w, "From: %s\n", preview.From)
		fmt.Fprintf(w, "To: %s\n", strings.Join(preview.To, ", "))
		fmt.Fprintf(w, "CC: %s\n", strings.Join(preview.CC, ", "))
		fmt.Fprintf(w, "BCC: %s\n", strings.Join(preview.BCC, ", "))
		fmt.Fprintf(w, "Subject: %s\n", preview.Subject)
		for _, attachment := range preview.Attachments {
			fmt.Fprintf(w, "Attachment: %s (%s, %d bytes)\n", attachment.Path, attachment.ContentType, attachment.ByteSize)
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, preview.Body)
		return nil
	}

	return writeOK(preview, output.WithSummary("Reply preview"))
}

func defaultReplySenderEmail(ctx context.Context) (string, error) {
	identity, err := sdk.Identity().GetIdentity(ctx)
	if err != nil {
		return "", convertSDKError(err)
	}
	if identity == nil {
		return "", output.ErrAPI(0, "could not determine sender identity")
	}
	for _, sender := range identity.Senders {
		if sender.Default && sender.EmailAddress != "" {
			return sender.EmailAddress, nil
		}
	}
	for _, sender := range identity.Senders {
		if sender.EmailAddress != "" {
			return sender.EmailAddress, nil
		}
	}
	if identity.PrimaryContact.EmailAddress != "" {
		return identity.PrimaryContact.EmailAddress, nil
	}
	return "", output.ErrAPI(0, "could not determine sender email address")
}

func defaultReplyVerificationDelays() []time.Duration {
	return []time.Duration{
		0,
		250 * time.Millisecond,
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
	}
}

func verifyReplyCreated(ctx context.Context, threadID, previousEntryID, senderID int64, sentContent string, delays []time.Duration) (int64, error) {
	var lastErr error
	for _, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return 0, output.ErrAPI(0, "HEY accepted the reply, but verification was canceled. Do not retry automatically; check the thread and Sent Mail")
			case <-timer.C:
			}
		}

		entryID, err := findMatchingReply(ctx, threadID, previousEntryID, senderID, sentContent)
		if err != nil {
			lastErr = convertSDKError(err)
			continue
		}
		lastErr = nil
		if entryID > 0 {
			return entryID, nil
		}
	}

	if lastErr != nil {
		return 0, output.ErrAPI(0, fmt.Sprintf("HEY accepted the reply, but the sent entry could not be verified: %v. Do not retry automatically; check the thread and Sent Mail", lastErr))
	}
	return 0, output.ErrAPI(0, "HEY accepted the reply, but no matching sent entry appeared. Do not retry automatically; check the thread and Sent Mail")
}

func findMatchingReply(ctx context.Context, threadID, previousEntryID, senderID int64, sentContent string) (int64, error) {
	var candidateErr error
	for page := 1; page <= maxReplyVerificationPages; page++ {
		pageValue := strconv.Itoa(page)
		entries, err := sdk.Topics().GetEntries(ctx, threadID, &generated.GetTopicEntriesParams{Page: &pageValue})
		if err != nil {
			return 0, err
		}
		if entries == nil || len(*entries) == 0 {
			if candidateErr != nil {
				return 0, candidateErr
			}
			return 0, nil
		}
		for _, entry := range *entries {
			if entry.Id <= previousEntryID || (entry.Kind != "" && entry.Kind != "message") {
				continue
			}
			message, err := sdk.Messages().Get(ctx, entry.Id)
			if err != nil {
				candidateErr = err
				continue
			}
			if replyMessageMatches(entry, message, senderID, sentContent) {
				return entry.Id, nil
			}
		}
	}
	return 0, fmt.Errorf("reply verification stopped after %d pages", maxReplyVerificationPages)
}

func replyMessageMatches(entry generated.Entry, message *generated.Message, senderID int64, sentContent string) bool {
	if message == nil {
		return false
	}
	fromSender := entry.Creator.Id == senderID || message.Creator.Id == senderID || message.Sender.Id == senderID
	if !fromSender {
		return false
	}
	expected := normalizedReplyContent(sentContent)
	actual := normalizedReplyContent(message.Content)
	return expected != "" && actual == expected
}

func normalizedReplyContent(content string) string {
	return strings.Join(strings.Fields(htmlutil.ToText(content)), " ")
}
