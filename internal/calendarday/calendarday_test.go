package calendarday

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func instant(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %s: %v", value, err)
	}
	return at
}

func utcDay(t *testing.T, date string) *generated.CalendarPeriod {
	t.Helper()
	start := instant(t, date+"T00:00:00Z")
	return &generated.CalendarPeriod{
		Kind:       "day",
		StartsAt:   start,
		EndsAt:     instant(t, date+"T23:59:59Z"),
		Recordings: generated.CalendarRecordingsResponse{},
	}
}

func withEvents(period *generated.CalendarPeriod, events ...generated.Recording) *generated.CalendarPeriod {
	period.Recordings[EventType] = append(period.Recordings[EventType], events...)
	return period
}

func titles(events []generated.Recording) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Title)
	}
	return names
}

func hasTitle(events []generated.Recording, title string) bool {
	for _, event := range events {
		if event.Title == title {
			return true
		}
	}
	return false
}

func TestSpanOfDayEndingAtLastSecond(t *testing.T) {
	span := SpanOf(utcDay(t, "2026-09-20"))
	if got := span.First.Format(dateLayout); got != "2026-09-20" {
		t.Errorf("First = %s", got)
	}
	if got := span.Last.Format(dateLayout); got != "2026-09-20" {
		t.Errorf("Last = %s", got)
	}
}

func TestSpanOfWeek(t *testing.T) {
	week := &generated.CalendarPeriod{
		Kind:     "week",
		StartsAt: instant(t, "2026-09-14T00:00:00Z"),
		EndsAt:   instant(t, "2026-09-20T23:59:59Z"),
	}
	span := SpanOf(week)
	before, after := span.Neighbours()
	if before != "2026-09-13" || after != "2026-09-21" {
		t.Errorf("Neighbours = %s, %s", before, after)
	}
}

func TestSpanOfEndAtMidnightExcludesThatDay(t *testing.T) {
	period := &generated.CalendarPeriod{
		StartsAt: instant(t, "2026-09-14T00:00:00Z"),
		EndsAt:   instant(t, "2026-09-21T00:00:00Z"),
	}
	if got := SpanOf(period).Last.Format(dateLayout); got != "2026-09-20" {
		t.Errorf("Last = %s", got)
	}
}

func TestSpanLocalFollowsClockChange(t *testing.T) {
	newYork := mustLocation(t, "America/New_York")
	from, to := SpanOf(utcDay(t, "2026-11-01")).Local(newYork)
	if got := to.Sub(from); got != 25*time.Hour {
		t.Errorf("local day length = %s, want 25h", got)
	}
}

func TestOverlapsInstantEvent(t *testing.T) {
	from := instant(t, "2026-09-20T04:00:00Z")
	to := instant(t, "2026-09-21T04:00:00Z")
	event := generated.Recording{StartsAt: to}
	if Overlaps(event, from, to) {
		t.Error("an instant at the end of the span must not overlap")
	}
	event.StartsAt = from
	if !Overlaps(event, from, to) {
		t.Error("an instant at the start of the span must overlap")
	}
}

func TestOverlapsEventEndingAtStartOfSpan(t *testing.T) {
	from := instant(t, "2026-09-19T04:00:00Z")
	to := instant(t, "2026-09-20T04:00:00Z")
	event := generated.Recording{
		StartsAt: instant(t, "2026-09-18T22:00:00Z"),
		EndsAt:   from,
	}
	if Overlaps(event, from, to) {
		t.Error("an event that ends when the span starts must not overlap")
	}
}

func TestLocalEventsByZone(t *testing.T) {
	dinner := generated.Recording{Id: 4821, Title: "Late dinner with Priya Patel", StartsAt: instant(t, "2026-09-21T00:50:00Z"), EndsAt: instant(t, "2026-09-21T03:00:00Z")}
	concert := generated.Recording{Id: 4822, Title: "Evening concert", StartsAt: instant(t, "2026-09-18T22:00:00Z"), EndsAt: instant(t, "2026-09-19T01:30:00Z")}
	breakfast := generated.Recording{Id: 5, Title: "Breakfast", StartsAt: instant(t, "2026-09-20T13:00:00Z"), EndsAt: instant(t, "2026-09-20T14:00:00Z")}

	cases := []struct {
		name   string
		zone   string
		date   string
		main   []generated.Recording
		before []generated.Recording
		after  []generated.Recording
		want   []string
		wantNo []string
	}{
		{name: "dinner is on the 20th in New York", zone: "America/New_York", date: "2026-09-20", main: []generated.Recording{breakfast}, after: []generated.Recording{dinner}, want: []string{"Breakfast", "Late dinner with Priya Patel"}},
		{name: "dinner is not on the 21st in New York", zone: "America/New_York", date: "2026-09-21", main: []generated.Recording{dinner}, wantNo: []string{"Late dinner with Priya Patel"}},
		{name: "concert is not on the 19th in New York", zone: "America/New_York", date: "2026-09-19", main: []generated.Recording{concert}, wantNo: []string{"Evening concert"}},
		{name: "concert is on the 18th in New York", zone: "America/New_York", date: "2026-09-18", main: []generated.Recording{concert}, want: []string{"Evening concert"}},
		{name: "dinner is on the 21st in Tokyo", zone: "Asia/Tokyo", date: "2026-09-21", main: []generated.Recording{dinner}, want: []string{"Late dinner with Priya Patel"}},
		{name: "dinner is not on the 20th in Tokyo", zone: "Asia/Tokyo", date: "2026-09-20", after: []generated.Recording{dinner}, wantNo: []string{"Late dinner with Priya Patel"}},
		{name: "dinner is on the 21st in UTC", zone: "UTC", date: "2026-09-21", main: []generated.Recording{dinner}, want: []string{"Late dinner with Priya Patel"}},
		{name: "concert from before-day is on the 19th in UTC", zone: "UTC", date: "2026-09-19", before: []generated.Recording{concert}, want: []string{"Evening concert"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			main := withEvents(utcDay(t, tc.date), tc.main...)
			neighbours := []*generated.CalendarPeriod{
				withEvents(utcDay(t, "2026-09-01"), tc.before...),
				withEvents(utcDay(t, "2026-09-01"), tc.after...),
			}
			got := LocalEvents(main, neighbours, mustLocation(t, tc.zone))
			for _, title := range tc.want {
				if !hasTitle(got, title) {
					t.Errorf("missing %q in %v", title, titles(got))
				}
			}
			for _, title := range tc.wantNo {
				if hasTitle(got, title) {
					t.Errorf("unexpected %q in %v", title, titles(got))
				}
			}
		})
	}
}

func TestLocalEventsDayOfClockChange(t *testing.T) {
	newYork := mustLocation(t, "America/New_York")
	lateNight := generated.Recording{Id: 1, Title: "Late show", StartsAt: instant(t, "2026-11-02T04:30:00Z"), EndsAt: instant(t, "2026-11-02T04:45:00Z")}
	nextDay := generated.Recording{Id: 2, Title: "Early start", StartsAt: instant(t, "2026-11-02T05:00:00Z"), EndsAt: instant(t, "2026-11-02T06:00:00Z")}
	main := utcDay(t, "2026-11-01")
	after := withEvents(utcDay(t, "2026-11-02"), lateNight, nextDay)

	got := LocalEvents(main, []*generated.CalendarPeriod{after}, newYork)
	if !hasTitle(got, "Late show") || hasTitle(got, "Early start") {
		t.Errorf("got %v", titles(got))
	}
}

func TestLocalEventsKeepAllDayOfMainOnly(t *testing.T) {
	holiday := generated.Recording{Id: 9, Title: "Holiday", AllDay: true, StartsAt: instant(t, "2026-09-20T00:00:00Z"), EndsAt: instant(t, "2026-09-20T00:00:00Z")}
	otherHoliday := generated.Recording{Id: 10, Title: "Other holiday", AllDay: true, StartsAt: instant(t, "2026-09-21T00:00:00Z"), EndsAt: instant(t, "2026-09-21T00:00:00Z")}
	main := withEvents(utcDay(t, "2026-09-20"), holiday)
	after := withEvents(utcDay(t, "2026-09-21"), otherHoliday)

	got := LocalEvents(main, []*generated.CalendarPeriod{after}, mustLocation(t, "America/New_York"))
	if !hasTitle(got, "Holiday") || hasTitle(got, "Other holiday") {
		t.Errorf("got %v", titles(got))
	}
}

func TestLocalEventsDeduplicate(t *testing.T) {
	overnight := generated.Recording{Id: 3, Title: "Night flight", StartsAt: instant(t, "2026-09-20T23:00:00Z"), EndsAt: instant(t, "2026-09-21T05:00:00Z")}
	main := withEvents(utcDay(t, "2026-09-20"), overnight)
	after := withEvents(utcDay(t, "2026-09-21"), overnight)

	got := LocalEvents(main, []*generated.CalendarPeriod{after}, mustLocation(t, "UTC"))
	if len(got) != 1 {
		t.Errorf("got %d events, want 1", len(got))
	}
}

func TestLocalEventsDeduplicateOccurrencesWithoutId(t *testing.T) {
	first := generated.Recording{OccurrenceId: "7_2026-09-20", Title: "Standup", StartsAt: instant(t, "2026-09-20T14:00:00Z")}
	second := generated.Recording{OccurrenceId: "7_2026-09-21", Title: "Standup", StartsAt: instant(t, "2026-09-21T14:00:00Z")}
	main := withEvents(utcDay(t, "2026-09-21"), second)
	before := withEvents(utcDay(t, "2026-09-20"), first, second)

	got := LocalEvents(main, []*generated.CalendarPeriod{before}, mustLocation(t, "UTC"))
	if len(got) != 1 {
		t.Errorf("got %d events, want 1", len(got))
	}
}

func TestInDays(t *testing.T) {
	newYork := mustLocation(t, "America/New_York")
	day := func(date string) time.Time { return instant(t, date+"T00:00:00Z") }
	cases := []struct {
		name  string
		event generated.Recording
		first string
		last  string
		want  bool
	}{
		{"evening dinner is on its local day", generated.Recording{StartsAt: instant(t, "2026-09-21T00:50:00Z"), EndsAt: instant(t, "2026-09-21T03:00:00Z")}, "2026-09-20", "2026-09-20", true},
		{"evening dinner is not on the next day", generated.Recording{StartsAt: instant(t, "2026-09-21T00:50:00Z"), EndsAt: instant(t, "2026-09-21T03:00:00Z")}, "2026-09-21", "2026-09-21", false},
		{"concert ended before the day", generated.Recording{StartsAt: instant(t, "2026-09-18T22:00:00Z"), EndsAt: instant(t, "2026-09-19T01:30:00Z")}, "2026-09-19", "2026-09-19", false},
		{"concert is on the 18th", generated.Recording{StartsAt: instant(t, "2026-09-18T22:00:00Z"), EndsAt: instant(t, "2026-09-19T01:30:00Z")}, "2026-09-18", "2026-09-18", true},
		{"series that started a year before is listed", generated.Recording{Recurring: true, StartsAt: instant(t, "2025-09-15T14:00:00Z")}, "2026-09-20", "2026-09-20", true},
		{"series that starts the day after is not listed", generated.Recording{Recurring: true, StartsAt: instant(t, "2026-09-21T14:00:00Z")}, "2026-09-20", "2026-09-20", false},
		{"series that starts in the evening of the last day is listed", generated.Recording{Recurring: true, StartsAt: instant(t, "2026-09-21T02:00:00Z")}, "2026-09-20", "2026-09-20", true},
		{"event without a start is kept", generated.Recording{}, "2026-09-20", "2026-09-20", true},
		{"all-day event on its date", generated.Recording{AllDay: true, StartsAt: day("2026-09-20"), EndsAt: day("2026-09-20")}, "2026-09-20", "2026-09-20", true},
		{"all-day event outside", generated.Recording{AllDay: true, StartsAt: day("2026-09-20"), EndsAt: day("2026-09-20")}, "2026-09-21", "2026-09-22", false},
		{"multi-day all-day event overlaps", generated.Recording{AllDay: true, StartsAt: day("2026-09-18"), EndsAt: day("2026-09-22")}, "2026-09-20", "2026-09-20", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InDays(tc.event, day(tc.first), day(tc.last), newYork); got != tc.want {
				t.Errorf("InDays = %v, want %v", got, tc.want)
			}
		})
	}
}
