package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

const (
	topicWithRecipients = `<span class="entry__full-recipients">
		<a title="jane@example.com">Jane</a>
		CC: <a title="cc@example.com">Cee</a>
	</span>`
	topicEntries = `<div data-entry-id="11"></div><div data-entry-id="12"></div>`
)

// sentReply is what the server saw a reply arrive as.
type sentReply struct {
	Path    string
	Content string
	To      []string
	CC      []string
	BCC     []string
}

// threadReplyServer answers the two topic pages resolveThreadReply reads, the identity
// the SDK needs for a sending operation, and the reply itself — recording it so a test
// can say what actually went out.
func threadReplyServer(t *testing.T, topicHTML, entriesHTML string) (*httptest.Server, *sentReply) {
	t.Helper()
	sent := &sentReply{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/replies"):
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
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent.Path = r.URL.Path
			sent.Content = body.Message.Content
			sent.To, sent.CC, sent.BCC = body.Entry.Addressed.Directly, body.Entry.Addressed.Copied, body.Entry.Addressed.Blindcopied
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		case strings.Contains(r.URL.Path, "identity"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":1,"senders":[{"id":42,"default":true}]}`)
		case strings.HasSuffix(r.URL.Path, "/entries"):
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, entriesHTML)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, topicHTML)
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

func TestResolveThreadReply(t *testing.T) {
	server, _ := threadReplyServer(t, topicWithRecipients, topicEntries)
	withSDKPointedAt(t, server)

	target, err := resolveThreadReply(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// HEY replies to the last entry on the thread, not the first.
	if target.EntryID != 12 {
		t.Errorf("entry = %d, want the last one (12)", target.EntryID)
	}
	if len(target.Addressed.To) != 1 || target.Addressed.To[0] != "jane@example.com" {
		t.Errorf("to = %v", target.Addressed.To)
	}
	if len(target.Addressed.CC) != 1 || target.Addressed.CC[0] != "cc@example.com" {
		t.Errorf("cc = %v", target.Addressed.CC)
	}
}

// An unaddressed reply is saved as a draft rather than sent, so a thread we cannot read
// recipients from is refused before anything is written.
func TestResolveThreadReplyWithoutRecipients(t *testing.T) {
	server, _ := threadReplyServer(t, `<html><body>no recipients here</body></html>`, topicEntries)
	withSDKPointedAt(t, server)

	_, err := resolveThreadReply(context.Background(), 7)

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestResolveThreadReplyWithoutEntries(t *testing.T) {
	server, _ := threadReplyServer(t, topicWithRecipients, `<html><body></body></html>`)
	withSDKPointedAt(t, server)

	_, err := resolveThreadReply(context.Background(), 7)

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "not_found" {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}

// runCLI drives a command the way the binary does — through the root command, so the
// output writer and auth are set up — against a test server.
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
