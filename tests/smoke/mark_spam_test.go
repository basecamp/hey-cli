package smoke_test

import (
	"encoding/json"
	"testing"
)

func TestMarkSpam(t *testing.T) {
	posting, subject := createDisposableTopic(t, "mark spam test")
	topicID := extractTopicID(posting.AppURL)
	t.Cleanup(func() { cleanupDisposableTopic(t, topicID, subject) })

	type entry struct {
		ID int `json:"id"`
	}
	entries := dataAs[[]entry](t, heyJSON(t, "threads", topicID))
	if len(entries) == 0 {
		t.Skip("disposable topic has no email entry to mark as spam")
	}

	stdout := heyOK(t, "mark-spam", intStr(entries[0].ID), "--json")
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse mark-spam response: %v", err)
	}
	assertContains(t, resp.Summary, "marked as spam")
}

func TestMarkSpamValidation(t *testing.T) {
	heyFail(t, "mark-spam", "--json")
	heyFail(t, "mark-spam", "not-an-entry", "--json")
}
