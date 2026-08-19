package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

const replyForm = "<form action=\"/entries/12/replies\" method=\"post\">" +
	"<select name=\"entry[addressed][directly][]\"><option value=\"jane@example.com\" selected>Jane</option></select>" +
	"<select name=\"entry[addressed][copied][]\"><option value=\"cc@example.com\" selected>Cee</option></select>" +
	"<select name=\"entry[addressed][blindcopied][]\"></select>" +
	"</form>"

// sentReply is what the server saw a reply arrive as.
type sentReply struct {
	Path    string
	Content string
	To      []string
	CC      []string
	BCC     []string
}

// threadReplyServer answers the typed topic, live reply form, identity, and reply
// requests used by reply and compose tests.
func threadReplyServer(t *testing.T, formHTML string) (*httptest.Server, *sentReply) {
	t.Helper()
	sent := &sentReply{}
	latestEntryID := int64(12)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/topics/7" || r.URL.Path == "/topics/7.json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, "{\"id\":7,\"name\":\"Project update\",\"latest_entry\":{\"id\":%d}}", latestEntryID)
		case r.Method == http.MethodGet && r.URL.Path == "/entries/12/replies/new":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, formHTML)
		case r.Method == http.MethodGet && r.URL.Path == "/topics/7/entries.json":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "1" && latestEntryID == 13 {
				_, _ = fmt.Fprint(w, `[{"id":13,"kind":"message","creator":{"id":42,"email_address":"user@hey.com"}}]`)
			} else {
				_, _ = fmt.Fprint(w, `[]`)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/messages/13.json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       13,
				"content":  sent.Content,
				"is_reply": true,
				"creator":  map[string]any{"id": 42, "email_address": "user@hey.com"},
				"sender":   map[string]any{"id": 42, "email_address": "user@hey.com"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/entries/12/replies.json":
			var body struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				Entry struct {
					Addressed struct {
						Directly    []string `json:"directly"`
						Copied      []string `json:"copied"`
						Blindcopied []string `json:"blindcopied"`
					} `json:"addressed"`
				} `json:"entry"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode reply: %v", err)
			}
			sent.Path = r.URL.Path
			sent.Content = body.Message.Content
			sent.To = body.Entry.Addressed.Directly
			sent.CC = body.Entry.Addressed.Copied
			sent.BCC = body.Entry.Addressed.Blindcopied
			latestEntryID = 13
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, "{}")
		case r.Method == http.MethodGet && r.URL.Path == "/identity.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, "{\"id\":1,\"senders\":[{\"id\":42,\"default\":true,\"email_address\":\"user@hey.com\"}],\"primary_contact\":{\"id\":42,\"email_address\":\"user@hey.com\"}}")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, sent
}

// withSDKPointedAt builds the package-level client the commands use, aimed at a test
// server, and puts the old one back afterwards.
func withSDKPointedAt(t *testing.T, server *httptest.Server) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")

	previous := sdk
	t.Cleanup(func() { sdk = previous })
	initSDK(nil, server.URL)
}

func TestResolveThreadReplyUsesLatestEntryAndLiveEnvelope(t *testing.T) {
	server, _ := threadReplyServer(t, replyForm)
	withSDKPointedAt(t, server)

	target, err := resolveThreadReply(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if target.EntryID != 12 {
		t.Errorf("entry = %d, want 12", target.EntryID)
	}
	if target.Subject != "Project update" {
		t.Errorf("subject = %q", target.Subject)
	}
	if len(target.Addressed.To) != 1 || target.Addressed.To[0] != "jane@example.com" {
		t.Errorf("to = %v", target.Addressed.To)
	}
	if len(target.Addressed.CC) != 1 || target.Addressed.CC[0] != "cc@example.com" {
		t.Errorf("cc = %v", target.Addressed.CC)
	}
}

// An unaddressed reply is saved as a draft rather than sent, so a thread whose live
// reply form has no recipients is refused before anything is written.
func TestResolveThreadReplyWithoutRecipients(t *testing.T) {
	server, _ := threadReplyServer(t, "<form action=\"/entries/12/replies\"></form>")
	withSDKPointedAt(t, server)

	_, err := resolveThreadReply(context.Background(), 7)

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestResolveThreadReplyWithoutEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, "{\"id\":7,\"name\":\"Empty\",\"latest_entry\":{}}")
	}))
	t.Cleanup(server.Close)
	withSDKPointedAt(t, server)

	_, err := resolveThreadReply(context.Background(), 7)

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "not_found" {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}

// runCLI drives a command the way the binary does through the root command, so output
// and authentication are configured against the test server.
func runCLI(t *testing.T, server *httptest.Server, args ...string) error {
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
	root.SetArgs(append([]string{"--json", "--base-url", server.URL}, args...))

	return root.Execute()
}
