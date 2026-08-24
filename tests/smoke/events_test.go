package smoke_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

type smokeEvent struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
	AllDay   bool   `json:"all_day"`
	Notes    string `json:"notes"`
	Location string `json:"location"`
}

func TestEventList(t *testing.T) {
	events := dataAs[[]smokeEvent](t, heyJSON(t, "event", "list"))
	for _, event := range events {
		if event.ID == 0 || event.Title == "" {
			t.Errorf("event is missing an id or a title: %#v", event)
		}
	}
}

func TestEventListWindowsAndFormats(t *testing.T) {
	heyJSON(t, "event", "list", "--starts-on", "2026-01-01", "--ends-on", "2026-12-31")
	heyJSON(t, "event", "list", "--limit", "5")
	heyJSON(t, "event", "list", "--all")

	calendars := dataAs[[]struct {
		ID int `json:"id"`
	}](t, heyJSON(t, "calendar", "list"))
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}
	heyJSON(t, "event", "list", "--calendar", intStr(calendars[0].ID))

	for _, format := range []string{"--quiet", "--ids-only", "--count", "--markdown", "--styled"} {
		if _, stderr, code := hey(t, "event", "list", format); code != 0 {
			t.Errorf("event list %s failed (exit %d): %s", format, code, stderr)
		}
	}
}

func TestEventListInvalidCalendarID(t *testing.T) {
	heyFail(t, "event", "list", "--calendar", "999999999", "--json")
}

func TestEventCRUD(t *testing.T) {
	uid := uniqueID()
	title := fmt.Sprintf("Design review %s", uid)
	const day = "2099-04-16"

	stdout, stderr, code := hey(t, "event", "add", title,
		"--starts-on", day, "--start-time", "14:00", "--end-time", "15:00",
		"--location", "Studio B", "--notes", "Bring the printed mocks", "--json")
	if code != 0 {
		skipf(t, "event add failed (exit %d): %s", code, stderr)
	}
	var added Response
	if err := json.Unmarshal([]byte(stdout), &added); err != nil {
		t.Fatalf("failed to parse add response: %v", err)
	}
	assertContains(t, added.Summary, "created")

	event := dataAs[smokeEvent](t, added)
	if event.ID == 0 {
		t.Fatal("add response carries no event ID")
	}
	id := fmt.Sprint(event.ID)
	t.Cleanup(func() { _, _, _ = hey(t, "event", "delete", id, day) })

	if event.Title != title {
		t.Errorf("created title = %q, want %q", event.Title, title)
	}

	// Cross-verify: the event shows up on the calendar page.
	assertContains(t, fetchHTML(t, baseURL+"/calendar/days/"+day), title)

	// An edit names one field; everything else must survive the round trip, since
	// HEY clears whatever a write leaves out.
	renamed := title + " (revised)"
	stdout, stderr, code = hey(t, "event", "edit", id, day, "--title", renamed, "--json")
	if code != 0 {
		skipf(t, "event edit failed (exit %d): %s", code, stderr)
	}
	var edited Response
	if err := json.Unmarshal([]byte(stdout), &edited); err != nil {
		t.Fatalf("failed to parse edit response: %v", err)
	}
	updated := dataAs[smokeEvent](t, edited)
	if updated.Title != renamed {
		t.Errorf("edited title = %q, want %q", updated.Title, renamed)
	}
	if updated.Location != "Studio B" {
		t.Errorf("edit dropped the location: %q", updated.Location)
	}
	if updated.Notes == "" {
		t.Error("edit dropped the notes")
	}

	stdout, stderr, code = hey(t, "event", "delete", id, day, "--json")
	if code != 0 {
		skipf(t, "event delete failed (exit %d): %s", code, stderr)
	}
	var deleted Response
	if err := json.Unmarshal([]byte(stdout), &deleted); err != nil {
		t.Fatalf("failed to parse delete response: %v", err)
	}
	assertContains(t, deleted.Summary, "deleted")
}

func TestEventAllDay(t *testing.T) {
	uid := uniqueID()
	title := fmt.Sprintf("Offsite %s", uid)
	const day = "2099-05-20"

	stdout, stderr, code := hey(t, "event", "add", title, "--starts-on", day, "--all-day", "--json")
	if code != 0 {
		skipf(t, "event add failed (exit %d): %s", code, stderr)
	}
	var added Response
	if err := json.Unmarshal([]byte(stdout), &added); err != nil {
		t.Fatalf("failed to parse add response: %v", err)
	}
	event := dataAs[smokeEvent](t, added)
	t.Cleanup(func() { _, _, _ = hey(t, "event", "delete", fmt.Sprint(event.ID), day) })

	if !event.AllDay {
		t.Errorf("--all-day event is not all-day: %#v", event)
	}
}

func TestEventCommandsValidateInput(t *testing.T) {
	heyFail(t, "event", "add", "--json")
	heyFail(t, "event", "edit", "--json")
	heyFail(t, "event", "delete", "--json")
	heyFail(t, "event", "edit", "not-an-id", "--json")
	heyFail(t, "event", "delete", "not-an-id", "--json")
}
