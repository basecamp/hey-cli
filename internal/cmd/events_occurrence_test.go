package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

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
		case r.URL.Path == "/identity.json":
			_, _ = io.WriteString(w, `{"id":1,"time_zone":"UTC"}`)
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
// the flags did not name — notes, location, link, attached email, reminders, zones and the
// circle — is sent back. Its inherited countdown stays inherited by being left out.
func TestEventsEditOccurrenceCurrentChangesThatDayAlone(t *testing.T) {
	handler, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		"",
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
			if form.Has("countdown_interval_duration_value") || form.Has("countdown_interval_duration_unit") {
				t.Errorf("countdown = %q %q, want the series' countdown left inherited", form.Get("countdown_interval_duration_value"), form.Get("countdown_interval_duration_unit"))
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

// The wider scope is the same write with apply_to_future on. It starts a new series, so
// the caller states that series' complete schedule, including how many occurrences remain.
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
			if got := form.Get("calendar_recurrence_schedule[recurs_until_type]"); got != "count" {
				t.Errorf("recurs_until_type = %q, want count", got)
			}
			if got := form.Get("calendar_recurrence_schedule[recurs_count]"); got != "3" {
				t.Errorf("recurs_count = %q, want the three occurrences remaining", got)
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
		"--repeat", "every_other_week", "--repeat-times", "3", "--allow-plain-notes")
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
	countdown := `{"id":78,"type":"Calendar::Countdown","parent_id":9001,"label":"2 days before",` +
		`"starts_at":"2026-09-13T00:00:00Z","ends_at":"2026-09-15T13:30:00Z","calendar":{"id":9,"name":"Work"}}`
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

// The series' first day has the inherited countdown in the day response. A current-only
// edit still leaves it out rather than turning it into a countdown owned by that day.
func TestEventsEditOccurrenceOnTheFirstDayLeavesTheCountdownInherited(t *testing.T) {
	handler, _ := occurrenceServer(t, "2026-09-01",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`],"Calendar::Countdown":[`+occurrenceCountdownJSON+`]}`,
		"",
		func(t *testing.T, form url.Values) {
			if form.Has("countdown_interval_duration_value") || form.Has("countdown_interval_duration_unit") {
				t.Errorf("countdown = %q %q, want it left inherited", form.Get("countdown_interval_duration_value"), form.Get("countdown_interval_duration_unit"))
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
		{name: "future without a complete repeat schedule", args: []string{"--occurrence", "4821_2026-09-15", "--apply-to", "future", "--title", "Design review (moved)"}, want: "--apply-to future needs --repeat to define the new series"},
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
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
			"--repeat", "every_week", "--countdown", "0", "--allow-plain-notes")
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

// A current-only edit sends no inherited countdown and does not need to read the series'
// first day to decide that.
func TestEventsEditOccurrenceSendsNoInheritedCountdown(t *testing.T) {
	handler, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		"",
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

// A countdown whose label or timestamps cannot be read back is not guessed at and not
// dropped: the edit stops and says how to name or remove it.
func TestEventsEditOccurrenceFailsClosedOnACountdownItCannotRead(t *testing.T) {
	tests := []struct {
		name, countdown, label string
	}{
		{
			name:      "unknown label",
			countdown: strings.Replace(occurrenceCountdownJSON, "3 weeks before", "a while before", 1),
			label:     "a while before",
		},
		{
			name: "missing timestamps",
			countdown: `{"id":77,"type":"Calendar::Countdown","parent_id":4821,` +
				`"label":"3 weeks before","calendar":{"id":9,"name":"Work"}}`,
			label: "3 weeks before",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, writes := occurrenceServer(t, "2026-09-15",
				`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
				`{"Calendar::Countdown":[`+tt.countdown+`]}`,
				func(t *testing.T, form url.Values) {})
			_, err := runJSONCommand(t, handler,
				"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat", "every_week", "--title", "Design review (moved)", "--allow-plain-notes")
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeAPI || !strings.Contains(cliErr.Message, `"`+tt.label+`"`) {
				t.Fatalf("error = %v, want the countdown refusal", err)
			}
			if !strings.Contains(cliErr.Hint, "--countdown 0") {
				t.Errorf("hint = %q, want it to say how to remove the countdown", cliErr.Hint)
			}
			if writes.Load() != 0 {
				t.Errorf("writes = %d, want none", writes.Load())
			}
		})
	}
}

// A countdown created by the web form begins at midnight in the identity's zone, which can
// differ from both UTC and the event's zone. A future split still copies that countdown.
func TestEventsEditOccurrenceReadsAWebCountdownInTheIdentityZone(t *testing.T) {
	series := strings.ReplaceAll(occurrenceSeriesJSON,
		`"starts_at":"2026-09-01T12:00:00Z"`, `"starts_at":"2026-09-01T14:00:00Z"`)
	series = strings.ReplaceAll(series,
		`"ends_at":"2026-09-01T13:00:00Z"`, `"ends_at":"2026-09-01T15:00:00Z"`)
	series = strings.ReplaceAll(series, `Europe/Zagreb`, `Europe/London`)
	countdown := `{"id":77,"type":"Calendar::Countdown","parent_id":4821,"label":"1 week before",` +
		`"starts_at":"2026-08-25T04:00:00Z","ends_at":"2026-09-01T14:00:00Z","calendar":{"id":9,"name":"Work"}}`

	base, _ := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+series+`]}`,
		`{"Calendar::Countdown":[`+countdown+`]}`,
		func(t *testing.T, form url.Values) {
			if got := form.Get("countdown_interval_duration_value"); got != "1" {
				t.Errorf("countdown value = %q, want one", got)
			}
			if got := form.Get("countdown_interval_duration_unit"); got != "604800" {
				t.Errorf("countdown unit = %q, want weeks", got)
			}
		})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/identity.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":1,"time_zone":"America/New_York"}`)
			return
		}
		base.ServeHTTP(w, r)
	})

	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
		"--repeat", "every_week", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
}

// The identity zone is part of decoding a stored countdown: without it the first plausible
// UTC or event-zone answer cannot be checked for ambiguity. A failed identity read must stop
// before the replacement write rather than silently using that unchecked answer.
func TestEventsEditOccurrenceStopsWhenTheCountdownIdentityZoneCannotBeRead(t *testing.T) {
	base, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		`{"Calendar::Countdown":[`+occurrenceCountdownJSON+`]}`,
		func(t *testing.T, form url.Values) { t.Error("wrote an occurrence with an unchecked countdown") })
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/identity.json" {
			http.Error(w, `{"error":"identity unavailable"}`, http.StatusInternalServerError)
			return
		}
		base.ServeHTTP(w, r)
	})

	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
		"--repeat", "every_week", "--allow-plain-notes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeAPI {
		t.Fatalf("error = %v, want the identity API error", err)
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
		case r.URL.Path == "/identity.json":
			_, _ = io.WriteString(w, `{"id":1,"time_zone":"UTC"}`)
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
		`"starts_at_time_zone":"America/New_York","ends_at_time_zone":"America/New_York",` +
		`"recurrence_schedule":{"kind":"every_week","preset":true},"calendar":{"id":9,"name":"Work"}}`
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
			args := []string{"event", "edit", "4821", "--occurrence", "4821_2026-03-08", "--apply-to", scope, "--title", "Early standup (moved)"}
			if scope == "future" {
				args = append(args, "--repeat", "every_week")
			}
			_, err := runJSONCommand(t, handler, args...)
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
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
		"--repeat", "every_week", "--calendar", "10", "--allow-plain-notes")
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
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
			"--repeat", "every_week", "--title", "Design review (vendor, final)")
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
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
			"--repeat", "every_week", "--calendar", "9", "--title", "Design review (vendor, final)")
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
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
			"--repeat", "every_week", "--title", "Design review (vendor, final)")
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
			"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
			"--repeat", "every_week", "--invite", "bob@example.org", "--title", "Design review (vendor, final)")
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

// An eight-day countdown on an all-day series is labelled "1 weeks before", and a future
// split copies the eight days to the replacement series, not the week.
func TestEventsEditOccurrenceKeepsAnEightDayCountdown(t *testing.T) {
	allDay := `{"id":4821,"type":"Calendar::Event","title":"Sarah's birthday","recurring":true,"all_day":true,` +
		`"starts_at":"2026-09-01T00:00:00Z","ends_at":"2026-09-01T00:00:00Z",` +
		`"recurrence_schedule":{"kind":"every_year","preset":true},"calendar":{"id":9,"name":"Work"}}`
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
		"event", "edit", "4821", "--occurrence", "4821_2027-09-01", "--apply-to", "future", "--repeat", "every_year", "--title", "Sarah's birthday (party)")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
}

// A repeat count of nothing is not "forever": zero and a negative count are refused
// before anything is read, on a future split, a create and a whole-series edit.
func TestEventsRefuseARepeatCountOfNothing(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "future edit, zero", args: []string{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat", "every_week", "--repeat-times=0"}},
		{name: "future edit, negative", args: []string{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat", "every_week", "--repeat-times=-1"}},
		{name: "add, zero", args: []string{"event", "add", "Standup", "--repeat", "every_weekday", "--repeat-times=0"}},
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
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want none before the flags are read", requests.Load())
			}
		})
	}
}

// An explicitly supplied recurrence value must carry content. This is especially important
// for a future split: --repeat= must not pass the complete-schedule gate and fall back to
// HEY's "custom" behavior, which would copy and restart a finite count.
func TestEventsRefuseEmptyRepeatValuesBeforeReading(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "future edit, empty frequency", args: []string{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat="}, want: "--repeat needs a frequency"},
		{name: "future edit, empty last day", args: []string{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat", "every_week", "--repeat-until="}, want: "--repeat-until needs a date"},
		{name: "add, empty frequency", args: []string{"event", "add", "Standup", "--repeat="}, want: "--repeat needs a frequency"},
		{name: "whole event, empty last day", args: []string{"event", "edit", "4821", "--repeat", "every_week", "--repeat-until="}, want: "--repeat-until needs a date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "unexpected request", http.StatusInternalServerError)
			}), tt.args...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, tt.want) {
				t.Fatalf("error = %v, want usage error containing %q", err, tt.want)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want none before the flags are read", requests.Load())
			}
		})
	}
}

// "custom" is HEY's instruction to copy an opaque recurrence schedule. It belongs only
// to a future occurrence split: a create has no schedule to copy, and a whole-series edit
// can leave its recurrence untouched by omitting --repeat. HEY copies an opaque COUNT too,
// which can restart its full count; the command's help warns about that server behavior.
func TestEventsEditOccurrenceCopiesACustomFutureSchedule(t *testing.T) {
	series := strings.Replace(occurrenceSeriesJSON,
		`"recurrence_schedule":{"kind":"every_week","preset":true}`,
		`"recurrence_schedule":{"kind":"custom","preset":false}`, 1)
	virtual := strings.Replace(series, `{"id":4821`,
		`{"id":0,"parent_id":4821,"occurrence_id":"4821_2026-09-15"`, 1)
	virtual = strings.Replace(virtual, `"starts_at":"2026-09-01T12:00:00Z"`, `"starts_at":"2026-09-15T15:00:00Z"`, 1)
	virtual = strings.Replace(virtual, `"ends_at":"2026-09-01T13:00:00Z"`, `"ends_at":"2026-09-15T16:30:00Z"`, 1)
	base, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+series+`]}`, `{}`, func(t *testing.T, form url.Values) {
			if got := form.Get("repeat_frequency"); got != "custom" {
				t.Errorf("repeat_frequency = %q, want custom", got)
			}
			if form.Has("calendar_recurrence_schedule[recurs_until_type]") || form.Has("calendar_recurrence_schedule[recurs_until_date]") || form.Has("calendar_recurrence_schedule[recurs_count]") {
				t.Errorf("custom recurrence carried a replacement limit: %v", form)
			}
			if got := form.Get("calendar_event[starts_at_time]"); got != "17:00:00" {
				t.Errorf("starts_at_time = %q, want the custom occurrence HEY served", got)
			}
			if got := form.Get("calendar_event[ends_at_time]"); got != "18:30:00" {
				t.Errorf("ends_at_time = %q, want the custom occurrence HEY served", got)
			}
		})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/calendar/days/2026-09-15.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"kind":"day","recordings":{"Calendar::Event":[`+virtual+`]}}`)
			return
		}
		base.ServeHTTP(w, r)
	})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
		"--repeat", "custom", "--allow-plain-notes", "--title", "Design review (new agenda)")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
	}
}

func TestEventsEditOccurrenceCurrentUsesTheServedCustomVirtualSchedule(t *testing.T) {
	series := strings.Replace(occurrenceSeriesJSON,
		`"recurrence_schedule":{"kind":"every_week","preset":true}`,
		`"recurrence_schedule":{"kind":"custom","preset":false}`, 1)
	virtual := strings.Replace(series, `{"id":4821`,
		`{"id":0,"parent_id":4821,"occurrence_id":"4821_2026-09-15"`, 1)
	virtual = strings.Replace(virtual, `"starts_at":"2026-09-01T12:00:00Z"`, `"starts_at":"2026-09-15T15:00:00Z"`, 1)
	virtual = strings.Replace(virtual, `"ends_at":"2026-09-01T13:00:00Z"`, `"ends_at":"2026-09-15T16:30:00Z"`, 1)
	base, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+series+`]}`, "", func(t *testing.T, form url.Values) {
			if got := form.Get("calendar_event[starts_at_time]"); got != "17:00:00" {
				t.Errorf("starts_at_time = %q, want the custom occurrence HEY served", got)
			}
			if got := form.Get("calendar_event[ends_at_time]"); got != "18:30:00" {
				t.Errorf("ends_at_time = %q, want the custom occurrence HEY served", got)
			}
		})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/calendar/days/2026-09-15.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"kind":"day","recordings":{"Calendar::Event":[`+virtual+`]}}`)
			return
		}
		base.ServeHTTP(w, r)
	})

	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current",
		"--title", "Design review (new agenda)", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
	}
}

func TestEventsEditOccurrenceRefusesAnUnservedCustomVirtualOccurrence(t *testing.T) {
	series := strings.Replace(occurrenceSeriesJSON,
		`"recurrence_schedule":{"kind":"every_week","preset":true}`,
		`"recurrence_schedule":{"kind":"custom","preset":false}`, 1)
	base, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+series+`]}`, "",
		func(t *testing.T, form url.Values) {
			t.Error("wrote a custom occurrence whose schedule was not served")
		})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/calendar/days/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"kind":"day","recordings":{}}`)
			return
		}
		base.ServeHTTP(w, r)
	})

	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current",
		"--title", "Design review (new agenda)", "--allow-plain-notes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeNotFound || !strings.Contains(cliErr.Message, "4821_2026-09-15") {
		t.Fatalf("error = %v, want the unserved occurrence not-found error", err)
	}
	if writes.Load() != 0 {
		t.Errorf("writes = %d, want none", writes.Load())
	}
}

func TestEventsRefuseCustomRepeatOutsideAFutureSplit(t *testing.T) {
	tests := [][]string{
		{"event", "add", "Standup", "--repeat", "custom"},
		{"event", "edit", "4821", "--repeat", "custom"},
		{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat", "custom", "--repeat-times", "2"},
	}
	for _, args := range tests {
		var requests atomic.Int32
		_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}), args...)
		var cliErr *apierr.Error
		if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage {
			t.Fatalf("%v: error = %v, want usage error", args, err)
		}
		if requests.Load() != 0 {
			t.Errorf("%v: requests = %d, want none", args, requests.Load())
		}
	}
}

func TestEventsRepeatHelpLimitsCustomToFutureOccurrenceEdits(t *testing.T) {
	for _, command := range []*cobra.Command{newEventsAddCommand().cmd, newEventsEditCommand().cmd} {
		usage := command.Flags().Lookup("repeat").Usage
		if !strings.Contains(usage, "custom only") || !strings.Contains(usage, "future occurrence edit") {
			t.Errorf("%s --repeat help = %q, want the custom scope", command.CommandPath(), usage)
		}
	}
}

func TestEventsEditOccurrenceValidatesExplicitFlagsBeforeReading(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "reminder", args: []string{"--remind", "soon"}, want: "invalid remind: soon"},
		{name: "start date", args: []string{"--starts-on", "next-week"}, want: "invalid starts-on date"},
		{name: "end date", args: []string{"--ends-on", "eventually"}, want: "invalid ends-on date"},
		{name: "start time", args: []string{"--start-time", "morning"}, want: "invalid start-time: morning"},
		{name: "end time", args: []string{"--end-time", "later"}, want: "invalid end-time: later"},
		{name: "time zone", args: []string{"--time-zone", "Not/AZone"}, want: "invalid time-zone: Not/AZone"},
		{name: "empty time zone", args: []string{"--time-zone="}, want: "--time-zone needs a time zone"},
		{name: "local pseudo-zone", args: []string{"--time-zone", "Local"}, want: "invalid time-zone: Local"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			args := []string{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current"}
			args = append(args, tt.args...)
			_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "unexpected request", http.StatusInternalServerError)
			}), args...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, tt.want) {
				t.Fatalf("error = %v, want usage error containing %q", err, tt.want)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want none", requests.Load())
			}
		})
	}
}

// Haystack truncates the parent at the occurrence identifier but cancels realized children
// from the selected recording's actual start. Until those boundaries agree, a future split
// of a moved occurrence can destroy a preceding edit or leave a following edit behind.
func TestEventsRefuseAFutureSplitOfAMovedRealizedOccurrence(t *testing.T) {
	for _, movedStart := range []string{"2026-09-10T12:00:00Z", "2026-09-20T12:00:00Z"} {
		t.Run(movedStart, func(t *testing.T) {
			movedAt, err := time.Parse(time.RFC3339, movedStart)
			if err != nil {
				t.Fatal(err)
			}
			realized := fmt.Sprintf(`{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-09-15",`+
				`"title":"Design review","starts_at":%q,"ends_at":%q,`+
				`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb","calendar":{"id":9,"name":"Work"}}`,
				movedAt.Format(time.RFC3339), movedAt.Add(time.Hour).Format(time.RFC3339))
			handler, writes := occurrenceServer(t, "2026-09-15",
				`{"Calendar::Event":[`+occurrenceSeriesJSON+`,`+realized+`]}`, "",
				func(t *testing.T, form url.Values) { t.Error("wrote a future split for a moved occurrence") })
			_, err = runJSONCommand(t, handler,
				"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
				"--starts-on", "2026-09-15", "--ends-on", "2026-09-15",
				"--start-time", "14:00", "--end-time", "15:00", "--repeat", "every_week")
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "was moved") {
				t.Fatalf("error = %v, want moved-occurrence usage error", err)
			}
			if writes.Load() != 0 {
				t.Errorf("writes = %d, want none", writes.Load())
			}
		})
	}
}

// The API does not expose a custom rule's occurrence boundary. A realized custom occurrence
// can happen at a different clock time from its parent and then be moved back to the parent's
// clock, which would evade the preset comparison above. Fail closed before Haystack can
// cancel neighboring realized children from that guessed boundary. A virtual custom day is
// still covered by TestEventsEditOccurrenceCopiesACustomFutureSchedule.
func TestEventsRefuseAFutureSplitOfARealizedCustomOccurrence(t *testing.T) {
	series := strings.Replace(occurrenceSeriesJSON,
		`"recurrence_schedule":{"kind":"every_week","preset":true}`,
		`"recurrence_schedule":{"kind":"custom","preset":false}`, 1)
	realized := `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-09-15",` +
		`"title":"Design review","starts_at":"2026-09-15T12:00:00Z","ends_at":"2026-09-15T13:00:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb","calendar":{"id":9,"name":"Work"}}`
	handler, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+series+`,`+realized+`]}`, "",
		func(t *testing.T, form url.Values) { t.Error("wrote a future split for a realized custom occurrence") })

	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
		"--repeat", "every_week", "--allow-plain-notes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "opaque custom schedule") {
		t.Fatalf("error = %v, want the custom-schedule usage error", err)
	}
	if writes.Load() != 0 {
		t.Errorf("writes = %d, want none", writes.Load())
	}
}

// Splitting at the first occurrence destroys the old parent, so there is no retained
// occurrence to overlap and the replacement can move before the old first day.
func TestEventsAllowMovingAFirstOccurrenceAndItsFutureEarlier(t *testing.T) {
	handler, writes := occurrenceServer(t, "2026-09-01",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`, `{}`, func(t *testing.T, form url.Values) {
			if got := form.Get("calendar_event[starts_at]"); got != "2026-08-31" {
				t.Errorf("starts_at = %q, want 2026-08-31", got)
			}
		})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-01", "--apply-to", "future",
		"--starts-on", "2026-08-31", "--ends-on", "2026-08-31", "--repeat", "every_week", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
	}
}

// HEY removes the selected occurrence before it starts the replacement, so an earlier
// time is valid when the occurrence the parent keeps has already ended.
func TestEventsAllowAFutureSeriesStartingEarlierOnTheSelectedDay(t *testing.T) {
	handler, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`, `{}`, func(t *testing.T, form url.Values) {
			if got := form.Get("calendar_event[starts_at_time]"); got != "11:30:00" {
				t.Errorf("starts_at_time = %q, want 11:30:00", got)
			}
			if got := form.Get("apply_to_future"); got != "1" {
				t.Errorf("apply_to_future = %q, want 1", got)
			}
		})
	_, err := runJSONCommand(t, handler,
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future",
		"--start-time", "11:30", "--end-time", "12:30", "--repeat", "every_week", "--allow-plain-notes")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
	}
}

// A replacement recurrence must reach at least its first day. Otherwise HEY accepts the
// write as a one-off event and truncates the old series behind it.
func TestEventsRefuseARepeatEndBeforeTheFutureSeriesStarts(t *testing.T) {
	tests := []struct {
		name     string
		startsOn string
		until    string
	}{
		{name: "occurrence day", until: "2026-09-14"},
		{name: "moved day", startsOn: "2026-10-01", until: "2026-09-30"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, writes := occurrenceServer(t, "2026-09-15",
				`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`, "", func(t *testing.T, form url.Values) {
					t.Error("wrote the invalid replacement series")
				})
			args := []string{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat", "every_week"}
			if tt.startsOn != "" {
				args = append(args, "--starts-on", tt.startsOn, "--ends-on", tt.startsOn)
			}
			args = append(args, "--repeat-until", tt.until, "--allow-plain-notes")
			_, err := runJSONCommand(t, handler, args...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "repeat-until") || !strings.Contains(cliErr.Message, "before starts-on") {
				t.Fatalf("error = %v, want the recurrence-boundary usage error", err)
			}
			if writes.Load() != 0 {
				t.Errorf("writes = %d, want none", writes.Load())
			}
		})
	}

	t.Run("new event before calendar discovery", func(t *testing.T) {
		var requests atomic.Int32
		_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}), "event", "add", "Standup", "--starts-on", "2026-09-15", "--repeat", "every_week", "--repeat-until", "2026-09-14")
		var cliErr *apierr.Error
		if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "before starts-on") {
			t.Fatalf("error = %v, want the recurrence-boundary usage error", err)
		}
		if requests.Load() != 0 {
			t.Errorf("requests = %d, want none before calendar discovery", requests.Load())
		}
	})
}

// The day and the week say which identifiers an occurrence carries: a virtual id names the
// series, while a written-out day's id and recording_id name its own event route.
func TestEventsPeriodHelpNamesOccurrenceIdentifiers(t *testing.T) {
	for _, command := range []*eventsPeriodCommand{newEventsDayCommand(), newEventsWeekCommand()} {
		for _, want := range []string{"parent_id", "recording_id", "occurrence_id", "own event id"} {
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
			// Haystack builds an occurrence's end as its projected start plus the
			// series' elapsed duration. It deliberately becomes 04:30 EDT rather than
			// preserving the parent's 03:30 wall clock across spring-forward.
			name:   "elapsed duration across spring-forward matches HEY",
			series: timed("2026-03-01T06:30:00Z", "2026-03-01T08:30:00Z", "America/New_York"),
			day:    "2026-03-08", start: "2026-03-08T06:30:00Z", end: "2026-03-08T08:30:00Z",
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
// "4 weeks before" and one day is "0 months before". The recording's full span validates
// that lossy label; the exact interval is reconstructed from its start and the beginning of
// the event's day.
func TestCountdownFromRecording(t *testing.T) {
	const month = time.Duration(hey.CountdownUnitMonths) * time.Second
	span := func(interval, elapsed time.Duration) (time.Time, time.Time) {
		beginning := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		return beginning.Add(-interval), beginning.Add(elapsed)
	}
	tests := []struct {
		name     string
		label    string
		interval time.Duration
		elapsed  time.Duration
		value    int
		unit     hey.CountdownUnit
		invalid  bool
	}{
		{name: "weeks at midnight", label: "3 weeks before", interval: 21 * 24 * time.Hour, value: 3, unit: hey.CountdownUnitWeeks},
		{name: "weeks on a timed event", label: "3 weeks before", interval: 21 * 24 * time.Hour, elapsed: 12 * time.Hour, value: 3, unit: hey.CountdownUnitWeeks},
		{name: "one day on a timed event", label: "1 days before", interval: 24 * time.Hour, elapsed: 12 * time.Hour, value: 1, unit: hey.CountdownUnitDays},
		{name: "days", label: "2 days before", interval: 2 * 24 * time.Hour, elapsed: 12 * time.Hour, value: 2, unit: hey.CountdownUnitDays},
		{name: "months", label: "6 months before", interval: 6 * month, elapsed: 9 * time.Hour, value: 6, unit: hey.CountdownUnitMonths},
		{name: "eight days labelled a week", label: "1 weeks before", interval: 8 * 24 * time.Hour, value: 8, unit: hey.CountdownUnitDays},
		{name: "29 days labelled four weeks", label: "4 weeks before", interval: 29 * 24 * time.Hour, value: 29, unit: hey.CountdownUnitDays},
		{name: "30 days at midnight", label: "30 days before", interval: 30 * 24 * time.Hour, value: 30, unit: hey.CountdownUnitDays},
		{name: "one day on an all-day event", label: "0 months before", interval: 24 * time.Hour, value: 1, unit: hey.CountdownUnitDays},
		{name: "a month the label settles", label: "1 months before", interval: month, elapsed: 3 * time.Hour, value: 1, unit: hey.CountdownUnitMonths},
		{name: "missing span", label: "2 weeks before", invalid: true},
		{name: "largest accepted label", label: "30 days before", interval: 30 * 24 * time.Hour, value: 30, unit: hey.CountdownUnitDays},
		{name: "label above the form limit", label: "31 days before", interval: 24 * time.Hour, invalid: true},
		{name: "overflowing label", label: "999999999999999999999999 days before", interval: 24 * time.Hour, invalid: true},
		{name: "span above the form limit", label: "30 days before", interval: 31 * 24 * time.Hour, invalid: true},
		{name: "zero with an unexplained span", label: "0 months before", interval: 20 * time.Hour, invalid: true},
		{name: "zero with no span", label: "0 months before", invalid: true},
		{name: "no countdown", label: "Countdown", interval: 24 * time.Hour, invalid: true},
		{name: "empty", label: "", interval: 24 * time.Hour, invalid: true},
		{name: "unknown unit", label: "3 fortnights before", interval: 42 * 24 * time.Hour, invalid: true},
		{name: "words", label: "three weeks before", interval: 21 * 24 * time.Hour, invalid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recording := generated.Recording{Type: recordingTypeCountdown, Label: tt.label}
			if tt.interval > 0 {
				recording.StartsAt, recording.EndsAt = span(tt.interval, tt.elapsed)
			}
			event := generated.Recording{StartsAt: recording.EndsAt}
			countdown, err := countdownFromRecording(recording, event)
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

	t.Run("equal and reversed timestamps", func(t *testing.T) {
		end := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		for name, start := range map[string]time.Time{
			"equal":    end,
			"reversed": end.Add(time.Hour),
		} {
			t.Run(name, func(t *testing.T) {
				countdown := generated.Recording{Label: "2 weeks before", StartsAt: start, EndsAt: end}
				if got, err := countdownFromRecording(countdown, generated.Recording{StartsAt: end}); err == nil {
					t.Fatalf("countdownFromRecording() = %+v, want an error", got)
				}
			})
		}
	})
}

// A fall-back day can run for 25 hours. HEY starts a countdown from the beginning of the
// event's local day, so that extra hour belongs to the clock time, not to the countdown.
func TestCountdownFromRecordingSubtractsALongLocalDay(t *testing.T) {
	end := time.Date(2026, 11, 2, 4, 30, 0, 0, time.UTC)
	tests := []struct {
		name, label string
		start       time.Time
		want        hey.CountdownParams
	}{
		{
			name:  "weeks",
			label: "22 days before",
			start: time.Date(2026, 10, 11, 4, 0, 0, 0, time.UTC),
			want:  hey.CountdownParams{Value: 3, Unit: hey.CountdownUnitWeeks},
		},
		{
			name:  "month with an above-range label",
			label: "31 days before",
			start: time.Date(2026, 10, 1, 17, 30, 54, 0, time.UTC),
			want:  hey.CountdownParams{Value: 1, Unit: hey.CountdownUnitMonths},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			countdown := generated.Recording{Label: tt.label, StartsAt: tt.start, EndsAt: end}
			event := generated.Recording{StartsAt: end, StartsAtTimeZone: "America/New_York"}
			got, err := countdownFromRecording(countdown, event)
			if err != nil {
				t.Fatalf("countdownFromRecording: %v", err)
			}
			if got != tt.want {
				t.Errorf("countdown = %+v, want %+v", got, tt.want)
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
		"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future", "--repeat", "every_week", "--title", "Design review (moved)", "--allow-plain-notes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeAPI || !strings.Contains(cliErr.Message, `"0 months before"`) {
		t.Fatalf("error = %v, want the countdown refusal", err)
	}
	if writes.Load() != 0 {
		t.Errorf("writes = %d, want none", writes.Load())
	}
}

// A one-day countdown on an all-day series is "0 months before" with a span of exactly a
// day, and a future split copies it as the day it is.
func TestEventsEditOccurrenceKeepsAOneDayCountdown(t *testing.T) {
	allDay := `{"id":4821,"type":"Calendar::Event","title":"Sarah's birthday","recurring":true,"all_day":true,` +
		`"starts_at":"2026-09-01T00:00:00Z","ends_at":"2026-09-01T00:00:00Z",` +
		`"recurrence_schedule":{"kind":"every_year","preset":true},"calendar":{"id":9,"name":"Work"}}`
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
		"event", "edit", "4821", "--occurrence", "4821_2027-09-01", "--apply-to", "future", "--repeat", "every_year", "--title", "Sarah's birthday (party)")
	if err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
}

// occurrenceDeleteServer answers what an occurrence delete reads and writes: the calendars
// and the occurrence's own day, for a future delete's boundary check, and the delete itself,
// whose apply_to_future it records. Anything else is an error, so every request is accounted
// for.
func occurrenceDeleteServer(t *testing.T, date, day string, status int) (http.Handler, *[]string) {
	t.Helper()
	var deletes []string
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/calendars.json":
			_, _ = io.WriteString(w, oneCalendarJSON)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars/9/recordings.json" && r.URL.Query().Get("starts_on") == date:
			_, _ = io.WriteString(w, day)
		case r.Method == http.MethodDelete && r.URL.Path == "/calendar/events/4821/occurrences/"+date+".json":
			deletes = append(deletes, r.URL.Query().Get("apply_to_future"))
			w.WriteHeader(status)
			if status >= http.StatusBadRequest {
				_, _ = io.WriteString(w, `{"status":404,"error":"Not Found"}`)
			}
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}), &deletes
}

// The one that matters: one day of the series is deleted through the occurrence route with
// apply_to_future off, which is what writes the day into the series' exceptions. Deleting
// that day's own id would not: HEY would draw it again from the series. A current delete
// has nothing to read first.
func TestEventsDeleteOccurrenceCurrentDeletesThatDayAlone(t *testing.T) {
	handler, deletes := occurrenceDeleteServer(t, "2026-09-15", "", http.StatusNoContent)
	response, err := runJSONCommand(t, handler,
		"event", "delete", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current")
	if err != nil {
		t.Fatalf("execute occurrence delete: %v", err)
	}
	if len(*deletes) != 1 || (*deletes)[0] != "false" {
		t.Errorf("apply_to_future = %v, want one delete of that day alone", *deletes)
	}
	if response.Summary != "Occurrence deleted" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["occurrence_id"] != "4821_2026-09-15" || data["apply_to"] != "current" {
		t.Errorf("structured data = %#v, want the occurrence and its scope", response.Data)
	}
}

// The wider scope is the same delete with apply_to_future on: that day and every one after
// it. It reads the day first, for the boundary check a future edit makes, and a virtual day
// passes it.
func TestEventsDeleteOccurrenceFutureDeletesTheDaysFromThisOneOn(t *testing.T) {
	handler, deletes := occurrenceDeleteServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`, http.StatusNoContent)
	response, err := runJSONCommand(t, handler,
		"event", "delete", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future")
	if err != nil {
		t.Fatalf("execute occurrence delete: %v", err)
	}
	if len(*deletes) != 1 || (*deletes)[0] != "true" {
		t.Errorf("apply_to_future = %v, want one delete of this day and the following", *deletes)
	}
	if response.Summary != "Occurrence and the following deleted" {
		t.Errorf("summary = %q", response.Summary)
	}
}

// A future delete from a written-out day has the boundary problem a future edit has: HEY
// stops the series at the day's occurrence date but cancels the written-out days from the
// day's actual start. A day moved off its series time, or any written-out day of an opaque
// custom schedule, is refused before anything is deleted.
func TestEventsDeleteOccurrenceFutureRefusesAnUntrustworthyBoundary(t *testing.T) {
	moved := `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-09-15",` +
		`"title":"Design review","starts_at":"2026-09-18T12:00:00Z","ends_at":"2026-09-18T13:00:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb","calendar":{"id":9,"name":"Work"}}`
	inPlace := `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-09-15",` +
		`"title":"Design review","starts_at":"2026-09-15T12:00:00Z","ends_at":"2026-09-15T13:00:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb","calendar":{"id":9,"name":"Work"}}`
	custom := strings.Replace(occurrenceSeriesJSON,
		`"recurrence_schedule":{"kind":"every_week","preset":true}`,
		`"recurrence_schedule":{"kind":"custom","preset":false}`, 1)
	for _, tt := range []struct {
		name, day, want string
	}{
		{name: "moved", day: `{"Calendar::Event":[` + occurrenceSeriesJSON + `,` + moved + `]}`, want: "was moved"},
		{name: "custom", day: `{"Calendar::Event":[` + custom + `,` + inPlace + `]}`, want: "opaque custom schedule"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler, deletes := occurrenceDeleteServer(t, "2026-09-15", tt.day, http.StatusNoContent)
			_, err := runJSONCommand(t, handler,
				"event", "delete", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "future")
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage ||
				!strings.Contains(cliErr.Message, tt.want) || !strings.Contains(cliErr.Message, "cannot be deleted") {
				t.Fatalf("error = %v, want a usage refusal saying %q", err, tt.want)
			}
			if !strings.Contains(cliErr.Hint, "--apply-to current") {
				t.Errorf("hint = %q, want the way round", cliErr.Hint)
			}
			if len(*deletes) != 0 {
				t.Errorf("deletes = %v, want none", *deletes)
			}
		})
	}
}

// Everything the flags get wrong is refused before a request is made, as for an edit, with
// the hints naming the delete.
func TestEventsDeleteOccurrenceRefusesWhatItCannotMean(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "apply-to without occurrence", args: []string{"--apply-to", "current"}, want: "--apply-to needs --occurrence"},
		{name: "occurrence without apply-to", args: []string{"--occurrence", "4821_2026-09-15"}, want: "--apply-to is required with --occurrence"},
		{name: "occurrence given empty", args: []string{"--occurrence=", "--apply-to", "current"}, want: "--occurrence needs an occurrence_id"},
		{name: "unknown scope", args: []string{"--occurrence", "4821_2026-09-15", "--apply-to", "all"}, want: "invalid apply-to: all"},
		{name: "not an occurrence id", args: []string{"--occurrence", "4821-2026-09-15", "--apply-to", "current"}, want: "invalid occurrence: 4821-2026-09-15"},
		{name: "other series", args: []string{"--occurrence", "4822_2026-09-15", "--apply-to", "current"}, want: "occurrence 4822_2026-09-15 belongs to series 4822, not 4821"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request = %s %s", r.Method, r.URL)
				http.NotFound(w, r)
			})
			_, err := runJSONCommand(t, handler, append([]string{"event", "delete", "4821"}, tt.args...)...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || cliErr.Message != tt.want {
				t.Fatalf("error = %v, want usage %q", err, tt.want)
			}
			if strings.Contains(cliErr.Hint, "hey event edit") {
				t.Errorf("hint = %q, want it to name the delete", cliErr.Hint)
			}
		})
	}
}

// A hint that names a command names one that runs: the series the occurrence belongs to,
// with the scope that was given, since --apply-to is required.
func TestEventsOccurrenceHintsCarryTheScope(t *testing.T) {
	for _, tt := range []struct {
		command, scope string
	}{
		{command: "delete", scope: "current"},
		{command: "delete", scope: "future"},
		{command: "edit", scope: "current"},
	} {
		t.Run(tt.command+" "+tt.scope, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request = %s %s", r.Method, r.URL)
				http.NotFound(w, r)
			})
			_, err := runJSONCommand(t, handler,
				"event", tt.command, "4821", "--occurrence", "4822_2026-09-15", "--apply-to", tt.scope)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage {
				t.Fatalf("error = %v, want a usage refusal", err)
			}
			if want := "hey event " + tt.command + " 4822 --occurrence 4822_2026-09-15 --apply-to " + tt.scope; cliErr.Hint != want {
				t.Errorf("hint = %q, want %q", cliErr.Hint, want)
			}
		})
	}

	// A series id that names a day HEY wrote out is found on the day's read, and the hint
	// points at the series with the same scope.
	realized := `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-09-15",` +
		`"title":"Design review","starts_at":"2026-09-15T12:00:00Z","ends_at":"2026-09-15T13:00:00Z","calendar":{"id":9,"name":"Work"}}`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/calendars.json":
			_, _ = io.WriteString(w, oneCalendarJSON)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars/9/recordings.json":
			_, _ = io.WriteString(w, `{"Calendar::Event":[`+occurrenceSeriesJSON+`,`+realized+`]}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	_, err := runJSONCommand(t, handler,
		"event", "delete", "9001", "--occurrence", "9001_2026-09-15", "--apply-to", "future")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage {
		t.Fatalf("error = %v, want a usage refusal", err)
	}
	if want := "hey event delete 4821 --occurrence 4821_2026-09-15 --apply-to future"; cliErr.Hint != want {
		t.Errorf("hint = %q, want %q", cliErr.Hint, want)
	}
}

// HEY's own refusal — a date that is not a day of the series, or a series the caller cannot
// delete from, both 404 on the occurrence route — reaches the caller as not-found.
func TestEventsDeleteOccurrenceReportsHEYsRefusal(t *testing.T) {
	handler, _ := occurrenceDeleteServer(t, "2026-09-16", "", http.StatusNotFound)
	_, err := runJSONCommand(t, handler,
		"event", "delete", "4821", "--occurrence", "4821_2026-09-16", "--apply-to", "current")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeNotFound {
		t.Fatalf("error = %v, want not-found", err)
	}
}
