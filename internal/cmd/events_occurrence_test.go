package cmd

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// The series as a calendar's recordings listing serves it: one row, on the day it began,
// carrying everything a write has to send back.
const occurrenceSeriesJSON = `{"id":4821,"type":"Calendar::Event","title":"Design review","recurring":true,` +
	`"starts_at":"2026-09-01T12:00:00Z","ends_at":"2026-09-01T13:00:00Z",` +
	`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb",` +
	`"description":"Bring the roadmap","location":"Studio, 3rd floor","url":"https://meet.example.com/design",` +
	`"attached_entry":{"id":551},"reminders":[{"duration":600}],"highlighted":true,` +
	`"recurrence_schedule":{"kind":"every_week","preset":true},"calendar":{"id":9,"name":"Work"}}`

// The countdown HEY keeps under the series: a recording of its own, ending where the series
// begins, with the only description of its length in its label.
const occurrenceCountdownJSON = `{"id":77,"type":"Calendar::Countdown","parent_id":4821,"label":"3 weeks before",` +
	`"starts_at":"2026-08-11T00:00:00Z","ends_at":"2026-09-01T12:00:00Z","calendar":{"id":9,"name":"Work"}}`

// occurrenceServer answers what an occurrence edit reads and writes. The day read is the
// occurrence's own day and the first-day read is the series' first day, where its
// countdown sits; an empty firstDay says that read must not happen.
func occurrenceServer(t *testing.T, date, day, firstDay string, onPatch func(t *testing.T, form url.Values)) (http.Handler, *atomic.Int32) {
	t.Helper()
	next := func(from string) string {
		parsed, err := time.Parse(dateLayout, from)
		if err != nil {
			t.Fatalf("date %q: %v", from, err)
		}
		return parsed.AddDate(0, 0, 1).Format(dateLayout)
	}
	var writes atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query()
		switch {
		case r.URL.Path == "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars/9/recordings.json" && query.Get("starts_on") == date:
			if got := query.Get("ends_on"); got != next(date) {
				t.Errorf("ends_on = %q, want the day after %s", got, date)
			}
			_, _ = io.WriteString(w, day)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars/9/recordings.json" && query.Get("starts_on") == "2026-09-01":
			if firstDay == "" {
				t.Errorf("read the series' first day, want no such read")
			}
			if got := query.Get("ends_on"); got != "2026-09-02" {
				t.Errorf("ends_on = %q, want the day after the series began", got)
			}
			_, _ = io.WriteString(w, firstDay)
		case r.Method == http.MethodPatch && r.URL.Path == "/calendar/events/4821/occurrences/"+date+".json":
			writes.Add(1)
			onPatch(t, eventForm(t, r))
			_, _ = io.WriteString(w, `{"id":9001,"parent_id":4821,"occurrence_id":"4821_`+date+`","title":"Design review (moved)"}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}), &writes
}

// The one that matters: one day of the series is written through the occurrence route with
// apply_to_future off, on the day's own date at the series' own clock time, and everything
// the flags did not name — notes, location, link, attached email, reminders, zones, the
// circle and the countdown — is sent back.
func TestEventsEditOccurrenceCurrentChangesThatDayAlone(t *testing.T) {
	handler, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		`{"Calendar::Countdown":[`+occurrenceCountdownJSON+`]}`,
		func(t *testing.T, form url.Values) {
			want := map[string]string{
				"apply_to_future":                          "0",
				"repeat_frequency":                         "custom",
				"calendar_event[summary]":                  "Design review (moved)",
				"calendar_event[starts_at]":                "2026-09-15",
				"calendar_event[ends_at]":                  "2026-09-15",
				"calendar_event[all_day]":                  "0",
				"calendar_event[starts_at_time]":           "14:00:00",
				"calendar_event[ends_at_time]":             "15:00:00",
				"calendar_event[starts_at_time_zone_name]": "Europe/Zagreb",
				"calendar_event[ends_at_time_zone_name]":   "Europe/Zagreb",
				"calendar_event[description]":              "Bring the roadmap",
				"calendar_event[location]":                 "Studio, 3rd floor",
				"calendar_event[url]":                      "https://meet.example.com/design",
				"calendar_event[entry_id]":                 "551",
				"calendar_event[highlighted]":              "1",
				"countdown_interval_duration_value":        "3",
				"countdown_interval_duration_unit":         "604800",
			}
			for field, value := range want {
				if got := form.Get(field); got != value {
					t.Errorf("%s = %q, want %q", field, got, value)
				}
			}
			if got := form["timed_reminder_durations[]"]; len(got) != 1 || got[0] != "600" {
				t.Errorf("reminders = %v, want the ten-minute reminder sent back", got)
			}
			if got := form["calendar_event[attendance_email_addresses][]"]; got != nil {
				t.Errorf("attendees = %v, want none submitted", got)
			}
			if form.Has("calendar_event[calendar_id]") {
				t.Errorf("calendar_id = %q, want the day left on its calendar", form.Get("calendar_event[calendar_id]"))
			}
			if form.Has("calendar_recurrence_schedule[recurs_until_type]") {
				t.Errorf("recurs_until_type was sent, want the schedule left alone")
			}
		})

	response, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current",
		"--title", "Design review (moved)", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
	}
	if response.Summary != "Occurrence updated" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["occurrence_id"] != "4821_2026-09-15" {
		t.Errorf("structured data = %#v, want the occurrence HEY answered", response.Data)
	}
}

// The wider scope is the same write with apply_to_future on. It is the one that may change
// the schedule, and a schedule it is not given is left as it is rather than ended.
func TestEventsEditOccurrenceFutureChangesTheDaysFromThisOneOn(t *testing.T) {
	handler, _ := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		`{"Calendar::Countdown":[`+occurrenceCountdownJSON+`]}`,
		func(t *testing.T, form url.Values) {
			if got := form.Get("apply_to_future"); got != "1" {
				t.Errorf("apply_to_future = %q, want 1", got)
			}
			if got := form.Get("repeat_frequency"); got != "every_other_week" {
				t.Errorf("repeat_frequency = %q", got)
			}
			if got := form.Get("calendar_recurrence_schedule[recurs_until_type]"); got != "forever" {
				t.Errorf("recurs_until_type = %q", got)
			}
			if got := form.Get("calendar_event[starts_at]"); got != "2026-09-15" {
				t.Errorf("starts_at = %q, want the new series to begin on this day", got)
			}
			if got := form.Get("calendar_event[location]"); got != "Studio, 3rd floor" {
				t.Errorf("location = %q, want it sent back", got)
			}
			if got := form.Get("countdown_interval_duration_value"); got != "3" {
				t.Errorf("countdown value = %q, want the series' countdown on the new series", got)
			}
		})

	response, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
		"--repeat", "every_other_week", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if response.Summary != "Occurrence and the following updated" {
		t.Errorf("summary = %q", response.Summary)
	}
}

// A day HEY has already written out as a recording of its own carries its own title, times
// and countdown, and those — not the series' — are what an edit of that day keeps.
func TestEventsEditOccurrencePrefersTheDayHEYWroteOut(t *testing.T) {
	realized := `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-09-15",` +
		`"title":"Design review (with the vendor)","starts_at":"2026-09-15T13:30:00Z","ends_at":"2026-09-15T14:30:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb","location":"Vendor's office",` +
		`"reminders":[{"duration":1800}],"calendar":{"id":9,"name":"Work"}}`
	countdown := `{"id":78,"type":"Calendar::Countdown","parent_id":9001,"label":"2 days before","calendar":{"id":9,"name":"Work"}}`
	handler, _ := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`,`+realized+`],"Calendar::Countdown":[`+countdown+`]}`,
		"",
		func(t *testing.T, form url.Values) {
			if got := form.Get("calendar_event[summary]"); got != "Design review (with the vendor)" {
				t.Errorf("summary = %q, want the day's own title", got)
			}
			if got := form.Get("calendar_event[starts_at_time]"); got != "15:30:00" {
				t.Errorf("starts_at_time = %q, want the day's own clock time", got)
			}
			if got := form.Get("calendar_event[location]"); got != "Vendor's office" {
				t.Errorf("location = %q, want the day's own location", got)
			}
			if got := form.Get("calendar_event[description]"); got != "" {
				t.Errorf("description = %q, want the day's own (none)", got)
			}
			if got := form["timed_reminder_durations[]"]; len(got) != 1 || got[0] != "1800" {
				t.Errorf("reminders = %v, want the day's own", got)
			}
			if got := form.Get("countdown_interval_duration_value"); got != "2" {
				t.Errorf("countdown value = %q, want the day's own", got)
			}
			if got := form.Get("countdown_interval_duration_unit"); got != "86400" {
				t.Errorf("countdown unit = %q, want days", got)
			}
			if got := form.Get("calendar_event[highlighted]"); got != "0" {
				t.Errorf("highlighted = %q, want the day's own", got)
			}
		})

	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--link", "https://meet.example.com/vendor")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
}

// The series' first day is the one day its countdown is already on, so editing that
// occurrence reads nothing more.
func TestEventsEditOccurrenceOnTheFirstDayReadsOnce(t *testing.T) {
	handler, _ := occurrenceServer(t, "2026-09-01",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`],"Calendar::Countdown":[`+occurrenceCountdownJSON+`]}`,
		"",
		func(t *testing.T, form url.Values) {
			if got := form.Get("countdown_interval_duration_value"); got != "3" {
				t.Errorf("countdown value = %q, want the countdown found on the day", got)
			}
			if got := form.Get("calendar_event[starts_at]"); got != "2026-09-01" {
				t.Errorf("starts_at = %q", got)
			}
		})

	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "2026-09-01", "--occurrence", "4821_2026-09-01", "--apply-to", "current",
		"--title", "Design review (moved)", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
}

// Every flag combination that cannot mean anything is refused before a request is made.
func TestEventsEditOccurrenceRefusesWhatItCannotMean(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "apply-to without occurrence", args: []string{"--apply-to", "current"}, want: "--apply-to needs --occurrence"},
		{name: "occurrence without apply-to", args: []string{"--occurrence", "4821_2026-09-15"}, want: "--apply-to is required with --occurrence"},
		{name: "unknown scope", args: []string{"--occurrence", "4821_2026-09-15", "--apply-to", "all"}, want: "invalid apply-to: all"},
		{name: "no underscore", args: []string{"--occurrence", "4821-2026-09-15", "--apply-to", "current"}, want: "invalid occurrence: 4821-2026-09-15"},
		{name: "date without dashes", args: []string{"--occurrence", "4821_20260915", "--apply-to", "current"}, want: "invalid occurrence: 4821_20260915"},
		{name: "padded series", args: []string{"--occurrence", "04821_2026-09-15", "--apply-to", "current"}, want: "invalid occurrence: 04821_2026-09-15"},
		{name: "trailing text", args: []string{"--occurrence", "4821_2026-09-15x", "--apply-to", "current"}, want: "invalid occurrence: 4821_2026-09-15x"},
		{name: "no date", args: []string{"--occurrence", "4821_", "--apply-to", "current"}, want: "invalid occurrence: 4821_"},
		{name: "other series", args: []string{"--occurrence", "4822_2026-09-15", "--apply-to", "current"}, want: "occurrence 4822_2026-09-15 belongs to series 4822, not 4821"},
		{name: "date is not the day", args: []string{"2026-09-01", "--occurrence", "4821_2026-09-15", "--apply-to", "current"}, want: "date 2026-09-01 is not the day of occurrence 4821_2026-09-15"},
		{name: "repeat on one day", args: []string{"--occurrence", "4821_2026-09-15", "--apply-to", "current", "--repeat", "every_week"}, want: "--repeat cannot apply to one day of a series"},
		{name: "repeat-until on one day", args: []string{"--occurrence", "4821_2026-09-15", "--apply-to", "current", "--repeat-until", "2026-12-31"}, want: "--repeat-until cannot apply to one day of a series"},
		{name: "repeat-times on one day", args: []string{"--occurrence", "4821_2026-09-15", "--apply-to", "current", "--repeat-times", "4"}, want: "--repeat-times cannot apply to one day of a series"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				t.Errorf("unexpected request = %s %s", r.Method, r.URL)
				http.NotFound(w, r)
			})
			_, err := runJSONCommand(t, handler, append([]string{"event", "edit", "4821"}, tt.args...)...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || cliErr.Message != tt.want {
				t.Fatalf("error = %v, want usage %q", err, tt.want)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want none", requests.Load())
			}
		})
	}
}

// Notes are only served as plain text, so an edit that would send formatted notes back as
// text is refused until the caller says the loss is acceptable or replaces them.
func TestEventsEditOccurrenceRefusesToFlattenNotesUnasked(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		handler, writes := occurrenceServer(t, "2026-09-15",
			`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
			`{}`,
			func(t *testing.T, form url.Values) {})
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (moved)")
		var cliErr *apierr.Error
		if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "serves only as plain text") {
			t.Fatalf("error = %v, want the plain-notes refusal", err)
		}
		if !strings.Contains(cliErr.Hint, "--allow-plain-notes") {
			t.Errorf("hint = %q, want it to name the flag", cliErr.Hint)
		}
		if writes.Load() != 0 {
			t.Errorf("writes = %d, want none", writes.Load())
		}
	})

	t.Run("replaced", func(t *testing.T) {
		handler, writes := occurrenceServer(t, "2026-09-15",
			`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
			`{}`,
			func(t *testing.T, form url.Values) {
				if got := form.Get("calendar_event[description]"); got != "Bring the roadmap and the budget" {
					t.Errorf("description = %q", got)
				}
			})
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--notes", "Bring the roadmap and the budget")
		if err != nil {
			t.Fatalf("execute occurrence edit: %v", err)
		}
		if writes.Load() != 1 {
			t.Errorf("writes = %d, want one", writes.Load())
		}
	})

	t.Run("no notes to lose", func(t *testing.T) {
		series := strings.Replace(occurrenceSeriesJSON, `"description":"Bring the roadmap",`, "", 1)
		handler, writes := occurrenceServer(t, "2026-09-15",
			`{"Calendar::Event":[`+series+`]}`,
			`{}`,
			func(t *testing.T, form url.Values) {
				if got := form.Get("calendar_event[description]"); got != "" {
					t.Errorf("description = %q, want none", got)
				}
			})
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (moved)")
		if err != nil {
			t.Fatalf("execute occurrence edit: %v", err)
		}
		if writes.Load() != 1 {
			t.Errorf("writes = %d, want one", writes.Load())
		}
	})
}

// A day that cannot be read is not written: an occurrence whose series is not on its day,
// an id that is itself one day of a series, and an event that does not repeat are each
// refused with the reason, and nothing is sent.
func TestEventsEditOccurrenceRefusesADayItCannotRead(t *testing.T) {
	realizedOther := `{"id":4821,"type":"Calendar::Event","parent_id":4000,"occurrence_id":"4000_2026-09-15","title":"Standup","starts_at":"2026-09-15T07:00:00Z","ends_at":"2026-09-15T07:15:00Z","calendar":{"id":9}}`
	oneOff := strings.Replace(occurrenceSeriesJSON, `"recurring":true`, `"recurring":false`, 1)
	tests := []struct {
		name string
		day  string
		code string
		want string
	}{
		{name: "not on its day", day: `{"Calendar::Event":[]}`, code: apierr.CodeNotFound, want: `occurrence "4821_2026-09-15" not found`},
		{name: "id is a day of another series", day: `{"Calendar::Event":[` + realizedOther + `]}`, code: apierr.CodeUsage, want: "event 4821 is one day of series 4000, not a series"},
		{name: "does not repeat", day: `{"Calendar::Event":[` + oneOff + `]}`, code: apierr.CodeUsage, want: "event 4821 does not repeat, so it has no occurrences"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, writes := occurrenceServer(t, "2026-09-15", tt.day, "", func(t *testing.T, form url.Values) {})
			_, err := runJSONCommand(t, handler,
				"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (moved)", "--allow-plain-notes")
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != tt.code || cliErr.Message != tt.want {
				t.Fatalf("error = %v, want %s %q", err, tt.code, tt.want)
			}
			if writes.Load() != 0 {
				t.Errorf("writes = %d, want none", writes.Load())
			}
		})
	}
}

// --countdown 0 is the one way an occurrence edit removes a countdown, and saying so means
// the countdown is not read back at all.
func TestEventsEditOccurrenceRemovesTheCountdownOnlyWhenTold(t *testing.T) {
	handler, _ := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		"",
		func(t *testing.T, form url.Values) {
			for _, field := range []string{"countdown_interval_duration_value", "countdown_interval_duration_unit"} {
				if form.Has(field) {
					t.Errorf("%s = %q, want none", field, form.Get(field))
				}
			}
		})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--countdown", "0", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
}

// A countdown --countdown names replaces the one the series has, without reading it back.
func TestEventsEditOccurrenceTakesTheCountdownItIsGiven(t *testing.T) {
	handler, _ := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		"",
		func(t *testing.T, form url.Values) {
			if got := form.Get("countdown_interval_duration_value"); got != "2" {
				t.Errorf("countdown value = %q", got)
			}
			if got := form.Get("countdown_interval_duration_unit"); got != "2629746" {
				t.Errorf("countdown unit = %q, want months", got)
			}
		})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current",
		"--countdown", "2", "--countdown-unit", "months", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
}

// A series with no countdown sends none, after looking on its first day for one.
func TestEventsEditOccurrenceSendsNoCountdownForASeriesWithout(t *testing.T) {
	handler, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		func(t *testing.T, form url.Values) {
			if form.Has("countdown_interval_duration_value") {
				t.Errorf("countdown value = %q, want none", form.Get("countdown_interval_duration_value"))
			}
		})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (moved)", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
	}
}

// A countdown whose label cannot be read back is not guessed at and not dropped: the edit
// stops and says how to name or remove it.
func TestEventsEditOccurrenceFailsClosedOnACountdownItCannotRead(t *testing.T) {
	odd := strings.Replace(occurrenceCountdownJSON, "3 weeks before", "a while before", 1)
	handler, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		`{"Calendar::Countdown":[`+odd+`]}`,
		func(t *testing.T, form url.Values) {})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (moved)", "--allow-plain-notes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeAPI || !strings.Contains(cliErr.Message, `"a while before"`) {
		t.Fatalf("error = %v, want the countdown refusal", err)
	}
	if !strings.Contains(cliErr.Hint, "--countdown 0") {
		t.Errorf("hint = %q, want it to say how to remove the countdown", cliErr.Hint)
	}
	if writes.Load() != 0 {
		t.Errorf("writes = %d, want none", writes.Load())
	}
}

// HEY's own refusal of the write — a date that is not a day of the series, or a series the
// caller cannot edit, both 404 on the occurrence route — reaches the caller as not-found.
func TestEventsEditOccurrenceReportsHEYsRefusal(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars/9/recordings.json":
			_, _ = io.WriteString(w, `{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`)
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (moved)", "--allow-plain-notes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeNotFound {
		t.Fatalf("error = %v, want not-found", err)
	}
}

// Logged out, the occurrence edit is refused before anything is read, like every other
// data-access command.
func TestEventsEditOccurrenceRequiresAuth(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	stubInteractive(t, false)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--json", "event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (moved)"})

	err := root.Execute()
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeAuth {
		t.Fatalf("error = %v, want auth", err)
	}
}

// An id on its own still changes the whole series through the event route, with nothing
// the occurrence flags add on the wire — --allow-plain-notes is accepted and changes
// nothing there, since a whole-event edit has always sent the notes back as text.
func TestEventsEditWholeSeriesIsUnchangedByTheOccurrenceFlags(t *testing.T) {
	var writes atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars/9/recordings.json":
			_, _ = io.WriteString(w, `{"Calendar::Event":[`+occurrenceSeriesJSON+`],"Calendar::Countdown":[`+occurrenceCountdownJSON+`]}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/calendar/events/4821.json":
			writes.Add(1)
			form := eventForm(t, r)
			if form.Has("apply_to_future") {
				t.Errorf("apply_to_future = %q, want none on a whole-event edit", form.Get("apply_to_future"))
			}
			if form.Has("repeat_frequency") {
				t.Errorf("repeat_frequency = %q, want the schedule left alone", form.Get("repeat_frequency"))
			}
			if form.Has("countdown_interval_duration_value") {
				t.Errorf("countdown value = %q, want the whole-event edit unchanged", form.Get("countdown_interval_duration_value"))
			}
			if form.Has("calendar_event[highlighted]") {
				t.Errorf("highlighted = %q, want the whole-event edit unchanged", form.Get("calendar_event[highlighted]"))
			}
			if got := form.Get("calendar_event[starts_at]"); got != "2026-09-01" {
				t.Errorf("starts_at = %q, want the series' own first day", got)
			}
			if got := form.Get("calendar_event[description]"); got != "Bring the roadmap" {
				t.Errorf("description = %q, want the notes sent back", got)
			}
			_, _ = io.WriteString(w, `{"id":4821,"title":"Design review (moved)"}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	response, err := runJSONCommand(t, handler, "event", "edit", "4821", "--title", "Design review (moved)", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute events edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
	}
	if response.Summary != "Event updated" {
		t.Errorf("summary = %q", response.Summary)
	}
}

// HEY names a day by the UTC date of its start and gives it the series' wall-clock time in
// the series' zone, so the wall-clock day can be the day either side of the one named.
func TestOccurrenceInstants(t *testing.T) {
	timed := func(start, end, zone string) generated.Recording {
		startsAt, err := time.Parse(time.RFC3339, start)
		if err != nil {
			t.Fatal(err)
		}
		endsAt, err := time.Parse(time.RFC3339, end)
		if err != nil {
			t.Fatal(err)
		}
		return generated.Recording{StartsAt: startsAt, EndsAt: endsAt, StartsAtTimeZone: zone, EndsAtTimeZone: zone}
	}
	day := func(date string) time.Time {
		parsed, err := time.Parse(dateLayout, date)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	tests := []struct {
		name       string
		series     generated.Recording
		day        string
		start, end string
	}{
		{
			name:   "afternoon in Zagreb",
			series: timed("2026-09-01T12:00:00Z", "2026-09-01T13:00:00Z", "Europe/Zagreb"),
			day:    "2026-09-15", start: "2026-09-15T12:00:00Z", end: "2026-09-15T13:00:00Z",
		},
		{
			name:   "late evening in New York, named by the next UTC day",
			series: timed("2026-09-02T03:30:00Z", "2026-09-02T04:00:00Z", "America/New_York"),
			day:    "2026-09-16", start: "2026-09-16T03:30:00Z", end: "2026-09-16T04:00:00Z",
		},
		{
			name:   "early morning in Tokyo, named by the previous UTC day",
			series: timed("2026-08-31T16:00:00Z", "2026-08-31T17:00:00Z", "Asia/Tokyo"),
			day:    "2026-09-14", start: "2026-09-14T16:00:00Z", end: "2026-09-14T17:00:00Z",
		},
		{
			name:   "across a DST change the wall-clock time holds",
			series: timed("2026-10-01T12:00:00Z", "2026-10-01T13:00:00Z", "Europe/Zagreb"),
			day:    "2026-11-05", start: "2026-11-05T13:00:00Z", end: "2026-11-05T14:00:00Z",
		},
		{
			name:   "no zone is UTC",
			series: timed("2026-09-01T09:00:00Z", "2026-09-01T09:30:00Z", ""),
			day:    "2026-09-15", start: "2026-09-15T09:00:00Z", end: "2026-09-15T09:30:00Z",
		},
		{
			name: "two all-day days",
			series: generated.Recording{AllDay: true,
				StartsAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)},
			day: "2026-09-15", start: "2026-09-15T00:00:00Z", end: "2026-09-16T00:00:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := occurrenceInstants(tt.series, day(tt.day))
			if got := start.UTC().Format(time.RFC3339); got != tt.start {
				t.Errorf("start = %s, want %s", got, tt.start)
			}
			if got := end.UTC().Format(time.RFC3339); got != tt.end {
				t.Errorf("end = %s, want %s", got, tt.end)
			}
		})
	}
}

func TestCountdownFromLabel(t *testing.T) {
	tests := []struct {
		label   string
		value   int
		unit    hey.CountdownUnit
		invalid bool
	}{
		{label: "3 weeks before", value: 3, unit: hey.CountdownUnitWeeks},
		{label: "1 days before", value: 1, unit: hey.CountdownUnitDays},
		{label: "1 day before", value: 1, unit: hey.CountdownUnitDays},
		{label: "6 months before", value: 6, unit: hey.CountdownUnitMonths},
		{label: "Countdown", invalid: true},
		{label: "", invalid: true},
		{label: "3 fortnights before", invalid: true},
		{label: "three weeks before", invalid: true},
	}
	for _, tt := range tests {
		countdown, err := countdownFromLabel(tt.label)
		if tt.invalid {
			if err == nil {
				t.Errorf("countdownFromLabel(%q) = %+v, want an error", tt.label, countdown)
			}
			continue
		}
		if err != nil {
			t.Errorf("countdownFromLabel(%q): %v", tt.label, err)
			continue
		}
		if countdown.Value != tt.value || countdown.Unit != tt.unit {
			t.Errorf("countdownFromLabel(%q) = %+v, want %d %d", tt.label, countdown, tt.value, tt.unit)
		}
	}
}
