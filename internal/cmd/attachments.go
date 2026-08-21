package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

const maxConcurrentAttachmentMessageFetches = 8

type attachmentsCommand struct {
	cmd *cobra.Command
}

type threadAttachment struct {
	ID          string `json:"id"`
	MessageID   int64  `json:"message_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	ByteSize    *int64 `json:"byte_size,omitempty"`
	URL         string `json:"-"`
}

func newAttachmentsCommand() *attachmentsCommand {
	attachmentsCommand := &attachmentsCommand{}
	attachmentsCommand.cmd = &cobra.Command{
		Use:   "attachments <thread-id>",
		Short: "List and save files from a thread",
		Annotations: map[string]string{
			"agent_notes": "Lists downloadable attachments from every message in a known thread. Pass a returned ID to `hey attachments save <id>`.",
		},
		Example: `  hey attachments 12345
  hey attachments 12345 --json
  hey attachments save 67890:1
  hey attachments save 67890:1 --output ./quarterly-report.pdf`,
		RunE: attachmentsCommand.run,
		Args: usageExactOneArg(),
	}

	attachmentsCommand.cmd.AddCommand(newAttachmentsSaveCommand().cmd)
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

	attachments, truncated, err := attachmentsInThread(cmd.Context(), threadID)
	if err != nil {
		return err
	}

	notice := ""
	if truncated {
		notice = fmt.Sprintf("Thread has more entries than the %d pages read; attachments below those are missing.", maxThreadEntryPages)
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
			Command:     fmt.Sprintf("hey attachments save %s", attachments[0].ID),
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

// attachmentsInThread reads every message in a thread and lists what each one carries.
// The bool reports that the thread has more entries than the page cap allows, so the list
// is missing whatever hangs off them.
func attachmentsInThread(ctx context.Context, threadID int64) ([]threadAttachment, bool, error) {
	collected, err := threadEntryPages(ctx, threadID)
	if err != nil {
		return nil, false, err
	}

	entries := collected.Items
	messages := make([]*generated.Message, len(entries))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxConcurrentAttachmentMessageFetches)
	for index, entry := range entries {
		group.Go(func() error {
			message, getErr := sdk.Messages().Get(groupCtx, entry.Id)
			if getErr != nil {
				return getErr
			}
			if message == nil {
				return fmt.Errorf("message %d returned no data", entry.Id)
			}
			messages[index] = message
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, false, apierr.FromSDK(err)
	}

	var attachments []threadAttachment
	for entryIndex, entry := range entries {
		for attachmentIndex, attachment := range htmlutil.ExtractAttachments(messages[entryIndex].Content) {
			attachments = append(attachments, threadAttachment{
				ID:          attachmentID(entry.Id, attachmentIndex+1),
				MessageID:   entry.Id,
				Filename:    attachment.Filename,
				ContentType: attachment.ContentType,
				ByteSize:    attachment.ByteSize,
				URL:         attachment.URL,
			})
		}
	}
	return attachments, collected.Truncated, nil
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
