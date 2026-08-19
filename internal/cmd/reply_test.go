package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/output"
)

func runReply(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"reply", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var response output.Response
	if buf.Len() > 0 {
		if decodeErr := json.Unmarshal(buf.Bytes(), &response); decodeErr != nil {
			t.Fatalf("decode response: %v\n%s", decodeErr, buf.String())
		}
	}
	return response, err
}

func TestReplyUsesLiveEnvelopeAndVerifiesCreatedEntry(t *testing.T) {
	server, sent := threadReplyServer(t, replyForm)

	response, err := runReply(t, server, "7", "-m", "Thanks for the update.", "--expect-entry", "12")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.Summary != "Reply sent" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["thread_id"] != float64(7) || data["entry_id"] != float64(13) {
		t.Errorf("data = %#v", response.Data)
	}
	if sent.Content != "Thanks for the update." {
		t.Errorf("content = %q", sent.Content)
	}
	if len(sent.To) != 1 || sent.To[0] != "jane@example.com" {
		t.Errorf("to = %#v", sent.To)
	}
	if len(sent.CC) != 1 || sent.CC[0] != "cc@example.com" {
		t.Errorf("cc = %#v", sent.CC)
	}
}

func TestReplyPreviewShowsEnvelopeAndAttachmentsWithoutSending(t *testing.T) {
	server, sent := threadReplyServer(t, replyForm)
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("report contents"), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := runReply(t, server, "7", "-m", "Preview body", "--attach", path, "--preview")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if sent.Path != "" {
		t.Fatalf("preview sent a request to %q", sent.Path)
	}
	if response.Summary != "Reply preview" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", response.Data)
	}
	if data["from"] != "user@hey.com" || data["subject"] != "Re: Project update" || data["body"] != "Preview body" {
		t.Errorf("preview data = %#v", data)
	}
	if data["entry_id"] != float64(12) {
		t.Errorf("preview entry_id = %#v, want 12", data["entry_id"])
	}
	attachments, ok := data["attachments"].([]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("attachments = %#v", data["attachments"])
	}
	attachment, ok := attachments[0].(map[string]any)
	if !ok || attachment["path"] != path || attachment["filename"] != "report.txt" || attachment["byte_size"] != float64(15) {
		t.Errorf("attachment = %#v", attachments[0])
	}
}

func TestReplyRequiresPreviewedEntry(t *testing.T) {
	server, sent := threadReplyServer(t, replyForm)

	_, err := runReply(t, server, "7", "-m", "Unbound reply")
	if err == nil || !strings.Contains(err.Error(), "requires --expect-entry") {
		t.Fatalf("unbound send error = %v", err)
	}
	if sent.Path != "" {
		t.Fatalf("unbound send wrote to %q", sent.Path)
	}

	_, err = runReply(t, server, "7", "-m", "Invalid target", "--expect-entry", "0")
	if err == nil || !strings.Contains(err.Error(), "positive entry ID") {
		t.Fatalf("invalid target error = %v", err)
	}
	if sent.Path != "" {
		t.Fatalf("invalid target wrote to %q", sent.Path)
	}

	_, err = runReply(t, server, "7", "-m", "Stale reply", "--expect-entry", "11")
	if err == nil || !strings.Contains(err.Error(), "thread changed after preview") {
		t.Fatalf("stale send error = %v", err)
	}
	if sent.Path != "" {
		t.Fatalf("stale send wrote to %q", sent.Path)
	}
}

func TestVerifyReplyCreatedRejectsUnchangedTopic(t *testing.T) {
	server := replyVerificationServer(t, []verificationCandidate{})
	withSDKPointedAt(t, server)

	_, err := verifyReplyCreated(context.Background(), 7, 12, 42, "Reply body", []time.Duration{0})
	if err == nil || !strings.Contains(err.Error(), "no matching sent entry appeared") || !strings.Contains(err.Error(), "Do not retry automatically") {
		t.Fatalf("error = %v, want unverified-send safety error", err)
	}
}

func TestVerifyReplyCreatedWaitsForPropagation(t *testing.T) {
	server := replyVerificationServer(t,
		[]verificationCandidate{},
		[]verificationCandidate{{ID: 13, SenderID: 42, Content: "<div>Reply body</div>"}},
	)
	withSDKPointedAt(t, server)

	entryID, err := verifyReplyCreated(context.Background(), 7, 12, 42, "Reply body", []time.Duration{0, 0})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if entryID != 13 {
		t.Errorf("entry = %d, want 13", entryID)
	}
}

func TestVerifyReplyCreatedRejectsAnotherParticipantsEntry(t *testing.T) {
	server := replyVerificationServer(t,
		[]verificationCandidate{{ID: 13, SenderID: 99, Content: "Reply body"}},
	)
	withSDKPointedAt(t, server)

	_, err := verifyReplyCreated(context.Background(), 7, 12, 42, "Reply body", []time.Duration{0})
	if err == nil || !strings.Contains(err.Error(), "no matching sent entry appeared") {
		t.Fatalf("error = %v, want unrelated entry rejection", err)
	}
}

func TestVerifyReplyCreatedFindsReplyAfterUnrelatedEntry(t *testing.T) {
	server := replyVerificationServer(t,
		[]verificationCandidate{
			{ID: 13, SenderID: 99, Content: "Reply body"},
			{ID: 14, SenderID: 42, Content: "<p>Reply body</p>"},
		},
	)
	withSDKPointedAt(t, server)

	entryID, err := verifyReplyCreated(context.Background(), 7, 12, 42, "Reply body", []time.Duration{0})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if entryID != 14 {
		t.Errorf("entry = %d, want 14", entryID)
	}
}

func TestVerifyReplyCreatedRejectsDifferentContentFromCurrentUser(t *testing.T) {
	server := replyVerificationServer(t,
		[]verificationCandidate{{ID: 13, SenderID: 42, Content: "Different message"}},
	)
	withSDKPointedAt(t, server)

	_, err := verifyReplyCreated(context.Background(), 7, 12, 42, "Reply body", []time.Duration{0})
	if err == nil || !strings.Contains(err.Error(), "no matching sent entry appeared") {
		t.Fatalf("error = %v, want content mismatch rejection", err)
	}
}

func TestVerifyReplyCreatedRejectsLongerContentWithMatchingPrefix(t *testing.T) {
	server := replyVerificationServer(t,
		[]verificationCandidate{{ID: 13, SenderID: 42, Content: "Reply body with an unrelated ending"}},
	)
	withSDKPointedAt(t, server)

	_, err := verifyReplyCreated(context.Background(), 7, 12, 42, "Reply body", []time.Duration{0})
	if err == nil || !strings.Contains(err.Error(), "no matching sent entry appeared") {
		t.Fatalf("error = %v, want prefix-only content rejection", err)
	}
}

func TestReplyVerificationWindowCoversSlowPropagation(t *testing.T) {
	var window time.Duration
	for _, delay := range defaultReplyVerificationDelays() {
		window += delay
	}
	if window < 10*time.Second {
		t.Errorf("verification window = %s, want at least 10s", window)
	}
}

type verificationCandidate struct {
	ID       int64
	SenderID int64
	Content  string
}

func replyVerificationServer(t *testing.T, batches ...[]verificationCandidate) *httptest.Server {
	t.Helper()
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/topics/7/entries.json":
			if r.URL.Query().Get("page") != "1" {
				_, _ = fmt.Fprint(w, `[]`)
				return
			}
			index := reads
			if index >= len(batches) {
				index = len(batches) - 1
			}
			reads++
			if index < 0 {
				_, _ = fmt.Fprint(w, `[]`)
				return
			}
			entries := make([]map[string]any, 0, len(batches[index]))
			for _, candidate := range batches[index] {
				entries = append(entries, map[string]any{
					"id":      candidate.ID,
					"kind":    "message",
					"creator": map[string]any{"id": candidate.SenderID},
				})
			}
			_ = json.NewEncoder(w).Encode(entries)
		case strings.HasPrefix(r.URL.Path, "/messages/") && strings.HasSuffix(r.URL.Path, ".json"):
			messageID, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/messages/"), ".json"), 10, 64)
			if err != nil {
				t.Fatalf("parse message ID: %v", err)
			}
			for _, batch := range batches {
				for _, candidate := range batch {
					if candidate.ID == messageID {
						_ = json.NewEncoder(w).Encode(map[string]any{
							"id":      candidate.ID,
							"content": candidate.Content,
							"creator": map[string]any{"id": candidate.SenderID},
							"sender":  map[string]any{"id": candidate.SenderID},
						})
						return
					}
				}
			}
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
