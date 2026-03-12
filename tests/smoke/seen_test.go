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
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)
	if len(data.Postings) == 0 {
		t.Fatal("no postings in imbox to test seen/unseen")
	}

	posting := data.Postings[0]
	postingID := intStr(posting.ID)

	// Mark as unseen.
	stdout := heyOK(t, "unseen", postingID, "--json")
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
	stdout = heyOK(t, "seen", postingID, "--json")
	var seenResp Response
	if err := json.Unmarshal([]byte(stdout), &seenResp); err != nil {
		t.Fatalf("failed to parse seen response: %v", err)
	}
	assertContains(t, seenResp.Summary, "marked as seen")
}

func TestSeenMultiple(t *testing.T) {
	resp := heyJSON(t, "box", "imbox")
	type Posting struct {
		ID int `json:"id"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)
	if len(data.Postings) < 2 {
		t.Fatal("need at least 2 postings to test multi-seen")
	}

	id1 := intStr(data.Postings[0].ID)
	id2 := intStr(data.Postings[1].ID)

	stdout := heyOK(t, "seen", id1, id2, "--json")
	var resp2 Response
	if err := json.Unmarshal([]byte(stdout), &resp2); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	assertContains(t, resp2.Summary, "2 posting(s) marked as seen")
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
