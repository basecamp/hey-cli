package cmd

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// A repeating event is one row on a calendar, so `hey event list` answers it on the day the
// series began. A day is HEY's own expansion: the occurrence falls on the day asked for,
// carrying that day's times, and everything that is not an event stays out of the answer.
// HEY serves the occurrence virtual — no id of its own, the series in parent_id — and the
// row resolves that to the series id, which is what edit and delete take.
func TestEventsDayExpandsRecurringEvents(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/calendar/days/2026-09-02.json" {
			t.Errorf("request = %s %s, want the day read", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"day","starts_at":"2026-09-02T00:00:00Z","ends_at":"2026-09-02T23:59:59Z","recordings":{`+
			`"Calendar::Event":[`+
			`{"id":301,"title":"Design review","starts_at":"2026-09-02T14:00:00Z","ends_at":"2026-09-02T15:00:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Work"}},`+
			`{"title":"Standup","starts_at":"2026-09-02T09:15:00Z","ends_at":"2026-09-02T09:30:00Z","type":"Calendar::Event","recurring":true,"parent_id":204,"occurrence_id":"204_2026-09-02","calendar":{"id":9,"name":"Work"}}`+
			`],`+
			`"Calendar::Habit":[{"id":11,"title":"Morning strength training"}],`+
			`"Calendar::Todo":[{"id":3,"title":"Send notes"}]}}`)
	}), "event", "day", "2026-09-02")
	if err != nil {
		t.Fatalf("execute event day: %v", err)
	}
	if response.Summary != "2 events (on 2026-09-02)" {
		t.Errorf("summary = %q", response.Summary)
	}
	events, ok := response.Data.([]any)
	if !ok || len(events) != 2 {
		t.Fatalf("data = %#v, want the two events and nothing else", response.Data)
	}
	first, ok := events[0].(map[string]any)
	if !ok || first["title"] != "Standup" {
		t.Errorf("first event = %#v, want the occurrence, in the order the day reads", events[0])
	}
	if first["occurrence_id"] != "204_2026-09-02" || first["starts_at"] != "2026-09-02T09:15:00Z" {
		t.Errorf("occurrence = %#v, want the day's own times", first)
	}
	if first["id"] != float64(204) {
		t.Errorf("occurrence id = %v, want the series, which is what edit and delete take", first["id"])
	}
	if _, ok := first["recording_id"]; ok {
		t.Errorf("recording_id = %v, want no own id for a virtual occurrence", first["recording_id"])
	}
}

// HEY serves a countdown beside its event, not on the event. Keep that separate
// recording in the event's JSON and styled row, without counting it as an event.
func TestEventsPeriodShowsTheEventsCountdown(t *testing.T) {
	for _, tt := range []struct{ name, path string }{
		{"day", "/calendar/days/2026-09-28.json"},
		{"week", "/calendar/weeks/2026-09-28.json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != tt.path {
					t.Errorf("request = %s %s, want %s", r.Method, r.URL.Path, tt.path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"kind":"`+tt.name+`","recordings":{`+
					`"Calendar::Event":[`+
					`{"id":301,"title":"Calendar countdown check-in","starts_at":"2026-09-28T19:00:00Z","ends_at":"2026-09-28T19:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Personal"}},`+
					`{"id":302,"title":"Project planning","starts_at":"2026-09-28T21:00:00Z","ends_at":"2026-09-28T21:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Personal"}}],`+
					`"Calendar::Countdown":[{"id":303,"parent_id":301,"label":"2 days before","starts_at":"2026-09-26T00:00:00Z","ends_at":"2026-09-28T00:00:00Z","type":"Calendar::Countdown"}]}}`)
			})

			response, err := runJSONCommand(t, handler, "event", tt.name, "2026-09-28")
			if err != nil {
				t.Fatalf("read %s: %v", tt.name, err)
			}
			rows, ok := response.Data.([]any)
			if !ok || len(rows) != 2 {
				t.Fatalf("rows = %#v, want two events", response.Data)
			}
			first := rows[0].(map[string]any)
			countdown, ok := first["countdown"].(map[string]any)
			if !ok || countdown["label"] != "2 days before" || countdown["starts_at"] != "2026-09-26T00:00:00Z" || countdown["ends_at"] != "2026-09-28T00:00:00Z" {
				t.Errorf("event countdown = %#v, want the served recording", first["countdown"])
			}
			if _, exists := rows[1].(map[string]any)["countdown"]; exists {
				t.Errorf("other event has a countdown: %#v", rows[1])
			}

			styled, err := runStyledCommand(t, handler, "event", tt.name, "2026-09-28")
			if err != nil {
				t.Fatalf("render %s: %v", tt.name, err)
			}
			if !strings.Contains(styled, "Countdown") || !strings.Contains(styled, "2 days before") {
				t.Errorf("styled event hides its countdown: %s", styled)
			}
		})
	}
}

// A countdown label comes from HEY, not the terminal. A table cell strips
// escapes before measuring or printing it.
func TestEventsPeriodStyledSanitizesCountdownLabel(t *testing.T) {
	styled, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"day","recordings":{`+
			`"Calendar::Event":[{"id":301,"title":"Design review","starts_at":"2026-09-28T19:00:00Z","ends_at":"2026-09-28T20:00:00Z","type":"Calendar::Event"}],`+
			`"Calendar::Countdown":[{"id":302,"parent_id":301,"label":"2 days\u001b[31m before","starts_at":"2026-09-26T00:00:00Z","ends_at":"2026-09-28T00:00:00Z","type":"Calendar::Countdown"}]}}`)
	}), "event", "day", "2026-09-28")
	if err != nil {
		t.Fatalf("render day: %v", err)
	}
	if strings.Contains(styled, "\x1b[31m") || !strings.Contains(styled, "2 days before") {
		t.Errorf("countdown label was not sanitized: %q", styled)
	}
}

// A recurring series can have several occurrences in a week but only one
// countdown recording. It belongs to the occurrence on the countdown's day,
// not every occurrence with the same series ID.
func TestEventsWeekDoesNotAttachOneCountdownToEveryOccurrence(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"week","recordings":{`+
			`"Calendar::Event":[`+
			`{"parent_id":204,"occurrence_id":"204_2026-09-28","title":"Morning standup","starts_at":"2026-09-28T14:00:00Z","ends_at":"2026-09-28T14:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Personal"}},`+
			`{"parent_id":204,"occurrence_id":"204_2026-09-29","title":"Morning standup","starts_at":"2026-09-29T14:00:00Z","ends_at":"2026-09-29T14:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Personal"}}],`+
			`"Calendar::Countdown":[{"id":205,"parent_id":204,"label":"2 days before","starts_at":"2026-09-26T00:00:00Z","ends_at":"2026-09-28T00:00:00Z","type":"Calendar::Countdown"}]}}`)
	}), "event", "week", "2026-09-28")
	if err != nil {
		t.Fatalf("read week: %v", err)
	}
	rows := response.Data.([]any)
	if got := rows[0].(map[string]any)["countdown"]; got == nil {
		t.Errorf("first occurrence's countdown = %v, want 2 days before", got)
	}
	if _, exists := rows[1].(map[string]any)["countdown"]; exists {
		t.Errorf("second occurrence borrowed the first's countdown: %#v", rows[1])
	}
}

// A countdown ending just after an earlier occurrence starts belongs to the
// next occurrence, never to the nearest event on the wrong side of its start.
func TestEventsWeekCountdownBelongsToItsLaterOccurrence(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"week","recordings":{`+
			`"Calendar::Event":[`+
			`{"parent_id":204,"occurrence_id":"204_2026-09-28","title":"Evening check-in","starts_at":"2026-09-28T23:00:00Z","ends_at":"2026-09-28T23:30:00Z","type":"Calendar::Event"},`+
			`{"parent_id":204,"occurrence_id":"204_2026-09-29","title":"Evening check-in","starts_at":"2026-09-29T15:00:00Z","ends_at":"2026-09-29T15:30:00Z","type":"Calendar::Event"}],`+
			`"Calendar::Countdown":[{"id":205,"parent_id":204,"label":"2 days before","starts_at":"2026-09-27T00:00:00Z","ends_at":"2026-09-29T00:00:00Z","type":"Calendar::Countdown"}]}}`)
	}), "event", "week", "2026-09-29")
	if err != nil {
		t.Fatalf("read week: %v", err)
	}
	rows := response.Data.([]any)
	if _, exists := rows[0].(map[string]any)["countdown"]; exists {
		t.Errorf("earlier occurrence borrowed tomorrow's countdown: %#v", rows[0])
	}
	if got := rows[1].(map[string]any)["countdown"].(map[string]any)["label"]; got != "2 days before" {
		t.Errorf("later occurrence countdown = %v", got)
	}
}

// An edited day can have its own countdown as well as the series countdown.
// The day's own value wins, regardless of the order HEY serves them in.
func TestEventsWeekPrefersRealizedDaysOwnCountdown(t *testing.T) {
	own := `{"id":205,"parent_id":9001,"label":"1 day before","starts_at":"2026-09-27T00:00:00Z","ends_at":"2026-09-28T00:00:00Z","type":"Calendar::Countdown"}`
	inherited := `{"id":206,"parent_id":204,"label":"2 days before","starts_at":"2026-09-26T00:00:00Z","ends_at":"2026-09-28T00:00:00Z","type":"Calendar::Countdown"}`
	for _, tt := range []struct{ name, countdowns string }{
		{"own first", own + "," + inherited},
		{"inherited first", inherited + "," + own},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"kind":"week","recordings":{`+
					`"Calendar::Event":[{"id":9001,"parent_id":204,"occurrence_id":"204_2026-09-28",`+
					`"title":"Design review","starts_at":"2026-09-28T19:00:00Z","ends_at":"2026-09-28T20:00:00Z","type":"Calendar::Event"}],`+
					`"Calendar::Countdown":[`+tt.countdowns+`]}}`)
			}), "event", "week", "2026-09-28")
			if err != nil {
				t.Fatalf("read week: %v", err)
			}
			rows := response.Data.([]any)
			if got := rows[0].(map[string]any)["countdown"].(map[string]any)["label"]; got != "1 day before" {
				t.Errorf("realized day countdown = %v, want its own", got)
			}
		})
	}
}

// The UTC date HEY sends for an all-day countdown belongs to the timed
// occurrence's UTC date, even when the event's own zone is already tomorrow.
func TestEventCountdownUsesHEYsUTCDate(t *testing.T) {
	rows := occurrenceEventRows([]generated.Recording{
		{ParentId: 204, StartsAt: time.Date(2026, 9, 27, 15, 30, 0, 0, time.UTC), StartsAtTimeZone: "Asia/Tokyo"},
		{ParentId: 204, StartsAt: time.Date(2026, 9, 28, 15, 30, 0, 0, time.UTC), StartsAtTimeZone: "Asia/Tokyo"},
	})
	attachEventCountdowns(rows, []generated.Recording{{
		ParentId: 204, Label: "2 days before",
		StartsAt: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC),
	}})
	if rows[0].Countdown == nil || rows[0].Countdown.Label != "2 days before" {
		t.Errorf("first occurrence = %+v, want its countdown", rows[0].Countdown)
	}
	if rows[1].Countdown != nil {
		t.Errorf("second occurrence borrowed the first countdown: %+v", rows[1].Countdown)
	}
}

func TestEventCountdownEndingAtTheEventStart(t *testing.T) {
	start := time.Date(2026, 9, 28, 19, 0, 0, 0, time.UTC)
	rows := eventRows([]generated.Recording{{Id: 301, StartsAt: start}})
	attachEventCountdowns(rows, []generated.Recording{{
		ParentId: 301, Label: "2 days before",
		StartsAt: start.AddDate(0, 0, -2), EndsAt: start,
	}})
	if rows[0].Countdown == nil || rows[0].Countdown.EndsAt != start {
		t.Errorf("countdown ending when the event starts = %+v, want the served time", rows[0].Countdown)
	}
}

func TestEventCountdownDoesNotInventMissingTimes(t *testing.T) {
	rows := eventRows([]generated.Recording{{Id: 301, StartsAt: time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)}})
	attachEventCountdowns(rows, []generated.Recording{{ParentId: 301, Label: "2 days before", EndsAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)}})
	if rows[0].Countdown != nil {
		t.Errorf("countdown with no start time = %+v, want none", rows[0].Countdown)
	}
}

// A day an earlier edit has written out has an id of its own, and HEY's event routes act
// on that id for that day alone. The published id keeps that established meaning while
// recording_id makes the distinction from a virtual occurrence explicit.
func TestEventsDayKeepsARealizedOccurrenceID(t *testing.T) {
	var deletedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/calendar/days/2026-09-15.json":
			_, _ = io.WriteString(w, `{"kind":"day","starts_at":"2026-09-15T00:00:00Z","ends_at":"2026-09-15T23:59:59Z","recordings":{`+
				`"Calendar::Event":[`+
				`{"id":9001,"parent_id":4821,"occurrence_id":"4821_2026-09-15","title":"Design review (with the vendor)","starts_at":"2026-09-15T13:30:00Z","ends_at":"2026-09-15T14:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Work"}}`+
				`]}}`)
		case r.Method == http.MethodDelete:
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("request = %s %s, want the day read or an event delete", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	response, err := runJSONCommand(t, handler, "event", "day", "2026-09-15")
	if err != nil {
		t.Fatalf("execute event day: %v", err)
	}
	events, ok := response.Data.([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("data = %#v, want the one event", response.Data)
	}
	row, ok := events[0].(map[string]any)
	if !ok || row["id"] != float64(9001) || row["recording_id"] != float64(9001) || row["parent_id"] != float64(4821) || row["occurrence_id"] != "4821_2026-09-15" {
		t.Errorf("row = %#v, want the day's own id and recording_id with its series in parent_id", events[0])
	}

	if _, err := runJSONCommand(t, handler, "event", "delete", fmt.Sprintf("%.0f", row["id"])); err != nil {
		t.Fatalf("delete the id from the day response: %v", err)
	}
	if deletedPath != "/calendar/events/9001.json" {
		t.Errorf("delete path = %q, want the realized day alone", deletedPath)
	}
}

// Styled period output carries every identifier needed to act on an occurrence. A moved
// realized day cannot reconstruct its occurrence id from the date drawn in the table, and
// its own recording id is what edits or deletes that day without the rest of the series.
func TestEventsPeriodStyledPublishesOccurrenceIdentifiers(t *testing.T) {
	for _, tt := range []struct {
		name, period, path string
	}{
		{name: "day", period: "day", path: "/calendar/days/2026-09-15.json"},
		{name: "week", period: "week", path: "/calendar/weeks/2026-09-15.json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			styled, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != tt.path {
					t.Errorf("request = %s %s, want %s", r.Method, r.URL.Path, tt.path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"kind":"`+tt.period+`","recordings":{"Calendar::Event":[`+
					`{"parent_id":204,"occurrence_id":"204_2026-09-15","title":"Standup","starts_at":"2026-09-15T09:15:00Z","ends_at":"2026-09-15T09:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Work"}},`+
					`{"id":9001,"parent_id":4821,"occurrence_id":"4821_2026-09-13","title":"Design review","starts_at":"2026-09-15T13:30:00Z","ends_at":"2026-09-15T14:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Work"}}]}}`)
			}), "event", tt.name, "2026-09-15")
			if err != nil {
				t.Fatalf("execute event %s: %v", tt.name, err)
			}
			for _, want := range []string{"Series ID", "Occurrence ID", "Recording ID", "204_2026-09-15", "4821_2026-09-13", "9001"} {
				if !strings.Contains(styled, want) {
					t.Errorf("styled output does not contain %q:\n%s", want, styled)
				}
			}
		})
	}
}

func TestEventsWeekReadsTheWeekPeriod(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/calendar/weeks/2026-09-02.json" {
			t.Errorf("request = %s %s, want the week read", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"week","starts_at":"2026-08-31T00:00:00Z","ends_at":"2026-09-06T23:59:59Z","recordings":{`+
			`"Calendar::Event":[`+
			`{"title":"Standup","starts_at":"2026-09-04T09:15:00Z","ends_at":"2026-09-04T09:30:00Z","type":"Calendar::Event","recurring":true,"parent_id":204,"occurrence_id":"204_2026-09-04","calendar":{"id":9,"name":"Work"}},`+
			`{"title":"Standup","starts_at":"2026-09-02T09:15:00Z","ends_at":"2026-09-02T09:30:00Z","type":"Calendar::Event","recurring":true,"parent_id":204,"occurrence_id":"204_2026-09-02","calendar":{"id":9,"name":"Work"}}`+
			`]}}`)
	}), "event", "week", "2026-09-02")
	if err != nil {
		t.Fatalf("execute event week: %v", err)
	}
	if response.Summary != "2 events (in the week of 2026-09-02)" {
		t.Errorf("summary = %q", response.Summary)
	}
	events, ok := response.Data.([]any)
	if !ok || len(events) != 2 {
		t.Fatalf("data = %#v, want both occurrences", response.Data)
	}
	first, ok := events[0].(map[string]any)
	if !ok || first["occurrence_id"] != "204_2026-09-02" {
		t.Errorf("first event = %#v, want the earlier occurrence first", events[0])
	}
}

// inLocalZone pins the process's local zone so the suite reads the same wherever it runs.
func inLocalZone(t *testing.T, offsetHours int) {
	t.Helper()
	local := time.Local
	time.Local = time.FixedZone(fmt.Sprintf("UTC%+d", offsetHours), offsetHours*60*60)
	t.Cleanup(func() { time.Local = local })
}

// The listing reads in the order HEY draws the span: each day's all-day band first, then
// the timed events by clock. HEY's JSON is always UTC, so east of Greenwich a 23:30Z event
// belongs to the next local day — stamped-midnight instants alone would draw it above that
// day's all-day band, and a small --limit could keep the wrong visual row.
func TestEventsDaySortsTheAllDayBandFirst(t *testing.T) {
	inLocalZone(t, 2)
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"day","starts_at":"2026-09-02T00:00:00Z","ends_at":"2026-09-02T23:59:59Z","recordings":{`+
			`"Calendar::Event":[`+
			`{"id":77,"title":"Late night sync","starts_at":"2026-09-01T23:30:00Z","ends_at":"2026-09-02T00:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Work"}},`+
			`{"id":88,"title":"Company holiday","all_day":true,"starts_at":"2026-09-02T00:00:00Z","ends_at":"2026-09-02T00:00:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Work"}}`+
			`]}}`)
	}), "event", "day", "2026-09-02")
	if err != nil {
		t.Fatalf("execute event day: %v", err)
	}
	events, ok := response.Data.([]any)
	if !ok || len(events) != 2 {
		t.Fatalf("data = %#v, want both events", response.Data)
	}
	first, ok := events[0].(map[string]any)
	if !ok || first["title"] != "Company holiday" {
		t.Errorf("first event = %#v, want the all-day band on top: both rows are the reader's 2026-09-02", events[0])
	}
}

// A styled boundary reads on the reader's clock — HEY's JSON is always UTC — while an
// all-day date is the day it names and does not shift.
func TestEventBoundaryDrawsTheReadersClock(t *testing.T) {
	inLocalZone(t, 2)
	if got := eventBoundary(time.Date(2026, 9, 2, 14, 0, 0, 0, time.UTC), false); got != "2026-09-02T16:00" {
		t.Errorf("timed boundary = %q, want the reader's 16:00", got)
	}
	if got := eventBoundary(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), true); got != "2026-09-02" {
		t.Errorf("all-day boundary = %q, want the unshifted day", got)
	}
}

// With no date the read asks HEY for "now", which the server resolves in the account's own
// time zone — the CLI process's clock could be a day off either way around midnight.
func TestEventsDayDefaultsToNow(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/calendar/days/now.json" {
			t.Errorf("request = %s %s, want the day read for now", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"day","starts_at":"2026-09-02T00:00:00Z","ends_at":"2026-09-02T23:59:59Z","recordings":{"Calendar::Event":[]}}`)
	}), "event", "day")
	if err != nil {
		t.Fatalf("execute event day: %v", err)
	}
	if response.Summary != "0 events (today)" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestEventsDayRejectsABadDate(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}), "event", "day", "next tuesday")
	if err == nil || !strings.Contains(err.Error(), "invalid date") {
		t.Fatalf("error = %v, want an invalid date", err)
	}
	if requests.Load() != 0 {
		t.Errorf("requests = %d, want 0", requests.Load())
	}
}

func TestEventsDayHonorsTheLimit(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"day","starts_at":"2026-09-02T00:00:00Z","ends_at":"2026-09-02T23:59:59Z","recordings":{`+
			`"Calendar::Event":[`+
			`{"id":301,"title":"Design review","starts_at":"2026-09-02T14:00:00Z","ends_at":"2026-09-02T15:00:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Work"}},`+
			`{"id":204,"title":"Standup","starts_at":"2026-09-02T09:15:00Z","ends_at":"2026-09-02T09:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Work"}}`+
			`]}}`)
	}), "event", "day", "2026-09-02", "--limit", "1")
	if err != nil {
		t.Fatalf("execute event day: %v", err)
	}
	events, ok := response.Data.([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("data = %#v, want the one earliest event", response.Data)
	}
	if response.Notice == "" {
		t.Error("notice = empty, want a truncation notice")
	}
}
