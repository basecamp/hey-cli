package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestParseDirectUploadMissingSignedID(t *testing.T) {
	body := []byte(`{"attachable_sgid":"sgid","direct_upload":{"url":"https://example.com/upload"}}`)
	if _, err := parseDirectUpload(body); err == nil {
		t.Fatal("expected error when signed_id is missing")
	}
}

func TestParseDirectUploadMissingAttachableSGID(t *testing.T) {
	body := []byte(`{"signed_id":"signed","direct_upload":{"url":"https://example.com/upload"}}`)
	if _, err := parseDirectUpload(body); err == nil {
		t.Fatal("expected error when attachable_sgid is missing")
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

func TestAttachFilesMultipleAttachmentsUploadsAllInOrder(t *testing.T) {
	first := writeCmdTempFile(t, "first.pdf", []byte("first pdf"))
	second := writeCmdTempFile(t, "second.png", []byte("second png"))

	var created []string
	var uploaded []string
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/rails/active_storage/direct_uploads.json", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Blob struct {
				Filename string `json:"filename"`
			} `json:"blob"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode direct upload body: %v", err)
		}
		created = append(created, body.Blob.Filename)
		idx := len(created)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"signed_id":"signed-` + body.Blob.Filename + `","attachable_sgid":"sgid-` + body.Blob.Filename + `","direct_upload":{"url":"` + srv.URL + `/upload/` + string(rune('0'+idx)) + `","headers":{"Content-Type":"application/octet-stream"}}}`))
	})
	mux.HandleFunc("/upload/1", func(w http.ResponseWriter, r *http.Request) {
		uploaded = append(uploaded, "first")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/upload/2", func(w http.ResponseWriter, r *http.Request) {
		uploaded = append(uploaded, "second")
		w.WriteHeader(http.StatusNoContent)
	})

	oldSDK := sdk
	sdk = hey.NewClient(&hey.Config{BaseURL: srv.URL, CacheEnabled: false}, nil, hey.WithAuthStrategy(noopAuth{}))
	t.Cleanup(func() { sdk = oldSDK })

	c := &composeCommand{attachments: []string{first, second}}
	content, err := c.attachFiles(context.Background(), "body")
	if err != nil {
		t.Fatalf("attachFiles: %v", err)
	}

	if strings.Join(created, ",") != "first.pdf,second.png" {
		t.Fatalf("created direct uploads = %v, want first.pdf then second.png", created)
	}
	if strings.Join(uploaded, ",") != "first,second" {
		t.Fatalf("uploaded blobs = %v, want first then second", uploaded)
	}
	firstIndex := strings.Index(content, "sgid-first.pdf")
	secondIndex := strings.Index(content, "sgid-second.png")
	if firstIndex < 0 || secondIndex < 0 || firstIndex > secondIndex {
		t.Fatalf("attachment markup order wrong: %q", content)
	}
}

func TestAttachFilesInvalidAmongMultipleDoesNotUpload(t *testing.T) {
	valid := writeCmdTempFile(t, "valid.pdf", []byte("valid"))
	missing := filepath.Join(t.TempDir(), "missing.pdf")

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	oldSDK := sdk
	sdk = hey.NewClient(&hey.Config{BaseURL: srv.URL, CacheEnabled: false}, nil, hey.WithAuthStrategy(noopAuth{}))
	t.Cleanup(func() { sdk = oldSDK })

	c := &composeCommand{attachments: []string{valid, missing}}
	if _, err := c.attachFiles(context.Background(), "body"); err == nil {
		t.Fatal("expected missing file error")
	}
	if called {
		t.Fatal("server should not be called when one attachment path is invalid")
	}
}

func TestComposeInvalidThreadIDDoesNotUploadAttachments(t *testing.T) {
	attachment := writeCmdTempFile(t, "valid.pdf", []byte("valid"))
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rails/active_storage/direct_uploads.json" {
			called = true
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := runComposeForAttachmentTest(t, srv, "--subject", "Update", "--thread-id", "not-a-number", "-m", "body", "-a", attachment)
	if err == nil {
		t.Fatal("expected invalid thread ID error")
	}
	if called {
		t.Fatal("direct upload should not be called when thread ID is invalid")
	}
}

func runComposeForAttachmentTest(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	root.SetArgs(append([]string{"compose", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	return output.Response{}, err
}

func writeCmdTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}
