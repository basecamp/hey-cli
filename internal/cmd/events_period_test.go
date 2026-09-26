package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
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

// A day an earlier edit has written out has an id of its own, which the published id keeps
// while recording_id makes the distinction from a virtual occurrence explicit. That id edits
// the day alone, but it does not delete it: HEY would draw the day again from the series.
// The delete refuses it and names the occurrence delete, which is what removes the day.
func TestEventsDayKeepsARealizedOccurrenceID(t *testing.T) {
	const realized = `{"id":9001,"parent_id":4821,"occurrence_id":"4821_2026-09-15","title":"Design review (with the vendor)","starts_at":"2026-09-15T13:30:00Z","ends_at":"2026-09-15T14:30:00Z","type":"Calendar::Event","calendar":{"id":9,"name":"Work"}}`
	var deletedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/calendar/days/2026-09-15.json":
			_, _ = io.WriteString(w, `{"kind":"day","starts_at":"2026-09-15T00:00:00Z","ends_at":"2026-09-15T23:59:59Z","recordings":{`+
				`"Calendar::Event":[`+realized+`]}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars/9/recordings.json":
			_, _ = io.WriteString(w, `{"Calendar::Event":[`+realized+`]}`)
		case r.Method == http.MethodDelete:
			deletedPath = r.URL.Path + "?" + r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("request = %s %s, want the day read, the event looked for, or a delete", r.Method, r.URL.Path)
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

	_, err = runJSONCommand(t, handler, "event", "delete", fmt.Sprintf("%.0f", row["id"]))
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage {
		t.Fatalf("delete the id from the day response: error = %v, want a usage refusal", err)
	}
	if want := "hey event delete 4821 --occurrence 4821_2026-09-15 --apply-to current"; cliErr.Hint != want {
		t.Errorf("hint = %q, want %q", cliErr.Hint, want)
	}
	if deletedPath != "" {
		t.Fatalf("deleted %q, want nothing deleted by the day's own id", deletedPath)
	}

	if _, err := runJSONCommand(t, handler, "event", "delete", fmt.Sprintf("%.0f", row["parent_id"]),
		"--occurrence", row["occurrence_id"].(string), "--apply-to", "current"); err != nil {
		t.Fatalf("delete the day through its occurrence: %v", err)
	}
	if deletedPath != "/calendar/events/4821/occurrences/2026-09-15.json?apply_to_future=false" {
		t.Errorf("delete = %q, want the day deleted through its occurrence", deletedPath)
	}
}

// Styled period output carries every identifier needed to act on an occurrence. A moved
// realized day cannot reconstruct its occurrence id from the date drawn in the table, which
// is what deletes that day, and its own recording id is what edits it without the rest of
// the series.
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
