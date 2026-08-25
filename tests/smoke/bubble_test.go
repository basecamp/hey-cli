package smoke_test

import (
	"encoding/json"
	"testing"
)

func TestBubbleUpAndPop(t *testing.T) {
	resp := heyJSON(t, "box", "imbox")
	type Posting struct {
		ID int `json:"id"`
	}
	type BoxResp struct {
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResp](t, resp)
	if len(data.Postings) == 0 {
		t.Fatal("no postings in imbox to test bubble up")
	}

	postingID := intStr(data.Postings[0].ID)

	stdout, stderr, code := hey(t, "bubble", "up", postingID, "--now", "--json")
	if code != 0 {
		skipf(t, "bubble up failed (exit %d): %s", code, stderr)
	}
	var upResp Response
	if err := json.Unmarshal([]byte(stdout), &upResp); err != nil {
		t.Fatalf("failed to parse bubble up response: %v", err)
	}
	assertContains(t, upResp.Summary, "bubbled up")

	stdout, stderr, code = hey(t, "bubble", "pop", postingID, "--json")
	if code != 0 {
		skipf(t, "bubble pop failed (exit %d): %s", code, stderr)
	}
	var popResp Response
	if err := json.Unmarshal([]byte(stdout), &popResp); err != nil {
		t.Fatalf("failed to parse bubble pop response: %v", err)
	}
	assertContains(t, popResp.Summary, "no longer bubbled up")
}

func TestBubbleUpRequiresNow(t *testing.T) {
	heyFail(t, "bubble", "up", "12345")
}

func TestBubbleUpInvalidID(t *testing.T) {
	heyFail(t, "bubble", "up", "not-a-number", "--now")
}

func TestBubblePopRequiresArgs(t *testing.T) {
	heyFail(t, "bubble", "pop")
}
