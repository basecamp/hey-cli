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

type preparedAttachment struct {
	path        string
	filename    string
	contentType string
	byteSize    int64
	file        *os.File
}

func attachFiles(ctx context.Context, content string, paths []string) (string, error) {
	if len(paths) == 0 {
		return content, nil
	}

	attachments, err := prepareAttachments(paths)
	if err != nil {
		return "", err
	}
	defer closePreparedAttachments(attachments)
	return attachPreparedFiles(ctx, content, attachments)
}

func attachPreparedFiles(ctx context.Context, content string, attachments []preparedAttachment) (string, error) {
	uploads := make([]uploadedAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		upload, err := uploadAttachment(ctx, attachment)
		if err != nil {
			return "", err
		}
		uploads = append(uploads, upload)
	}
	return appendUploadedAttachments(content, uploads), nil
}

func prepareAttachments(paths []string) ([]preparedAttachment, error) {
	attachments := make([]preparedAttachment, 0, len(paths))
	for _, path := range paths {
		pathInfo, err := os.Stat(path)
		if err != nil {
			closePreparedAttachments(attachments)
			return nil, output.ErrUsage(fmt.Sprintf("could not inspect attachment %q: %v", path, err))
		}
		if !pathInfo.Mode().IsRegular() {
			closePreparedAttachments(attachments)
			return nil, output.ErrUsage(fmt.Sprintf("attachment %q is not a regular file", path))
		}
		file, err := os.Open(path)
		if err != nil {
			closePreparedAttachments(attachments)
			return nil, output.ErrUsage(fmt.Sprintf("could not open attachment %q: %v", path, err))
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			closePreparedAttachments(attachments)
			return nil, output.ErrUsage(fmt.Sprintf("could not inspect attachment %q: %v", path, err))
		}
		if !info.Mode().IsRegular() {
			_ = file.Close()
			closePreparedAttachments(attachments)
			return nil, output.ErrUsage(fmt.Sprintf("attachment %q is not a regular file", path))
		}
		contentType, err := attachmentContentType(file, path)
		if err != nil {
			_ = file.Close()
			closePreparedAttachments(attachments)
			return nil, err
		}
		attachments = append(attachments, preparedAttachment{
			path:        path,
			filename:    filepath.Base(path),
			contentType: contentType,
			byteSize:    info.Size(),
			file:        file,
		})
	}
	return attachments, nil
}

func closePreparedAttachments(attachments []preparedAttachment) {
	for _, attachment := range attachments {
		_ = attachment.file.Close()
	}
}

func uploadAttachment(ctx context.Context, attachment preparedAttachment) (uploadedAttachment, error) {
	upload, err := sdk.Attachments().Upload(ctx, attachment.filename, attachment.contentType, attachment.file)
	if err != nil {
		return uploadedAttachment{}, convertSDKError(err)
	}
	if upload == nil || upload.AttachableSgid == "" {
		return uploadedAttachment{}, fmt.Errorf("HEY returned an empty attachment reference for %q", attachment.path)
	}
	return uploadedAttachment{
		Filename:    attachment.filename,
		ContentType: attachment.contentType,
		ByteSize:    attachment.byteSize,
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
