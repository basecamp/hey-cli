package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
)

// customerUpdateMarkdown is the portable subset an automated sender writes in:
// paragraphs, `- ` bullets, **bold** and *italic*, and nothing else.
const customerUpdateMarkdown = `Hi Alice,

Here is this week's update from the Nova desk.

- **Shipped** the billing import
- *Started* the audit log
- Fixed twelve reconciliation bugs

Next week we move on to the export pipeline.`

// composeSend is what a test server saw, and what it was told to answer with.
type composeSend struct {
	Path           string
	Subject        string
	Content        string
	ActingSenderID int64
	To             []string
	CC             []string
	BCC            []string

	// SendStatus, SendLocation and SendBody are what POST /messages.json answers.
	// The zero value is HEY's own draft-save shape: 204 with a Location naming the
	// entry.
	SendStatus   int
	SendLocation string
	SendBody     string

	// ReadbackStatus and ReadbackJSON are what GET /messages/{id}.json answers.
	// An empty ReadbackJSON echoes what was posted back as the stored message.
	ReadbackStatus int
	ReadbackJSON   string

	// Reads counts the readbacks, so a test can say the message was fetched once.
	Reads int
}

// composeSendServer answers the identity a send needs, the send itself, and the
// readback of the message it created.
func composeSendServer(t *testing.T) (*httptest.Server, *composeSend) {
	t.Helper()
	sent := &composeSend{SendStatus: http.StatusNoContent, SendLocation: "https://app.hey.com/messages/9101"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/messages"):
			var body struct {
				ActingSenderID int64 `json:"acting_sender_id"`
				Message        struct {
					Subject string `json:"subject"`
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
			sent.Subject = body.Message.Subject
			sent.Content = body.Message.Content
			sent.ActingSenderID = body.ActingSenderID
			sent.To = body.Entry.Addressed.Directly
			sent.CC = body.Entry.Addressed.Copied
			sent.BCC = body.Entry.Addressed.Blindcopied
			if sent.SendLocation != "" {
				w.Header().Set("Location", sent.SendLocation)
			}
			if sent.SendBody != "" {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(sent.SendStatus)
			if sent.SendBody != "" {
				fmt.Fprint(w, sent.SendBody)
			}
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/messages/"):
			sent.Reads++
			if sent.ReadbackStatus != 0 && sent.ReadbackStatus != http.StatusOK {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"message":"not found"}`, sent.ReadbackStatus)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if sent.ReadbackJSON != "" {
				fmt.Fprint(w, sent.ReadbackJSON)
				return
			}
			payload, _ := json.Marshal(map[string]any{
				"id":      9101,
				"subject": sent.Subject,
				"content": sent.Content,
				"url":     "https://app.hey.com/topics/7742",
				"sender": map[string]any{
					"id": 42, "name": "Nova Desk", "email_address": "nova@example.com",
				},
				"creator": map[string]any{
					"id": 42, "name": "Nova Desk", "email_address": "nova@example.com",
				},
				"addressed": map[string]any{
					"directly": contactsFor(sent.To),
					"copied":   contactsFor(sent.CC),
				},
			})
			_, _ = w.Write(payload)
		case strings.Contains(r.URL.Path, "identity"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":1,"accounts":[{"id":8,"status":"active"}],"senders":[{"id":42,"account_id":8,"default":true}]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, sent
}

func contactsFor(emails []string) []map[string]any {
	contacts := make([]map[string]any, 0, len(emails))
	for i, email := range emails {
		contacts = append(contacts, map[string]any{"id": 100 + i, "email_address": email})
	}
	return contacts
}

// composeEnvelope is the machine contract `hey compose --json` answers with.
type composeEnvelope struct {
	OK   bool `json:"ok"`
	Data struct {
		Sent         bool   `json:"sent"`
		MessageID    int64  `json:"message_id"`
		TopicID      int64  `json:"topic_id"`
		AppURL       string `json:"app_url"`
		Verification struct {
			Status  string `json:"status"`
			Method  string `json:"method"`
			Reason  string `json:"reason"`
			Subject string `json:"subject"`
			Sender  struct {
				Name         string `json:"name"`
				EmailAddress string `json:"email_address"`
			} `json:"sender"`
			Recipients struct {
				To           []string `json:"to"`
				CC           []string `json:"cc"`
				BCC          []string `json:"bcc"`
				BCCDisclosed bool     `json:"bcc_disclosed"`
				Truncated    bool     `json:"truncated"`
			} `json:"recipients"`
			BodyMarkdown       string `json:"body_markdown"`
			BodyMarkdownSHA256 string `json:"body_markdown_sha256"`
			BodyTruncated      bool   `json:"body_truncated"`
			MatchesSent        struct {
				Subject    bool `json:"subject"`
				Body       bool `json:"body"`
				Recipients bool `json:"recipients"`
			} `json:"matches_sent"`
		} `json:"verification"`
	} `json:"data"`
	Summary string `json:"summary"`
}

func composeJSON(t *testing.T, stdout string) composeEnvelope {
	t.Helper()
	var envelope composeEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("compose did not answer JSON (%v): %s", err, stdout)
	}
	return envelope
}

// The whole contract in one run: a send answers a handle, the handle is read back, and
// the readback names the exact message, its sender, its recipients, its subject and its
// body as canonical Markdown.
func TestComposeAnswersAHandleAndVerifiesTheMessageItCreated(t *testing.T) {
	server, sent := composeSendServer(t)

	stdout, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--cc", "bob@example.com",
		"--subject", "Inovo Customer Update — Week 12",
		"-m", customerUpdateMarkdown)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	envelope := composeJSON(t, stdout)
	if !envelope.OK || !envelope.Data.Sent {
		t.Fatalf("envelope = %+v, want an ok send", envelope)
	}
	if envelope.Data.MessageID != 9101 {
		t.Errorf("message_id = %d, want 9101 from the Location header", envelope.Data.MessageID)
	}
	if envelope.Data.TopicID != 7742 {
		t.Errorf("topic_id = %d, want 7742 from the message's own URL", envelope.Data.TopicID)
	}
	if envelope.Data.AppURL != "https://app.hey.com/topics/7742" {
		t.Errorf("app_url = %q", envelope.Data.AppURL)
	}
	if sent.Reads != 1 {
		t.Errorf("readbacks = %d, want exactly one", sent.Reads)
	}

	verification := envelope.Data.Verification
	if verification.Status != "verified" {
		t.Fatalf("status = %q (%s), want verified", verification.Status, verification.Reason)
	}
	if verification.Method != "message_read" {
		t.Errorf("method = %q", verification.Method)
	}
	if verification.Sender.EmailAddress != "nova@example.com" {
		t.Errorf("sender = %q, want the address HEY sent it as", verification.Sender.EmailAddress)
	}
	if verification.Subject != "Inovo Customer Update — Week 12" {
		t.Errorf("subject = %q", verification.Subject)
	}
	if want := []string{"alice@example.com"}; !equalStrings(verification.Recipients.To, want) {
		t.Errorf("to = %v, want %v", verification.Recipients.To, want)
	}
	if want := []string{"bob@example.com"}; !equalStrings(verification.Recipients.CC, want) {
		t.Errorf("cc = %v, want %v", verification.Recipients.CC, want)
	}
	if len(verification.Recipients.BCC) != 0 || verification.Recipients.BCCDisclosed {
		t.Errorf("bcc = %v disclosed = %v, want an undisclosed empty list",
			verification.Recipients.BCC, verification.Recipients.BCCDisclosed)
	}
	if !verification.MatchesSent.Subject || !verification.MatchesSent.Body || !verification.MatchesSent.Recipients {
		t.Errorf("matches_sent = %+v, want every comparison to hold", verification.MatchesSent)
	}
}

// The body comes back as the Markdown it was written in: paragraphs, `- ` bullets,
// **bold** and *italic* survive the round trip through HEY's Trix HTML byte for byte,
// which is what lets a caller compare what it sent with what was stored.
func TestComposeRoundTripsThePortableMarkdownSubset(t *testing.T) {
	server, _ := composeSendServer(t)

	stdout, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12",
		"-m", customerUpdateMarkdown)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	body := composeJSON(t, stdout).Data.Verification.BodyMarkdown
	if body != customerUpdateMarkdown {
		t.Errorf("body_markdown =\n%q\nwant\n%q", body, customerUpdateMarkdown)
	}
	for _, want := range []string{
		"Hi Alice,\n\nHere is this week's update",
		"- **Shipped** the billing import",
		"- *Started* the audit log",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body_markdown does not carry %q", want)
		}
	}
	if digest := composeJSON(t, stdout).Data.Verification.BodyMarkdownSHA256; digest != bodyDigest(customerUpdateMarkdown) {
		t.Errorf("body_markdown_sha256 = %q, want the digest of the canonical Markdown", digest)
	}
}

// A send the server accepted but named nothing for is neither a success nor a failure:
// the message may exist. It is reported as ambiguous, with the code and exit status
// that say "do not retry, reconcile".
func TestComposeRefusesAnAcceptedSendItCannotReadBack(t *testing.T) {
	server, sent := composeSendServer(t)
	sent.SendLocation = ""

	_, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12", "-m", "Body.")

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("error = %v, want the CLI's typed error", err)
	}
	if cliErr.Code != apierr.CodeAmbiguous {
		t.Errorf("code = %q, want %q", cliErr.Code, apierr.CodeAmbiguous)
	}
	if !strings.Contains(cliErr.Message, "may have been sent") {
		t.Errorf("message = %q, want it to say the send may have landed", cliErr.Message)
	}
	if sent.Reads != 0 {
		t.Errorf("readbacks = %d, want none — there is nothing to read", sent.Reads)
	}
}

// A readback that fails leaves the send reported with its handle and an honest
// unverified status: the message exists, we simply could not show it yet.
func TestComposeReportsAnUnreadableMessageAsUnverified(t *testing.T) {
	server, sent := composeSendServer(t)
	sent.ReadbackStatus = http.StatusNotFound

	stdout, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	envelope := composeJSON(t, stdout)
	if envelope.Data.MessageID != 9101 {
		t.Errorf("message_id = %d, want the handle to survive a failed readback", envelope.Data.MessageID)
	}
	if envelope.Data.Verification.Status != "unverified" {
		t.Errorf("status = %q, want unverified", envelope.Data.Verification.Status)
	}
	if envelope.Data.Verification.Reason == "" {
		t.Error("an unverified send must say why")
	}
	if envelope.Data.Verification.Subject != "" || len(envelope.Data.Verification.Recipients.To) != 0 {
		t.Error("nothing was read back, so nothing may be reported as read back")
	}
}

// A recipient nobody asked for is the failure this contract exists to catch.
func TestComposeReportsAnUnexpectedRecipientAsAMismatch(t *testing.T) {
	server, sent := composeSendServer(t)
	sent.ReadbackJSON = `{
		"id": 9101,
		"subject": "Inovo Customer Update — Week 12",
		"content": "<p>Body.</p>",
		"sender": {"id": 42, "name": "Nova Desk", "email_address": "nova@example.com"},
		"addressed": {"directly": [
			{"id": 100, "email_address": "alice@example.com"},
			{"id": 101, "email_address": "mallory@example.com"}
		]}
	}`

	stdout, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	verification := composeJSON(t, stdout).Data.Verification
	if verification.Status != "mismatch" {
		t.Errorf("status = %q, want mismatch", verification.Status)
	}
	if verification.MatchesSent.Recipients {
		t.Error("recipients must not compare equal when the readback carries one nobody asked for")
	}
	if want := []string{"alice@example.com", "mallory@example.com"}; !equalStrings(verification.Recipients.To, want) {
		t.Errorf("to = %v, want the readback's own list %v", verification.Recipients.To, want)
	}
}

// HEY does not serve a sent message's BCC line back. That is reported as undisclosed
// rather than as an empty list that was proved empty, and it does not fail the
// comparison: a caller may allow an omitted BCC while still refusing a recipient it
// did not ask for.
func TestComposeReportsAnUndisclosedBCCWithoutFabricatingIt(t *testing.T) {
	server, _ := composeSendServer(t)

	stdout, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--bcc", "carol@example.org",
		"--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	verification := composeJSON(t, stdout).Data.Verification
	if verification.Recipients.BCCDisclosed {
		t.Error("bcc_disclosed must be false when HEY served no blindcopied list")
	}
	if len(verification.Recipients.BCC) != 0 {
		t.Errorf("bcc = %v, want nothing invented", verification.Recipients.BCC)
	}
	if !verification.MatchesSent.Recipients {
		t.Error("an undisclosed BCC is not a mismatch")
	}
	if verification.Status != "verified" {
		t.Errorf("status = %q, want verified", verification.Status)
	}
}

// A body that came back changed is a mismatch, not a verified send.
func TestComposeReportsAChangedBodyAsAMismatch(t *testing.T) {
	server, sent := composeSendServer(t)
	sent.ReadbackJSON = `{
		"id": 9101,
		"subject": "Inovo Customer Update — Week 12",
		"content": "<p>Something else entirely.</p>",
		"sender": {"id": 42, "email_address": "nova@example.com"},
		"addressed": {"directly": [{"id": 100, "email_address": "alice@example.com"}]}
	}`

	stdout, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	verification := composeJSON(t, stdout).Data.Verification
	if verification.Status != "mismatch" || verification.MatchesSent.Body {
		t.Errorf("verification = %+v, want a body mismatch", verification)
	}
	if verification.BodyMarkdown != "Something else entirely." {
		t.Errorf("body_markdown = %q, want what HEY actually stored", verification.BodyMarkdown)
	}
}

// A send HEY refused outright never reaches the handle logic: it keeps the terminal
// code it came with, so a caller can tell "rejected before delivery" from "accepted but
// unreadable".
func TestComposeKeepsATerminalRejectionSeparateFromAnAmbiguousOne(t *testing.T) {
	server, sent := composeSendServer(t)
	sent.SendStatus = http.StatusTooManyRequests
	sent.SendLocation = ""

	_, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12", "-m", "Body.")

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeRateLimit {
		t.Fatalf("error = %v, want a rate_limit error", err)
	}
}

// The line somebody reading along gets is the one they always got.
func TestComposeStyledOutputIsUnchanged(t *testing.T) {
	server, _ := composeSendServer(t)

	stdout, _, err := runCLIRaw(t, server, "--styled", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if strings.TrimSpace(stdout) != "Message sent." {
		t.Errorf("styled output = %q, want the one line it has always written", stdout)
	}
}

// bodyDigest is over the canonical Markdown, so a test can state the expected digest
// without repeating the implementation.
func TestBodyDigestIsOverTheCanonicalMarkdown(t *testing.T) {
	md := htmlutil.ToMarkdown(htmlutil.FromMarkdown(customerUpdateMarkdown))
	if bodyDigest(md.String()) != bodyDigest(customerUpdateMarkdown) {
		t.Error("the portable subset must survive the round trip unchanged")
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A body past the inline bound is left out rather than half-published: half a body
// invites a comparison that looks like it succeeded. The digest still covers the whole
// of it, so the body can be compared even when it cannot be shown.
func TestVerifiedBodyLeavesAnOversizedBodyToItsDigest(t *testing.T) {
	small := "<p>Short enough.</p>"
	body, truncated, digest := verifiedBody(small)
	if truncated || body.String() != "Short enough." {
		t.Errorf("body = %q truncated = %v, want the whole of a small body", body.String(), truncated)
	}
	if digest != bodyDigest("Short enough.") {
		t.Errorf("digest = %q, want the digest of the canonical Markdown", digest)
	}

	large := "<p>" + strings.Repeat("a", maxVerifiedBodyBytes+1) + "</p>"
	body, truncated, digest = verifiedBody(large)
	if !truncated {
		t.Error("a body past the bound must say so")
	}
	if !body.IsEmpty() {
		t.Error("a body past the bound must not be published in part")
	}
	if digest != bodyDigest(strings.Repeat("a", maxVerifiedBodyBytes+1)) {
		t.Error("the digest must cover the whole body, bound or no bound")
	}
}
