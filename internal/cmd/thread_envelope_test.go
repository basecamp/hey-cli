package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// threadEnvelopeServer answers thread 7 as one entry whose message carries the whole
// addressing envelope: who sent it, who it went to, who was copied, and its subject.
func threadEnvelopeServer(t *testing.T, messageJSON string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/topics/7/entries.json":
			fmt.Fprint(w, `[{"id":9101,"kind":"message","summary":"Here is this week's update","created_at":"2026-04-12T09:30:00Z"}]`)
		case strings.HasPrefix(r.URL.Path, "/messages/"):
			fmt.Fprint(w, messageJSON)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

const customerUpdateMessageJSON = `{
	"id": 9101,
	"subject": "Inovo Customer Update — Week 12",
	"content": "<p>Hi Alice,</p>\n<ul>\n<li><strong>Shipped</strong> the billing import</li>\n</ul>",
	"creator": {"id": 42, "name": "Nova Desk", "email_address": "nova@example.com"},
	"sender": {"id": 42, "name": "Nova Desk", "email_address": "nova@example.com"},
	"addressed": {
		"directly": [{"id": 100, "name": "Alice Nakamura", "email_address": "alice@example.com"}],
		"copied": [{"id": 101, "name": "Bob Okonkwo", "email_address": "bob@example.com"}]
	}
}`

// threadReadEnvelope is what `hey thread read --json` answers per entry, for a caller
// that has to prove which message it is looking at and who it reached.
type threadReadEnvelope struct {
	Data []struct {
		ID      int64  `json:"id"`
		Subject string `json:"subject"`
		Sender  struct {
			Name         string `json:"name"`
			EmailAddress string `json:"email_address"`
		} `json:"sender"`
		Creator struct {
			EmailAddress string `json:"email_address"`
		} `json:"creator"`
		Addressed struct {
			To           []string `json:"to"`
			CC           []string `json:"cc"`
			BCC          []string `json:"bcc"`
			BCCDisclosed bool     `json:"bcc_disclosed"`
			Truncated    bool     `json:"truncated"`
		} `json:"addressed"`
		Body      string `json:"body"`
		BodyState string `json:"body_state"`
	} `json:"data"`
}

// The recipients are read off the message HEY served, not inferred from a position in a
// list or found by searching the body.
func TestThreadReadNamesTheEntrysSenderSubjectAndRecipients(t *testing.T) {
	server := threadEnvelopeServer(t, customerUpdateMessageJSON)

	stdout, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7")
	if err != nil {
		t.Fatalf("thread read: %v", err)
	}

	var envelope threadReadEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("thread read did not answer JSON (%v): %s", err, stdout)
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("entries = %d, want 1", len(envelope.Data))
	}

	entry := envelope.Data[0]
	if entry.ID != 9101 {
		t.Errorf("id = %d, want 9101", entry.ID)
	}
	if entry.Subject != "Inovo Customer Update — Week 12" {
		t.Errorf("subject = %q", entry.Subject)
	}
	if entry.Sender.EmailAddress != "nova@example.com" {
		t.Errorf("sender = %q, want the address the message went out as", entry.Sender.EmailAddress)
	}
	if want := []string{"alice@example.com"}; !equalStrings(entry.Addressed.To, want) {
		t.Errorf("to = %v, want %v", entry.Addressed.To, want)
	}
	if want := []string{"bob@example.com"}; !equalStrings(entry.Addressed.CC, want) {
		t.Errorf("cc = %v, want %v", entry.Addressed.CC, want)
	}
	if len(entry.Addressed.BCC) != 0 || entry.Addressed.BCCDisclosed {
		t.Errorf("bcc = %v disclosed = %v, want an undisclosed empty list",
			entry.Addressed.BCC, entry.Addressed.BCCDisclosed)
	}
	if !strings.Contains(entry.Body, "- **Shipped** the billing import") {
		t.Errorf("body = %q, want the Markdown the HTML converts to", entry.Body)
	}
	if entry.BodyState != "hydrated" {
		t.Errorf("body_state = %q", entry.BodyState)
	}
}

// A BCC HEY does serve back is reported as disclosed, so a caller can tell the two
// cases apart rather than reading an empty list as proof.
func TestThreadReadSaysWhenABCCLineWasServed(t *testing.T) {
	server := threadEnvelopeServer(t, `{
		"id": 9101,
		"subject": "Inovo Customer Update — Week 12",
		"content": "<p>Hi Alice,</p>",
		"sender": {"id": 42, "email_address": "nova@example.com"},
		"addressed": {
			"directly": [{"id": 100, "email_address": "alice@example.com"}],
			"blindcopied": [{"id": 102, "email_address": "carol@example.org"}]
		}
	}`)

	stdout, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7")
	if err != nil {
		t.Fatalf("thread read: %v", err)
	}
	var envelope threadReadEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("thread read did not answer JSON (%v): %s", err, stdout)
	}
	entry := envelope.Data[0]
	if !entry.Addressed.BCCDisclosed {
		t.Error("bcc_disclosed must be true when HEY served a blindcopied list")
	}
	if want := []string{"carol@example.org"}; !equalStrings(entry.Addressed.BCC, want) {
		t.Errorf("bcc = %v, want %v", entry.Addressed.BCC, want)
	}
}

// A count reads the index alone, so there is no message to take an envelope from and
// none is invented.
func TestThreadReadCountCarriesNoEnvelope(t *testing.T) {
	server := threadEnvelopeServer(t, customerUpdateMessageJSON)

	stdout, _, err := runCLIRaw(t, server, "--count", "thread", "read", "7")
	if err != nil {
		t.Fatalf("thread read --count: %v", err)
	}
	if strings.TrimSpace(stdout) != "1" {
		t.Errorf("count = %q, want 1", stdout)
	}
}

// A recipient list longer than the bound is cut to it and says so, rather than a
// message addressed to a mailing list being retained whole.
func TestThreadReadBoundsTheRecipientsItRetains(t *testing.T) {
	recipients := make([]string, 0, maxRetainedRecipients+5)
	for i := range maxRetainedRecipients + 5 {
		recipients = append(recipients, fmt.Sprintf(`{"id":%d,"email_address":"reader%d@example.com"}`, 200+i, i))
	}
	server := threadEnvelopeServer(t, fmt.Sprintf(`{
		"id": 9101,
		"subject": "Inovo Customer Update — Week 12",
		"content": "<p>Hi.</p>",
		"sender": {"id": 42, "email_address": "nova@example.com"},
		"addressed": {"directly": [%s]}
	}`, strings.Join(recipients, ",")))

	stdout, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7")
	if err != nil {
		t.Fatalf("thread read: %v", err)
	}
	var envelope threadReadEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("thread read did not answer JSON (%v): %s", err, stdout)
	}
	entry := envelope.Data[0]
	if len(entry.Addressed.To) != maxRetainedRecipients {
		t.Errorf("to = %d addresses, want the bound of %d", len(entry.Addressed.To), maxRetainedRecipients)
	}
	if !entry.Addressed.Truncated {
		t.Error("a cut list must say it was cut")
	}
}
