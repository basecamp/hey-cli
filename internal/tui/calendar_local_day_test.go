package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

const (
	dinnerJSON    = `{"id":4821,"title":"Late dinner with Priya Patel","type":"CalendarEvent","starts_at":"2026-09-21T00:50:00Z","ends_at":"2026-09-21T03:00:00Z"}`
	concertJSON   = `{"id":4822,"title":"Evening concert","type":"CalendarEvent","starts_at":"2026-09-18T22:00:00Z","ends_at":"2026-09-19T01:30:00Z"}`
	breakfastJSON = `{"id":4824,"title":"Breakfast with Maria Lopez","type":"CalendarEvent","starts_at":"2026-09-20T13:00:00Z","ends_at":"2026-09-20T14:00:00Z"}`
)

func inNewYork(t *testing.T) {
	t.Helper()
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("time zone database unavailable: %v", err)
	}
	previous := time.Local
	time.Local = newYork
	t.Cleanup(func() { time.Local = previous })
}

func dayJSON(date string, events ...string) string {
	return fmt.Sprintf(`{"starts_at":"%sT00:00:00Z","ends_at":"%sT23:59:59Z","kind":"day","recordings":{"Calendar::Event":[%s]}}`,
		date, date, strings.Join(events, ","))
}

func weekJSON(first, last string, events ...string) string {
	return fmt.Sprintf(`{"starts_at":"%sT00:00:00Z","ends_at":"%sT23:59:59Z","kind":"week","recordings":{"Calendar::Event":[%s]}}`,
		first, last, strings.Join(events, ","))
}

func localDayView(t *testing.T, mode calendarViewMode, day time.Time, answers map[string]string) *calendarView {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if body, ok := answers[req.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		date := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/calendar/days/"), ".json")
		_, _ = w.Write([]byte(dayJSON(date)))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newCalendarView(vc)
	v.Resize(80, 30)
	v.now = func() time.Time { return day }
	v.viewMode = mode
	return v
}

func readTitles(t *testing.T, v *calendarView) []string {
	t.Helper()
	msg, ok := v.requestRecordings()().(recordingsLoadedMsg)
	if !ok {
		t.Fatal("read did not answer recordings")
	}
	if msg.err != nil {
		t.Fatalf("read failed: %v", msg.err)
	}
	titles := make([]string, 0, len(msg.recordings))
	for _, recording := range msg.recordings {
		titles = append(titles, recording.Title)
	}
	return titles
}

func hasTitle(titles []string, prefix string) bool {
	for _, title := range titles {
		if strings.HasPrefix(title, prefix) {
			return true
		}
	}
	return false
}

func TestCalendarDayViewShowsTheEveningDinnerOnItsLocalDay(t *testing.T) {
	inNewYork(t)
	answers := map[string]string{
		"/calendar/days/2026-09-20.json": dayJSON("2026-09-20", breakfastJSON),
		"/calendar/days/2026-09-21.json": dayJSON("2026-09-21", dinnerJSON),
	}
	day := time.Date(2026, 9, 20, 12, 0, 0, 0, time.Local)
	titles := readTitles(t, localDayView(t, viewDay, day, answers))

	if !hasTitle(titles, "Late dinner with Priya Patel") || !hasTitle(titles, "Breakfast") {
		t.Errorf("titles of 2026-09-20 = %v, want the dinner and breakfast", titles)
	}
}

func TestCalendarDayViewDropsTheEveningDinnerFromTheNextLocalDay(t *testing.T) {
	inNewYork(t)
	answers := map[string]string{
		"/calendar/days/2026-09-21.json": dayJSON("2026-09-21", dinnerJSON),
	}
	day := time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)
	titles := readTitles(t, localDayView(t, viewDay, day, answers))

	if hasTitle(titles, "Late dinner with Priya Patel") {
		t.Errorf("titles of 2026-09-21 = %v, want no dinner", titles)
	}
}

func TestCalendarDayViewDropsTheBusThatEndedTheDayBefore(t *testing.T) {
	inNewYork(t)
	answers := map[string]string{
		"/calendar/days/2026-09-19.json": dayJSON("2026-09-19", concertJSON),
		"/calendar/days/2026-09-18.json": dayJSON("2026-09-18", concertJSON),
	}
	day := time.Date(2026, 9, 19, 12, 0, 0, 0, time.Local)
	titles := readTitles(t, localDayView(t, viewDay, day, answers))

	if hasTitle(titles, "Evening concert") {
		t.Errorf("titles of 2026-09-19 = %v, want no concert", titles)
	}
}

func TestCalendarWeekViewPlacesTheEveningDinnerOnSunday(t *testing.T) {
	inNewYork(t)
	answers := map[string]string{
		"/calendar/weeks/2026-09-17.json": weekJSON("2026-09-14", "2026-09-20", breakfastJSON),
		"/calendar/days/2026-09-21.json":  dayJSON("2026-09-21", dinnerJSON),
	}
	day := time.Date(2026, 9, 17, 12, 0, 0, 0, time.Local)
	v := localDayView(t, viewWeek, day, answers)
	msg := v.requestRecordings()().(recordingsLoadedMsg)
	if msg.err != nil {
		t.Fatalf("read failed: %v", msg.err)
	}

	byDate := eventsByDate(msg.recordings)
	sunday := byDate["2026-09-20"]
	foundDinner := false
	for _, event := range sunday {
		if strings.HasPrefix(event.Title, "Late dinner with Priya Patel") {
			foundDinner = true
		}
	}
	if !foundDinner {
		t.Errorf("events on 2026-09-20 = %v, want the dinner", sunday)
	}
	if len(byDate["2026-09-21"]) != 0 {
		t.Errorf("events on 2026-09-21 = %v, want none", byDate["2026-09-21"])
	}
}

func TestCalendarReadFailsWhenANeighbourDayFails(t *testing.T) {
	inNewYork(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/calendar/days/2026-09-21.json" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"neighbour failed"}`))
			return
		}
		_, _ = w.Write([]byte(dayJSON("2026-09-20", breakfastJSON)))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newCalendarView(vc)
	v.Resize(80, 30)
	v.now = func() time.Time { return time.Date(2026, 9, 20, 12, 0, 0, 0, time.Local) }

	msg := v.requestRecordings()().(recordingsLoadedMsg)
	if msg.err == nil {
		t.Error("a failed neighbour read must fail the whole read")
	}
}

func TestDayColumnsClipASpanningEventToTheDay(t *testing.T) {
	inNewYork(t)
	const daySpan = 96
	dinner := Recording{StartsAt: at("2026-09-21T00:50:00Z"), EndsAt: at("2026-09-21T04:20:00Z")}
	hotel := Recording{StartsAt: at("2026-09-16T18:00:00Z"), EndsAt: at("2026-09-18T15:00:00Z")}
	anchorOn := func(day int) time.Time { return time.Date(2026, 9, day, 12, 0, 0, 0, time.Local) }

	cases := []struct {
		name         string
		event        Recording
		day          int
		wantStartPos int
		wantEndPos   int
	}{
		{"dinner on its evening", dinner, 20, 20*60 + 50, daySpan},
		{"dinner after midnight", dinner, 21, 0, 20},
		{"hotel fills a middle day", hotel, 17, 0, daySpan},
		{"hotel on its last day", hotel, 18, 0, 11 * 60},
		{"hotel on its first day", hotel, 16, 14 * 60, daySpan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := dayColumns(tc.event.Starts(), tc.event.Ends(), anchorOn(tc.day), daySpan)
			wantStart := tc.wantStartPos * daySpan / (24 * 60)
			wantEnd := tc.wantEndPos * daySpan / (24 * 60)
			if tc.wantEndPos == daySpan {
				wantEnd = daySpan
			}
			if start != wantStart || end != wantEnd {
				t.Errorf("columns = %d..%d, want %d..%d", start, end, wantStart, wantEnd)
			}
		})
	}
}

func TestDayViewDrawsASpanningEventAcrossTheHoursOfEachDay(t *testing.T) {
	inNewYork(t)
	hotel := []Recording{{ID: 4823, Title: "Conference hotel stay", StartsAt: at("2026-09-16T18:00:00Z"), EndsAt: at("2026-09-18T15:00:00Z"), Type: "Calendar::Event"}}
	const colWidth = 4
	cases := []struct {
		day       int
		fromHour  int
		untilHour int
	}{
		{16, 14, 24},
		{17, 0, 24},
		{18, 0, 11},
	}
	for _, tc := range cases {
		anchor := time.Date(2026, 9, tc.day, 12, 0, 0, 0, time.Local)
		lines := strings.Split(stripANSI(renderDayView(hotel, nil, nil, anchor, calendarFixtureNow(), "", 100, 14, selection{}, nil, true)), "\n")
		row := lines[rowContaining(t, lines, "f")]
		column := len([]rune(row[:strings.Index(row, "f")]))
		if column < tc.fromHour*colWidth || column >= tc.untilHour*colWidth {
			t.Errorf("2026-09-%d: title at column %d, want inside hours %d to %d", tc.day, column, tc.fromHour, tc.untilHour)
		}
	}
}
