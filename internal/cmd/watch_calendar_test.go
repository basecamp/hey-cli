package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/auth"
)

func TestWatchingCalendars(t *testing.T) {
	command := newWatchCommand()
	changes, err := command.watchedChanges()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !command.watchingCalendars(changes) {
		t.Error("the calendars should be watched by default")
	}

	command.boxes = []string{"imbox"}
	if command.watchingCalendars(changes) {
		t.Error("--box scopes the watch to mail, so the calendars should be off")
	}

	command = newWatchCommand()
	command.events = []string{"added", "new"}
	changes, err = command.watchedChanges()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if command.watchingCalendars(changes) {
		t.Error("an --events list naming only mail changes should leave the calendars off")
	}

	command.events = []string{"recording_added"}
	changes, err = command.watchedChanges()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !command.watchingCalendars(changes) {
		t.Error("asking for a calendar change should watch the calendars")
	}
}

func TestCalendarCursor(t *testing.T) {
	changesURL := "https://app.hey.com/calendars/512/recording/changes.json?since=2026-08-18T09%3A00%3A00.000Z&v=1"
	started := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)

	command := newWatchCommand()
	cursor, err := command.calendarCursor(changesURL, started)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cursor.Since != "2026-08-18T09:00:00.000Z" || cursor.Version != "1" {
		t.Errorf("cursor = %+v, want the server's own since and version", cursor)
	}

	command.since = "2026-08-17T08:30:00Z"
	cursor, err = command.calendarCursor(changesURL, started)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cursor.Since != "2026-08-17T08:30:00.000Z" {
		t.Errorf("since = %q, want --since to move it back", cursor.Since)
	}
	if cursor.Version != "1" {
		t.Errorf("version = %q, want it to survive --since", cursor.Version)
	}

	command.since = "last tuesday"
	if _, err := command.calendarCursor(changesURL, started); err == nil {
		t.Error("expected an error for a --since we can't read")
	}
}

func TestCalendarCursorNoLaterThan(t *testing.T) {
	started := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)

	late := hey.CalendarChangesCursor{Since: "2026-08-18T09:30:00.000Z", Version: "1"}
	if capped := calendarCursorNoLaterThan(late, started); capped.Since != "2026-08-18T09:00:00.000Z" {
		t.Errorf("since = %q, want a cursor later than the start moved back to it", capped.Since)
	}

	early := hey.CalendarChangesCursor{Since: "2026-08-18T08:00:00.000Z"}
	if kept := calendarCursorNoLaterThan(early, started); kept.Since != "2026-08-18T08:00:00.000Z" {
		t.Errorf("since = %q, want a cursor before the start left alone", kept.Since)
	}

	unreadable := hey.CalendarChangesCursor{Since: "whenever"}
	if kept := calendarCursorNoLaterThan(unreadable, started); kept.Since != "whenever" {
		t.Errorf("since = %q, want a cursor we can't read left as it is", kept.Since)
	}
}

func newTestCalendarsWatch(t *testing.T, calendars ...*watchedCalendar) *calendarsWatch {
	t.Helper()
	watch := &calendarsWatch{
		calendars: map[int64]*watchedCalendar{},
		unread:    map[int64]bool{},
		pending:   map[int64]bool{},
		wake:      make(chan struct{}, 1),
		poll:      time.NewTicker(time.Hour),
	}
	t.Cleanup(watch.poll.Stop)
	for _, calendar := range calendars {
		watch.calendars[calendar.id] = calendar
	}
	return watch
}

func recordingChangesCursor(t *testing.T, serverURL string, calendarID string) hey.CalendarChangesCursor {
	t.Helper()
	cursor, err := hey.CalendarChangesCursorFrom(serverURL + "/calendars/" + calendarID + "/recording/changes.json?since=2026-08-18T09%3A00%3A00.000Z&v=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return cursor
}

func TestWatchReadsRungCalendars(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")

	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-18T09%3A14%3A22.031Z&v=1>; rel="next"`)
		_, _ = w.Write([]byte(`{
			"added": {"Calendar::Event": [{"id": 88001, "title": "Dentist appointment", "created_at": "2026-08-18T09:10:00.000Z"}]},
			"updated": {"Calendar::JournalEntry": [{"id": 88002, "updated_at": "2026-08-18T09:12:00.000Z"}]},
			"deleted": {"Calendar::Todo": [{"id": 88003, "deleted_at": "2026-08-18T09:14:00.000Z", "type": "Calendar::Todo"}]}
		}`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("recording_added", "recording_updated", "recording_deleted")
	watch.calendar = newTestCalendarsWatch(t, &watchedCalendar{id: 512, name: "Household", cursor: recordingChangesCursor(t, server.URL, "512")})

	watch.calendar.ring(512)
	<-watch.calendar.wake
	if err := watch.readRungCalendars(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(requested) != 1 || !strings.Contains(requested[0], "/calendars/512/recording/changes.json") {
		t.Fatalf("requests = %v, want one read of the calendar's own feed", requested)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want one per change: %q", len(lines), out.String())
	}

	var added watchEvent
	if err := json.Unmarshal([]byte(lines[0]), &added); err != nil {
		t.Fatalf("first line isn't JSON: %v", err)
	}
	if added.Change != "recording_added" || added.RecordingID != 88001 || added.RecordingType != "Calendar::Event" {
		t.Errorf("added = %+v, want event 88001", added)
	}
	if added.Calendar == nil || added.Calendar.ID != 512 || added.Calendar.Name != "Household" {
		t.Errorf("calendar = %+v, want the calendar the change is on", added.Calendar)
	}
	if added.Box != nil || added.New != nil {
		t.Errorf("event = %+v, want no box and no new on a calendar line", added)
	}
	if added.Recording == nil || added.Recording.Title != "Dentist appointment" {
		t.Error("an added recording should carry the recording itself")
	}

	var deleted watchEvent
	if err := json.Unmarshal([]byte(lines[2]), &deleted); err != nil {
		t.Fatalf("third line isn't JSON: %v", err)
	}
	if deleted.Change != "recording_deleted" || deleted.RecordingID != 88003 || deleted.RecordingType != "Calendar::Todo" {
		t.Errorf("deleted = %+v, want todo 88003 with its type", deleted)
	}
	if deleted.Recording != nil {
		t.Error("a deleted recording is gone, so there's nothing to carry")
	}

	if watch.calendar.calendars[512].cursor.Since != "2026-08-18T09:14:22.031Z" {
		t.Errorf("cursor = %+v, want it moved to where the feed left off", watch.calendar.calendars[512].cursor)
	}
}

func TestWatchCalendarSkipsAheadOnAFullSync(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/calendars.json" {
			_, _ = w.Write([]byte(`{
				"calendars": [{"calendar": {"id": 512, "name": "Household"},
				               "recording_changes_url": "/calendars/512/recording/changes.json?since=2026-08-18T11%3A00%3A00.000Z&v=1",
				               "signed_stream_name": "signed-household"}],
				"calendar_changes_url": "/calendar/changes.json?since=2026-08-18T11%3A00%3A00.000Z"
			}`))
			return
		}
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("recording_added", "calendar_resync")
	watch.calendar = newTestCalendarsWatch(t, &watchedCalendar{id: 512, name: "Household", cursor: recordingChangesCursor(t, server.URL, "512")})

	if err := watch.readCalendar(context.Background(), watch.calendar.calendars[512]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var event watchEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &event); err != nil {
		t.Fatalf("output isn't JSON: %q", out.String())
	}
	if event.Change != watchCalendarResync || event.Calendar == nil || event.Calendar.ID != 512 {
		t.Errorf("event = %+v, want a calendar_resync naming the calendar", event)
	}
	if watch.calendar.calendars[512].cursor.Since != "2026-08-18T11:00:00.000Z" {
		t.Errorf("cursor = %+v, want the server's own fresh cursor", watch.calendar.calendars[512].cursor)
	}
}

func TestWatchCalendarStopsWatchingAGoneCalendar(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/calendars.json" {
			_, _ = w.Write([]byte(`{"calendars": [], "calendar_changes_url": "/calendar/changes.json?since=2026-08-18T11%3A00%3A00.000Z"}`))
			return
		}
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("recording_added", "calendar_resync")
	watch.calendar = newTestCalendarsWatch(t, &watchedCalendar{id: 512, name: "Household", cursor: recordingChangesCursor(t, server.URL, "512")})

	if err := watch.readCalendar(context.Background(), watch.calendar.calendars[512]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q, want no resync for a calendar that is gone", out.String())
	}
	if _, watching := watch.calendar.calendars[512]; watching {
		t.Error("a calendar the server no longer lists should stop being watched")
	}
}

func TestWatchPollReportsCalendarChanges(t *testing.T) {
	t.Setenv("HEY_TOKEN", "test-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/calendars/") {
			w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-18T09%3A20%3A00.000Z&v=1>; rel="next"`)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("Link", `<`+r.URL.Path+`?since=2026-08-18T09%3A20%3A00.000Z>; rel="next"`)
		_, _ = w.Write([]byte(`{
			"added": [{"calendar": {"id": 514, "name": "Book Club", "updated_at": "2026-08-18T09:14:00.000Z"},
			           "recording_changes_url": "/calendars/514/recording/changes.json?since=2026-08-18T09%3A14%3A00.000Z&v=1",
			           "signed_stream_name": "signed-bookclub"}],
			"updated": [{"id": 512, "name": "Home", "updated_at": "2026-08-18T09:15:00.000Z"}],
			"deleted": [{"id": 513, "deleted_at": "2026-08-18T09:16:00.000Z"}]
		}`))
	}))
	defer server.Close()
	initSDK(auth.NewManager(server.URL, server.Client(), t.TempDir()), server.URL)

	watch, out := newTestWatch("calendar_added", "calendar_updated", "calendar_deleted")
	listCursor, err := hey.CalendarChangesCursorFrom(server.URL + "/calendar/changes.json?since=2026-08-18T09%3A00%3A00.000Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	watch.calendar = newTestCalendarsWatch(t,
		&watchedCalendar{id: 512, name: "Household"},
		&watchedCalendar{id: 513, name: "Old Projects"})
	watch.calendar.listCursor = listCursor

	if err := watch.pollCalendarList(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want added, updated and deleted: %q", len(lines), out.String())
	}

	var added watchEvent
	if err := json.Unmarshal([]byte(lines[0]), &added); err != nil {
		t.Fatalf("first line isn't JSON: %v", err)
	}
	if added.Change != watchCalendarAdded || added.Calendar.ID != 514 || added.Calendar.Name != "Book Club" {
		t.Errorf("added = %+v, want the new calendar", added)
	}
	if _, watching := watch.calendar.calendars[514]; !watching {
		t.Error("a calendar that arrived should be watched from here on")
	}

	var updated watchEvent
	if err := json.Unmarshal([]byte(lines[1]), &updated); err != nil {
		t.Fatalf("second line isn't JSON: %v", err)
	}
	if updated.Change != watchCalendarUpdated || updated.Calendar.Name != "Home" {
		t.Errorf("updated = %+v, want the calendar's new name", updated)
	}
	if watch.calendar.calendars[512].name != "Home" {
		t.Errorf("name = %q, want the watch to carry the rename", watch.calendar.calendars[512].name)
	}

	var deleted watchEvent
	if err := json.Unmarshal([]byte(lines[2]), &deleted); err != nil {
		t.Fatalf("third line isn't JSON: %v", err)
	}
	if deleted.Change != watchCalendarDeleted || deleted.Calendar.ID != 513 || deleted.Calendar.Name != "Old Projects" {
		t.Errorf("deleted = %+v, want the calendar under the name the watch knew it by", deleted)
	}
	if _, watching := watch.calendar.calendars[513]; watching {
		t.Error("a calendar that left should stop being watched")
	}

	if watch.calendar.listCursor.Since != "2026-08-18T09:20:00.000Z" {
		t.Errorf("cursor = %+v, want it moved to where the feed left off", watch.calendar.listCursor)
	}
}

func TestWatchReadyWaitsForACalendarBehind(t *testing.T) {
	watch, out := newTestWatch("recording_added")
	watch.calendar = newTestCalendarsWatch(t, &watchedCalendar{id: 512, name: "Household"})
	watch.calendar.unread[512] = true
	watch.catchingUp = true

	watch.readyOnceCaughtUp(context.Background())
	if out.Len() != 0 {
		t.Errorf("wrote %q, want no ready while a calendar is behind", out.String())
	}

	delete(watch.calendar.unread, 512)
	watch.readyOnceCaughtUp(context.Background())
	if !strings.Contains(out.String(), `"change":"ready"`) {
		t.Errorf("wrote %q, want the ready once the calendar is read", out.String())
	}
}

func TestCalendarEventEnvironment(t *testing.T) {
	event := watchEvent{
		Change:        "recording_added",
		At:            "2026-08-18T09:14:22.031Z",
		Calendar:      &watchEventCalendar{ID: 512, Name: "Household"},
		RecordingID:   88001,
		RecordingType: "Calendar::Event",
	}

	environment := strings.Join(event.environment(), "\n")
	for _, want := range []string{"HEY_CHANGE=recording_added", "HEY_CALENDAR_ID=512", "HEY_CALENDAR_NAME=Household", "HEY_RECORDING_ID=88001", "HEY_RECORDING_TYPE=Calendar::Event"} {
		if !strings.Contains(environment, want) {
			t.Errorf("environment = %q, want %s", environment, want)
		}
	}
	if strings.Contains(environment, "HEY_BOX_ID") {
		t.Errorf("environment = %q, want no box on a calendar event", environment)
	}
}

func TestCalendarWatchLine(t *testing.T) {
	line := watchLine(watchEvent{
		Change:        "recording_added",
		At:            "2026-08-18T09:14:22.031Z",
		Calendar:      &watchEventCalendar{ID: 512, Name: "House\x1b[31mhold"},
		RecordingID:   88001,
		RecordingType: "Calendar::Event",
		Recording:     &generated.Recording{Title: "Dentist \x1b[31mappointment"},
	})

	if !strings.Contains(line, "Household") || strings.Contains(line, "\x1b[31m") {
		t.Errorf("line = %q, want the calendar named and every escape stripped", line)
	}
	if !strings.Contains(line, "Dentist appointment") {
		t.Errorf("line = %q, want the recording's title", line)
	}
}
