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
	localDayDinner    = `{"id":4821,"title":"Late dinner with Priya Patel","starts_at":"2026-09-21T00:50:00Z","ends_at":"2026-09-21T02:40:00Z","type":"Calendar::Event"}`
	localDayConcert   = `{"id":4822,"title":"Evening concert","starts_at":"2026-09-18T22:00:00Z","ends_at":"2026-09-19T01:30:00Z","type":"Calendar::Event"}`
	localDayBreakfast = `{"id":4824,"title":"Breakfast with Maria Lopez","starts_at":"2026-09-20T13:00:00Z","ends_at":"2026-09-20T14:00:00Z","type":"Calendar::Event"}`
	localDayCountdown = `{"id":4840,"parent_id":4821,"label":"1 day before","starts_at":"2026-09-20T00:00:00Z","ends_at":"2026-09-21T00:00:00Z","type":"Calendar::Countdown"}`
)

func localDayJSON(kind, first, last, events, countdowns string) string {
	return `{"kind":"` + kind + `","starts_at":"` + first + `T00:00:00Z","ends_at":"` + last + `T23:59:59Z","recordings":{"Calendar::Event":[` +
		events + `],"Calendar::Countdown":[` + countdowns + `]}}`
}

type localDayServer struct {
	zone       string
	paths      map[string]string
	recordings string
	failPath   string

	mu            sync.Mutex
	requested     []string
	identityReads int
}

func (s *localDayServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requested = append(s.requested, r.URL.Path+"?"+r.URL.RawQuery)
	if r.URL.Path == "/identity.json" {
		s.identityReads++
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	body, found := s.paths[r.URL.Path]
	switch {
	case r.URL.Path == s.failPath:
		http.Error(w, `{"error":"unavailable"}`, http.StatusInternalServerError)
	case r.URL.Path == "/identity.json":
		zone := "null"
		if s.zone != "" {
			zone = `"` + s.zone + `"`
		}
		_, _ = io.WriteString(w, `{"id":1,"name":"Jason Fried","time_zone":`+zone+`}`)
	case r.URL.Path == "/calendars.json":
		_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9102,"name":"Personal","owned":true,"personal":true}}]}`)
	case r.URL.Path == "/calendars/9102/recordings.json":
		_, _ = io.WriteString(w, s.recordings)
	case found:
		_, _ = io.WriteString(w, body)
	case strings.HasPrefix(r.URL.Path, "/calendar/days/"):
		date := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/calendar/days/"), ".json")
		_, _ = io.WriteString(w, localDayJSON("day", date, date, "", ""))
	default:
		http.NotFound(w, r)
	}
}

func (s *localDayServer) readWidened() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range s.requested {
		if strings.Contains(path, "starts_on=2026-09-19") && strings.Contains(path, "ends_on=2026-09-22") {
			return true
		}
	}
	return false
}

func runLocalDay(t *testing.T, server *localDayServer, args ...string) string {
	t.Helper()
	stdout, _, err := runFormattedCommandWithStderr(t, server, []string{"--json"}, args...)
	if err != nil {
		t.Fatalf("hey %s: %v", strings.Join(args, " "), err)
	}
	return stdout
}

func TestEventsDayKeepsTheEveningEventWithItsCountdown(t *testing.T) {
	server := &localDayServer{zone: "America/New_York", paths: map[string]string{
		"/calendar/days/2026-09-21.json": localDayJSON("day", "2026-09-21", "2026-09-21", localDayDinner, localDayCountdown),
	}}
	onTheEvening := runLocalDay(t, server, "event", "day", "2026-09-20")
	if !strings.Contains(onTheEvening, "Late dinner with Priya Patel") || !strings.Contains(onTheEvening, "1 day before") {
		t.Errorf("2026-09-20 = %s, want the dinner with its countdown", onTheEvening)
	}
	if onTheNextDay := runLocalDay(t, server, "event", "day", "2026-09-21"); strings.Contains(onTheNextDay, "Late dinner") {
		t.Errorf("2026-09-21 = %s, want no dinner", onTheNextDay)
	}
}

func TestEventsWeekKeepsAnEventFromTheNeighbourDay(t *testing.T) {
	server := &localDayServer{zone: "America/New_York", paths: map[string]string{
		"/calendar/weeks/2026-09-14.json": localDayJSON("week", "2026-09-14", "2026-09-20", localDayBreakfast, ""),
		"/calendar/days/2026-09-21.json":  localDayJSON("day", "2026-09-21", "2026-09-21", localDayDinner, ""),
	}}
	got := runLocalDay(t, server, "event", "week", "2026-09-14")
	if !strings.Contains(got, "Late dinner") || !strings.Contains(got, "Breakfast with Maria Lopez") {
		t.Errorf("week = %s, want both events", got)
	}
}

func TestEventsListWidensTheRequestAndDropsOtherDays(t *testing.T) {
	server := &localDayServer{zone: "America/New_York", recordings: `{"Calendar::Event":[` + localDayDinner + `,` + localDayConcert + `]}`}
	got := runLocalDay(t, server, "event", "list", "--calendar", "9102", "--starts-on", "2026-09-20", "--ends-on", "2026-09-20")
	if !server.readWidened() {
		t.Error("want the window widened by a day before and two days after")
	}
	if !strings.Contains(got, "Late dinner") || strings.Contains(got, "Evening concert") {
		t.Errorf("list = %s, want the dinner and not the concert", got)
	}
}

func TestEventsEditFindsAnEveningEventByItsLocalDate(t *testing.T) {
	server := &localDayServer{zone: "America/New_York", recordings: `{"Calendar::Event":[` + localDayDinner + `]}`}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":4821,"title":"Dinner with Priya Patel"}`)
			return
		}
		server.ServeHTTP(w, r)
	})
	if _, err := runJSONCommand(t, handler, "event", "edit", "4821", "2026-09-20", "--title", "Dinner with Priya Patel"); err != nil {
		t.Fatalf("event edit: %v", err)
	}
	if !server.readWidened() {
		t.Error("want the widened window")
	}
}

func TestEventsDayWithoutAnAccountZoneUsesTheMachinesZone(t *testing.T) {
	inLocalZone(t, -4)
	server := &localDayServer{paths: map[string]string{
		"/calendar/days/2026-09-21.json": localDayJSON("day", "2026-09-21", "2026-09-21", localDayDinner, ""),
	}}
	previousClock := clockNow
	clockNow = func() time.Time { return time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { clockNow = previousClock })

	stdout, stderr, err := runFormattedCommandWithStderr(t, server, []string{"--json"}, "event", "day")
	if err != nil || !strings.Contains(stdout, "Late dinner") {
		t.Fatalf("event day = %s, %v", stdout, err)
	}
	if strings.Count(stderr, "Notice:") != 1 || server.identityReads != 1 {
		t.Errorf("notices in %q, identity reads %d, want one of each", stderr, server.identityReads)
	}
}

func TestEventsDayFailsWhenTheZoneOrANeighbourCannotBeRead(t *testing.T) {
	for _, server := range []*localDayServer{
		{failPath: "/identity.json"},
		{zone: "UTC", failPath: "/calendar/days/2026-09-21.json"},
	} {
		if _, err := runJSONCommand(t, server, "event", "day", "2026-09-20"); err == nil {
			t.Errorf("a failed read of %s was ignored", server.failPath)
		}
	}
}
