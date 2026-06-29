package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/attachments"
	"github.com/basecamp/hey-cli/internal/output"
)

// directUploadsPath is HEY's Active Storage direct-upload endpoint.
const directUploadsPath = "/rails/active_storage/direct_uploads.json"

// newAttachmentUploader builds an uploader backed by the SDK client. Blob bytes
// are PUT to a self-authenticating Active Storage URL, so the transfer uses a
// plain HTTP client with no HEY credentials.
func newAttachmentUploader() *attachments.Uploader {
	return attachments.NewUploader(&sdkDirectUploadCreator{client: sdk}, &http.Client{Timeout: 30 * time.Second})
}

// sdkDirectUploadCreator creates Active Storage direct uploads through the HEY
// SDK client, keeping the HEY API call inside the SDK abstraction.
type sdkDirectUploadCreator struct {
	client *hey.Client
}

func (c *sdkDirectUploadCreator) CreateDirectUpload(ctx context.Context, blob attachments.Blob) (*attachments.DirectUpload, error) {
	body := map[string]any{
		"blob": map[string]any{
			"filename":     blob.Filename,
			"byte_size":    blob.ByteSize,
			"checksum":     blob.Checksum,
			"content_type": blob.ContentType,
		},
	}
	resp, err := c.client.PostMutation(ctx, directUploadsPath, body)
	if err != nil {
		return nil, convertSDKError(err)
	}
	return parseDirectUpload(resp.Data)
}

func parseDirectUpload(data []byte) (*attachments.DirectUpload, error) {
	var payload struct {
		SignedID       string `json:"signed_id"`
		AttachableSGID string `json:"attachable_sgid"`
		DirectUpload   struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"direct_upload"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, output.ErrAPI(0, fmt.Sprintf("could not parse direct upload response: %v", err))
	}
	if payload.SignedID == "" {
		return nil, output.ErrAPI(0, "direct upload response missing signed ID")
	}
	if payload.AttachableSGID == "" {
		return nil, output.ErrAPI(0, "direct upload response missing attachable SGID")
	}
	if payload.DirectUpload.URL == "" {
		return nil, output.ErrAPI(0, "direct upload response missing upload URL")
	}
	return &attachments.DirectUpload{
		SignedID:       payload.SignedID,
		AttachableSGID: payload.AttachableSGID,
		URL:            payload.DirectUpload.URL,
		Headers:        payload.DirectUpload.Headers,
	}, nil
}
