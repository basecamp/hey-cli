package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
)

type attachmentServerState struct {
	mu             sync.Mutex
	entryReads     int
	directUploads  int
	storageUploads int
	uploadAccounts []string
	sentContents   []string
	events         []string
	blobStatus     int
	nilMessage     bool
}

func attachmentServer(t *testing.T) (*httptest.Server, *attachmentServerState) {
	t.Helper()
	state := &attachmentServerState{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/topics/42/entries.json":
			state.mu.Lock()
			state.entryReads++
			state.mu.Unlock()
			_, _ = w.Write([]byte(`[{"id":101,"kind":"message"},{"id":102,"kind":"message"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/messages/101.json":
			_, _ = w.Write([]byte(`{
				"id":101,
				"content":"<figure data-trix-attachment='{\"sgid\":\"sgid-pdf\",\"url\":\"/rails/active_storage/blobs/report.pdf\",\"filename\":\"quarterly-report.pdf\",\"contentType\":\"application/pdf\",\"filesize\":23}'></figure>"
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/messages/102.json":
			state.mu.Lock()
			nilMessage := state.nilMessage
			state.mu.Unlock()
			if nilMessage {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte(`{"id":102,"content":"<p>No files here</p>"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/rails/active_storage/blobs/report.pdf":
			state.mu.Lock()
			blobStatus := state.blobStatus
			state.mu.Unlock()
			if blobStatus != 0 {
				http.Error(w, "download failed", blobStatus)
				return
			}
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("downloaded report bytes"))
		case r.Method == http.MethodPost && r.URL.Path == "/rails/active_storage/direct_uploads.json":
			state.mu.Lock()
			state.directUploads++
			state.uploadAccounts = append(state.uploadAccounts, r.URL.Query().Get("filtered_account_id"))
			state.events = append(state.events, "reserve")
			state.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{
				"signed_id":"signed-upload",
				"attachable_sgid":"sgid-upload",
				"direct_upload":{"url":%q,"headers":{"Content-Type":"application/pdf"}}
			}`, server.URL+"/storage/upload")
		case r.Method == http.MethodPut && r.URL.Path == "/storage/upload":
			state.mu.Lock()
			state.storageUploads++
			state.events = append(state.events, "upload")
			state.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/identity.json":
			if got := r.URL.Query().Get("filtered_account_id"); got != "" {
				t.Errorf("identity account = %q, want unscoped", got)
			}
			_, _ = w.Write([]byte(`{"id":1,"accounts":[{"id":9,"status":"active"}],"senders":[{"id":42,"account_id":9,"default":true}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/topics/7.json":
			_, _ = w.Write([]byte(`{"id":7,"account_id":9,"entries":[{"id":11},{"id":12}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/messages/12.json":
			_, _ = w.Write([]byte(messageAddressedToJane))
		case r.Method == http.MethodPost && r.URL.Path == "/entries/12/replies.json":
			if got := r.URL.Query().Get("filtered_account_id"); got != "9" {
				t.Errorf("reply account = %q, want 9", got)
			}
			var body struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			state.mu.Lock()
			state.sentContents = append(state.sentContents, body.Message.Content)
			state.events = append(state.events, "send")
			state.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/messages.json":
			var body struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			state.mu.Lock()
			state.sentContents = append(state.sentContents, body.Message.Content)
			state.events = append(state.events, "send")
			state.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Logf("unhandled attachment test request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, state
}

func runAttachmentCommand(t *testing.T, server *httptest.Server, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	root := newRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(append([]string{"--json", "--base-url", server.URL}, args...))
	err := root.Execute()
	return output.String(), err
}

func renderedEmbeddedHTMLFigure(t *testing.T, content string) string {
	t.Helper()
	attributes, err := json.Marshal(struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	}{ContentType: "text/html", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	return `<figure data-trix-attachment="` + stdhtml.EscapeString(string(attributes)) + `"></figure>`
}

func renderedCanonicalEmbeddedHTML(content string) string {
	return `<action-text-attachment content-type="text/html" content="` + stdhtml.EscapeString(content) + `"></action-text-attachment>`
}

func runAttachmentCommandWithStdin(t *testing.T, server *httptest.Server, input string, args ...string) (string, error) {
	t.Helper()
	stdin, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = stdin.Close()
	})
	return runAttachmentCommand(t, server, args...)
}

func TestAttachmentsListsFilesFromKnownThread(t *testing.T) {
	server, state := attachmentServer(t)
	stdout, err := runAttachmentCommand(t, server, "attachment", "list", "42")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK   bool               `json:"ok"`
		Data []threadAttachment `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout)
	}
	if !response.OK || len(response.Data) != 1 {
		t.Fatalf("response = %+v", response)
	}
	attachment := response.Data[0]
	if attachment.ID != "101:1" || attachment.MessageID != 101 || attachment.Filename != "quarterly-report.pdf" || attachment.ByteSize == nil || *attachment.ByteSize != 23 {
		t.Errorf("attachment = %+v", attachment)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.entryReads != 1 {
		t.Errorf("read the entry index %d times, want once for a thread that fits on a page", state.entryReads)
	}
}

func TestAttachmentsListsAndSavesNamedFilesInRenderedOrder(t *testing.T) {
	embeddedImage := `<action-text-attachment sgid="sgid-logo" content-type="image/png" url="/rails/active_storage/blobs/conference-logo.png" filename="conference-logo.png" filesize="9"></action-text-attachment>`
	embeddedPDF := renderedCanonicalEmbeddedHTML(`<action-text-attachment sgid="sgid-agenda" content-type="application/pdf" url="/rails/active_storage/blobs/conference-agenda.pdf" filename="conference-agenda.pdf" filesize="17"></action-text-attachment>`)
	directPDF := `<action-text-attachment sgid="sgid-map" content-type="application/pdf" url="/rails/active_storage/blobs/venue-map.pdf" filename="venue-map.pdf" filesize="9"></action-text-attachment>`
	content := strings.Join([]string{
		renderedEmbeddedHTMLFigure(t, embeddedImage),
		renderedEmbeddedHTMLFigure(t, embeddedPDF),
		directPDF,
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/topics/42/entries.json":
			_, _ = w.Write([]byte(`[{"id":101,"kind":"message"}]`))
		case "/messages/101.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 101, "content": content})
		case "/rails/active_storage/blobs/conference-agenda.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("conference agenda"))
		case "/rails/active_storage/blobs/venue-map.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("venue map"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	stdout, err := runAttachmentCommand(t, server, "attachment", "list", "42")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Data []threadAttachment `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout)
	}
	if len(response.Data) != 3 {
		t.Fatalf("listed attachments = %+v, want three named files", response.Data)
	}
	for index, filename := range []string{"conference-logo.png", "conference-agenda.pdf", "venue-map.pdf"} {
		if response.Data[index].Filename != filename {
			t.Errorf("attachment %d = %+v, want filename %q", index, response.Data[index], filename)
		}
	}
	if !strings.HasPrefix(response.Data[0].ID, "101:e-") || !strings.HasPrefix(response.Data[1].ID, "101:e-") || response.Data[0].ID == response.Data[1].ID {
		t.Errorf("embedded attachment IDs = %q, %q, want distinct embedded IDs", response.Data[0].ID, response.Data[1].ID)
	}
	if response.Data[2].ID != "101:1" {
		t.Errorf("direct attachment ID = %q, want its released ID 101:1", response.Data[2].ID)
	}

	for _, test := range []struct {
		id       string
		filename string
		content  string
	}{
		{id: response.Data[1].ID, filename: "conference-agenda.pdf", content: "conference agenda"},
		{id: "101:01", filename: "venue-map.pdf", content: "venue map"},
	} {
		destination := filepath.Join(t.TempDir(), test.filename)
		if _, err := runAttachmentCommand(t, server, "attachment", "save", test.id, "--output", destination); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(destination); err != nil {
			t.Fatal(err)
		} else if string(got) != test.content {
			t.Errorf("saved attachment = %q, want %q", got, test.content)
		}
	}
}

// A thread longer than one page is walked by following HEY's cursor, so each attachment
// is listed once and the list is not claimed to be truncated.
func TestAttachmentsFollowsTheCursorThroughALongThread(t *testing.T) {
	pages := [][]int64{{103, 102}, {101}}
	reads := &threadEntriesReads{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/topics/42/entries.json":
			reads.mu.Lock()
			reads.entries++
			reads.mu.Unlock()
			index := threadEntriesPageIndex(len(pages), r.URL.Query().Get("page"))
			if index+1 < len(pages) {
				w.Header().Set("Link", fmt.Sprintf(`<http://%s/topics/42/entries.json?page=%s>; rel="next"`,
					r.Host, threadEntriesCursor(index+1)))
			}
			entries := make([]string, 0, len(pages[index]))
			for _, id := range pages[index] {
				entries = append(entries, fmt.Sprintf(`{"id":%d,"kind":"message"}`, id))
			}
			_, _ = fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
		case strings.HasPrefix(r.URL.Path, "/messages/"):
			var id int64
			_, _ = fmt.Sscanf(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/messages/"), ".json"), "%d", &id)
			_, _ = fmt.Fprintf(w, `{"id":%d,"content":%q}`, id, fmt.Sprintf(
				`<figure data-trix-attachment='{"sgid":"sgid-%d","url":"/rails/active_storage/blobs/%d.pdf","filename":"quarterly-report-%d.pdf","contentType":"application/pdf","filesize":23}'></figure>`,
				id, id, id))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	stdout, err := runAttachmentCommand(t, server, "attachment", "list", "42")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Data   []threadAttachment `json:"data"`
		Notice string             `json:"notice"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout)
	}

	if len(response.Data) != 3 {
		t.Fatalf("listed %d attachments, want 3", len(response.Data))
	}
	for index, want := range []string{"103:1", "102:1", "101:1"} {
		if response.Data[index].ID != want {
			t.Errorf("attachment %d = %q, want %q", index, response.Data[index].ID, want)
		}
	}
	if response.Notice != "" {
		t.Errorf("notice = %q, want none for a thread that was read to the end", response.Notice)
	}
	if entryReads, _ := reads.counts(); entryReads != 2 {
		t.Errorf("read %d entry pages, want 2", entryReads)
	}
}

func TestAttachmentsRejectsAnEmptyMessageResponse(t *testing.T) {
	server, state := attachmentServer(t)
	state.mu.Lock()
	state.nilMessage = true
	state.mu.Unlock()

	if _, err := runAttachmentCommand(t, server, "attachment", "list", "42"); err == nil {
		t.Fatal("empty message response should fail attachment discovery")
	}
}

func TestAttachmentsSavePreservesExistingFilesUnlessForced(t *testing.T) {
	server, _ := attachmentServer(t)
	destination := filepath.Join(t.TempDir(), "saved-report.pdf")
	if _, err := runAttachmentCommand(t, server, "attachment", "save", "101:1", "--output", destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "downloaded report bytes" {
		t.Fatalf("saved data = %q, error = %v", data, err)
	}

	if err := os.WriteFile(destination, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = runAttachmentCommand(t, server, "attachment", "save", "101:1", "--output", destination)
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" || !strings.Contains(cliErr.Message, "use --force") {
		t.Fatalf("existing destination error = %v", err)
	}
	data, _ = os.ReadFile(destination)
	if string(data) != "keep me" {
		t.Errorf("existing destination was changed to %q", data)
	}

	if _, err := runAttachmentCommand(t, server, "attachment", "save", "101:1", "--output", destination, "--force"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(destination)
	if string(data) != "downloaded report bytes" {
		t.Errorf("forced destination = %q", data)
	}
}

func TestAttachmentsSaveRemovesPartialFileOnDownloadFailure(t *testing.T) {
	server, state := attachmentServer(t)
	state.mu.Lock()
	state.blobStatus = http.StatusInternalServerError
	state.mu.Unlock()
	directory := t.TempDir()
	destination := filepath.Join(directory, "saved-report.pdf")

	if _, err := runAttachmentCommand(t, server, "attachment", "save", "101:1", "--output", destination); err == nil {
		t.Fatal("failed download should return an error")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Errorf("failed download left destination behind: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("failed download left temporary files: %v", entries)
	}
}

func TestComposeUploadsAttachmentsBeforeSending(t *testing.T) {
	server, state := attachmentServer(t)
	path := filepath.Join(t.TempDir(), "quarterly-report.pdf")
	if err := os.WriteFile(path, []byte("report contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runAttachmentCommand(t, server,
		"compose", "--to", "alice@example.com", "--subject", "Quarterly report", "-m", "Attached.", "--attach", path,
	); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.directUploads != 1 || state.storageUploads != 1 || len(state.sentContents) != 1 {
		t.Fatalf("state = %+v", state)
	}
	if strings.Join(state.events, ",") != "reserve,upload,send" {
		t.Errorf("events = %v", state.events)
	}
	content := state.sentContents[0]
	if !strings.Contains(content, "<p>Attached.</p><br>") || !strings.Contains(content, `action-text-attachment sgid="sgid-upload"`) || !strings.Contains(content, `filename="quarterly-report.pdf"`) {
		t.Errorf("sent content = %q", content)
	}
}

func TestComposeReadsPipedBodyWithAttachments(t *testing.T) {
	server, state := attachmentServer(t)
	path := filepath.Join(t.TempDir(), "quarterly-report.pdf")
	if err := os.WriteFile(path, []byte("report contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runAttachmentCommandWithStdin(t, server, "Piped compose body\n",
		"compose", "--to", "alice@example.com", "--subject", "Quarterly report", "--attach", path,
	); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.sentContents) != 1 || !strings.HasPrefix(state.sentContents[0], "<p>Piped compose body</p><br>") {
		t.Errorf("sent content = %q", state.sentContents)
	}
}

func TestComposeSupportsAttachmentOnlyMessages(t *testing.T) {
	server, state := attachmentServer(t)
	path := filepath.Join(t.TempDir(), "quarterly-report.pdf")
	if err := os.WriteFile(path, []byte("report contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runAttachmentCommand(t, server,
		"compose", "--to", "alice@example.com", "--subject", "Quarterly report", "--attach", path,
	); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.sentContents) != 1 || !strings.HasPrefix(state.sentContents[0], `<action-text-attachment`) {
		t.Errorf("attachment-only content = %q", state.sentContents)
	}
}

func TestReplyUploadsAttachmentsBeforeSending(t *testing.T) {
	server, state := attachmentServer(t)
	path := filepath.Join(t.TempDir(), "quarterly-report.pdf")
	if err := os.WriteFile(path, []byte("report contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runAttachmentCommand(t, server, "reply", "7", "-m", "Attached.", "--attach", path); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if strings.Join(state.events, ",") != "reserve,upload,send" || len(state.sentContents) != 1 || !strings.Contains(state.sentContents[0], `sgid="sgid-upload"`) {
		t.Errorf("reply attachment state = %+v", state)
	}
	if len(state.uploadAccounts) != 1 || state.uploadAccounts[0] != "9" {
		t.Errorf("reply upload accounts = %v, want [9]", state.uploadAccounts)
	}
}

func TestReplySupportsAttachmentOnlyMessages(t *testing.T) {
	server, state := attachmentServer(t)
	path := filepath.Join(t.TempDir(), "quarterly-report.pdf")
	if err := os.WriteFile(path, []byte("report contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runAttachmentCommand(t, server, "reply", "7", "--attach", path); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.sentContents) != 1 || !strings.HasPrefix(state.sentContents[0], `<action-text-attachment`) {
		t.Errorf("attachment-only reply content = %q", state.sentContents)
	}
}

func TestReplyReadsPipedBodyWithAttachments(t *testing.T) {
	server, state := attachmentServer(t)
	path := filepath.Join(t.TempDir(), "quarterly-report.pdf")
	if err := os.WriteFile(path, []byte("report contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runAttachmentCommandWithStdin(t, server, "Piped reply body\n",
		"reply", "7", "--attach", path,
	); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.sentContents) != 1 || !strings.HasPrefix(state.sentContents[0], "<p>Piped reply body</p><br>") {
		t.Errorf("sent content = %q", state.sentContents)
	}
}

func TestComposeValidatesEveryAttachmentBeforeUploading(t *testing.T) {
	server, state := attachmentServer(t)
	path := filepath.Join(t.TempDir(), "quarterly-report.pdf")
	if err := os.WriteFile(path, []byte("report contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runAttachmentCommand(t, server,
		"compose", "--to", "alice@example.com", "--subject", "Quarterly report", "-m", "Attached.",
		"--attach", path, "--attach", filepath.Join(t.TempDir(), "missing.pdf"),
	)
	if err == nil {
		t.Fatal("missing attachment should fail")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.directUploads != 0 || state.storageUploads != 0 || len(state.sentContents) != 0 {
		t.Errorf("invalid attachment performed writes: %+v", state)
	}
}

func TestAttachmentContentTypeUsesBrowserCompatibleMediaType(t *testing.T) {
	contentType, err := attachmentContentType(strings.NewReader("plain text"), "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/plain" {
		t.Errorf("content type = %q, want text/plain", contentType)
	}
	contentType, err = attachmentContentType(strings.NewReader("plain text"), "notes.unknown")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/plain" {
		t.Errorf("sniffed content type = %q, want text/plain", contentType)
	}
}

func TestAttachmentDestinationUsesSafeFilename(t *testing.T) {
	directory := t.TempDir()
	for filename, want := range map[string]string{
		"../../quarterly-report.pdf": `quarterly-report.pdf`,
		`..\..\project-notes.txt`:    `project-notes.txt`,
		`CON.pdf`:                    `_CON.pdf`,
		`CONIN$`:                     `_CONIN$`,
		`LPT².log`:                   `_LPT².log`,
		`notes?.txt`:                 `notes_.txt`,
		"line\nbreak.txt":            `line_break.txt`,
		`quarterly-report. `:         `quarterly-report`,
	} {
		destination, err := attachmentDestination(directory, filename)
		if err != nil {
			t.Errorf("attachmentDestination(%q): %v", filename, err)
			continue
		}
		if destination != filepath.Join(directory, want) {
			t.Errorf("attachmentDestination(%q) = %q, want %q", filename, destination, filepath.Join(directory, want))
		}
	}
	for _, filename := range []string{"", ".", ".."} {
		if _, err := attachmentDestination("", filename); err == nil {
			t.Errorf("unsafe filename %q was accepted", filename)
		}
	}
	explicit := filepath.Join(t.TempDir(), "chosen-name.pdf")
	if destination, err := attachmentDestination(explicit, ".."); err != nil || destination != explicit {
		t.Errorf("explicit destination = %q, %v", destination, err)
	}
}

func TestAttachmentByteSizeDistinguishesEmptyFromUnknown(t *testing.T) {
	zero := int64(0)
	attachment := threadAttachment{ID: "101:1", ByteSize: &zero}
	encoded, err := json.Marshal(attachment)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"byte_size":0`) || formatOptionalByteSize(attachment.ByteSize) != "0 B" {
		t.Errorf("empty attachment = %s, %q", encoded, formatOptionalByteSize(attachment.ByteSize))
	}
	if formatOptionalByteSize(nil) != "—" {
		t.Errorf("unknown attachment size = %q", formatOptionalByteSize(nil))
	}
}

func TestMarkdownSafeTextRemovesControls(t *testing.T) {
	safe := markdownSafeText("report\x1b[31m\r\n.pdf")
	if strings.ContainsAny(safe, "\x1b\r\n") {
		t.Errorf("Markdown-safe text still contains controls: %q", safe)
	}
}

func TestAttachmentsForMarkdownEscapesUntrustedFields(t *testing.T) {
	attachments := []threadAttachment{{
		Filename:    "[report](https://example.invalid)|draft\n<img>.pdf",
		ContentType: "text/plain|preview",
	}}
	safe := attachmentsForMarkdown(attachments)
	if strings.ContainsAny(safe[0].Filename, "\r\n\x1b") ||
		!strings.Contains(safe[0].Filename, `\[report\]\(`) ||
		!strings.Contains(safe[0].Filename, `\<img\>`) ||
		!strings.Contains(safe[0].ContentType, `\|`) {
		t.Errorf("Markdown attachment = %+v", safe[0])
	}
	if attachments[0].Filename != "[report](https://example.invalid)|draft\n<img>.pdf" {
		t.Errorf("JSON attachment data was changed to %q", attachments[0].Filename)
	}
}

func TestSavedAttachmentForMarkdownEscapesFilenameAndPath(t *testing.T) {
	attachment := savedAttachment{Filename: "[report](https://example.invalid)", Path: "<download>/report.pdf"}
	safe := savedAttachmentForMarkdown(attachment)
	if !strings.Contains(safe.Filename, `\[report\]\(`) || !strings.Contains(safe.Path, `\<download\>`) {
		t.Errorf("Markdown saved attachment = %+v", safe)
	}
	if attachment.Filename != "[report](https://example.invalid)" {
		t.Errorf("JSON saved attachment data was changed to %q", attachment.Filename)
	}
}

func TestAppendUploadedAttachmentsSupportsAttachmentOnlyMessages(t *testing.T) {
	content := appendUploadedAttachments("", []uploadedAttachment{{
		Filename:    `report & "notes".pdf`,
		ContentType: "application/pdf",
		ByteSize:    128,
		SGID:        `sgid&1`,
	}})
	if strings.HasPrefix(content, "<br>") {
		t.Errorf("attachment-only content starts with a break: %q", content)
	}
	for _, want := range []string{`sgid="sgid&amp;1"`, `filename="report &amp; &#34;notes&#34;.pdf"`, `filesize="128"`} {
		if !strings.Contains(content, want) {
			t.Errorf("content %q does not contain %q", content, want)
		}
	}
}

func TestAttachmentIDsPreserveDirectPositionsAsEmbeddedFilesAreAdded(t *testing.T) {
	directReport := htmlutil.Attachment{SGID: "sgid-report"}
	directMap := htmlutil.Attachment{SGID: "sgid-map"}
	embeddedLogo := htmlutil.Attachment{SGID: "sgid-logo", Embedded: true}
	embeddedAgenda := htmlutil.Attachment{SGID: "sgid-agenda", Embedded: true}

	legacy := attachmentIDs(101, []htmlutil.Attachment{directReport, directMap})
	expanded := attachmentIDs(101, []htmlutil.Attachment{embeddedLogo, directReport, embeddedAgenda, directMap})
	if legacy[0] != "101:1" || legacy[1] != "101:2" || expanded[1] != legacy[0] || expanded[3] != legacy[1] {
		t.Errorf("legacy IDs = %v, expanded IDs = %v", legacy, expanded)
	}
	if moved := attachmentIDs(101, []htmlutil.Attachment{embeddedAgenda, embeddedLogo}); expanded[0] != moved[1] {
		t.Errorf("embedded logo ID changed from %q to %q when its position changed", expanded[0], moved[1])
	}
}

func TestAttachmentIDsCoverFilesWithoutSGIDsAndDuplicateRepresentations(t *testing.T) {
	withoutSGID := htmlutil.Attachment{
		URL:         "/rails/blobs/report.pdf",
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Embedded:    true,
	}
	first := attachmentIDs(101, []htmlutil.Attachment{withoutSGID})[0]
	moved := attachmentIDs(101, []htmlutil.Attachment{{SGID: "sgid-logo", Embedded: true}, withoutSGID})[1]
	if first != moved {
		t.Errorf("fallback attachment ID changed from %q to %q when its position changed", first, moved)
	}

	duplicateAttachments := []htmlutil.Attachment{withoutSGID, withoutSGID}
	duplicates := attachmentIDs(101, duplicateAttachments)
	if duplicates[0] == duplicates[1] || !strings.HasSuffix(duplicates[1], ".2") {
		t.Errorf("duplicate attachment IDs = %v, want distinct occurrence suffixes", duplicates)
	}
	for _, id := range duplicates {
		if _, _, err := parseAttachmentID(id); err != nil {
			t.Errorf("parseAttachmentID(%q): %v", id, err)
		}
		if _, found := findAttachmentByID(101, id, duplicateAttachments); !found {
			t.Errorf("findAttachmentByID(%q) did not resolve a duplicate representation", id)
		}
	}
}

func TestParseAttachmentID(t *testing.T) {
	messageID, selector, err := parseAttachmentID("0101:+2")
	if err != nil || messageID != 101 || selector != "2" {
		t.Fatalf("parseAttachmentID = %d, %q, %v", messageID, selector, err)
	}
	embeddedID := attachmentIDs(101, []htmlutil.Attachment{{SGID: "sgid-embedded", Embedded: true}})[0]
	messageID, selector, err = parseAttachmentID(embeddedID)
	if err != nil || messageID != 101 || !strings.HasPrefix(selector, "e-") {
		t.Fatalf("parseAttachmentID(%q) = %d, %q, %v", embeddedID, messageID, selector, err)
	}
	for _, id := range []string{"", "101", "101:0", "101:e-short", "101:e-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.1", "101:e-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.02", "x:1", "1:2:3"} {
		if _, _, err := parseAttachmentID(id); err == nil {
			t.Errorf("parseAttachmentID(%q) succeeded", id)
		}
	}
}
