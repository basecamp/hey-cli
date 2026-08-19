package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type recordedBulkReplyRequest struct {
	Method string
	Path   string
	Query  string
	Body   []byte
}

type bulkReplyServerState struct {
	mu            sync.Mutex
	requests      []recordedBulkReplyRequest
	draft         string
	draftStatus   int
	delivery      string
	undoStatus    int
	directUploads int
	storage       int
}

func (s *bulkReplyServerState) snapshot() []recordedBulkReplyRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedBulkReplyRequest(nil), s.requests...)
}

func bulkReplyServer(t *testing.T) (*httptest.Server, *bulkReplyServerState) {
	t.Helper()
	state := &bulkReplyServerState{
		draft: `{
			"content":"<div>Signing off with a tag!</div>",
			"entries":[
				{"id":11,"topic_id":21,"topic_name":"Quarterly planning","addressed":{"directly":[{"id":31,"name":"Jane Doe","email_address":"jane@example.com"}],"copied":[{"id":32,"name":"Bob Smith","email_address":"bob@example.org"}],"blindcopied":[{"id":33,"email_address":"audit@example.com"}]}},
				{"id":12,"topic_id":22,"topic_name":"Design review","addressed":{"directly":[{"id":34,"name":"Alice Jones","email_address":"alice@example.com"}]}}
			]
		}`,
		delivery: `{"id":900,"entries_count":2,"delayed":true,"undo_send_url":"https://app.hey.com/bulk_replies/900/undo_send"}`,
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		state.mu.Lock()
		state.requests = append(state.requests, recordedBulkReplyRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body.Bytes()})
		draft := state.draft
		draftStatus := state.draftStatus
		delivery := state.delivery
		undoStatus := state.undoStatus
		state.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/bulk_replies/new.json":
			if draftStatus != 0 {
				w.WriteHeader(draftStatus)
				_, _ = w.Write([]byte(`{"error":"No threads for replying were found"}`))
				return
			}
			_, _ = w.Write([]byte(draft))
		case r.Method == http.MethodPost && r.URL.Path == "/bulk_replies.json":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(delivery))
		case r.Method == http.MethodPost && r.URL.Path == "/bulk_replies/900/undo_send":
			if undoStatus != 0 {
				w.WriteHeader(undoStatus)
				_, _ = w.Write([]byte(`{"errors":["The undo window has expired"]}`))
				return
			}
			w.Header().Set("Location", "/bulk_replies/900")
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodGet && r.URL.Path == "/bulk_replies/900":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/rails/active_storage/direct_uploads.json":
			state.mu.Lock()
			state.directUploads++
			state.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"signed_id":"signed-upload","attachable_sgid":"sgid-upload","direct_upload":{"url":%q,"headers":{"Content-Type":"application/pdf"}}}`, server.URL+"/storage/upload")
		case r.Method == http.MethodPut && r.URL.Path == "/storage/upload":
			state.mu.Lock()
			state.storage++
			state.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, state
}

func runBulkReply(t *testing.T, server *httptest.Server, formatArgs, args []string) (string, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := newRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	commandArgs := append([]string{"bulk-reply", "--base-url", server.URL}, formatArgs...)
	commandArgs = append(commandArgs, args...)
	root.SetArgs(commandArgs)
	err := root.Execute()
	return output.String(), err
}

func TestBulkReplyPreviewShowsExactRecipientsInEveryFormat(t *testing.T) {
	server, state := bulkReplyServer(t)
	tests := []struct {
		name       string
		formatArgs []string
		contains   []string
	}{
		{"json", []string{"--json"}, []string{`"topic_name": "Quarterly planning"`, `"email_address": "jane@example.com"`, `"skipped_count": 1`}},
		{"quiet", []string{"--quiet"}, []string{`"topic_name": "Quarterly planning"`, `"email_address": "bob@example.org"`}},
		{"markdown", []string{"--markdown"}, []string{"Quarterly planning", `jane\@example\.com`, `audit\@example\.com`}},
		{"styled", []string{"--styled"}, []string{"Quarterly planning", "Jane Doe <jane@example.com>", "1 posting skipped", "Signing off with a tag!"}},
		{"ids", []string{"--ids-only"}, []string{"11\n12\n"}},
		{"count", []string{"--count"}, []string{"2\n"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := runBulkReply(t, server, tt.formatArgs, []string{"preview", "101", "202", "303"})
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range tt.contains {
				if !strings.Contains(output, expected) {
					t.Errorf("output does not contain %q:\n%s", expected, output)
				}
			}
		})
	}

	requests := state.snapshot()
	if len(requests) != len(tests) {
		t.Fatalf("requests = %d, want %d", len(requests), len(tests))
	}
	for _, request := range requests {
		if request.Method != http.MethodGet || request.Path != "/bulk_replies/new.json" || request.Query != "posting_ids=101%2C202%2C303" {
			t.Errorf("preview request = %+v", request)
		}
	}
}

func TestBulkReplySendPreservesDraftAndReportsDelayedDelivery(t *testing.T) {
	server, state := bulkReplyServer(t)
	output, err := runBulkReply(t, server, []string{"--json"}, []string{"send", "101", "202", "303", "-m", "Thanks everyone"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"entries_count": 2`) || !strings.Contains(output, `"delayed": true`) || !strings.Contains(output, `"command": "hey bulk-reply undo 900"`) || !strings.Contains(output, `"skipped_count": 1`) {
		t.Errorf("delivery output = %s", output)
	}

	requests := state.snapshot()
	if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodPost {
		t.Fatalf("requests = %+v", requests)
	}
	var request struct {
		EntryIDs []int64 `json:"entry_ids"`
		Message  struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(requests[1].Body, &request); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(request.EntryIDs) != "[11 12]" {
		t.Errorf("entry_ids = %v", request.EntryIDs)
	}
	want := "<div>Thanks everyone</div><br><div>Signing off with a tag!</div>"
	if request.Message.Content != want {
		t.Errorf("content = %q, want %q", request.Message.Content, want)
	}
}

func TestBulkReplySendReportsImmediateDeliveryWithoutUndo(t *testing.T) {
	server, state := bulkReplyServer(t)
	state.delivery = `{"id":901,"entries_count":2,"delayed":false}`
	output, err := runBulkReply(t, server, []string{"--json"}, []string{"send", "101", "202", "-m", "Thanks"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"summary": "2 replies sent"`) || strings.Contains(output, `"action": "undo"`) {
		t.Errorf("immediate output = %s", output)
	}
}

func TestBulkReplySendSupportsEveryOutputFormatWithoutPostSendErrors(t *testing.T) {
	tests := []struct {
		name       string
		formatArgs []string
		expected   string
	}{
		{"json", []string{"--json"}, `"id": 900`},
		{"quiet", []string{"--quiet"}, `"id": 900`},
		{"markdown", []string{"--markdown"}, "**id:** 900"},
		{"styled", []string{"--styled"}, "Delivery ID: 900"},
		{"ids", []string{"--ids-only"}, "900\n"},
		{"count", []string{"--count"}, "2\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, state := bulkReplyServer(t)
			output, err := runBulkReply(t, server, test.formatArgs, []string{"send", "101", "202", "-m", "Thanks"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, test.expected) {
				t.Errorf("output does not contain %q: %s", test.expected, output)
			}
			requests := state.snapshot()
			if len(requests) != 2 || requests[1].Path != "/bulk_replies.json" {
				t.Errorf("send requests = %+v", requests)
			}
		})
	}
}

func TestBulkReplySendSupportsAttachments(t *testing.T) {
	server, state := bulkReplyServer(t)
	attachment := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(attachment, []byte("quarterly report"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runBulkReply(t, server, []string{"--json"}, []string{"send", "101", "202", "-m", "Attached", "--attach", attachment})
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	if state.directUploads != 1 || state.storage != 1 {
		t.Errorf("uploads = direct:%d storage:%d", state.directUploads, state.storage)
	}
	state.mu.Unlock()
	requests := state.snapshot()
	if len(requests) != 4 || requests[len(requests)-1].Path != "/bulk_replies.json" {
		t.Fatalf("requests = %+v", requests)
	}
	if !strings.Contains(string(requests[len(requests)-1].Body), `action-text-attachment`) || !strings.Contains(string(requests[len(requests)-1].Body), `sgid-upload`) {
		t.Errorf("send body did not include attachment: %s", requests[len(requests)-1].Body)
	}
}

func TestBulkReplySendStopsWhenNoEntriesResolve(t *testing.T) {
	for _, draftStatus := range []int{0, http.StatusNotFound} {
		t.Run(fmt.Sprintf("status_%d", draftStatus), func(t *testing.T) {
			server, state := bulkReplyServer(t)
			state.draft = `{"content":"<div>Name tag</div>","entries":[]}`
			state.draftStatus = draftStatus
			attachment := filepath.Join(t.TempDir(), "report.pdf")
			if err := os.WriteFile(attachment, []byte("report"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := runBulkReply(t, server, []string{"--json"}, []string{"send", "101", "-m", "This must not send", "--attach", attachment})
			if err == nil || !strings.Contains(err.Error(), "no replyable threads") {
				t.Fatalf("error = %v", err)
			}
			requests := state.snapshot()
			if len(requests) != 1 || requests[0].Method != http.MethodGet {
				t.Errorf("empty draft made mutation requests: %+v", requests)
			}
		})
	}
}

func TestBulkReplyPreviewTurnsNoReplyableNotFoundIntoEmptyPreview(t *testing.T) {
	server, state := bulkReplyServer(t)
	state.draftStatus = http.StatusNotFound
	output, err := runBulkReply(t, server, []string{"--json"}, []string{"preview", "101", "202"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"data": []`) || !strings.Contains(output, `"skipped_count": 2`) {
		t.Errorf("empty preview = %s", output)
	}
}

func TestBulkReplyValidatesPositiveUniqueIDsBeforeRequest(t *testing.T) {
	tests := [][]string{
		{"preview", "0"},
		{"preview", "-1"},
		{"preview", "not-an-id"},
		{"send", "101", "101", "-m", "No"},
		{"send", "101", "-m", ""},
		{"send", "101", "-m", "No", "--attach", filepath.Join(t.TempDir(), "missing.pdf")},
		{"undo", "0"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			server, state := bulkReplyServer(t)
			if _, err := runBulkReply(t, server, []string{"--json"}, args); err == nil {
				t.Fatal("expected validation error")
			}
			if requests := state.snapshot(); len(requests) != 0 {
				t.Errorf("validation failure made requests: %+v", requests)
			}
		})
	}
}

func TestReadBulkReplyMessageSupportsInlineStdinEditorAndAttachmentsOnly(t *testing.T) {
	unused := func() (string, error) { return "", errors.New("should not read stdin") }
	unusedEditor := func(string) (string, error) { return "", errors.New("should not open editor") }

	message, err := readBulkReplyMessage("Inline", 0, true, unused, unusedEditor)
	if err != nil || message != "Inline" {
		t.Errorf("inline = %q, %v", message, err)
	}
	message, err = readBulkReplyMessage("", 0, false, func() (string, error) { return "From stdin", nil }, unusedEditor)
	if err != nil || message != "From stdin" {
		t.Errorf("stdin = %q, %v", message, err)
	}
	message, err = readBulkReplyMessage("", 0, true, unused, func(initial string) (string, error) {
		if initial != "" {
			t.Errorf("editor initial content = %q", initial)
		}
		return "  From editor\n", nil
	})
	if err != nil || message != "From editor" {
		t.Errorf("editor = %q, %v", message, err)
	}
	message, err = readBulkReplyMessage("", 1, true, unused, unusedEditor)
	if err != nil || message != "" {
		t.Errorf("attachment-only = %q, %v", message, err)
	}
}

func TestBulkReplyUndoSupportsDataOnlyFormats(t *testing.T) {
	for _, test := range []struct {
		name       string
		formatArgs []string
		expected   string
	}{{"ids", []string{"--ids-only"}, "900\n"}, {"count", []string{"--count"}, "1\n"}} {
		t.Run(test.name, func(t *testing.T) {
			server, _ := bulkReplyServer(t)
			output, err := runBulkReply(t, server, test.formatArgs, []string{"undo", "900"})
			if err != nil {
				t.Fatal(err)
			}
			if output != test.expected {
				t.Errorf("output = %q, want %q", output, test.expected)
			}
		})
	}
}

func TestBulkReplyUndoSuccessAndExpiredWindow(t *testing.T) {
	server, _ := bulkReplyServer(t)
	output, err := runBulkReply(t, server, []string{"--json"}, []string{"undo", "900"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"undone": true`) || !strings.Contains(output, `"id": 900`) {
		t.Errorf("undo output = %s", output)
	}

	server, state := bulkReplyServer(t)
	state.undoStatus = http.StatusUnprocessableEntity
	_, err = runBulkReply(t, server, []string{"--json"}, []string{"undo", "900"})
	if err == nil {
		t.Fatal("expired undo should fail")
	}
	if converted := apierr.AsError(err); converted.Code != "api" {
		t.Errorf("expired undo code = %q, want api (%v)", converted.Code, err)
	}
}
