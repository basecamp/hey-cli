package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func newYork(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("time zone database unavailable: %v", err)
	}
	return loc
}

func dayJSON(date string, events ...string) string {
	return `{"starts_at":"` + date + `T00:00:00Z","ends_at":"` + date + `T23:59:59Z","kind":"day","recordings":{"Calendar::Event":[` + strings.Join(events, ",") + `]}}`
}

func TestRequestRecordingsKeepsTheEventsOfTheLocalDay(t *testing.T) {
	zone := newYork(t)
	const dinner = `{"id":4821,"title":"Late dinner with Priya Patel","type":"CalendarEvent","starts_at":"2026-09-21T00:50:00Z","ends_at":"2026-09-21T02:40:00Z"}`
	const concert = `{"id":4822,"title":"Evening concert","type":"CalendarEvent","starts_at":"2026-09-18T22:00:00Z","ends_at":"2026-09-19T01:30:00Z"}`
	answers := map[string]string{
		"/calendar/days/2026-09-19.json": dayJSON("2026-09-19", concert),
		"/calendar/days/2026-09-21.json": dayJSON("2026-09-21", dinner),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, ok := answers[req.URL.Path]
		if !ok {
			body = dayJSON(strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/calendar/days/"), ".json"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	cases := []struct {
		day       int
		title     string
		wantShown bool
	}{
		{20, "Late dinner", true},
		{21, "Late dinner", false},
		{19, "Evening concert", false},
	}
	for _, tc := range cases {
		v := newCalendarView(vc)
		v.now = func() time.Time { return time.Date(2026, 9, tc.day, 12, 0, 0, 0, zone) }
		msg := v.requestRecordings()().(recordingsLoadedMsg)
		if msg.err != nil {
			t.Fatalf("read of 2026-09-%d: %v", tc.day, msg.err)
		}
		shown := false
		for _, recording := range msg.recordings {
			shown = shown || strings.HasPrefix(recording.Title, tc.title)
		}
		if shown != tc.wantShown {
			t.Errorf("2026-09-%d shows %q = %v, want %v", tc.day, tc.title, shown, tc.wantShown)
		}
	}
}

func TestDayColumnsClipASpanningEventToTheDay(t *testing.T) {
	zone := newYork(t)
	const daySpan = 96
	dinner := Recording{StartsAt: at("2026-09-21T00:50:00Z").In(zone), EndsAt: at("2026-09-21T04:20:00Z").In(zone)}
	stay := Recording{StartsAt: at("2026-09-16T18:00:00Z").In(zone), EndsAt: at("2026-09-18T15:00:00Z").In(zone)}
	cases := []struct {
		name       string
		event      Recording
		day        int
		start, end int
	}{
		{"dinner on its evening", dinner, 20, 83, daySpan},
		{"dinner after midnight", dinner, 21, 0, 1},
		{"stay on its first day", stay, 16, 56, daySpan},
		{"stay on a middle day", stay, 17, 0, daySpan},
		{"stay on its last day", stay, 18, 0, 44},
	}
	for _, tc := range cases {
		anchor := time.Date(2026, 9, tc.day, 12, 0, 0, 0, zone)
		if start, end := dayColumns(tc.event.StartsAt, tc.event.EndsAt, anchor, daySpan); start != tc.start || end != tc.end {
			t.Errorf("%s: columns %d..%d, want %d..%d", tc.name, start, end, tc.start, tc.end)
		}
	}
}
