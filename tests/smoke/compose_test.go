package smoke_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCompose(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Smoke test %s", uid)

	stdout, stderr, code := hey(t, "compose",
		"--to", "david@basecamp.com",
		"--subject", subject,
		"-m", "Hello from smoke test",
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

	// Cross-verify: fetch the thread page and check the subject appears.
	composeData := dataAs[map[string]any](t, resp)
	if appURL, ok := composeData["app_url"].(string); ok {
		topicID := extractTopicID(appURL)
		if topicID != "" {
			html := fetchHTML(t, fmt.Sprintf("%s/topics/%s", baseURL, topicID))
			assertContains(t, html, subject)
		}
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

	threadsResp := heyJSON(t, "threads", topicID)
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
	for _, p := range data.Postings {
		if composeCode == 0 && p.Summary == subject {
			topicID = extractTopicID(p.AppURL)
			break
		}
		if p.AppURL != "" {
			topicID = extractTopicID(p.AppURL)
			break
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

func TestDrafts(t *testing.T) {
	resp := heyJSON(t, "drafts")
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
	resp := heyJSON(t, "drafts", "--limit", "2")
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
	resp := heyJSON(t, "drafts", "--all")
	// Just verify the command succeeds with --all.
	_ = resp
}

func TestThreadsNoArgument(t *testing.T) {
	heyFail(t, "threads", "--json")
}

func TestReplyNoArgument(t *testing.T) {
	heyFail(t, "reply", "--json")
}
