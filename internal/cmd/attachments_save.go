package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	attachmentfiles "github.com/basecamp/hey-cli/internal/attachments"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

type attachmentsSaveCommand struct {
	cmd    *cobra.Command
	output string
	force  bool
}

type savedAttachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Path     string `json:"path"`
	ByteSize int64  `json:"byte_size"`
}

func newAttachmentsSaveCommand() *attachmentsSaveCommand {
	attachmentsSaveCommand := &attachmentsSaveCommand{}
	attachmentsSaveCommand.cmd = &cobra.Command{
		Use:   "save <attachment-id>",
		Short: "Save an attachment to disk",
		Annotations: map[string]string{
			"agent_notes": "Saves the attachment identified by an ID from `hey attachment list <thread-id>`. Existing files are preserved unless --force is set.",
		},
		Example: `  hey attachment save 67890:1
  hey attachment save 67890:1 --output ./quarterly-report.pdf
  hey attachment save 67890:1 --output ./downloads --force`,
		RunE: attachmentsSaveCommand.run,
		Args: usageExactOneArg(),
	}

	attachmentsSaveCommand.cmd.Flags().StringVarP(&attachmentsSaveCommand.output, "output", "o", "", "Destination file or directory")
	attachmentsSaveCommand.cmd.Flags().BoolVar(&attachmentsSaveCommand.force, "force", false, "Replace an existing destination file")
	return attachmentsSaveCommand
}

func (c *attachmentsSaveCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	messageID, position, err := parseAttachmentID(args[0])
	if err != nil {
		return err
	}
	message, err := sdk.Messages().Get(cmd.Context(), messageID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if message == nil {
		return apierr.ErrNotFound("message", strconv.FormatInt(messageID, 10))
	}
	attachments := htmlutil.ExtractAttachments(message.Content)
	if position > len(attachments) {
		return apierr.ErrNotFound("attachment", args[0])
	}
	attachment := attachments[position-1]

	destination, err := attachmentDestination(c.output, attachment.Filename)
	if err != nil {
		return err
	}
	byteSize, err := downloadAttachmentFile(cmd.Context(), destination, attachment.URL, c.force)
	if err != nil {
		return err
	}

	result := savedAttachment{
		ID:       args[0],
		Filename: attachment.Filename,
		Path:     destination,
		ByteSize: byteSize,
	}
	outputResult := result
	if writer.EffectiveFormat() == output.FormatMarkdown {
		outputResult = savedAttachmentForMarkdown(outputResult)
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Saved %s (%s)", destination, formatByteSize(result.ByteSize)),
		fmt.Sprintf("Attachment saved to %s", destination),
		outputResult)
}

func savedAttachmentForMarkdown(attachment savedAttachment) savedAttachment {
	attachment.Filename = markdownSafeText(attachment.Filename)
	attachment.Path = markdownSafeText(attachment.Path)
	return attachment
}

func parseAttachmentID(id string) (int64, int, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 2 {
		return 0, 0, apierr.ErrUsage(fmt.Sprintf("invalid attachment ID: %s", id))
	}
	messageID, messageErr := strconv.ParseInt(parts[0], 10, 64)
	position, positionErr := strconv.Atoi(parts[1])
	if messageErr != nil || positionErr != nil || messageID <= 0 || position <= 0 {
		return 0, 0, apierr.ErrUsage(fmt.Sprintf("invalid attachment ID: %s", id))
	}
	return messageID, position, nil
}

func attachmentDestination(outputPath, filename string) (string, error) {
	return attachmentfiles.Destination(outputPath, filename)
}

func downloadAttachmentFile(ctx context.Context, destination, sourceURL string, force bool) (int64, error) {
	written, err := attachmentfiles.Save(ctx, sdk, destination, sourceURL, force)
	if err == nil {
		return written, nil
	}
	var attachmentErr *apierr.Error
	if errors.As(err, &attachmentErr) {
		return written, err
	}
	return written, apierr.FromSDK(err)
}
