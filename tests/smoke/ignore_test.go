package smoke_test

import (
	"encoding/json"
	"testing"
)

type ignorePosting struct {
	ID    int  `json:"id"`
	Muted bool `json:"muted"`
}

func TestIgnoreAndStopIgnoring(t *testing.T) {
	type BoxResp struct {
		Postings []ignorePosting `json:"postings"`
	}

	box := dataAs[BoxResp](t, heyJSON(t, "box", "imbox"))
	if len(box.Postings) == 0 {
		t.Skip("no threads in Imbox to ignore")
	}
	posting := box.Postings[0]
	postingID := intStr(posting.ID)

	if posting.Muted {
		heyOK(t, "stop-ignoring", postingID, "--json")
		t.Cleanup(func() { heyOK(t, "ignore", postingID, "--json") })
	} else {
		t.Cleanup(func() { heyOK(t, "stop-ignoring", postingID, "--json") })
	}

	stdout := heyOK(t, "ignore", postingID, "--json")
	var ignoreResp Response
	if err := json.Unmarshal([]byte(stdout), &ignoreResp); err != nil {
		t.Fatalf("failed to parse ignore response: %v", err)
	}
	assertContains(t, ignoreResp.Summary, "1 thread ignored")

	afterIgnore := dataAs[BoxResp](t, heyJSON(t, "box", "imbox", "--all"))
	muted, found := findPostingMute(afterIgnore.Postings, posting.ID)
	if !found {
		t.Fatal("ignored thread disappeared from Imbox")
	}
	if !muted {
		t.Error("thread is not ignored after hey ignore")
	}

	stdout = heyOK(t, "stop-ignoring", postingID, "--json")
	var stopResp Response
	if err := json.Unmarshal([]byte(stdout), &stopResp); err != nil {
		t.Fatalf("failed to parse stop-ignoring response: %v", err)
	}
	assertContains(t, stopResp.Summary, "Stopped ignoring 1 thread")

	afterStop := dataAs[BoxResp](t, heyJSON(t, "box", "imbox", "--all"))
	muted, found = findPostingMute(afterStop.Postings, posting.ID)
	if !found {
		t.Fatal("thread disappeared from Imbox after hey stop-ignoring")
	}
	if muted {
		t.Error("thread is still ignored after hey stop-ignoring")
	}
}

func findPostingMute(postings []ignorePosting, id int) (muted, found bool) {
	for _, posting := range postings {
		if posting.ID == id {
			return posting.Muted, true
		}
	}
	return false, false
}

func TestIgnoreAndStopIgnoringValidation(t *testing.T) {
	for _, command := range []string{"ignore", "stop-ignoring"} {
		t.Run(command+" requires an ID", func(t *testing.T) {
			heyFail(t, command, "--json")
		})
		t.Run(command+" rejects invalid IDs", func(t *testing.T) {
			heyFail(t, command, "not-a-number", "--json")
		})
	}
}
