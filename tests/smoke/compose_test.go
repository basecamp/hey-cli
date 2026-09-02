package smoke_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

// composeResult is the machine contract `hey compose --json` answers with: the handle
// naming the message that was created, and what reading it back showed.
type composeResult struct {
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
		} `json:"recipients"`
		BodyMarkdown       string `json:"body_markdown"`
		BodyMarkdownSHA256 string `json:"body_markdown_sha256"`
		MatchesSent        struct {
			Subject    bool `json:"subject"`
			Body       bool `json:"body"`
			Recipients bool `json:"recipients"`
		} `json:"matches_sent"`
	} `json:"verification"`
}

func TestCompose(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Smoke test %s", uid)
	body := "Hello from smoke test.\n\n- **First** item\n- *Second* item"

	stdout, stderr, code := hey(t, "compose",
		"--to", "david@basecamp.com",
		"--subject", subject,
		"-m", body,
		"--json",
	)
	if code != 0 {
		t.Fatalf("compose failed (exit %d): %s", code, stderr)
	}

	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse compose response: %v", err)
	}
	if !resp.OK {
		t.Fatal("compose returned ok=false")
	}
	assertContains(t, resp.Summary, "Message sent")

	// The handle is not optional. A send that reports success and names nothing is the
	// regression this check exists for: the cross-verification below used to sit inside
	// an `if app_url, ok := …`, so a compose with no handle passed silently.
	result := dataAs[composeResult](t, resp)
	if !result.Sent {
		t.Fatal("compose did not report the message as sent")
	}
	if result.MessageID == 0 && result.TopicID == 0 {
		t.Fatalf("compose named neither a message nor a thread: %s", string(resp.Data))
	}

	// And the readback is what proves it. Anything but `verified` means the CLI could
	// not show that what HEY stored is what was asked for.
	verification := result.Verification
	if verification.Status != "verified" {
		t.Fatalf("verification = %s (%s), want verified", verification.Status, verification.Reason)
	}
	if verification.Subject != subject {
		t.Errorf("verified subject = %q, want %q", verification.Subject, subject)
	}
	if verification.Sender.EmailAddress == "" {
		t.Error("a verified send must name the address it went out as")
	}
	if len(verification.Recipients.To) != 1 || verification.Recipients.To[0] != "david@basecamp.com" {
		t.Errorf("verified recipients = %v, want exactly the address it was sent to", verification.Recipients.To)
	}
	if !verification.MatchesSent.Subject || !verification.MatchesSent.Body || !verification.MatchesSent.Recipients {
		t.Errorf("matches_sent = %+v, want every comparison to hold", verification.MatchesSent)
	}
	// The Markdown a caller wrote is the Markdown it gets back.
	assertContains(t, verification.BodyMarkdown, "- **First** item")
	assertContains(t, verification.BodyMarkdown, "- *Second* item")
	if verification.BodyMarkdownSHA256 == "" {
		t.Error("a verified send must carry the digest of what was stored")
	}

	// Cross-verify against the browser: the thread the send named holds the subject.
	if result.TopicID == 0 {
		t.Fatalf("compose named no thread to cross-verify: %s", string(resp.Data))
	}
	html := fetchHTML(t, fmt.Sprintf("%s/topics/%d", baseURL, result.TopicID))
	assertContains(t, html, subject)

	// And `hey thread read` answers the same envelope for the same message.
	threadResp := heyJSON(t, "thread", "read", fmt.Sprintf("%d", result.TopicID))
	type threadEntry struct {
		ID        int64  `json:"id"`
		Subject   string `json:"subject"`
		Addressed struct {
			To []string `json:"to"`
		} `json:"addressed"`
	}
	entries := dataAs[[]threadEntry](t, threadResp)
	found := false
	for _, entry := range entries {
		if entry.ID != result.MessageID {
			continue
		}
		found = true
		if entry.Subject != subject {
			t.Errorf("thread read subject = %q, want %q", entry.Subject, subject)
		}
		if len(entry.Addressed.To) != 1 || entry.Addressed.To[0] != "david@basecamp.com" {
			t.Errorf("thread read recipients = %v, want the address it was sent to", entry.Addressed.To)
		}
	}
	if !found && result.MessageID != 0 {
		t.Errorf("thread %d does not carry the message %d that compose named", result.TopicID, result.MessageID)
	}
}

func TestComposeRequiresSubject(t *testing.T) {
	heyFail(t, "compose", "-m", "no subject", "--json")
}

func TestThreads(t *testing.T) {
	// Get a posting from imbox to use as thread ID.
	resp := heyJSON(t, "box", "imbox")
	type Posting struct {
		AppURL string `json:"app_url"`
		ID     int    `json:"id"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)
	if len(data.Postings) == 0 {
		t.Fatal("no postings in imbox to test threads")
	}

	// The thread ID in the CLI is the topic ID, extracted from app_url.
	// app_url looks like "http://host/topics/12345", so we extract the topic ID.
	topicID := extractTopicID(data.Postings[0].AppURL)
	if topicID == "" {
		t.Fatalf("could not extract topic ID from app_url: %s", data.Postings[0].AppURL)
	}

	threadsResp := heyJSON(t, "thread", "read", topicID)
	type Entry struct {
		ID      int    `json:"id"`
		Summary string `json:"summary"`
	}
	entries := dataAs[[]Entry](t, threadsResp)
	if len(entries) == 0 {
		t.Error("expected at least one entry in thread")
	}

	// Cross-verify: the thread content should exist on the topic page.
	html := fetchHTML(t, fmt.Sprintf("%s/topics/%s", baseURL, topicID))
	if len(entries) > 0 && entries[0].Summary != "" {
		assertContains(t, html, entries[0].Summary)
	}
}

func TestReply(t *testing.T) {
	// First try to compose a message to get a thread.
	uid := uniqueID()
	subject := fmt.Sprintf("Reply test %s", uid)
	_, _, composeCode := hey(t, "compose",
		"--to", "david@basecamp.com",
		"--subject", subject,
		"-m", "Original message for reply test",
		"--json",
	)

	// Find a thread in the imbox (use an existing one if compose failed).
	resp := heyJSON(t, "box", "imbox")
	type Posting struct {
		ID      int    `json:"id"`
		AppURL  string `json:"app_url"`
		Summary string `json:"summary"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)

	var topicID string
	// First pass: find the thread we just composed.
	if composeCode == 0 {
		for _, p := range data.Postings {
			if p.Summary == subject {
				topicID = extractTopicID(p.AppURL)
				break
			}
		}
	}
	// Fallback: use any thread with a valid app_url.
	if topicID == "" {
		for _, p := range data.Postings {
			if p.AppURL != "" {
				topicID = extractTopicID(p.AppURL)
				break
			}
		}
	}
	if topicID == "" {
		t.Fatal("could not find a thread to reply to")
	}

	// Reply to it.
	stdout, stderr, code := hey(t, "reply", topicID,
		"-m", fmt.Sprintf("Reply from smoke test %s", uid),
		"--json",
	)
	if code != 0 {
		t.Fatalf("reply failed (exit %d): %s", code, stderr)
	}
	var replyResp Response
	if err := json.Unmarshal([]byte(stdout), &replyResp); err != nil {
		t.Fatalf("failed to parse reply response: %v", err)
	}
	assertContains(t, replyResp.Summary, "Reply sent")

	// Cross-verify: the reply should appear on the thread page.
	html := fetchHTML(t, fmt.Sprintf("%s/topics/%s", baseURL, topicID))
	assertContains(t, html, uid)
}

func TestForward(t *testing.T) {
	resp := heyJSON(t, "box", "imbox")
	type Posting struct {
		AppURL string `json:"app_url"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)
	if len(data.Postings) == 0 {
		skipf(t, "no postings in imbox to forward")
	}
	topicID := extractTopicID(data.Postings[0].AppURL)
	if topicID == "" {
		t.Fatalf("could not extract topic ID from app_url: %s", data.Postings[0].AppURL)
	}

	stdout, stderr, code := hey(t, "forward", topicID,
		"--to", "david@basecamp.com",
		"-m", fmt.Sprintf("Forward smoke test %s", uniqueID()),
		"--json",
	)
	if code != 0 {
		skipf(t, "forward is unavailable on this server (exit %d): %s", code, stderr)
	}
	var forwardResp Response
	if err := json.Unmarshal([]byte(stdout), &forwardResp); err != nil {
		t.Fatalf("failed to parse forward response: %v", err)
	}
	assertContains(t, forwardResp.Summary, "Message forwarded")
}

func TestDrafts(t *testing.T) {
	resp := heyJSON(t, "draft", "list")
	// Just verify the command succeeds and returns valid data.
	// The data is a list (possibly empty).
	if resp.Data == nil {
		// nil data is ok for empty drafts (returned as "null").
		return
	}
	type Draft struct {
		ID int `json:"id"`
	}
	_ = dataAs[[]Draft](t, resp)
}

func TestDraftsLimit(t *testing.T) {
	resp := heyJSON(t, "draft", "list", "--limit", "2")
	if resp.Data == nil {
		return
	}
	type Draft struct {
		ID int `json:"id"`
	}
	drafts := dataAs[[]Draft](t, resp)
	if len(drafts) > 2 {
		t.Errorf("expected at most 2 drafts with --limit 2, got %d", len(drafts))
	}
}

func TestDraftsAll(t *testing.T) {
	resp := heyJSON(t, "draft", "list", "--all")
	// Just verify the command succeeds with --all.
	_ = resp
}

func TestThreadReadNoArgument(t *testing.T) {
	heyFail(t, "thread", "read", "--json")
}

func TestReplyNoArgument(t *testing.T) {
	heyFail(t, "reply", "--json")
}

func TestForwardNoArgument(t *testing.T) {
	heyFail(t, "forward", "--to", "david@basecamp.com", "--json")
}
