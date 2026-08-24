package cmd

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// eventForm reads the form-encoded body an event write sends. Calendar events take forms
// rather than JSON, because that is the shape HEY's endpoints parse.
func eventForm(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading form: %v", err)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("parsing form %q: %v", body, err)
	}
	return values
}

func TestEventsAddTimedEvent(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/calendar/events.json" {
			t.Errorf("request = %s %s, want POST /calendar/events.json", r.Method, r.URL.Path)
		}
		form := eventForm(t, r)
		if got := form.Get("calendar_event[summary]"); got != "Design review" {
			t.Errorf("summary = %q", got)
		}
		if got := form.Get("calendar_event[calendar_id]"); got != "9" {
			t.Errorf("calendar_id = %q", got)
		}
		if got := form.Get("calendar_event[starts_at]"); got != "2026-09-02" {
			t.Errorf("starts_at = %q", got)
		}
		if got := form.Get("calendar_event[ends_at]"); got != "2026-09-02" {
			t.Errorf("ends_at = %q, want the start date", got)
		}
		if got := form.Get("calendar_event[all_day]"); got != "0" {
			t.Errorf("all_day = %q, want 0", got)
		}
		if got := form.Get("calendar_event[starts_at_time]"); got != "14:00:00" {
			t.Errorf("starts_at_time = %q", got)
		}
		if got := form.Get("calendar_event[ends_at_time]"); got != "15:00:00" {
			t.Errorf("ends_at_time = %q", got)
		}
		if got := form.Get("calendar_event[starts_at_time_zone_name]"); got != "Europe/Zagreb" {
			t.Errorf("starts_at_time_zone_name = %q", got)
		}
		if got := form.Get("calendar_event[location]"); got != "Studio, 3rd floor" {
			t.Errorf("location = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":4821,"title":"Design review","type":"CalendarEvent"}`)
	}),
		"event", "add", "Design review", "--calendar", "9",
		"--starts-on", "2026-09-02", "--start-time", "14:00", "--end-time", "15:00",
		"--time-zone", "Europe/Zagreb", "--location", "Studio, 3rd floor")
	if err != nil {
		t.Fatalf("execute events add: %v", err)
	}
	if response.Summary != "Event created" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["id"] != float64(4821) {
		t.Errorf("structured data = %#v", response.Data)
	}
}

// An event nobody named a time for is an all-day event. `hey event add "Sarah's birthday"`
// is the everyday case, and answering it with a nine-o'clock appointment would be a guess.
func TestEventsAddWithoutATimeIsAllDay(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		form := eventForm(t, r)
		if got := form.Get("calendar_event[all_day]"); got != "1" {
			t.Errorf("all_day = %q, want 1", got)
		}
		if got := form.Get("calendar_event[starts_at_time]"); got != "" {
			t.Errorf("starts_at_time = %q, want none on an all-day event", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":4822,"title":"Sarah's birthday"}`)
	}), "event", "add", "Sarah's birthday", "--calendar", "9", "--starts-on", "2026-09-02")
	if err != nil {
		t.Fatalf("execute events add: %v", err)
	}
}

// A start with no end runs for an hour, which is what the web form offers too.
func TestEventsAddDefaultsTheEndTime(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := eventForm(t, r).Get("calendar_event[ends_at_time]"); got != "10:15:00" {
			t.Errorf("ends_at_time = %q, want an hour after 09:15", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":4823,"title":"Standup"}`)
	}), "event", "add", "Standup", "--calendar", "9", "--starts-on", "2026-09-02", "--start-time", "09:15")
	if err != nil {
		t.Fatalf("execute events add: %v", err)
	}
}

func TestEventsAddRepeatsAndReminds(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		form := eventForm(t, r)
		if got := form.Get("repeat_frequency"); got != "every_weekday" {
			t.Errorf("repeat_frequency = %q", got)
		}
		if got := form.Get("calendar_recurrence_schedule[recurs_until_type]"); got != "count" {
			t.Errorf("recurs_until_type = %q, want count", got)
		}
		if got := form.Get("calendar_recurrence_schedule[recurs_count]"); got != "20" {
			t.Errorf("recurs_count = %q", got)
		}
		if got := form["timed_reminder_durations[]"]; len(got) != 2 || got[0] != "86400" || got[1] != "3600" {
			t.Errorf("reminders = %v, want a day and an hour in seconds", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":4824,"title":"Standup"}`)
	}),
		"event", "add", "Standup", "--calendar", "9", "--start-time", "09:15",
		"--repeat", "every_weekday", "--repeat-times", "20", "--remind", "1d", "--remind", "1h")
	if err != nil {
		t.Fatalf("execute events add: %v", err)
	}
}

// Without --calendar a new event goes on the first calendar it can be filed on. The personal
// calendar is in the list HEY serves and answers 404 when filed on, which is the trap.
func TestEventsAddSkipsTheUnfileableCalendars(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":7,"name":"Personal","personal":true,"owned":true}},{"calendar":{"id":8,"name":"Holidays","external":true,"owned":true}},{"calendar":{"id":9,"name":"Work","owned":true}}]}`)
		case "/calendar/events.json":
			if got := eventForm(t, r).Get("calendar_event[calendar_id]"); got != "9" {
				t.Errorf("calendar_id = %q, want the first fileable calendar", got)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":4825,"title":"Design review"}`)
		default:
			http.NotFound(w, r)
		}
	}), "event", "add", "Design review", "--starts-on", "2026-09-02")
	if err != nil {
		t.Fatalf("execute events add: %v", err)
	}
}

// The one that matters: HEY clears the notes, location, link, attached email, reminders and
// time zones on any write that leaves them out, so renaming an event has to send all of them
// back or it wipes the lot.
func TestEventsEditKeepsWhatItWasNotAskedToChange(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true}}]}`)
		case r.URL.Path == "/calendars/9/recordings.json":
			_, _ = io.WriteString(w, `{"Calendar::Event":[{"id":4821,"title":"Design review","starts_at":"2026-09-02T12:00:00Z","ends_at":"2026-09-02T13:00:00Z","starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb","description":"Bring the roadmap","location":"Studio, 3rd floor","url":"https://meet.example.com/design","attached_entry":{"id":551},"reminders":[{"duration":600}]}]}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/calendar/events/4821.json":
			form := eventForm(t, r)
			if got := form.Get("calendar_event[summary]"); got != "Design review (moved)" {
				t.Errorf("summary = %q", got)
			}
			if got := form.Get("calendar_event[description]"); got != "Bring the roadmap" {
				t.Errorf("description = %q, want the notes sent back", got)
			}
			if got := form.Get("calendar_event[location]"); got != "Studio, 3rd floor" {
				t.Errorf("location = %q, want the location sent back", got)
			}
			if got := form.Get("calendar_event[url]"); got != "https://meet.example.com/design" {
				t.Errorf("url = %q, want the link sent back", got)
			}
			if got := form.Get("calendar_event[entry_id]"); got != "551" {
				t.Errorf("entry_id = %q, want the attached email sent back", got)
			}
			if got := form["timed_reminder_durations[]"]; len(got) != 1 || got[0] != "600" {
				t.Errorf("reminders = %v, want the ten-minute reminder sent back", got)
			}
			if got := form.Get("calendar_event[starts_at_time_zone_name]"); got != "Europe/Zagreb" {
				t.Errorf("starts_at_time_zone_name = %q, want the zone sent back", got)
			}
			// 12:00Z is 14:00 in Zagreb, and that wall-clock time is what HEY stores.
			if got := form.Get("calendar_event[starts_at_time]"); got != "14:00:00" {
				t.Errorf("starts_at_time = %q, want the event's own clock time", got)
			}
			if got := form.Get("calendar_event[starts_at]"); got != "2026-09-02" {
				t.Errorf("starts_at = %q", got)
			}
			// Nothing was said about guests, so nothing is said on the wire: submitting a
			// roster makes the caller the organizer and sends invitations.
			if got := form["calendar_event[attendance_email_addresses][]"]; got != nil {
				t.Errorf("attendees = %v, want none submitted", got)
			}
			_, _ = io.WriteString(w, `{"id":4821,"title":"Design review (moved)"}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}), "event", "edit", "4821", "--title", "Design review (moved)")
	if err != nil {
		t.Fatalf("execute events edit: %v", err)
	}
}

// Giving a date reads that day alone rather than a window around today.
func TestEventsEditReadsTheDayItIsGiven(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/calendars/9/recordings.json":
			if got := r.URL.Query().Get("starts_on"); got != "2026-09-02" {
				t.Errorf("starts_on = %q", got)
			}
			if got := r.URL.Query().Get("ends_on"); got != "2026-09-02" {
				t.Errorf("ends_on = %q", got)
			}
			_, _ = io.WriteString(w, `{"Calendar::Event":[{"id":4821,"title":"Design review","all_day":true,"starts_at":"2026-09-02T00:00:00Z","ends_at":"2026-09-02T00:00:00Z"}]}`)
		case r.Method == http.MethodPatch:
			if got := eventForm(t, r).Get("calendar_event[location]"); got != "Studio, 3rd floor" {
				t.Errorf("location = %q", got)
			}
			_, _ = io.WriteString(w, `{"id":4821,"title":"Design review"}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}), "event", "edit", "4821", "2026-09-02", "--calendar", "9", "--location", "Studio, 3rd floor")
	if err != nil {
		t.Fatalf("execute events edit: %v", err)
	}
}

// Writing blind would clear the notes, location, link and reminders of whichever event 4821
// turns out to be, so an event that cannot be read is not written.
func TestEventsEditRefusesAnEventItCannotRead(t *testing.T) {
	var writes atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true}}]}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}), "event", "edit", "4821", "--title", "Design review (moved)")
	if err == nil || !strings.Contains(err.Error(), `event "4821" not found`) {
		t.Fatalf("error = %v, want a not-found refusal", err)
	}
	if writes.Load() != 0 {
		t.Errorf("writes = %d, want none", writes.Load())
	}
}

func TestEventsDeleteCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/calendar/events/4821" {
			t.Errorf("request = %s %s, want DELETE /calendar/events/4821", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}), "event", "delete", "4821")
	if err != nil {
		t.Fatalf("execute events delete: %v", err)
	}
	if response.Summary != "Event deleted" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestParseNotice(t *testing.T) {
	tests := []struct {
		notice  string
		seconds float64
		invalid bool
	}{
		{notice: "10m", seconds: 600},
		{notice: "1h", seconds: 3600},
		{notice: "2d", seconds: 172800},
		{notice: "90s", seconds: 90},
		{notice: "0m", invalid: true},
		{notice: "-1h", invalid: true},
		{notice: "0d", invalid: true},
		{notice: "tomorrow", invalid: true},
		{notice: "d", invalid: true},
	}
	for _, tt := range tests {
		duration, err := parseNotice(tt.notice)
		if tt.invalid {
			if err == nil {
				t.Errorf("parseNotice(%q) = %v, want an error", tt.notice, duration)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseNotice(%q): %v", tt.notice, err)
			continue
		}
		if duration.Seconds() != tt.seconds {
			t.Errorf("parseNotice(%q) = %v, want %v seconds", tt.notice, duration, tt.seconds)
		}
	}
}
