package smoke_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type smokeEvent struct {
	ID           int64  `json:"id"`
	RecordingID  int64  `json:"recording_id"`
	Title        string `json:"title"`
	StartsAt     string `json:"starts_at"`
	AllDay       bool   `json:"all_day"`
	Notes        string `json:"description"`
	Location     string `json:"location"`
	OccurrenceID string `json:"occurrence_id"`
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
	t.Cleanup(func() { _, _, _ = hey(t, "event", "delete", id) })

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

	stdout, stderr, code = hey(t, "event", "delete", id, "--json")
	if code != 0 {
		skipf(t, "event delete failed (exit %d): %s", code, stderr)
	}
	var deleted Response
	if err := json.Unmarshal([]byte(stdout), &deleted); err != nil {
		t.Fatalf("failed to parse delete response: %v", err)
	}
	assertContains(t, deleted.Summary, "deleted")
}

func TestEventOccurrenceEditScopes(t *testing.T) {
	uid := uniqueID()
	title := fmt.Sprintf("Weekly design review %s", uid)
	first := time.Now().AddDate(2, 0, 0)
	firstDay := first.Format("2006-01-02")
	currentDay := first.AddDate(0, 0, 7).Format("2006-01-02")
	futureDay := first.AddDate(0, 0, 14).Format("2006-01-02")
	beyondLastDay := first.AddDate(0, 0, 35).Format("2006-01-02")

	stdout, stderr, code := hey(t, "event", "add", title,
		"--starts-on", firstDay, "--all-day", "--repeat", "every_week", "--repeat-times", "5",
		"--notes", "Bring the latest roadmap", "--json")
	if code != 0 {
		skipf(t, "repeating event add failed (exit %d): %s", code, stderr)
	}
	var added Response
	if err := json.Unmarshal([]byte(stdout), &added); err != nil {
		t.Fatalf("failed to parse repeating event response: %v", err)
	}
	series := dataAs[smokeEvent](t, added)
	if series.ID == 0 {
		t.Fatal("repeating event response carries no event ID")
	}
	seriesID := fmt.Sprint(series.ID)
	cleanupIDs := []string{seriesID}
	t.Cleanup(func() {
		for _, id := range cleanupIDs {
			_, _, _ = hey(t, "event", "delete", id)
		}
	})

	currentOccurrence := fmt.Sprintf("%d_%s", series.ID, currentDay)
	currentTitle := title + " (vendor review)"
	if _, stderr, code = hey(t, "event", "edit", seriesID,
		"--occurrence", currentOccurrence, "--apply-to", "current",
		"--title", currentTitle, "--allow-plain-notes", "--json"); code != 0 {
		skipf(t, "current occurrence edit failed (exit %d): %s", code, stderr)
	}
	currentEvents := dataAs[[]smokeEvent](t, heyJSON(t, "event", "day", currentDay))
	current, ok := findSmokeOccurrence(currentEvents, currentOccurrence)
	if !ok || current.Title != currentTitle {
		t.Errorf("current occurrence = %#v, want title %q", current, currentTitle)
	}
	if current.ID != series.ID || current.RecordingID == 0 || current.RecordingID == series.ID {
		t.Errorf("current occurrence ids = %#v, want the series id and a distinct recording_id", current)
	}
	styled, stderr, code := hey(t, "event", "day", currentDay, "--styled")
	if code != 0 {
		t.Fatalf("styled occurrence read failed (exit %d): %s", code, stderr)
	}
	for _, want := range []string{currentOccurrence, fmt.Sprint(current.RecordingID)} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled occurrence does not contain %q:\n%s", want, styled)
		}
	}

	futureOccurrence := fmt.Sprintf("%d_%s", series.ID, futureDay)
	heyFail(t, "event", "edit", seriesID,
		"--occurrence", futureOccurrence, "--apply-to", "future",
		"--location", "Studio C", "--allow-plain-notes", "--json")
	unchangedFutureEvents := dataAs[[]smokeEvent](t, heyJSON(t, "event", "day", futureDay))
	unchangedFuture, ok := findSmokeOccurrence(unchangedFutureEvents, futureOccurrence)
	if !ok || unchangedFuture.Location == "Studio C" {
		t.Errorf("future occurrence changed without an explicit repeat schedule: %#v", unchangedFuture)
	}

	heyFail(t, "event", "edit", seriesID,
		"--occurrence", futureOccurrence, "--apply-to", "future",
		"--starts-on", firstDay, "--ends-on", firstDay,
		"--repeat", "every_week", "--allow-plain-notes", "--json")
	earlierEvents := dataAs[[]smokeEvent](t, heyJSON(t, "event", "day", firstDay))
	matchingEarlier := 0
	for _, event := range earlierEvents {
		if event.Title == title {
			matchingEarlier++
		}
	}
	if matchingEarlier != 1 {
		t.Errorf("earlier occurrences after refused backward split = %d, want one: %#v", matchingEarlier, earlierEvents)
	}

	if _, stderr, code = hey(t, "event", "edit", seriesID,
		"--occurrence", futureOccurrence, "--apply-to", "future",
		"--repeat", "every_week", "--repeat-times", "3",
		"--location", "Studio C", "--allow-plain-notes", "--json"); code != 0 {
		skipf(t, "future occurrence edit failed (exit %d): %s", code, stderr)
	}
	futureEvents := dataAs[[]smokeEvent](t, heyJSON(t, "event", "day", futureDay))
	future, ok := findSmokeEventByTitle(futureEvents, title)
	if !ok {
		t.Fatalf("future occurrence not found among %#v", futureEvents)
	}
	if future.Location != "Studio C" {
		t.Errorf("future occurrence location = %q, want Studio C", future.Location)
	}
	if future.ID == 0 || future.ID == series.ID {
		t.Errorf("future series id = %d, want a new nonzero id", future.ID)
	} else {
		cleanupIDs = append(cleanupIDs, fmt.Sprint(future.ID))
	}

	beyondEvents := dataAs[[]smokeEvent](t, heyJSON(t, "event", "day", beyondLastDay))
	if beyond, ok := findSmokeEventByTitle(beyondEvents, title); ok {
		t.Errorf("finite future series grew past its three remaining occurrences: %#v", beyond)
	}
}

func findSmokeOccurrence(events []smokeEvent, occurrenceID string) (smokeEvent, bool) {
	for _, event := range events {
		if event.OccurrenceID == occurrenceID {
			return event, true
		}
	}
	return smokeEvent{}, false
}

func findSmokeEventByTitle(events []smokeEvent, title string) (smokeEvent, bool) {
	for _, event := range events {
		if event.Title == title {
			return event, true
		}
	}
	return smokeEvent{}, false
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
