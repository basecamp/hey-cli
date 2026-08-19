package smoke_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

type disposableTopicPosting struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	AppURL string `json:"app_url"`
}

func createDisposableTopic(t *testing.T, purpose string) (disposableTopicPosting, string) {
	t.Helper()
	subject := fmt.Sprintf("Disposable %s %s", purpose, uniqueID())
	_, stderr, code := hey(t, "compose",
		"--to", "david@basecamp.com",
		"--subject", subject,
		"-m", "This disposable thread verifies a mailbox control.",
		"--json",
	)
	if code != 0 {
		t.Skipf("could not create a disposable thread (exit %d): %s", code, stderr)
	}

	type boxResponse struct {
		Postings []disposableTopicPosting `json:"postings"`
	}
	box := dataAs[boxResponse](t, heyJSON(t, "box", "imbox", "--all"))
	for _, posting := range box.Postings {
		if posting.Name == subject {
			if extractTopicID(posting.AppURL) == "" {
				t.Fatalf("disposable thread has no topic ID: %s", posting.AppURL)
			}
			return posting, subject
		}
	}
	t.Skip("disposable thread did not appear in Imbox")
	return disposableTopicPosting{}, ""
}

func cleanupDisposableTopic(t *testing.T, topicID, subject string) {
	t.Helper()
	_, _, _ = hey(t, "restore", topicID, "--json")

	type boxResponse struct {
		Postings []disposableTopicPosting `json:"postings"`
	}
	resp, _, code := hey(t, "box", "imbox", "--all", "--json")
	if code != 0 {
		t.Logf("could not inspect Imbox while cleaning up %q", subject)
		return
	}
	var envelope Response
	if err := json.Unmarshal([]byte(resp), &envelope); err != nil {
		t.Logf("could not decode Imbox while cleaning up %q: %v", subject, err)
		return
	}
	box := dataAs[boxResponse](t, envelope)
	for _, posting := range box.Postings {
		if posting.Name == subject {
			_, stderr, trashCode := hey(t, "trash", intStr(posting.ID), "--json")
			if trashCode != 0 {
				t.Logf("could not move disposable thread %q to Trash: %s", subject, stderr)
			}
			return
		}
	}
}

func TestRestore(t *testing.T) {
	posting, subject := createDisposableTopic(t, "restore test")
	topicID := extractTopicID(posting.AppURL)
	t.Cleanup(func() { cleanupDisposableTopic(t, topicID, subject) })

	heyOK(t, "trash", intStr(posting.ID), "--json")
	stdout := heyOK(t, "restore", topicID, "--json")
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse restore response: %v", err)
	}
	assertContains(t, resp.Summary, "restored to active mail")

	type boxResponse struct {
		Postings []disposableTopicPosting `json:"postings"`
	}
	box := dataAs[boxResponse](t, heyJSON(t, "box", "imbox", "--all"))
	for _, candidate := range box.Postings {
		if candidate.Name == subject {
			return
		}
	}
	t.Errorf("restored topic %s did not return to Imbox", topicID)
}

func TestRestoreValidation(t *testing.T) {
	heyFail(t, "restore", "--json")
	heyFail(t, "restore", "not-a-topic", "--json")
}
