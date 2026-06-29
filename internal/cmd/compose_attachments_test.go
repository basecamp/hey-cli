package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/attachments"
	"github.com/basecamp/hey-cli/internal/output"
)

type noopAuth struct{}

func (noopAuth) Authenticate(context.Context, *http.Request) error { return nil }

func TestParseDirectUpload(t *testing.T) {
	body := []byte(`{
		"signed_id": "signed-abc",
		"attachable_sgid": "sgid-xyz",
		"direct_upload": {
			"url": "https://example.com/upload/blob",
			"headers": {"Content-Type": "image/png", "Content-MD5": "abc=="}
		}
	}`)

	upload, err := parseDirectUpload(body)
	if err != nil {
		t.Fatalf("parseDirectUpload: %v", err)
	}
	if upload.SignedID != "signed-abc" {
		t.Errorf("SignedID = %q, want signed-abc", upload.SignedID)
	}
	if upload.AttachableSGID != "sgid-xyz" {
		t.Errorf("AttachableSGID = %q, want sgid-xyz", upload.AttachableSGID)
	}
	if upload.URL != "https://example.com/upload/blob" {
		t.Errorf("URL = %q", upload.URL)
	}
	if upload.Headers["Content-Type"] != "image/png" {
		t.Errorf("Content-Type header = %q", upload.Headers["Content-Type"])
	}
}

func TestParseDirectUploadMissingURL(t *testing.T) {
	if _, err := parseDirectUpload([]byte(`{"signed_id":"x"}`)); err == nil {
		t.Fatal("expected error when upload URL is missing")
	}
}

func TestSDKDirectUploadCreator(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rails/active_storage/direct_uploads.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"signed_id":"sig","attachable_sgid":"sgid","direct_upload":{"url":"` + srvURL(r) + `/up","headers":{"Content-Type":"application/pdf"}}}`))
	}))
	defer srv.Close()

	client := hey.NewClient(&hey.Config{BaseURL: srv.URL, CacheEnabled: false}, nil, hey.WithAuthStrategy(noopAuth{}))
	creator := &sdkDirectUploadCreator{client: client}

	upload, err := creator.CreateDirectUpload(context.Background(), attachments.Blob{
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		ByteSize:    1234,
		Checksum:    "deadbeef==",
	})
	if err != nil {
		t.Fatalf("CreateDirectUpload: %v", err)
	}
	if upload.SignedID != "sig" || upload.AttachableSGID != "sgid" {
		t.Errorf("unexpected upload: %+v", upload)
	}
	for _, want := range []string{`"filename":"report.pdf"`, `"byte_size":1234`, `"checksum":"deadbeef=="`, `"content_type":"application/pdf"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("request body missing %s; got %s", want, gotBody)
		}
	}
}

func srvURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestAttachFilesNoAttachments(t *testing.T) {
	c := &composeCommand{}
	got, err := c.attachFiles(context.Background(), "unchanged body")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "unchanged body" {
		t.Errorf("content = %q, want unchanged", got)
	}
}

func TestAttachFilesMissingFileFailsBeforeSend(t *testing.T) {
	c := &composeCommand{attachments: []string{filepath.Join(t.TempDir(), "ghost.png")}}
	_, err := c.attachFiles(context.Background(), "body")
	if err == nil {
		t.Fatal("expected error for missing attachment")
	}
	if e := output.AsError(err); e.Code != "usage" {
		t.Errorf("error code = %q, want usage", e.Code)
	}
}

func TestAttachFilesDirectoryFails(t *testing.T) {
	c := &composeCommand{attachments: []string{t.TempDir()}}
	if _, err := c.attachFiles(context.Background(), "body"); err == nil {
		t.Fatal("expected error for directory attachment")
	}
}
