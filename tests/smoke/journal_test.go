package smoke_test

import (
	"testing"
)

func TestJournalWriteAndRead(t *testing.T) {
	uid := uniqueID()
	content := "Journal entry from smoke test: " + uid

	// Use a far-future date to avoid collisions with real entries.
	date := "2099-06-15"

	// Write
	_, stderr, code := hey(t, "journal", "write", date, "-c", content, "--json")
	if code != 0 {
		t.Fatalf("journal write failed (exit %d): %s", code, stderr)
	}

	// Read back
	resp := heyJSON(t, "journal", "read", date)
	if resp.Data == nil || string(resp.Data) == "null" {
		t.Fatal("expected journal entry data, got null")
	}

	raw := string(resp.Data)
	assertContains(t, raw, uid)

	// Cross-verify: the entry should appear on the journal edit page.
	html := fetchHTML(t, baseURL+"/calendar/days/"+date+"/journal_entry/edit")
	assertContains(t, html, uid)
}

func TestJournalList(t *testing.T) {
	resp := heyJSON(t, "journal", "list")

	type JournalEntry struct {
		Date string `json:"date"`
	}
	entries := dataAs[[]JournalEntry](t, resp)

	// Cross-verify: if entries were returned, pick one and verify it exists.
	if len(entries) > 0 && entries[0].Date != "" {
		html := fetchHTML(t, baseURL+"/calendar/days/"+entries[0].Date+"/journal_entry/edit")
		if len(html) == 0 {
			t.Errorf("journal entry page for %s returned empty HTML", entries[0].Date)
		}
	}
}

func TestJournalReadToday(t *testing.T) {
	// Reading today's journal should succeed even if empty.
	_, _, code := hey(t, "journal", "read", "--json")
	if code != 0 {
		t.Error("expected journal read (today) to succeed")
	}
}

func TestJournalListLimit(t *testing.T) {
	resp := heyJSON(t, "journal", "list", "--limit", "2")
	type JournalEntry struct {
		Date string `json:"date"`
	}
	entries := dataAs[[]JournalEntry](t, resp)
	if len(entries) > 2 {
		t.Errorf("expected at most 2 entries with --limit 2, got %d", len(entries))
	}
}

func TestJournalListAll(t *testing.T) {
	resp := heyJSON(t, "journal", "list", "--all")
	_ = resp
}

func TestJournalWriteAndReadWithContent(t *testing.T) {
	uid := uniqueID()
	content := "Overwrite test " + uid
	date := "2099-07-04"

	// First write
	_, stderr, code := hey(t, "journal", "write", date, "-c", "original content "+uid, "--json")
	if code != 0 {
		t.Fatalf("journal write failed (exit %d): %s", code, stderr)
	}

	// Overwrite
	_, stderr, code = hey(t, "journal", "write", date, "-c", content, "--json")
	if code != 0 {
		t.Fatalf("journal write (overwrite) failed (exit %d): %s", code, stderr)
	}

	// Read and verify overwrite took effect
	resp := heyJSON(t, "journal", "read", date)
	raw := string(resp.Data)
	assertContains(t, raw, "Overwrite test")

	// Cross-verify: the browser should show the overwritten content, not the original.
	html := fetchHTML(t, baseURL+"/calendar/days/"+date+"/journal_entry/edit")
	assertContains(t, html, "Overwrite test")
	assertNotContains(t, html, "original content")
}
