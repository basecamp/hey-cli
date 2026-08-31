package cmd

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
