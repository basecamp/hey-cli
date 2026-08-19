package smoke_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestTrash(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Disposable trash test %s", uid)
	_, stderr, code := hey(t, "compose",
		"--to", "david@basecamp.com",
		"--subject", subject,
		"-m", "This disposable thread verifies the Trash command.",
		"--json",
	)
	if code != 0 {
		t.Skipf("could not create a disposable thread (exit %d): %s", code, stderr)
	}

	boxResp := heyJSON(t, "box", "imbox")
	type Posting struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	box := dataAs[BoxResp](t, boxResp)

	var postingID int
	for _, posting := range box.Postings {
		if posting.Name == subject {
			postingID = posting.ID
			break
		}
	}
	if postingID == 0 {
		t.Skip("disposable thread did not appear in Imbox")
	}

	stdout := heyOK(t, "trash", intStr(postingID), "--json")
	var trashResp Response
	if err := json.Unmarshal([]byte(stdout), &trashResp); err != nil {
		t.Fatalf("failed to parse trash response: %v", err)
	}
	assertContains(t, trashResp.Summary, "1 thread moved to Trash")

	refreshed := dataAs[BoxResp](t, heyJSON(t, "box", "imbox"))
	for _, posting := range refreshed.Postings {
		if posting.ID == postingID {
			t.Errorf("trashed thread %d is still in Imbox", postingID)
		}
	}
}

func TestTrashAndSpamValidation(t *testing.T) {
	for _, command := range []string{"trash", "spam"} {
		t.Run(command+" requires an ID", func(t *testing.T) {
			heyFail(t, command, "--json")
		})
		t.Run(command+" rejects invalid IDs", func(t *testing.T) {
			heyFail(t, command, "not-a-number", "--json")
		})
	}
}
