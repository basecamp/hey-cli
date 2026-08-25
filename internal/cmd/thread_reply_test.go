package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// messageAddressedToJane is entry 12 as HEY serves it: Rick wrote it, Jane was on the
// To line and Cee on the CC line.
const messageAddressedToJane = `{
	"id": 12,
	"creator": {"id": 3, "name": "Rick Sanchez", "email_address": "rick@example.com"},
	"sender": {"id": 3, "name": "Rick Sanchez", "email_address": "rick@example.com"},
	"addressed": {
		"directly": [{"id": 1, "name": "Jane Doe", "email_address": "jane@example.com"}],
		"copied": [{"id": 2, "name": "Cee Lo", "email_address": "cc@example.com"}]
	}
}`

// messageWithoutRecipients is an entry HEY tells us nothing addressable about.
const messageWithoutRecipients = `{"id": 12}`

// sentReply is what the server saw a reply arrive as.
type sentReply struct {
	Path                 string
	Content              string
	TopicAccountFilter   string
	MessageAccountFilter string
	ActingSenderID       int64
	Status               string
	To                   []string
	CC                   []string
	BCC                  []string

	// ReplyNewJSON, when set before the command runs, is what GET
	// /entries/{id}/replies/new answers — HEY's own computed reply recipients.
	// Empty means the endpoint 404s and the CLI falls back to computing locally.
	ReplyNewJSON string
}

// threadReplyServer answers the typed topic, the latest entry's message, the identity
// the SDK needs for a sending operation, and the reply itself — recording it so a test
// can say what actually went out.
func threadReplyServer(t *testing.T, messageJSON string, entryIDs ...int64) (*httptest.Server, *sentReply) {
	t.Helper()
	sent := &sentReply{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/replies/new.json"):
			if sent.ReplyNewJSON == "" {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, sent.ReplyNewJSON)
		case strings.Contains(r.URL.Path, "/replies"):
			if got := r.URL.Query().Get("filtered_account_id"); got != "9" {
				t.Errorf("reply account = %q, want 9", got)
			}
			var body struct {
				ActingSenderID int64 `json:"acting_sender_id"`
				Message        struct {
					Content string `json:"content"`
				} `json:"message"`
				Entry struct {
					Status    string `json:"status"`
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
			sent.ActingSenderID = body.ActingSenderID
			sent.Status = body.Entry.Status
			sent.To, sent.CC, sent.BCC = body.Entry.Addressed.Directly, body.Entry.Addressed.Copied, body.Entry.Addressed.Blindcopied
			if body.Entry.Status == "drafted" {
				// A saved draft answers no body; the entry id rides in Location.
				w.Header().Set("Location", "https://app.hey.com/messages/9101")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		case strings.Contains(r.URL.Path, "identity"):
			if got := r.URL.Query().Get("filtered_account_id"); got != "" {
				t.Errorf("identity account = %q, want unscoped", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":1,"accounts":[{"id":8,"status":"active"},{"id":9,"status":"active"}],"senders":[{"id":42,"account_id":9,"default":true}]}`)
		case r.URL.Path == "/topics/7.json":
			sent.TopicAccountFilter = r.URL.Query().Get("filtered_account_id")
			w.Header().Set("Content-Type", "application/json")
			entries := make([]string, 0, len(entryIDs))
			for _, id := range entryIDs {
				entries = append(entries, fmt.Sprintf(`{"id":%d}`, id))
			}
			fmt.Fprintf(w, `{"id":7,"account_id":9,"entries":[%s]}`, strings.Join(entries, ","))
		case strings.HasPrefix(r.URL.Path, "/messages/"):
			sent.MessageAccountFilter = r.URL.Query().Get("filtered_account_id")
			if r.URL.Path != "/messages/12.json" {
				t.Errorf("read %s, want the thread's latest entry", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, messageJSON)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
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
	previousRoot := rootSDK
	t.Cleanup(func() {
		sdk = previous
		rootSDK = previousRoot
	})
	initSDK(nil, server.URL)
}

func TestResolveThreadReply(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)
	withSDKPointedAt(t, server)

	target, err := resolveThreadReply(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// HEY replies to the last entry on the thread, not the first.
	if target.EntryID != 12 {
		t.Errorf("entry = %d, want the last one (12)", target.EntryID)
	}
	if target.AccountID != 9 {
		t.Errorf("account = %d, want 9", target.AccountID)
	}
	// The reply reaches whoever the entry was addressed to plus whoever wrote it.
	if want := []string{"jane@example.com", "rick@example.com"}; !reflect.DeepEqual(target.Addressed.To, want) {
		t.Errorf("to = %v, want %v", target.Addressed.To, want)
	}
	if want := []string{"cc@example.com"}; !reflect.DeepEqual(target.Addressed.CC, want) {
		t.Errorf("cc = %v, want %v", target.Addressed.CC, want)
	}
	if len(target.Addressed.BCC) != 0 {
		t.Errorf("bcc = %v, want nothing", target.Addressed.BCC)
	}
	if target.client == nil {
		t.Fatal("reply target has no thread-account client")
	}
	if sent.TopicAccountFilter != "" {
		t.Errorf("topic account filter = %q, want unscoped discovery", sent.TopicAccountFilter)
	}
	if sent.MessageAccountFilter != "9" {
		t.Errorf("message account filter = %q, want thread account 9", sent.MessageAccountFilter)
	}
}

// A thread whose participants change partway through is answered as the conversation
// now stands: the latest entry's recipients, not the first entry's.
func TestResolveThreadReplyFollowsTheLatestEntrysRecipients(t *testing.T) {
	server, _ := threadReplyServer(t, `{
		"id": 12,
		"creator": {"id": 4, "name": "Beth Smith", "email_address": "beth@example.com"},
		"addressed": {
			"directly": [
				{"id": 1, "name": "Jane Doe", "email_address": "jane@example.com"},
				{"id": 5, "name": "Morty Smith", "email_address": "morty@example.com"}
			]
		}
	}`, 11, 12)
	withSDKPointedAt(t, server)

	target, err := resolveThreadReply(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"jane@example.com", "morty@example.com", "beth@example.com"}
	if !reflect.DeepEqual(target.Addressed.To, want) {
		t.Errorf("to = %v, want %v", target.Addressed.To, want)
	}
}

// An unaddressed reply is saved as a draft rather than sent, so a thread we cannot read
// recipients from is refused before anything is written.
func TestResolveThreadReplyWithoutRecipients(t *testing.T) {
	server, _ := threadReplyServer(t, messageWithoutRecipients, 11, 12)
	withSDKPointedAt(t, server)

	_, err := resolveThreadReply(context.Background(), 7)

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestResolveThreadReplyWithoutEntries(t *testing.T) {
	server, _ := threadReplyServer(t, messageAddressedToJane)
	withSDKPointedAt(t, server)

	_, err := resolveThreadReply(context.Background(), 7)

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "not_found" {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}

func TestRecipientsForReplyTo(t *testing.T) {
	contact := func(address string) generated.Contact {
		return generated.Contact{EmailAddress: address}
	}

	for _, testCase := range []struct {
		name    string
		message generated.Message
		want    replyRecipients
	}{
		{
			name: "the sender joins the To line",
			message: generated.Message{
				Sender:    contact("rick@example.com"),
				Addressed: generated.Addressed{Directly: []generated.Contact{contact("jane@example.com")}},
			},
			want: replyRecipients{To: []string{"jane@example.com", "rick@example.com"}},
		},
		{
			name: "the creator stands in for a missing sender",
			message: generated.Message{
				Creator:   contact("rick@example.com"),
				Addressed: generated.Addressed{Directly: []generated.Contact{contact("jane@example.com")}},
			},
			want: replyRecipients{To: []string{"jane@example.com", "rick@example.com"}},
		},
		{
			name: "an entry addressed only to us still reaches the person who wrote it",
			message: generated.Message{
				Sender:    contact("rick@example.com"),
				Addressed: generated.Addressed{Directly: []generated.Contact{contact("me@example.com")}},
			},
			want: replyRecipients{To: []string{"me@example.com", "rick@example.com"}},
		},
		{
			name: "the sender is never addressed twice, whatever line it arrived on",
			message: generated.Message{
				Sender: contact("Rick@example.com"),
				Addressed: generated.Addressed{
					Directly:    []generated.Contact{contact("rick@example.com"), contact("jane@example.com")},
					Copied:      []generated.Contact{contact("RICK@example.com")},
					Blindcopied: []generated.Contact{contact("bcc@example.com")},
				},
			},
			want: replyRecipients{
				To:  []string{"jane@example.com", "Rick@example.com"},
				BCC: []string{"bcc@example.com"},
			},
		},
		{
			name: "repeats and blanks are dropped",
			message: generated.Message{
				Sender: contact("rick@example.com"),
				Addressed: generated.Addressed{
					Directly: []generated.Contact{contact("jane@example.com"), contact(" "), contact("jane@example.com")},
				},
			},
			want: replyRecipients{To: []string{"jane@example.com", "rick@example.com"}},
		},
		{
			name:    "an entry HEY tells us nothing about addresses nobody",
			message: generated.Message{},
			want:    replyRecipients{},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := recipientsForReplyTo(testCase.message)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("recipients = %+v, want %+v", got, testCase.want)
			}
		})
	}
}

func TestReplySendsTheMessageAsMarkdown(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)

	err := runCLI(t, server, "--account", "8", "reply", "7",
		"-m", "Sounds **great** — Tuesday it is")
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}

	want := "<p>Sounds <strong>great</strong> — Tuesday it is</p>"
	if sent.Content != want {
		t.Errorf("content = %q, want %q", sent.Content, want)
	}
}

func TestReplySendsRawHTMLVerbatim(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)

	err := runCLI(t, server, "--account", "8", "reply", "7",
		"--message-html", "<p>Confirmed — <strong>Tuesday</strong>.</p>")
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if want := "<p>Confirmed — <strong>Tuesday</strong>.</p>"; sent.Content != want {
		t.Errorf("content = %q, want %q", sent.Content, want)
	}
}

func TestReplyReadsRawHTMLFromFileVerbatim(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)
	path := filepath.Join(t.TempDir(), "reply.html")
	want := "<p>Confirmed — <strong>Tuesday</strong>.</p>\n"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runCLI(t, server, "--account", "8", "reply", "7", "--message-html-file", path)
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if sent.Content != want {
		t.Errorf("content = %q, want exact file bytes %q", sent.Content, want)
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

// HEY's replies/new endpoint answers the reply's recipients with the acting user's own
// addresses excluded — the exclusion the local computation cannot do. When it answers,
// its list wins.
func TestReplyPrefersTheServersComputedRecipients(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)
	sent.ReplyNewJSON = `{"content":"<div>quoted</div>","is_reply":true,
		"addressed":{"directly":[{"id":31,"name":"Rick Ramirez","email_address":"rick@example.com"}]}}`

	if err := runCLI(t, server, "--account", "8", "reply", "7", "-m", "sounds good"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if len(sent.To) != 1 || sent.To[0] != "rick@example.com" {
		t.Errorf("to = %v, want the server-computed list alone", sent.To)
	}
	if len(sent.CC) != 0 {
		t.Errorf("cc = %v, want none", sent.CC)
	}
}

// An empty answer from replies/new means everyone HEY excludes is everyone there is —
// a thread with yourself — and the local computation is what keeps that reply
// addressable. The 404 case rides through every other test in this file, which runs
// against a fake that does not serve the endpoint at all.
func TestReplyFallsBackWhenTheServerAnswersNobody(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)
	sent.ReplyNewJSON = `{"content":"<div>quoted</div>","is_reply":true,"addressed":{}}`

	if err := runCLI(t, server, "--account", "8", "reply", "7", "-m", "note to self"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if len(sent.To) == 0 {
		t.Errorf("to = %v, want the locally computed recipients", sent.To)
	}
}

func TestReplyDraftSavesInsteadOfSending(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)

	if err := runCLI(t, server, "--account", "8", "reply", "7", "-m", "drafting this", "--draft"); err != nil {
		t.Fatalf("reply --draft: %v", err)
	}
	if sent.Status != "drafted" {
		t.Errorf("entry.status = %q, want drafted", sent.Status)
	}
	if !strings.Contains(sent.Path, "/entries/12/replies") {
		t.Errorf("path = %q, want the latest entry's replies", sent.Path)
	}
	if len(sent.To) == 0 {
		t.Errorf("a reply draft carries the thread's recipients, got none")
	}
	if !strings.Contains(sent.Content, "drafting this") {
		t.Errorf("content = %q", sent.Content)
	}
}
