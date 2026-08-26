package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type draftExportBlob struct {
	data   []byte
	status int
}

func draftExportServer(t *testing.T, editJSON string, blobs map[string]draftExportBlob, writes *[]draftWrite) http.Handler {
	t.Helper()
	draftHandler := draftLifecycleServer(t, editJSON, writes)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if blob, ok := blobs[r.URL.Path]; ok {
			if r.Method != http.MethodGet {
				t.Errorf("attachment request = %s %s, want GET", r.Method, r.URL.Path)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			status := blob.status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			if status >= http.StatusOK && status < http.StatusMultipleChoices {
				_, _ = w.Write(blob.data)
			}
			return
		}
		draftHandler.ServeHTTP(w, r)
	})
}

func draftExportEditJSON(content string) string {
	return `{"id":12345,"subject":"Quarterly planning","content":` + strconv.Quote(content) + `,
		"updated_at":"2026-08-24T10:00:00Z",
		"sender":{"id":77,"email_address":"projects@example.org"},
		"addressed":{"directly":[{"email_address":"maria@example.com"}],
		             "copied":[{"email_address":"priya@example.com"}]}}`
}

func TestDraftExportWritesCompletePrivateBundleWithoutMailboxWrites(t *testing.T) {
	firstBlob := []byte("first report")
	secondBlob := []byte("second report")
	content := `<div><strong>Quarterly planning</strong></div>
<action-text-attachment sgid="secret-one" url="/rails/blobs/first" filename="../../Report.PDF" content-type="application/pdf" filesize="12"></action-text-attachment>
<figure data-trix-attachment='{"sgid":"secret-two","url":"/rails/blobs/second","filename":"report.pdf","contentType":"application/pdf","filesize":13}'></figure>`
	var writes []draftWrite
	handler := draftExportServer(t, draftExportEditJSON(content), map[string]draftExportBlob{
		"/rails/blobs/first":  {data: firstBlob},
		"/rails/blobs/second": {data: secondBlob},
	}, &writes)
	destination := filepath.Join(t.TempDir(), "draft-12345")

	response, err := runJSONCommand(t, handler, "draft", "export", "12345", "--output", destination)
	if err != nil {
		t.Fatalf("draft export: %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("draft export wrote to HEY: %+v", writes)
	}
	data, _ := response.Data.(map[string]any)
	if data["draft_id"] != float64(12345) || data["path"] != destination {
		t.Errorf("result = %#v", response.Data)
	}

	htmlBody, err := os.ReadFile(filepath.Join(destination, "draft.html"))
	if err != nil || string(htmlBody) != content {
		t.Fatalf("draft.html = %q, error = %v", htmlBody, err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(destination, "draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifestBytes), "/rails/blobs/") || strings.Contains(string(manifestBytes), "secret-one") || strings.Contains(string(manifestBytes), "secret-two") {
		t.Errorf("draft.json exposes an internal attachment locator: %s", manifestBytes)
	}
	var manifest draftExportManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode draft.json: %v", err)
	}
	if manifest.Format != draftExportFormat || manifest.DraftID != 12345 || manifest.HTMLPath != "draft.html" || manifest.ExportedAt.IsZero() {
		t.Errorf("manifest header = %+v", manifest)
	}
	if manifest.Subject != "Quarterly planning" || !strings.Contains(manifest.Body, "Quarterly planning") || manifest.From != "projects@example.org" {
		t.Errorf("manifest draft metadata = %+v", manifest)
	}
	if len(manifest.To) != 1 || manifest.To[0] != "maria@example.com" || len(manifest.CC) != 1 || manifest.CC[0] != "priya@example.com" {
		t.Errorf("manifest recipients = to:%v cc:%v", manifest.To, manifest.CC)
	}
	if len(manifest.Attachments) != 2 {
		t.Fatalf("attachments = %+v", manifest.Attachments)
	}
	for index, want := range []struct {
		filename string
		path     string
		data     []byte
	}{
		{filename: "../../Report.PDF", path: "attachments/Report.PDF", data: firstBlob},
		{filename: "report.pdf", path: "attachments/report-2.pdf", data: secondBlob},
	} {
		attachment := manifest.Attachments[index]
		digest := fmt.Sprintf("%x", sha256.Sum256(want.data))
		if attachment.Position != index+1 || attachment.Filename != want.filename || attachment.Path != want.path || attachment.ByteSize != int64(len(want.data)) || attachment.SHA256 != digest {
			t.Errorf("attachment %d = %+v", index, attachment)
		}
		contents, readErr := os.ReadFile(filepath.Join(destination, filepath.FromSlash(want.path)))
		if readErr != nil || string(contents) != string(want.data) {
			t.Errorf("saved attachment %d = %q, error = %v", index, contents, readErr)
		}
	}

	for path, wantMode := range map[string]os.FileMode{
		destination: 0o700,
		filepath.Join(destination, "attachments"):               0o700,
		filepath.Join(destination, "draft.html"):                0o600,
		filepath.Join(destination, "draft.json"):                0o600,
		filepath.Join(destination, "attachments", "Report.PDF"): 0o600,
	} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Errorf("stat %s: %v", path, statErr)
			continue
		}
		if info.Mode().Perm() != wantMode {
			t.Errorf("mode for %s = %v; want %v", path, info.Mode().Perm(), wantMode)
		}
	}
}

func TestDraftExportPublishesNothingWhenAnAttachmentFails(t *testing.T) {
	content := `<action-text-attachment url="/rails/blobs/first" filename="first.txt" content-type="text/plain" filesize="5"></action-text-attachment>
<action-text-attachment url="/rails/blobs/second" filename="second.txt" content-type="text/plain" filesize="6"></action-text-attachment>`
	var writes []draftWrite
	handler := draftExportServer(t, draftExportEditJSON(content), map[string]draftExportBlob{
		"/rails/blobs/first":  {data: []byte("first")},
		"/rails/blobs/second": {status: http.StatusInternalServerError},
	}, &writes)
	parent := t.TempDir()
	destination := filepath.Join(parent, "draft-12345")

	if _, err := runJSONCommand(t, handler, "draft", "export", "12345", "--output", destination); err == nil {
		t.Fatal("failed attachment download should fail the export")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Errorf("failed export published a destination: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Errorf("failed export left staging files: %v, error = %v", entries, err)
	}
	if len(writes) != 0 {
		t.Errorf("failed export wrote to HEY: %+v", writes)
	}
}

func TestDraftExportRejectsReportedAttachmentSizeMismatch(t *testing.T) {
	content := `<action-text-attachment url="/rails/blobs/report" filename="report.txt" content-type="text/plain" filesize="99"></action-text-attachment>`
	var writes []draftWrite
	handler := draftExportServer(t, draftExportEditJSON(content), map[string]draftExportBlob{
		"/rails/blobs/report": {data: []byte("short")},
	}, &writes)
	parent := t.TempDir()
	destination := filepath.Join(parent, "draft-12345")

	_, err := runJSONCommand(t, handler, "draft", "export", "12345", "--output", destination)
	if err == nil || !strings.Contains(err.Error(), "HEY reported 99") {
		t.Fatalf("size mismatch error = %v", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Errorf("size mismatch published a destination: %v", err)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Errorf("size mismatch left staging files: %v", entries)
	}
}

func TestDraftExportPreservesUnrecognizedExistingDestination(t *testing.T) {
	requests := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Errorf("destination preflight should not request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})
	destination := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(destination, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, extra := range [][]string{nil, {"--force"}} {
		args := append([]string{"draft", "export", "12345", "--output", destination}, extra...)
		if _, err := runJSONCommand(t, handler, args...); err == nil {
			t.Errorf("existing destination with flags %v should fail", extra)
		}
		contents, err := os.ReadFile(keep)
		if err != nil || string(contents) != "keep me" {
			t.Errorf("existing destination changed to %q, error = %v", contents, err)
		}
	}
	if requests != 0 {
		t.Errorf("destination preflight made %d requests", requests)
	}
}

func TestDraftExportForceOnlyReplacesAnExactCompleteExport(t *testing.T) {
	content := `<div>Complete body.</div><action-text-attachment url="/rails/blobs/report" filename="report.txt" content-type="text/plain" filesize="6"></action-text-attachment>`
	var writes []draftWrite
	handler := draftExportServer(t, draftExportEditJSON(content), map[string]draftExportBlob{
		"/rails/blobs/report": {data: []byte("report")},
	}, &writes)
	destination := filepath.Join(t.TempDir(), "draft-12345")
	if _, err := runJSONCommand(t, handler, "draft", "export", "12345", "--output", destination); err != nil {
		t.Fatal(err)
	}
	if _, err := runJSONCommand(t, handler, "draft", "export", "12346", "--output", destination, "--force"); err == nil {
		t.Fatal("--force should refuse an export created from a different draft ID")
	}
	if htmlBody, err := os.ReadFile(filepath.Join(destination, "draft.html")); err != nil || string(htmlBody) != content {
		t.Errorf("different-ID force changed the existing export to %q, error = %v", htmlBody, err)
	}

	extraPath := filepath.Join(destination, "personal-notes.txt")
	if err := os.WriteFile(extraPath, []byte("keep this"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runJSONCommand(t, handler, "draft", "export", "12345", "--output", destination, "--force"); err == nil {
		t.Fatal("--force should refuse an export directory with an unexpected file")
	}
	if contents, err := os.ReadFile(extraPath); err != nil || string(contents) != "keep this" {
		t.Errorf("refused force changed extra file to %q, error = %v", contents, err)
	}
	if err := os.Remove(extraPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "draft.html"), []byte("old local copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runJSONCommand(t, handler, "draft", "export", "12345", "--output", destination, "--force"); err != nil {
		t.Fatalf("force exact export: %v", err)
	}
	htmlBody, err := os.ReadFile(filepath.Join(destination, "draft.html"))
	if err != nil || string(htmlBody) != content {
		t.Errorf("forced export body = %q, error = %v", htmlBody, err)
	}
	parentEntries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil || len(parentEntries) != 1 || parentEntries[0].Name() != filepath.Base(destination) {
		t.Errorf("force left prior/staging directories: %v, error = %v", parentEntries, err)
	}
	if len(writes) != 0 {
		t.Errorf("force export wrote to HEY: %+v", writes)
	}
}

func TestDraftExportRejectsHTMLFormatBeforeReadingHEY(t *testing.T) {
	requests := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	destination := filepath.Join(t.TempDir(), "draft-12345")
	_, _, err := runCLIRaw(t, server, "draft", "export", "12345", "--output", destination, "--html")
	if err == nil || !strings.Contains(err.Error(), "--html is not supported") {
		t.Fatalf("--html error = %v", err)
	}
	if requests != 0 {
		t.Errorf("--html refusal made %d requests", requests)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Errorf("--html refusal created destination: %v", err)
	}
}
