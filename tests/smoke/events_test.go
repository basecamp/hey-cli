package smoke_test

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	ParentID     int64  `json:"parent_id"`
	EditURL      string `json:"edit_url"`
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
		"--notes", "Bring the latest roadmap", "--countdown", "1", "--countdown-unit", "weeks", "--json")
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
		cleanupIDs = append(cleanupIDs, smokeEventSeriesIDsOnDay(t, futureDay, title)...)
		for _, id := range uniqueStrings(cleanupIDs) {
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
	if current.ID == 0 || current.ID == series.ID || current.RecordingID != current.ID {
		t.Errorf("current occurrence ids = %#v, want the realized id preserved as id and recording_id", current)
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

	// The current-only edit must leave the series countdown inherited. Changing the series'
	// countdown afterward should therefore change what the realized occurrence inherits.
	if _, stderr, code = hey(t, "event", "edit", seriesID, firstDay,
		"--countdown", "2", "--countdown-unit", "days", "--json"); code != 0 {
		skipf(t, "series countdown edit failed (exit %d): %s", code, stderr)
	}
	editURL := current.EditURL
	if strings.HasPrefix(editURL, "/") {
		editURL = baseURL + editURL
	}
	if editURL == "" {
		t.Fatal("realized occurrence carries no edit_url")
	}
	form := fetchHTML(t, editURL)
	if got := selectedOptionValue(form, "countdown_interval_duration_value"); got != "2" {
		t.Errorf("realized occurrence countdown value = %q, want the changed inherited value 2", got)
	}
	if got := selectedOptionValue(form, "countdown_interval_duration_unit"); got != "86400" {
		t.Errorf("realized occurrence countdown unit = %q, want inherited days", got)
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

func TestEventOccurrenceFutureSplitRejectsSameDayOverlap(t *testing.T) {
	uid := uniqueID()
	title := fmt.Sprintf("Overnight support rotation %s", uid)
	first := time.Now().AddDate(2, 1, 0)
	firstDay := first.Format("2006-01-02")
	firstEndDay := first.AddDate(0, 0, 1).Format("2006-01-02")
	selectedDay := firstEndDay

	stdout, stderr, code := hey(t, "event", "add", title,
		"--starts-on", firstDay, "--ends-on", firstEndDay,
		"--start-time", "23:00", "--end-time", "01:00", "--time-zone", "UTC",
		"--repeat", "every_day", "--repeat-times", "3", "--json")
	if code != 0 {
		skipf(t, "overnight repeating event add failed (exit %d): %s", code, stderr)
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
	t.Cleanup(func() { _, _, _ = hey(t, "event", "delete", seriesID) })

	occurrence := fmt.Sprintf("%d_%s", series.ID, selectedDay)
	_, stderr, code = hey(t, "event", "edit", seriesID,
		"--occurrence", occurrence, "--apply-to", "future",
		"--starts-on", selectedDay, "--ends-on", selectedDay,
		"--start-time", "00:00", "--end-time", "01:00", "--time-zone", "UTC",
		"--repeat", "every_day", "--repeat-times", "2", "--json")
	if code == 0 {
		t.Fatal("same-day backward split succeeded; want an overlap refusal")
	}
	assertContains(t, stderr, "before the selected occurrence")

	events := dataAs[[]smokeEvent](t, heyJSON(t, "event", "day", selectedDay))
	if event, ok := findSmokeOccurrence(events, occurrence); !ok || event.Title != title {
		t.Errorf("selected occurrence after refused split = %#v, want the original", event)
	}
}

func TestEventOccurrenceFutureSplitRejectsMovedEarlierDay(t *testing.T) {
	uid := uniqueID()
	title := fmt.Sprintf("Editorial check-in %s", uid)
	first := time.Now().AddDate(2, 2, 0)
	firstDay := first.Format("2006-01-02")
	selectedDay := first.AddDate(0, 0, 1).Format("2006-01-02")

	stdout, stderr, code := hey(t, "event", "add", title,
		"--starts-on", firstDay, "--start-time", "09:00", "--end-time", "10:00", "--time-zone", "UTC",
		"--repeat", "every_day", "--repeat-times", "4", "--json")
	if code != 0 {
		skipf(t, "daily repeating event add failed (exit %d): %s", code, stderr)
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
	t.Cleanup(func() { _, _, _ = hey(t, "event", "delete", seriesID) })

	occurrence := fmt.Sprintf("%d_%s", series.ID, selectedDay)
	if _, stderr, code = hey(t, "event", "edit", seriesID,
		"--occurrence", occurrence, "--apply-to", "current",
		"--starts-on", firstDay, "--ends-on", firstDay,
		"--start-time", "12:00", "--end-time", "13:00", "--time-zone", "UTC", "--json"); code != 0 {
		skipf(t, "move current occurrence earlier failed (exit %d): %s", code, stderr)
	}

	_, stderr, code = hey(t, "event", "edit", seriesID,
		"--occurrence", occurrence, "--apply-to", "future",
		"--repeat", "every_day", "--repeat-times", "3", "--json")
	if code == 0 {
		t.Fatal("future split from an earlier moved day succeeded; want an overlap refusal")
	}
	assertContains(t, stderr, "before the selected occurrence")
}

func smokeEventSeriesIDsOnDay(t *testing.T, day, title string) []string {
	t.Helper()
	stdout, _, code := hey(t, "event", "day", day, "--json")
	if code != 0 {
		return nil
	}
	var response Response
	if json.Unmarshal([]byte(stdout), &response) != nil {
		return nil
	}
	var events []smokeEvent
	if json.Unmarshal(response.Data, &events) != nil {
		return nil
	}
	var ids []string
	for _, event := range events {
		if event.Title != title {
			continue
		}
		id := event.ParentID
		if id == 0 {
			id = event.ID
		}
		if id != 0 {
			ids = append(ids, fmt.Sprint(id))
		}
	}
	return ids
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func selectedOptionValue(markup, name string) string {
	selectRE := regexp.MustCompile(`(?s)<select[^>]*name="` + regexp.QuoteMeta(name) + `"[^>]*>.*?</select>`)
	selectedRE := regexp.MustCompile(`<option[^>]*selected(?:="selected")?[^>]*>`)
	valueRE := regexp.MustCompile(`value="([^"]*)"`)
	selectHTML := selectRE.FindString(markup)
	option := selectedRE.FindString(selectHTML)
	match := valueRE.FindStringSubmatch(option)
	if len(match) == 2 {
		return match[1]
	}
	return ""
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
