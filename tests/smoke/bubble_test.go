package smoke_test

import (
	"encoding/json"
	"testing"
	"time"
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

	onDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	stdout, stderr, code = hey(t, "bubble", "up", postingID, "--on", onDate, "--json")
	if code != 0 {
		skipf(t, "bubble up --on failed (exit %d): %s", code, stderr)
	}
	var onResp Response
	if err := json.Unmarshal([]byte(stdout), &onResp); err != nil {
		t.Fatalf("failed to parse bubble up --on response: %v", err)
	}
	assertContains(t, onResp.Summary, "will bubble up on "+onDate)

	stdout, stderr, code = hey(t, "bubble", "up", postingID, "--tomorrow", "--json")
	if code != 0 {
		skipf(t, "bubble up --tomorrow failed (exit %d): %s", code, stderr)
	}
	var tomorrowResp Response
	if err := json.Unmarshal([]byte(stdout), &tomorrowResp); err != nil {
		t.Fatalf("failed to parse bubble up --tomorrow response: %v", err)
	}
	assertContains(t, tomorrowResp.Summary, "will bubble up tomorrow morning")

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

func TestBubbleUpRequiresExactlyOneSchedule(t *testing.T) {
	heyFail(t, "bubble", "up", "12345")
	heyFail(t, "bubble", "up", "12345", "--now", "--on", "2026-09-04")
	heyFail(t, "bubble", "up", "12345", "--weekend", "--next-week")
}

func TestBubbleUpInvalidID(t *testing.T) {
	heyFail(t, "bubble", "up", "not-a-number", "--now")
}

func TestBubblePopRequiresArgs(t *testing.T) {
	heyFail(t, "bubble", "pop")
}
