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
		{name: "occurrence given empty", args: []string{"--occurrence=", "--title", "Design review (moved)"}, want: "--occurrence needs an occurrence_id"},
		{name: "occurrence given empty with a scope", args: []string{"--occurrence", "", "--apply-to", "current"}, want: "--occurrence needs an occurrence_id"},
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

// --countdown 0 is the one way an occurrence edit removes a countdown. On one day alone it
// can only remove the day's own: HEY shows a day the series' countdown whenever it has
// none of its own, so with a series that has one the removal would be reported and change
// nothing, and it is refused instead. A future edit takes the countdown off the new
// series, so there it goes through.
func TestEventsEditOccurrenceRemovesTheCountdownOnlyWhereItCan(t *testing.T) {
	noCountdown := func(t *testing.T, form url.Values) {
		for _, field := range []string{"countdown_interval_duration_value", "countdown_interval_duration_unit"} {
			if form.Has(field) {
				t.Errorf("%s = %q, want none", field, form.Get(field))
			}
		}
	}

	t.Run("a series without one", func(t *testing.T) {
		handler, writes := occurrenceServer(t, "2026-09-15",
			`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
			`{}`,
			noCountdown)
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--countdown", "0", "--allow-plain-notes")
		if err != nil {
			t.Fatalf("execute occurrence edit: %v", err)
		}
		if writes.Load() != 1 {
			t.Errorf("writes = %d, want one", writes.Load())
		}
	})

	t.Run("one day of a series with one", func(t *testing.T) {
		handler, writes := occurrenceServer(t, "2026-09-15",
			`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
			`{"Calendar::Countdown":[`+occurrenceCountdownJSON+`]}`,
			noCountdown)
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--countdown", "0", "--allow-plain-notes")
		var cliErr *apierr.Error
		if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "the series has a countdown") {
			t.Fatalf("error = %v, want the refusal", err)
		}
		if !strings.Contains(cliErr.Hint, "--apply-to future") {
			t.Errorf("hint = %q, want it to say where the countdown can come off", cliErr.Hint)
		}
		if writes.Load() != 0 {
			t.Errorf("writes = %d, want none", writes.Load())
		}
	})

	t.Run("this day and the following", func(t *testing.T) {
		handler, writes := occurrenceServer(t, "2026-09-15",
			`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
			"",
			noCountdown)
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--countdown", "0", "--allow-plain-notes")
		if err != nil {
			t.Fatalf("execute occurrence edit: %v", err)
		}
		if writes.Load() != 1 {
			t.Errorf("writes = %d, want one", writes.Load())
		}
	})
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

// recordingsServer answers a calendar list and a table of recordings reads keyed by
// "<calendar id> <starts_on>", for the edits whose reads the day-and-first-day helper
// does not describe. A read with no entry is an error, so every request is accounted for.
func recordingsServer(t *testing.T, calendars string, reads map[string]string, patchPath string, onPatch func(t *testing.T, form url.Values)) (http.Handler, *atomic.Int32) {
	t.Helper()
	var writes atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/calendars.json":
			_, _ = io.WriteString(w, calendars)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/calendars/") && strings.HasSuffix(r.URL.Path, "/recordings.json"):
			calendar := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/calendars/"), "/recordings.json")
			key := calendar + " " + r.URL.Query().Get("starts_on")
			body, ok := reads[key]
			if !ok {
				t.Errorf("unexpected read of calendar %s from %s", calendar, r.URL.Query().Get("starts_on"))
				body = `{}`
			}
			_, _ = io.WriteString(w, body)
		case r.Method == http.MethodPatch && r.URL.Path == patchPath:
			writes.Add(1)
			onPatch(t, eventForm(t, r))
			_, _ = io.WriteString(w, `{"id":9001,"parent_id":4821}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}), &writes
}

const oneCalendarJSON = `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true}}]}`

// A day that was moved is still listed under the date it stands for, while its own
// countdown ends where it now starts. Its countdown is what the edit keeps — read from the
// day it moved to — and not the series'.
func TestEventsEditOccurrenceKeepsAMovedDaysOwnCountdown(t *testing.T) {
	moved := `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-09-15",` +
		`"title":"Design review (with the vendor)","starts_at":"2026-09-18T13:30:00Z","ends_at":"2026-09-18T14:30:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb","calendar":{"id":9,"name":"Work"}}`
	own := `{"id":78,"type":"Calendar::Countdown","parent_id":9001,"label":"2 days before",` +
		`"starts_at":"2026-09-16T00:00:00Z","ends_at":"2026-09-18T13:30:00Z","calendar":{"id":9,"name":"Work"}}`
	handler, writes := recordingsServer(t, oneCalendarJSON, map[string]string{
		"9 2026-09-15": `{"Calendar::Event":[` + occurrenceSeriesJSON + `,` + moved + `]}`,
		"9 2026-09-18": `{"Calendar::Event":[` + moved + `],"Calendar::Countdown":[` + own + `]}`,
	}, "/calendar/events/4821/occurrences/2026-09-15.json", func(t *testing.T, form url.Values) {
		if got := form.Get("countdown_interval_duration_value"); got != "2" {
			t.Errorf("countdown value = %q, want the moved day's own", got)
		}
		if got := form.Get("countdown_interval_duration_unit"); got != "86400" {
			t.Errorf("countdown unit = %q, want days", got)
		}
		if got := form.Get("calendar_event[starts_at]"); got != "2026-09-18" {
			t.Errorf("starts_at = %q, want the day where it was moved to", got)
		}
	})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (vendor, final)")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
	}
}

// A weekly series at 02:30 in New York has no 02:30 on the day the clocks spring forward.
// HEY draws that day at 03:30, so that is what a title-only edit sends back, whichever
// scope it reaches — anything else would move the day.
func TestEventsEditOccurrenceKeepsTheClockAcrossASpringForward(t *testing.T) {
	series := `{"id":4821,"type":"Calendar::Event","title":"Early standup","recurring":true,` +
		`"starts_at":"2026-03-01T07:30:00Z","ends_at":"2026-03-01T08:30:00Z",` +
		`"starts_at_time_zone":"America/New_York","ends_at_time_zone":"America/New_York","calendar":{"id":9,"name":"Work"}}`
	for _, scope := range []string{"current", "future"} {
		t.Run(scope, func(t *testing.T) {
			handler, _ := recordingsServer(t, oneCalendarJSON, map[string]string{
				"9 2026-03-08": `{"Calendar::Event":[` + series + `]}`,
				"9 2026-03-01": `{"Calendar::Event":[` + series + `]}`,
			}, "/calendar/events/4821/occurrences/2026-03-08.json", func(t *testing.T, form url.Values) {
				if got := form.Get("calendar_event[starts_at]"); got != "2026-03-08" {
					t.Errorf("starts_at = %q", got)
				}
				if got := form.Get("calendar_event[starts_at_time]"); got != "03:30:00" {
					t.Errorf("starts_at_time = %q, want the hour HEY moved it to", got)
				}
				if got := form.Get("calendar_event[ends_at_time]"); got != "04:30:00" {
					t.Errorf("ends_at_time = %q", got)
				}
				if got := form.Get("calendar_event[starts_at_time_zone_name]"); got != "America/New_York" {
					t.Errorf("starts_at_time_zone_name = %q", got)
				}
			})
			_, err := runJSONCommand(t, handler,
				"event", "edit", "4821", "--occurrence", "4821_2026-03-08", "--apply-to", scope, "--title", "Early standup (moved)")
			if err != nil {
				t.Fatalf("execute occurrence edit: %v", err)
			}
		})
	}
}

// --calendar on an occurrence edit is where the day goes, not where the series is looked
// for: the day is read over every calendar, and the write carries the destination.
func TestEventsEditOccurrenceMovesADayToAnotherCalendar(t *testing.T) {
	handler, writes := recordingsServer(t,
		`{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true}},{"calendar":{"id":10,"name":"Shared","owned":true}}]}`,
		map[string]string{
			"9 2026-09-15":  `{"Calendar::Event":[` + occurrenceSeriesJSON + `]}`,
			"10 2026-09-15": `{}`,
			"9 2026-09-01":  `{}`,
		}, "/calendar/events/4821/occurrences/2026-09-15.json", func(t *testing.T, form url.Values) {
			if got := form.Get("calendar_event[calendar_id]"); got != "10" {
				t.Errorf("calendar_id = %q, want the destination", got)
			}
			if got := form.Get("apply_to_future"); got != "1" {
				t.Errorf("apply_to_future = %q", got)
			}
		})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--calendar", "10", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
	}
}

// A day that was moved to another calendar stays there through a future edit: HEY records
// the new series on the series' calendar unless told otherwise, so the write names the
// day's. --calendar still says where to move it instead.
func TestEventsEditOccurrenceFutureKeepsAMovedDaysCalendar(t *testing.T) {
	twoCalendars := `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true}},{"calendar":{"id":10,"name":"Shared","owned":true}}]}`
	moved := `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-09-15",` +
		`"title":"Design review (with the vendor)","starts_at":"2026-09-15T12:00:00Z","ends_at":"2026-09-15T13:00:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb","calendar":{"id":10,"name":"Shared"}}`
	reads := func() map[string]string {
		return map[string]string{
			"9 2026-09-15":  `{"Calendar::Event":[` + occurrenceSeriesJSON + `]}`,
			"10 2026-09-15": `{"Calendar::Event":[` + moved + `]}`,
			"9 2026-09-01":  `{}`,
		}
	}

	t.Run("without --calendar", func(t *testing.T) {
		handler, _ := recordingsServer(t, twoCalendars, reads(), "/calendar/events/4821/occurrences/2026-09-15.json",
			func(t *testing.T, form url.Values) {
				if got := form.Get("calendar_event[calendar_id]"); got != "10" {
					t.Errorf("calendar_id = %q, want the day's own calendar", got)
				}
			})
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--title", "Design review (vendor, final)")
		if err != nil {
			t.Fatalf("execute occurrence edit: %v", err)
		}
	})

	t.Run("with --calendar", func(t *testing.T) {
		handler, _ := recordingsServer(t, twoCalendars, reads(), "/calendar/events/4821/occurrences/2026-09-15.json",
			func(t *testing.T, form url.Values) {
				if got := form.Get("calendar_event[calendar_id]"); got != "9" {
					t.Errorf("calendar_id = %q, want the destination named", got)
				}
			})
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--calendar", "9", "--title", "Design review (vendor, final)")
		if err != nil {
			t.Fatalf("execute occurrence edit: %v", err)
		}
	})

	t.Run("that day alone leaves the calendar unsaid", func(t *testing.T) {
		handler, _ := recordingsServer(t, twoCalendars, reads(), "/calendar/events/4821/occurrences/2026-09-15.json",
			func(t *testing.T, form url.Values) {
				if form.Has("calendar_event[calendar_id]") {
					t.Errorf("calendar_id = %q, want none: the day stays where it is", form.Get("calendar_event[calendar_id]"))
				}
			})
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (vendor, final)")
		if err != nil {
			t.Fatalf("execute occurrence edit: %v", err)
		}
	})
}

// A future edit records the new series from the series' guest list and invites it. A day
// that had come to have guests of its own is therefore refused until --invite says whose
// list the new series gets; that day alone keeps its own list without a word.
func TestEventsEditOccurrenceFutureRefusesToDropADaysOwnGuests(t *testing.T) {
	series := strings.Replace(occurrenceSeriesJSON, `"highlighted":true,`,
		`"highlighted":true,"attendances":[{"id":1,"email_address":"alice@example.com","status":"accepted","name":"Alice Chen"}],`, 1)
	ownGuests := `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-09-15",` +
		`"title":"Design review (with the vendor)","starts_at":"2026-09-15T12:00:00Z","ends_at":"2026-09-15T13:00:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb",` +
		`"attendances":[{"id":2,"email_address":"bob@example.org","status":"pending","name":"Bob Reyes"}],"calendar":{"id":9,"name":"Work"}}`
	day := `{"Calendar::Event":[` + series + `,` + ownGuests + `]}`

	t.Run("refused", func(t *testing.T) {
		handler, writes := occurrenceServer(t, "2026-09-15", day, "", func(t *testing.T, form url.Values) {})
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--title", "Design review (vendor, final)")
		var cliErr *apierr.Error
		if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "guest list of its own (bob@example.org)") {
			t.Fatalf("error = %v, want the guest-list refusal", err)
		}
		if writes.Load() != 0 {
			t.Errorf("writes = %d, want none", writes.Load())
		}
	})

	t.Run("with --invite", func(t *testing.T) {
		handler, writes := occurrenceServer(t, "2026-09-15", day, `{}`, func(t *testing.T, form url.Values) {
			if got := form["calendar_event[attendance_email_addresses][]"]; len(got) != 1 || got[0] != "bob@example.org" {
				t.Errorf("attendees = %v, want Bob alone", got)
			}
		})
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--invite", "bob@example.org", "--title", "Design review (vendor, final)")
		if err != nil {
			t.Fatalf("execute occurrence edit: %v", err)
		}
		if writes.Load() != 1 {
			t.Errorf("writes = %d, want one", writes.Load())
		}
	})

	t.Run("that day alone keeps its own", func(t *testing.T) {
		handler, writes := occurrenceServer(t, "2026-09-15", day, `{}`, func(t *testing.T, form url.Values) {
			if got := form["calendar_event[attendance_email_addresses][]"]; got != nil {
				t.Errorf("attendees = %v, want none submitted", got)
			}
		})
		_, err := runJSONCommand(t, handler,
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (vendor, final)")
		if err != nil {
			t.Fatalf("execute occurrence edit: %v", err)
		}
		if writes.Load() != 1 {
			t.Errorf("writes = %d, want one", writes.Load())
		}
	})
}

// An eight-day countdown on an all-day series is labelled "1 weeks before", and a title-only
// edit sends the eight days back, not the week.
func TestEventsEditOccurrenceKeepsAnEightDayCountdown(t *testing.T) {
	allDay := `{"id":4821,"type":"Calendar::Event","title":"Sarah's birthday","recurring":true,"all_day":true,` +
		`"starts_at":"2026-09-01T00:00:00Z","ends_at":"2026-09-01T00:00:00Z","calendar":{"id":9,"name":"Work"}}`
	eightDays := `{"id":77,"type":"Calendar::Countdown","parent_id":4821,"label":"1 weeks before",` +
		`"starts_at":"2026-08-24T00:00:00Z","ends_at":"2026-09-01T00:00:00Z","calendar":{"id":9,"name":"Work"}}`
	handler, _ := occurrenceServer(t, "2027-09-01",
		`{"Calendar::Event":[`+allDay+`]}`,
		`{"Calendar::Countdown":[`+eightDays+`]}`,
		func(t *testing.T, form url.Values) {
			if got := form.Get("countdown_interval_duration_value"); got != "8" {
				t.Errorf("countdown value = %q, want the eight days the recording spans", got)
			}
			if got := form.Get("countdown_interval_duration_unit"); got != "86400" {
				t.Errorf("countdown unit = %q, want days", got)
			}
		})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2027-09-01", "--apply-to", "current", "--title", "Sarah's birthday (party)")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
}

// A repeat count of nothing is not "forever": zero and a negative count are refused
// before anything is read, on the future edit that would split the series and on a create.
func TestEventsRefuseARepeatCountOfNothing(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "future edit, zero", args: []string{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat", "every_week", "--repeat-times=0"}},
		{name: "future edit, negative", args: []string{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat", "every_week", "--repeat-times=-1"}},
		{name: "add, zero", args: []string{"event", "add", "Standup", "--calendar", "9", "--repeat", "every_weekday", "--repeat-times=0"}},
		{name: "whole event, zero", args: []string{"event", "edit", "4821", "--repeat", "every_week", "--repeat-times", "0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/calendars.json":
					_, _ = io.WriteString(w, oneCalendarJSON)
				case r.Method == http.MethodGet:
					_, _ = io.WriteString(w, `{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`)
				default:
					t.Errorf("unexpected write = %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			})
			_, err := runJSONCommand(t, handler, tt.args...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "is not a number of occurrences") {
				t.Fatalf("error = %v, want the repeat-times refusal", err)
			}
			if strings.HasPrefix(tt.name, "future") && requests.Load() != 0 {
				t.Errorf("requests = %d, want none before the flags are read", requests.Load())
			}
		})
	}
}

// The day and the week say which id an occurrence carries: the series' for one HEY draws
// from the series, its own for a day HEY has written out, with the series in parent_id.
func TestEventsPeriodHelpNamesTheSeriesID(t *testing.T) {
	for _, command := range []*eventsPeriodCommand{newEventsDayCommand(), newEventsWeekCommand()} {
		for _, want := range []string{"parent_id", "occurrence_id", "written out"} {
			if !strings.Contains(command.cmd.Long, want) {
				t.Errorf("%s help does not mention %q", command.cmd.Name(), want)
			}
		}
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
			// 02:30 does not exist in New York on 2026-03-08; HEY moves it an hour on, to
			// 03:30 EDT, and Go's time.Date would have put it at 01:30 EST.
			name:   "a clock time the zone springs over",
			series: timed("2026-03-01T07:30:00Z", "2026-03-01T08:30:00Z", "America/New_York"),
			day:    "2026-03-08", start: "2026-03-08T07:30:00Z", end: "2026-03-08T08:30:00Z",
		},
		{
			name:   "the same clock time on an ordinary day",
			series: timed("2026-03-01T07:30:00Z", "2026-03-01T08:30:00Z", "America/New_York"),
			day:    "2026-03-15", start: "2026-03-15T06:30:00Z", end: "2026-03-15T07:30:00Z",
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

// HEY labels a countdown by trying months, then weeks, then days, taking a remainder of up
// to a whole day as a match — so eight days at midnight is "1 weeks before", 29 days is
// "4 weeks before" and one day is "0 months before". The recording's span is the countdown
// plus however far into its day the event starts, and that is what the length is read off;
// the label only settles days against HEY's month, which is not a whole number of them.
func TestCountdownFromRecording(t *testing.T) {
	const month = time.Duration(hey.CountdownUnitMonths) * time.Second
	span := func(length time.Duration) (time.Time, time.Time) {
		end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		return end.Add(-length), end
	}
	tests := []struct {
		name    string
		label   string
		length  time.Duration
		value   int
		unit    hey.CountdownUnit
		invalid bool
	}{
		{name: "weeks at midnight", label: "3 weeks before", length: 21 * 24 * time.Hour, value: 3, unit: hey.CountdownUnitWeeks},
		{name: "weeks on a timed event", label: "3 weeks before", length: 21*24*time.Hour + 12*time.Hour, value: 3, unit: hey.CountdownUnitWeeks},
		{name: "days, plural", label: "1 days before", length: 36 * time.Hour, value: 1, unit: hey.CountdownUnitDays},
		{name: "day", label: "1 day before", length: 36 * time.Hour, value: 1, unit: hey.CountdownUnitDays},
		{name: "months", label: "6 months before", length: 6*month + 9*time.Hour, value: 6, unit: hey.CountdownUnitMonths},
		{name: "eight days labelled a week", label: "1 weeks before", length: 8 * 24 * time.Hour, value: 8, unit: hey.CountdownUnitDays},
		{name: "29 days labelled four weeks", label: "4 weeks before", length: 29 * 24 * time.Hour, value: 29, unit: hey.CountdownUnitDays},
		{name: "30 days at midnight", label: "30 days before", length: 30 * 24 * time.Hour, value: 30, unit: hey.CountdownUnitDays},
		{name: "one day on an all-day event", label: "0 months before", length: 24 * time.Hour, value: 1, unit: hey.CountdownUnitDays},
		{name: "a month the label settles", label: "1 months before", length: month + 3*time.Hour, value: 1, unit: hey.CountdownUnitMonths},
		{name: "no span, so the label", label: "2 weeks before", value: 2, unit: hey.CountdownUnitWeeks},
		{name: "zero with an unexplained span", label: "0 months before", length: 20 * time.Hour, invalid: true},
		{name: "zero with no span", label: "0 months before", invalid: true},
		{name: "no countdown", label: "Countdown", length: 24 * time.Hour, invalid: true},
		{name: "empty", label: "", length: 24 * time.Hour, invalid: true},
		{name: "unknown unit", label: "3 fortnights before", length: 42 * 24 * time.Hour, invalid: true},
		{name: "words", label: "three weeks before", length: 21 * 24 * time.Hour, invalid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recording := generated.Recording{Type: recordingTypeCountdown, Label: tt.label}
			if tt.length > 0 {
				recording.StartsAt, recording.EndsAt = span(tt.length)
			}
			countdown, err := countdownFromRecording(recording)
			if tt.invalid {
				if err == nil {
					t.Fatalf("countdownFromRecording(%q) = %+v, want an error", tt.label, countdown)
				}
				return
			}
			if err != nil {
				t.Fatalf("countdownFromRecording(%q): %v", tt.label, err)
			}
			if countdown.Value != tt.value || countdown.Unit != tt.unit {
				t.Errorf("countdownFromRecording(%q) = %+v, want %d %d", tt.label, countdown, tt.value, tt.unit)
			}
		})
	}
}

// A countdown labelled "0 months before" whose span is under a day says nothing readable
// about its length, and is not guessed at: the edit stops before writing, as with any
// recording it cannot read.
func TestEventsEditOccurrenceFailsClosedOnAZeroCountdown(t *testing.T) {
	zero := strings.NewReplacer("3 weeks before", "0 months before", "2026-08-11T00:00:00Z", "2026-08-31T16:00:00Z").
		Replace(occurrenceCountdownJSON)
	handler, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		`{"Calendar::Countdown":[`+zero+`]}`,
		func(t *testing.T, form url.Values) {})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current", "--title", "Design review (moved)", "--allow-plain-notes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeAPI || !strings.Contains(cliErr.Message, `"0 months before"`) {
		t.Fatalf("error = %v, want the countdown refusal", err)
	}
	if writes.Load() != 0 {
		t.Errorf("writes = %d, want none", writes.Load())
	}
}

// A one-day countdown on an all-day series is "0 months before" with a span of exactly a
// day, and it is sent back as the day it is.
func TestEventsEditOccurrenceKeepsAOneDayCountdown(t *testing.T) {
	allDay := `{"id":4821,"type":"Calendar::Event","title":"Sarah's birthday","recurring":true,"all_day":true,` +
		`"starts_at":"2026-09-01T00:00:00Z","ends_at":"2026-09-01T00:00:00Z","calendar":{"id":9,"name":"Work"}}`
	oneDay := `{"id":77,"type":"Calendar::Countdown","parent_id":4821,"label":"0 months before",` +
		`"starts_at":"2026-08-31T00:00:00Z","ends_at":"2026-09-01T00:00:00Z","calendar":{"id":9,"name":"Work"}}`
	handler, _ := occurrenceServer(t, "2027-09-01",
		`{"Calendar::Event":[`+allDay+`]}`,
		`{"Calendar::Countdown":[`+oneDay+`]}`,
		func(t *testing.T, form url.Values) {
			if got := form.Get("countdown_interval_duration_value"); got != "1" {
				t.Errorf("countdown value = %q, want one", got)
			}
			if got := form.Get("countdown_interval_duration_unit"); got != "86400" {
				t.Errorf("countdown unit = %q, want days", got)
			}
			if got := form.Get("calendar_event[all_day]"); got != "1" {
				t.Errorf("all_day = %q", got)
			}
			if got := form.Get("calendar_event[starts_at]"); got != "2027-09-01" {
				t.Errorf("starts_at = %q", got)
			}
		})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2027-09-01", "--apply-to", "current", "--title", "Sarah's birthday (party)")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
}
