package smoke_test

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"
)

type disposableRestorePosting struct {
	ID      int64  `json:"id"`
	TopicID int64  `json:"topic_id"`
	Name    string `json:"name"`
}

func createDisposableRestoreTopic(t *testing.T, purpose string) disposableRestorePosting {
	t.Helper()
	subject := fmt.Sprintf("Disposable %s %s", purpose, uniqueID())
	_, stderr, code := hey(t, "compose",
		"--to", smokeEmail,
		"--subject", subject,
		"-m", "This disposable thread verifies restoring from Trash.",
		"--json",
	)
	if code != 0 {
		skipf(t, "could not create a disposable thread (exit %d): %s", code, stderr)
	}
	if _, err := waitForPostingIDBySubject(t, subject); err != nil {
		skipf(t, "disposable thread %q did not appear in the Imbox: %v", subject, err)
	}

	type boxResponse struct {
		Postings []disposableRestorePosting `json:"postings"`
	}
	box := dataAs[boxResponse](t, heyJSON(t, "box", "view", "imbox", "--all"))
	for _, posting := range box.Postings {
		if posting.Name == subject {
			if posting.TopicID <= 0 {
				t.Fatalf("disposable thread has no topic_id: %+v", posting)
			}
			return posting
		}
	}
	skipf(t, "disposable thread %q disappeared from the Imbox", subject)
	return disposableRestorePosting{}
}

func waitForTrashedTopicID(t *testing.T, subject string) int64 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		type searchResult struct {
			TopicID int64  `json:"topic_id"`
			Subject string `json:"subject"`
		}
		results := dataAs[[]searchResult](t, heyJSON(t, "search", "--subject", subject, "--in", "trash", "--all"))
		for _, result := range results {
			if result.Subject == subject && result.TopicID > 0 {
				return result.TopicID
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	skipf(t, "trashed thread %q did not appear in search", subject)
	return 0
}

func cleanupDisposableRestoreTopic(t *testing.T, posting disposableRestorePosting) {
	t.Helper()
	_, _, _ = hey(t, "restore", strconv.FormatInt(posting.TopicID, 10), "--json")

	postingID, err := waitForPostingIDBySubject(t, posting.Name)
	if err != nil || postingID == 0 {
		t.Logf("could not locate disposable thread %q while cleaning up: %v", posting.Name, err)
		return
	}
	if _, stderr, code := hey(t, "trash", strconv.FormatInt(postingID, 10), "--json"); code != 0 {
		t.Logf("could not move disposable thread %q to Trash: %s", posting.Name, stderr)
	}
}

func TestRestore(t *testing.T) {
	first := createDisposableRestoreTopic(t, "restore test")
	second := createDisposableRestoreTopic(t, "bulk restore test")
	t.Cleanup(func() { cleanupDisposableRestoreTopic(t, first) })
	t.Cleanup(func() { cleanupDisposableRestoreTopic(t, second) })

	heyOK(t, "trash", strconv.FormatInt(first.ID, 10), strconv.FormatInt(second.ID, 10), "--json")
	firstTopicID := waitForTrashedTopicID(t, first.Name)
	secondTopicID := waitForTrashedTopicID(t, second.Name)

	stdout := heyOK(t, "restore", strconv.FormatInt(firstTopicID, 10), strconv.FormatInt(secondTopicID, 10), "--json")
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("failed to parse restore response: %v", err)
	}
	if response.Summary != "2 threads restored from Trash" {
		t.Errorf("summary = %q, want two restored threads", response.Summary)
	}

	for _, posting := range []disposableRestorePosting{first, second} {
		if _, err := waitForPostingIDBySubject(t, posting.Name); err != nil {
			t.Errorf("restored thread %q did not return to the Imbox: %v", posting.Name, err)
		}
	}
}

func TestRestoreValidation(t *testing.T) {
	heyFail(t, "restore", "--json")
	heyFail(t, "restore", "not-a-thread", "--json")
}
