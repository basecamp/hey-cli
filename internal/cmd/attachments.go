package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type attachmentsCommand struct {
	cmd          *cobra.Command
	allowPartial bool
}

type threadAttachment struct {
	ID          string `json:"id"`
	MessageID   int64  `json:"message_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	ByteSize    *int64 `json:"byte_size,omitempty"`
	URL         string `json:"-"`
}

func newAttachmentCommand() *cobra.Command {
	attachment := &cobra.Command{
		Use:   "attachment",
		Short: "List and save files from a thread",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: list, save. Lists downloadable attachments from every message in a known thread. Pass a returned ID to `hey attachment save <id>`.",
		},
	}
	attachment.AddCommand(newAttachmentsCommand().cmd)
	attachment.AddCommand(newAttachmentsSaveCommand().cmd)
	return attachment
}

func newAttachmentsCommand() *attachmentsCommand {
	attachmentsCommand := &attachmentsCommand{}
	attachmentsCommand.cmd = &cobra.Command{
		Use:   "list <thread-id>",
		Short: "List a thread's attachments",
		Example: `  hey attachment list 12345
  hey attachment list 12345 --json
  hey attachment list 12345 --allow-partial`,
		RunE: attachmentsCommand.run,
		Args: usageExactOneArg(),
	}
	attachmentsCommand.cmd.Flags().BoolVar(&attachmentsCommand.allowPartial, "allow-partial", false,
		"List what could be read of a thread that could only be read in part, with a notice saying what is missing")

	return attachmentsCommand
}

func (c *attachmentsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := parsePositiveID(args[0], "thread")
	if err != nil {
		return err
	}

	attachments, notice, err := attachmentsInThread(cmd.Context(), threadID)
	if err != nil {
		return err
	}
	if notice != "" && !c.allowPartial {
		return errPartialThread(threadID, notice)
	}

	if writer.IsStyled() {
		if len(attachments) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No attachments found.")
		} else {
			table := newTable(cmd.OutOrStdout())
			table.addRow([]string{"ID", "Filename", "Type", "Size"})
			for _, attachment := range attachments {
				table.addRow([]string{
					attachment.ID,
					terminal.SanitizeLine(attachment.Filename),
					terminal.SanitizeLine(attachment.ContentType),
					formatOptionalByteSize(attachment.ByteSize),
				})
			}
			table.print()
		}
		if notice != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", notice)
		}
		return nil
	}
	if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}

	noun := "attachments"
	if len(attachments) == 1 {
		noun = "attachment"
	}
	options := []output.ResponseOption{
		output.WithSummary(fmt.Sprintf("%d %s in thread %d", len(attachments), noun, threadID)),
		output.WithNotice(notice),
	}
	if len(attachments) > 0 {
		options = append(options, output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "save",
			Command:     fmt.Sprintf("hey attachment save %s", attachments[0].ID),
			Description: "Save an attachment",
		}))
	}
	if writer.EffectiveFormat() == output.FormatMarkdown {
		attachments = attachmentsForMarkdown(attachments)
	}
	return writeOK(attachments, options...)
}

func attachmentsForMarkdown(attachments []threadAttachment) []threadAttachment {
	safe := make([]threadAttachment, len(attachments))
	copy(safe, attachments)
	for index := range safe {
		safe[index].Filename = markdownSafeText(safe[index].Filename)
		safe[index].ContentType = markdownSafeText(safe[index].ContentType)
	}
	return safe
}

// attachmentsInThread reads every message in a thread and lists what each one carries,
// newest entry first. Attachment metadata lives in the message HTML, so every format —
// a count included — reads the bodies. The notice says what could not be read, and is
// empty when everything was.
func attachmentsInThread(ctx context.Context, threadID int64) ([]threadAttachment, string, error) {
	thread, err := loadThread(ctx, threadID, true)
	if err != nil {
		return nil, "", err
	}

	var attachments []threadAttachment
	for index := len(thread.Entries) - 1; index >= 0; index-- {
		loaded := thread.Entries[index]
		if loaded.Message == nil {
			continue
		}
		for attachmentIndex, attachment := range htmlutil.ExtractAttachments(loaded.Message.Content) {
			attachments = append(attachments, threadAttachment{
				ID:          attachmentID(loaded.Entry.Id, attachmentIndex+1),
				MessageID:   loaded.Entry.Id,
				Filename:    attachment.Filename,
				ContentType: attachment.ContentType,
				ByteSize:    attachment.ByteSize,
				URL:         attachment.URL,
			})
		}
	}
	return attachments, threadNotice(thread), nil
}

func attachmentID(messageID int64, position int) string {
	return fmt.Sprintf("%d:%d", messageID, position)
}

func formatOptionalByteSize(size *int64) string {
	if size == nil {
		return "—"
	}
	return formatByteSize(*size)
}

func formatByteSize(size int64) string {
	switch {
	case size < 0:
		return "—"
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
}
