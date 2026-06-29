// Package attachments uploads local files through HEY's Active Storage
// direct-upload flow and renders the ActionText markup needed to embed
// them in a message body.
//
// The flow has three steps, matching Rails' direct-upload convention:
//
//  1. Create a direct upload by POSTing blob metadata (filename, byte size,
//     MD5 checksum, content type) to the HEY API. The response describes
//     where to PUT the bytes and how to reference the stored blob.
//  2. PUT the raw file bytes to the returned, self-authenticating URL.
//  3. Embed an <action-text-attachment> element referencing the blob in the
//     message content before sending.
package attachments

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Active Storage requires an MD5 checksum, not for security
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// Blob is the metadata Active Storage needs to create a direct upload.
type Blob struct {
	Filename    string
	ContentType string
	ByteSize    int64
	Checksum    string // base64-encoded MD5 digest
}

// DirectUpload describes where to upload the bytes and how to reference the
// blob once stored. It mirrors the JSON returned by Rails' direct-upload
// endpoint.
type DirectUpload struct {
	SignedID       string
	AttachableSGID string
	URL            string
	Headers        map[string]string
}

// Attachment is an uploaded file, ready to be embedded in message content.
type Attachment struct {
	Filename    string
	ContentType string
	ByteSize    int64
	SignedID    string
	SGID        string
}

// DirectUploadCreator creates an Active Storage direct upload via the HEY API.
// Implementations route the request through the SDK client.
type DirectUploadCreator interface {
	CreateDirectUpload(ctx context.Context, blob Blob) (*DirectUpload, error)
}

// Validate reports whether path refers to a readable regular file. It returns
// a usage error for missing files, directories, and unreadable files so the
// caller can reject bad input before sending anything.
func Validate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return apierr.ErrUsage(fmt.Sprintf("attachment not found: %s", path))
		}
		return apierr.ErrUsage(fmt.Sprintf("cannot read attachment %s: %v", path, err))
	}
	if info.IsDir() {
		return apierr.ErrUsage(fmt.Sprintf("attachment is a directory, not a file: %s", path))
	}
	if !info.Mode().IsRegular() {
		return apierr.ErrUsage(fmt.Sprintf("attachment is not a regular file: %s", path))
	}
	f, err := os.Open(path) //nolint:gosec // path is user-provided by design
	if err != nil {
		return apierr.ErrUsage(fmt.Sprintf("cannot read attachment %s: %v", path, err))
	}
	_ = f.Close()
	return nil
}

// AppendMarkup appends ActionText markup for each attachment to the message
// content. Content with no attachments is returned unchanged.
func AppendMarkup(content string, atts []*Attachment) string {
	if len(atts) == 0 {
		return content
	}
	var b strings.Builder
	b.WriteString(content)
	for _, att := range atts {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(att.Markup())
	}
	return b.String()
}

// Uploader uploads local files through the Active Storage direct-upload flow.
type Uploader struct {
	creator    DirectUploadCreator
	httpClient *http.Client
}

// NewUploader returns an Uploader that creates direct uploads via creator and
// transfers blob bytes with httpClient. The blob PUT targets a
// self-authenticating URL, so httpClient needs no HEY credentials.
func NewUploader(creator DirectUploadCreator, httpClient *http.Client) *Uploader {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Uploader{creator: creator, httpClient: httpClient}
}

// Upload validates path, creates a direct upload, transfers the bytes, and
// returns an Attachment describing the stored blob.
func (u *Uploader) Upload(ctx context.Context, path string) (*Attachment, error) {
	if err := Validate(path); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is user-provided by design
	if err != nil {
		return nil, apierr.ErrUsage(fmt.Sprintf("cannot read attachment %s: %v", path, err))
	}

	blob := Blob{
		Filename:    filepath.Base(path),
		ContentType: detectContentType(path, data),
		ByteSize:    int64(len(data)),
		Checksum:    checksum(data),
	}

	upload, err := u.creator.CreateDirectUpload(ctx, blob)
	if err != nil {
		return nil, err
	}

	if err := u.put(ctx, upload, data); err != nil {
		return nil, err
	}

	return &Attachment{
		Filename:    blob.Filename,
		ContentType: blob.ContentType,
		ByteSize:    blob.ByteSize,
		SignedID:    upload.SignedID,
		SGID:        attachableSGID(upload),
	}, nil
}

func (u *Uploader) put(ctx context.Context, upload *DirectUpload, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, upload.URL, bytes.NewReader(data))
	if err != nil {
		return apierr.ErrAPI(0, fmt.Sprintf("could not build upload request: %v", err))
	}
	for k, v := range upload.Headers {
		req.Header.Set(k, v)
	}

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return apierr.ErrNetwork(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return apierr.ErrAPI(resp.StatusCode, fmt.Sprintf("attachment upload failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	return nil
}

// Markup returns safe ActionText markup embedding the attachment by its signed
// global ID. All attribute values are HTML-escaped.
func (a *Attachment) Markup() string {
	return fmt.Sprintf(
		`<action-text-attachment sgid="%s" content-type="%s" filename="%s" filesize="%d"></action-text-attachment>`,
		html.EscapeString(a.SGID),
		html.EscapeString(a.ContentType),
		html.EscapeString(a.Filename),
		a.ByteSize,
	)
}

// attachableSGID prefers the attachable signed global ID, falling back to the
// blob's signed ID when the server omits it.
func attachableSGID(upload *DirectUpload) string {
	if upload.AttachableSGID != "" {
		return upload.AttachableSGID
	}
	return upload.SignedID
}

func detectContentType(path string, data []byte) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	if ct := http.DetectContentType(data); ct != "application/octet-stream" {
		return ct
	}
	return "application/octet-stream"
}

func checksum(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // Active Storage requires an MD5 checksum, not for security
	return base64.StdEncoding.EncodeToString(sum[:])
}
