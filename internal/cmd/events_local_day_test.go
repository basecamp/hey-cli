package cmd

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	localDayDinner    = `{"id":4821,"title":"Late dinner with Priya Patel","starts_at":"2026-09-21T00:50:00Z","ends_at":"2026-09-21T03:00:00Z","type":"Calendar::Event","calendar":{"id":9102,"name":"Personal"}}`
	localDayConcert   = `{"id":4822,"title":"Evening concert","starts_at":"2026-09-18T22:00:00Z","ends_at":"2026-09-19T01:30:00Z","type":"Calendar::Event","calendar":{"id":9102,"name":"Personal"}}`
	localDayBreakfast = `{"id":4824,"title":"Breakfast with Maria Lopez","starts_at":"2026-09-20T13:00:00Z","ends_at":"2026-09-20T14:00:00Z","type":"Calendar::Event","calendar":{"id":9102,"name":"Personal"}}`
)

func localDayJSON(date string, events ...string) string {
	return localDayJSONWith(date, strings.Join(events, ","), "")
}

func localDayJSONWith(date, events, countdowns string) string {
	body := `{"kind":"day","starts_at":"` + date + `T00:00:00Z","ends_at":"` + date + `T23:59:59Z","recordings":{"Calendar::Event":[` + events + `]`
	if countdowns != "" {
		body += `,"Calendar::Countdown":[` + countdowns + `]`
	}
	return body + `}}`
}

func localWeekJSON(first, last string, events ...string) string {
	return `{"kind":"week","starts_at":"` + first + `T00:00:00Z","ends_at":"` + last + `T23:59:59Z","recordings":{"Calendar::Event":[` + strings.Join(events, ",") + `]}}`
}

type localDayServer struct {
	zone            string
	identityStatus  int
	paths           map[string]string
	mu              sync.Mutex
	identityReads   int
	requestedPaths  []string
	listedRecording string
}

func (s *localDayServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	s.record(r)
	switch r.URL.Path {
	case "/identity.json":
		s.countIdentityRead()
		if s.identityStatus != 0 {
			http.Error(w, `{"error":"identity unavailable"}`, s.identityStatus)
			return
		}
		zone := "null"
		if s.zone != "" {
			zone = `"` + s.zone + `"`
		}
		_, _ = io.WriteString(w, `{"id":1,"name":"Jason Fried","time_zone":`+zone+`}`)
	case "/calendars.json":
		_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9102,"name":"Personal","owned":true,"personal":true}}]}`)
	case "/calendars/9102/recordings.json":
		_, _ = io.WriteString(w, s.listedRecording)
	default:
		if body, ok := s.paths[r.URL.Path]; ok {
			_, _ = io.WriteString(w, body)
			return
		}
		if date, ok := strings.CutPrefix(r.URL.Path, "/calendar/days/"); ok {
			_, _ = io.WriteString(w, localDayJSON(strings.TrimSuffix(date, ".json")))
			return
		}
		http.NotFound(w, r)
	}
}

func (s *localDayServer) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestedPaths = append(s.requestedPaths, r.URL.Path+"?"+r.URL.RawQuery)
}

func (s *localDayServer) countIdentityRead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identityReads++
}

func (s *localDayServer) requested() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requestedPaths...)
}

func (s *localDayServer) identityReadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.identityReads
}

func eventIDsOf(t *testing.T, data any) []float64 {
	t.Helper()
	rows, ok := data.([]any)
	if !ok {
		t.Fatalf("data = %#v, want a list", data)
	}
	ids := make([]float64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.(map[string]any)["id"].(float64))
	}
	return ids
}

func containsID(ids []float64, id float64) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func TestEventsDayKeepsTheEventsOfTheAccountsLocalDay(t *testing.T) {
	paths := map[string]string{
		"/calendar/days/2026-09-19.json": localDayJSON("2026-09-19", localDayConcert),
		"/calendar/days/2026-09-20.json": localDayJSON("2026-09-20", localDayBreakfast),
		"/calendar/days/2026-09-21.json": localDayJSON("2026-09-21", localDayDinner),
	}
	cases := []struct {
		date    string
		want    []float64
		wantNot []float64
	}{
		{date: "2026-09-20", want: []float64{4821, 4824}},
		{date: "2026-09-21", wantNot: []float64{4821}},
		{date: "2026-09-19", wantNot: []float64{4822}},
		{date: "2026-09-18", want: []float64{4822}},
	}
	for _, tc := range cases {
		t.Run(tc.date, func(t *testing.T) {
			server := &localDayServer{zone: "America/New_York", paths: paths}
			response, err := runJSONCommand(t, server, "event", "day", tc.date)
			if err != nil {
				t.Fatalf("event day %s: %v", tc.date, err)
			}
			ids := eventIDsOf(t, response.Data)
			for _, id := range tc.want {
				if !containsID(ids, id) {
					t.Errorf("event %.0f missing from %v", id, ids)
				}
			}
			for _, id := range tc.wantNot {
				if containsID(ids, id) {
					t.Errorf("event %.0f must not be on %s: %v", id, tc.date, ids)
				}
			}
		})
	}
}

func TestEventsDayCutsDaysInTheAccountZoneNotTheMachines(t *testing.T) {
	inLocalZone(t, 9)
	server := &localDayServer{zone: "America/New_York", paths: map[string]string{
		"/calendar/days/2026-09-21.json": localDayJSON("2026-09-21", localDayDinner),
	}}
	response, err := runJSONCommand(t, server, "event", "day", "2026-09-20")
	if err != nil {
		t.Fatalf("event day: %v", err)
	}
	if !containsID(eventIDsOf(t, response.Data), 4821) {
		t.Errorf("the dinner is on the account's 2026-09-20: %#v", response.Data)
	}
}

func TestEventsWeekPlacesTheEveningDinnerInTheLocalWeek(t *testing.T) {
	server := &localDayServer{zone: "America/New_York", paths: map[string]string{
		"/calendar/weeks/2026-09-14.json": localWeekJSON("2026-09-14", "2026-09-20", localDayBreakfast),
		"/calendar/days/2026-09-21.json":  localDayJSON("2026-09-21", localDayDinner),
	}}
	response, err := runJSONCommand(t, server, "event", "week", "2026-09-14")
	if err != nil {
		t.Fatalf("event week: %v", err)
	}
	if ids := eventIDsOf(t, response.Data); !containsID(ids, 4821) || !containsID(ids, 4824) {
		t.Errorf("week ids = %v, want the dinner and breakfast", ids)
	}
}

func TestEventsWeekDropsAnEventThatFallsInTheNextLocalWeek(t *testing.T) {
	lateSunday := `{"id":4830,"title":"Sunday night film","starts_at":"2026-09-21T03:30:00Z","ends_at":"2026-09-21T05:30:00Z","type":"Calendar::Event"}`
	nextMonday := `{"id":4831,"title":"Monday standup","starts_at":"2026-09-21T14:00:00Z","ends_at":"2026-09-21T14:30:00Z","type":"Calendar::Event"}`
	server := &localDayServer{zone: "America/New_York", paths: map[string]string{
		"/calendar/weeks/2026-09-14.json": localWeekJSON("2026-09-14", "2026-09-20"),
		"/calendar/days/2026-09-21.json":  localDayJSON("2026-09-21", lateSunday, nextMonday),
	}}
	response, err := runJSONCommand(t, server, "event", "week", "2026-09-14")
	if err != nil {
		t.Fatalf("event week: %v", err)
	}
	ids := eventIDsOf(t, response.Data)
	if !containsID(ids, 4830) || containsID(ids, 4831) {
		t.Errorf("week ids = %v, want the Sunday film and not Monday's standup", ids)
	}
}

func TestEventsDayKeepsTheCountdownOfAnEventFromTheNeighbouringDay(t *testing.T) {
	countdown := `{"id":4840,"parent_id":4821,"label":"1 day before","starts_at":"2026-09-20T00:00:00Z","ends_at":"2026-09-21T00:00:00Z","type":"Calendar::Countdown"}`
	server := &localDayServer{zone: "America/New_York", paths: map[string]string{
		"/calendar/days/2026-09-21.json": localDayJSONWith("2026-09-21", localDayDinner, countdown),
	}}
	response, err := runJSONCommand(t, server, "event", "day", "2026-09-20")
	if err != nil {
		t.Fatalf("event day: %v", err)
	}
	rows := response.Data.([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %#v, want the dinner", rows)
	}
	labelled, ok := rows[0].(map[string]any)["countdown"].(map[string]any)
	if !ok || labelled["label"] != "1 day before" {
		t.Errorf("countdown = %#v, want the neighbour's countdown", rows[0])
	}
}

func TestEventsDayWithoutAnAccountZoneUsesTheMachinesDaysAndSaysSoOnce(t *testing.T) {
	inLocalZone(t, -4)
	previousClock := clockNow
	clockNow = func() time.Time { return time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { clockNow = previousClock })

	server := &localDayServer{paths: map[string]string{
		"/calendar/days/2026-09-21.json": localDayJSON("2026-09-21", localDayDinner),
	}}
	stdout, stderr, err := runFormattedCommandWithStderr(t, server, []string{"--json"}, "event", "day")
	if err != nil {
		t.Fatalf("event day: %v", err)
	}
	if !strings.Contains(stdout, "4821") {
		t.Errorf("stdout = %s, want the dinner", stdout)
	}
	if got := strings.Count(stderr, "Notice:"); got != 1 {
		t.Errorf("stderr has %d notices, want one: %q", got, stderr)
	}
	if reads := server.identityReadCount(); reads != 1 {
		t.Errorf("identity read %d times, want once", reads)
	}
}

func TestEventsDayWithADateAndNoAccountZoneSaysSo(t *testing.T) {
	inLocalZone(t, -4)
	server := &localDayServer{paths: map[string]string{
		"/calendar/days/2026-09-21.json": localDayJSON("2026-09-21", localDayDinner),
	}}
	stdout, stderr, err := runFormattedCommandWithStderr(t, server, []string{"--json"}, "event", "day", "2026-09-20")
	if err != nil {
		t.Fatalf("event day: %v", err)
	}
	if !strings.Contains(stdout, "4821") {
		t.Errorf("stdout = %s, want the dinner", stdout)
	}
	if !strings.Contains(stderr, "so days are this machine's days") || strings.Count(stderr, "Notice:") != 1 {
		t.Errorf("stderr = %q, want one notice", stderr)
	}
}

func TestEventsDayRefusesWhenTheIdentityCannotBeRead(t *testing.T) {
	server := &localDayServer{identityStatus: http.StatusInternalServerError}
	if _, err := runJSONCommand(t, server, "event", "day", "2026-09-20"); err == nil {
		t.Error("an unreadable identity must fail the read")
	}
}

func TestEventsDayFailsWhenANeighbouringDayCannotBeRead(t *testing.T) {
	server := &localDayServer{zone: "UTC"}
	failing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/calendar/days/2026-09-21.json" {
			http.Error(w, `{"error":"unavailable"}`, http.StatusInternalServerError)
			return
		}
		server.ServeHTTP(w, r)
	})
	if _, err := runJSONCommand(t, failing, "event", "day", "2026-09-20"); err == nil {
		t.Error("a failed neighbouring read must fail the day")
	}
}

const (
	localDaySeries     = `{"id":204,"title":"Weekly planning","recurring":true,"starts_at":"2026-01-05T14:00:00Z","ends_at":"2026-01-05T15:00:00Z","type":"Calendar::Event"}`
	localDayAllDay     = `{"id":4825,"title":"Maria Lopez's birthday","all_day":true,"starts_at":"2026-09-20T00:00:00Z","ends_at":"2026-09-20T00:00:00Z","type":"Calendar::Event"}`
	localDayNextAllDay = `{"id":4826,"title":"Daylight saving ends","all_day":true,"starts_at":"2026-11-01T00:00:00Z","ends_at":"2026-11-01T00:00:00Z","type":"Calendar::Event"}`
	localDayRecordings = `{"Calendar::Event":[` + localDayDinner + `,` + localDayConcert + `,` + localDayBreakfast + `,` + localDaySeries + `,` + localDayAllDay + `,` + localDayNextAllDay + `]}`
)

func TestEventsListKeepsTheEventsOfTheLocalDays(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    []float64
		wantNot []float64
	}{
		{
			name:    "one day",
			args:    []string{"--starts-on", "2026-09-20", "--ends-on", "2026-09-20"},
			want:    []float64{4821, 4824, 204, 4825},
			wantNot: []float64{4822, 4826},
		},
		{
			name:    "the day after leaves the evening dinner behind",
			args:    []string{"--starts-on", "2026-09-21", "--ends-on", "2026-09-21"},
			want:    []float64{204},
			wantNot: []float64{4821, 4822, 4825},
		},
		{
			name:    "ends-on is inclusive",
			args:    []string{"--starts-on", "2026-09-19", "--ends-on", "2026-09-20"},
			want:    []float64{4821, 4824, 4825},
			wantNot: []float64{4822, 4826},
		},
		{
			name: "the concert is on the day it started",
			args: []string{"--starts-on", "2026-09-18", "--ends-on", "2026-09-18"},
			want: []float64{4822},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := &localDayServer{zone: "America/New_York", listedRecording: localDayRecordings}
			args := append([]string{"event", "list", "--calendar", "9102"}, tc.args...)
			response, err := runJSONCommand(t, server, args...)
			if err != nil {
				t.Fatalf("event list: %v", err)
			}
			ids := eventIDsOf(t, response.Data)
			for _, id := range tc.want {
				if !containsID(ids, id) {
					t.Errorf("event %.0f missing from %v", id, ids)
				}
			}
			for _, id := range tc.wantNot {
				if containsID(ids, id) {
					t.Errorf("event %.0f must not be listed: %v", id, ids)
				}
			}
		})
	}
}

func TestEventsListSummaryNamesTheDatesGiven(t *testing.T) {
	server := &localDayServer{zone: "America/New_York", listedRecording: localDayRecordings}
	response, err := runJSONCommand(t, server, "event", "list", "--calendar", "9102", "--starts-on", "2026-09-20", "--ends-on", "2026-09-20")
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	if !strings.Contains(response.Summary, "(2026-09-20 to 2026-09-20)") {
		t.Errorf("summary = %q, want the dates the reader gave", response.Summary)
	}
	wanted := "starts_on=2026-09-19&ends_on=2026-09-22"
	found := false
	for _, path := range server.requested() {
		if strings.Contains(path, "/calendars/9102/recordings.json") && strings.Contains(path, "ends_on=2026-09-22") && strings.Contains(path, "starts_on=2026-09-19") {
			found = true
		}
	}
	if !found {
		t.Errorf("requests = %v, want a read with %s", server.requestedPaths, wanted)
	}
}

func TestEventsEditFindsAnEveningEventByItsLocalDate(t *testing.T) {
	server := &localDayServer{zone: "America/New_York"}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/calendars/9102/recordings.json" {
			endsOn, err := time.Parse(dateLayout, r.URL.Query().Get("ends_on"))
			if err != nil {
				t.Errorf("ends_on = %q", r.URL.Query().Get("ends_on"))
			}
			w.Header().Set("Content-Type", "application/json")
			dinnerStart := time.Date(2026, 9, 21, 0, 50, 0, 0, time.UTC)
			if dinnerStart.After(endsOn) {
				_, _ = io.WriteString(w, `{"Calendar::Event":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{"Calendar::Event":[`+localDayDinner+`]}`)
			return
		}
		if r.Method == http.MethodPatch {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":4821,"title":"Late dinner with Priya Patel and Tom"}`)
			return
		}
		server.ServeHTTP(w, r)
	})

	_, err := runJSONCommand(t, handler, "event", "edit", "4821", "2026-09-20", "--title", "Late dinner with Priya Patel and Tom")
	if err != nil {
		t.Fatalf("event edit by the local date: %v", err)
	}
}
