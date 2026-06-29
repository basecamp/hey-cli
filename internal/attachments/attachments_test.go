package attachments

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestValidateMissingFile(t *testing.T) {
	err := Validate(filepath.Join(t.TempDir(), "nope.png"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidateDirectory(t *testing.T) {
	err := Validate(t.TempDir())
	if err == nil {
		t.Fatal("expected error for directory")
	}
}

func TestValidateRegularFile(t *testing.T) {
	path := writeTempFile(t, "report.pdf", []byte("hello"))
	if err := Validate(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUploadFlow(t *testing.T) {
	data := []byte("a tiny attachment payload")
	path := writeTempFile(t, "diagram.png", data)

	wantChecksum := base64.StdEncoding.EncodeToString(md5Sum(data))

	var gotBlob Blob
	var putBody []byte
	var putContentType string

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/upload/blob123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("blob PUT method = %s, want PUT", r.Method)
		}
		putContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		putBody = b
		w.WriteHeader(http.StatusNoContent)
	})

	creator := creatorFunc(func(ctx context.Context, blob Blob) (*DirectUpload, error) {
		gotBlob = blob
		return &DirectUpload{
			SignedID:       "signed-abc",
			AttachableSGID: "sgid-xyz",
			URL:            srv.URL + "/upload/blob123",
			Headers:        map[string]string{"Content-Type": "image/png"},
		}, nil
	})

	up := NewUploader(creator, srv.Client())
	att, err := up.Upload(context.Background(), path)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if gotBlob.Filename != "diagram.png" {
		t.Errorf("blob filename = %q, want diagram.png", gotBlob.Filename)
	}
	if gotBlob.ContentType != "image/png" {
		t.Errorf("blob content type = %q, want image/png", gotBlob.ContentType)
	}
	if gotBlob.ByteSize != int64(len(data)) {
		t.Errorf("blob byte size = %d, want %d", gotBlob.ByteSize, len(data))
	}
	if gotBlob.Checksum != wantChecksum {
		t.Errorf("blob checksum = %q, want %q", gotBlob.Checksum, wantChecksum)
	}
	if string(putBody) != string(data) {
		t.Errorf("uploaded bytes = %q, want %q", putBody, data)
	}
	if putContentType != "image/png" {
		t.Errorf("PUT content type = %q, want image/png", putContentType)
	}
	if att.SGID != "sgid-xyz" {
		t.Errorf("attachment SGID = %q, want sgid-xyz", att.SGID)
	}
	if att.Filename != "diagram.png" {
		t.Errorf("attachment filename = %q", att.Filename)
	}
}

func TestUploadPutFailure(t *testing.T) {
	path := writeTempFile(t, "diagram.png", []byte("x"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	creator := creatorFunc(func(ctx context.Context, blob Blob) (*DirectUpload, error) {
		return &DirectUpload{
			SignedID:       "signed-abc",
			AttachableSGID: "sgid-xyz",
			URL:            srv.URL,
			Headers:        map[string]string{"Content-Type": "image/png"},
		}, nil
	})

	up := NewUploader(creator, srv.Client())
	if _, err := up.Upload(context.Background(), path); err == nil {
		t.Fatal("expected error on non-2xx PUT")
	}
}

func TestUploadMissingAttachableSGIDFailsBeforePut(t *testing.T) {
	path := writeTempFile(t, "diagram.png", []byte("x"))
	putCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		putCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	creator := creatorFunc(func(ctx context.Context, blob Blob) (*DirectUpload, error) {
		return &DirectUpload{
			SignedID: "signed-abc",
			URL:      srv.URL,
		}, nil
	})

	up := NewUploader(creator, srv.Client())
	if _, err := up.Upload(context.Background(), path); err == nil {
		t.Fatal("expected error when attachable SGID is missing")
	}
	if putCalled {
		t.Fatal("blob PUT should not be attempted without an attachable SGID")
	}
}

func TestUploadValidatesBeforeCreate(t *testing.T) {
	called := false
	creator := creatorFunc(func(ctx context.Context, blob Blob) (*DirectUpload, error) {
		called = true
		return &DirectUpload{}, nil
	})
	up := NewUploader(creator, http.DefaultClient)
	if _, err := up.Upload(context.Background(), filepath.Join(t.TempDir(), "missing.png")); err == nil {
		t.Fatal("expected error for missing file")
	}
	if called {
		t.Error("creator should not be called when validation fails")
	}
}

func TestMarkupEscaping(t *testing.T) {
	att := &Attachment{
		Filename:    `evil"<name>.png`,
		ContentType: "image/png",
		ByteSize:    42,
		SGID:        "sgid-1",
	}
	markup := att.Markup()
	if strings.Contains(markup, `evil"<name>`) {
		t.Errorf("filename not escaped in markup: %s", markup)
	}
	if !strings.Contains(markup, "sgid-1") {
		t.Errorf("markup missing sgid: %s", markup)
	}
	if !strings.Contains(markup, "action-text-attachment") {
		t.Errorf("markup missing action-text-attachment element: %s", markup)
	}
}

func TestAppendMarkup(t *testing.T) {
	atts := []*Attachment{
		{Filename: "a.png", ContentType: "image/png", ByteSize: 1, SGID: "s1"},
		{Filename: "b.pdf", ContentType: "application/pdf", ByteSize: 2, SGID: "s2"},
	}

	combined := AppendMarkup("Hello there", atts)
	if !strings.HasPrefix(combined, "Hello there") {
		t.Errorf("combined content should start with original message: %q", combined)
	}
	if !strings.Contains(combined, "s1") || !strings.Contains(combined, "s2") {
		t.Errorf("combined content missing attachment sgids: %q", combined)
	}

	empty := AppendMarkup("", atts)
	if strings.HasPrefix(empty, "\n") {
		t.Errorf("empty message should not start with newline: %q", empty)
	}

	none := AppendMarkup("Hello", nil)
	if none != "Hello" {
		t.Errorf("no attachments should leave content unchanged, got %q", none)
	}
}

// creatorFunc adapts a function to the DirectUploadCreator interface.
type creatorFunc func(ctx context.Context, blob Blob) (*DirectUpload, error)

func (f creatorFunc) CreateDirectUpload(ctx context.Context, blob Blob) (*DirectUpload, error) {
	return f(ctx, blob)
}

func md5Sum(b []byte) []byte {
	sum := md5.Sum(b)
	return sum[:]
}
