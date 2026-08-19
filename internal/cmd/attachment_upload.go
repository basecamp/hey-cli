package cmd

import (
	"context"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/basecamp/hey-cli/internal/output"
)

type uploadedAttachment struct {
	Filename    string
	ContentType string
	ByteSize    int64
	SGID        string
}

func attachFiles(ctx context.Context, content string, paths []string) (string, error) {
	if len(paths) == 0 {
		return content, nil
	}

	if err := validateAttachmentPaths(paths); err != nil {
		return "", err
	}

	uploads := make([]uploadedAttachment, 0, len(paths))
	for _, path := range paths {
		upload, err := uploadAttachment(ctx, path)
		if err != nil {
			return "", err
		}
		uploads = append(uploads, upload)
	}
	return appendUploadedAttachments(content, uploads), nil
}

func validateAttachmentPaths(paths []string) error {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return output.ErrUsage(fmt.Sprintf("could not read attachment %q: %v", path, err))
		}
		if !info.Mode().IsRegular() {
			return output.ErrUsage(fmt.Sprintf("attachment %q is not a regular file", path))
		}
	}
	return nil
}

func uploadAttachment(ctx context.Context, path string) (uploadedAttachment, error) {
	file, err := os.Open(path)
	if err != nil {
		return uploadedAttachment{}, output.ErrUsage(fmt.Sprintf("could not open attachment %q: %v", path, err))
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return uploadedAttachment{}, output.ErrUsage(fmt.Sprintf("could not inspect attachment %q: %v", path, err))
	}
	if !info.Mode().IsRegular() {
		return uploadedAttachment{}, output.ErrUsage(fmt.Sprintf("attachment %q is not a regular file", path))
	}

	contentType, err := attachmentContentType(file, path)
	if err != nil {
		return uploadedAttachment{}, err
	}
	upload, err := sdk.Attachments().Upload(ctx, filepath.Base(path), contentType, file)
	if err != nil {
		return uploadedAttachment{}, convertSDKError(err)
	}
	if upload == nil || upload.AttachableSgid == "" {
		return uploadedAttachment{}, fmt.Errorf("HEY returned an empty attachment reference for %q", path)
	}
	return uploadedAttachment{
		Filename:    filepath.Base(path),
		ContentType: contentType,
		ByteSize:    info.Size(),
		SGID:        upload.AttachableSgid,
	}, nil
}

func attachmentContentType(file io.ReadSeeker, path string) (string, error) {
	if contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); contentType != "" {
		return attachmentMediaType(contentType), nil
	}

	buffer := make([]byte, 512)
	read, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("inspect attachment content %q: %w", path, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind attachment %q: %w", path, err)
	}
	return attachmentMediaType(http.DetectContentType(buffer[:read])), nil
}

func attachmentMediaType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return contentType
	}
	return mediaType
}

func sentWithAttachmentsSummary(summary string, count int) string {
	if count == 0 {
		return summary
	}
	noun := "attachments"
	if count == 1 {
		noun = "attachment"
	}
	return fmt.Sprintf("%s with %d %s", summary, count, noun)
}

func appendUploadedAttachments(content string, attachments []uploadedAttachment) string {
	var builder strings.Builder
	builder.WriteString(content)
	if content != "" && len(attachments) > 0 {
		builder.WriteString("<br>")
	}
	for _, attachment := range attachments {
		fmt.Fprintf(&builder,
			`<action-text-attachment sgid="%s" content-type="%s" filename="%s" filesize="%d"></action-text-attachment>`,
			stdhtml.EscapeString(attachment.SGID),
			stdhtml.EscapeString(attachment.ContentType),
			stdhtml.EscapeString(attachment.Filename),
			attachment.ByteSize,
		)
	}
	return builder.String()
}
