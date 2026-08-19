package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

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
		Use:   "save <id>",
		Short: "Save an attachment to disk",
		Annotations: map[string]string{
			"agent_notes": "Saves the attachment identified by an ID from `hey attachments <thread-id>`. Existing files are preserved unless --force is set.",
		},
		Example: `  hey attachments save 67890:1
  hey attachments save 67890:1 --output ./quarterly-report.pdf
  hey attachments save 67890:1 --output ./downloads --force`,
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
		return convertSDKError(err)
	}
	if message == nil {
		return output.ErrNotFound("message", strconv.FormatInt(messageID, 10))
	}
	attachments := htmlutil.ExtractAttachments(message.Content)
	if position > len(attachments) {
		return output.ErrNotFound("attachment", args[0])
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
	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Saved %s (%s)\n", destination, formatByteSize(result.ByteSize))
		return nil
	}
	return writeOK(result, output.WithSummary(fmt.Sprintf("Attachment saved to %s", destination)))
}

func parseAttachmentID(id string) (int64, int, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 2 {
		return 0, 0, output.ErrUsage(fmt.Sprintf("invalid attachment ID: %s", id))
	}
	messageID, messageErr := strconv.ParseInt(parts[0], 10, 64)
	position, positionErr := strconv.Atoi(parts[1])
	if messageErr != nil || positionErr != nil || messageID <= 0 || position <= 0 {
		return 0, 0, output.ErrUsage(fmt.Sprintf("invalid attachment ID: %s", id))
	}
	return messageID, position, nil
}

func attachmentDestination(outputPath, filename string) (string, error) {
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." || filename == ".." || filename == string(filepath.Separator) {
		return "", output.ErrUsage("attachment has no safe filename")
	}
	if outputPath == "" {
		return filename, nil
	}
	info, err := os.Stat(outputPath)
	if err == nil && info.IsDir() {
		return filepath.Join(outputPath, filename), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", output.ErrAPI(0, fmt.Sprintf("could not inspect output path: %v", err))
	}
	return filepath.Clean(outputPath), nil
}

func downloadAttachmentFile(ctx context.Context, destination, sourceURL string, force bool) (int64, error) {
	if !force {
		if _, err := os.Lstat(destination); err == nil {
			return 0, output.ErrUsage(fmt.Sprintf("destination already exists: %s (use --force to replace it)", destination))
		} else if !os.IsNotExist(err) {
			return 0, output.ErrAPI(0, fmt.Sprintf("could not inspect attachment destination: %v", err))
		}
	}

	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".hey-attachment-*")
	if err != nil {
		return 0, output.ErrAPI(0, fmt.Sprintf("could not create temporary attachment file: %v", err))
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	written, _, err := sdk.DownloadBlob(ctx, sourceURL, temporary)
	if err != nil {
		_ = temporary.Close()
		return written, convertSDKError(err)
	}
	if err := temporary.Close(); err != nil {
		return written, output.ErrAPI(0, fmt.Sprintf("could not close attachment file: %v", err))
	}

	if force {
		if err := replaceFile(temporaryPath, destination); err != nil {
			return written, output.ErrAPI(0, fmt.Sprintf("could not replace attachment file: %v", err))
		}
		return written, nil
	}
	if err := os.Link(temporaryPath, destination); os.IsExist(err) {
		return written, output.ErrUsage(fmt.Sprintf("destination already exists: %s (use --force to replace it)", destination))
	} else if err != nil {
		return written, output.ErrAPI(0, fmt.Sprintf("could not save attachment file: %v", err))
	}
	return written, nil
}
