package smoke_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSeenUnseen(t *testing.T) {
	// Get a posting from the imbox.
	resp := heyJSON(t, "box", "imbox")
	type Posting struct {
		ID     int    `json:"id"`
		AppURL string `json:"app_url"`
		Kind   string `json:"kind"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)
	if len(data.Postings) == 0 {
		t.Fatal("no postings in imbox to test seen/unseen")
	}

	var posting Posting
	found := false
	for _, candidate := range data.Postings {
		if candidate.Kind == "topic" {
			posting = candidate
			found = true
			break
		}
	}
	if !found {
		t.Skip("no email topics in Imbox to test seen/unseen")
	}
	postingID := intStr(posting.ID)

	// Mark as unseen.
	stdout := heyOK(t, "unseen", postingID, "--kind", posting.Kind, "--json")
	var unseenResp Response
	if err := json.Unmarshal([]byte(stdout), &unseenResp); err != nil {
		t.Fatalf("failed to parse unseen response: %v", err)
	}
	assertContains(t, unseenResp.Summary, "marked as unseen")

	// Cross-verify: the posting should still be accessible on its topic page.
	topicID := extractTopicID(posting.AppURL)
	if topicID != "" {
		html := fetchHTML(t, fmt.Sprintf("%s/topics/%s", baseURL, topicID))
		if len(html) == 0 {
			t.Error("topic page returned empty HTML after marking unseen")
		}
	}

	// Mark as seen.
	stdout = heyOK(t, "seen", postingID, "--kind", posting.Kind, "--json")
	var seenResp Response
	if err := json.Unmarshal([]byte(stdout), &seenResp); err != nil {
		t.Fatalf("failed to parse seen response: %v", err)
	}
	assertContains(t, seenResp.Summary, "marked as seen")
}

func TestSeenMultiple(t *testing.T) {
	resp := heyJSON(t, "box", "imbox")
	type Posting struct {
		ID   int    `json:"id"`
		Kind string `json:"kind"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)
	var topicIDs []string
	for _, posting := range data.Postings {
		if posting.Kind == "topic" {
			topicIDs = append(topicIDs, intStr(posting.ID))
			if len(topicIDs) == 2 {
				break
			}
		}
	}
	if len(topicIDs) < 2 {
		t.Fatal("need at least 2 postings to test multi-seen")
	}

	stdout := heyOK(t, "seen", topicIDs[0], topicIDs[1], "--kind", "topic", "--json")
	var resp2 Response
	if err := json.Unmarshal([]byte(stdout), &resp2); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	assertContains(t, resp2.Summary, "2 threads marked as seen")
}

func TestSeenInvalidID(t *testing.T) {
	heyFail(t, "seen", "not-a-number")
}

func TestSeenRequiresArgs(t *testing.T) {
	heyFail(t, "seen")
}

func TestUnseenRequiresArgs(t *testing.T) {
	heyFail(t, "unseen")
}

func TestUnseenInvalidID(t *testing.T) {
	heyFail(t, "unseen", "not-a-number")
}
