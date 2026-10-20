package calendarday

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

func instant(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func period(t *testing.T, first, last string, events ...generated.Recording) *generated.CalendarPeriod {
	t.Helper()
	return &generated.CalendarPeriod{
		StartsAt:   instant(t, first+"T00:00:00Z"),
		EndsAt:     instant(t, last+"T23:59:59Z"),
		Recordings: generated.CalendarRecordingsResponse{EventType: events},
	}
}

func TestSpanOf(t *testing.T) {
	newYork, _ := time.LoadLocation("America/New_York")
	cases := []struct {
		name, starts, ends, last, neighbours string
		hours                                int
	}{
		{"week", "2026-09-14T00:00:00Z", "2026-09-20T23:59:59Z", "2026-09-20", "2026-09-21", 7 * 24},
		{"end at midnight", "2026-09-14T00:00:00Z", "2026-09-21T00:00:00Z", "2026-09-20", "2026-09-21", 7 * 24},
		{"clock change", "2026-11-01T00:00:00Z", "2026-11-01T23:59:59Z", "2026-11-01", "2026-11-02", 25},
	}
	for _, tc := range cases {
		span := SpanOf(&generated.CalendarPeriod{StartsAt: instant(t, tc.starts), EndsAt: instant(t, tc.ends)})
		neighbours := strings.Join(span.Neighbours(newYork), " ")
		from, to := span.Local(newYork)
		if span.Last.Format(dateLayout) != tc.last || neighbours != tc.neighbours || int(to.Sub(from).Hours()) != tc.hours {
			t.Errorf("%s: last %s, neighbours %q, length %s", tc.name, span.Last.Format(dateLayout), neighbours, to.Sub(from))
		}
	}
}

func TestReadNeighboursReadsOnlyTheDaysTheZoneReaches(t *testing.T) {
	cases := []struct {
		name        string
		zone        string
		first, last string
		want        []string
	}{
		{"New York, day", "America/New_York", "2026-09-20", "2026-09-20", []string{"2026-09-21"}},
		{"Tokyo, day", "Asia/Tokyo", "2026-09-20", "2026-09-20", []string{"2026-09-19"}},
		{"UTC, day", "UTC", "2026-09-20", "2026-09-20", nil},
		{"New York, week", "America/New_York", "2026-09-14", "2026-09-20", []string{"2026-09-21"}},
		{"London, BST ends", "Europe/London", "2027-10-31", "2027-10-31", []string{"2027-10-30"}},
		{"London, BST starts", "Europe/London", "2026-03-29", "2026-03-29", nil},
		{"New York, EDT ends", "America/New_York", "2026-11-01", "2026-11-01", []string{"2026-11-02"}},
		{"Kolkata, day", "Asia/Kolkata", "2026-09-20", "2026-09-20", []string{"2026-09-19"}},
		{"St John's, day", "America/St_Johns", "2026-09-20", "2026-09-20", []string{"2026-09-21"}},
	}
	for _, tc := range cases {
		loc, err := time.LoadLocation(tc.zone)
		if err != nil {
			t.Skipf("time zone database unavailable: %v", err)
		}
		var mu sync.Mutex
		var asked []string
		readDay := func(_ context.Context, date string) (*generated.CalendarPeriod, error) {
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, date)
			return period(t, date, date), nil
		}
		main := period(t, tc.first, tc.last)
		periods, err := ReadNeighbours(t.Context(), main, loc, readDay)
		slices.Sort(asked)
		if err != nil || !slices.Equal(asked, tc.want) || len(periods) != len(tc.want) {
			t.Errorf("%s: asked %v, read %d periods, error %v, want %v", tc.name, asked, len(periods), err, tc.want)
		}
	}
}

func TestLocalEvents(t *testing.T) {
	event := func(id int64, title, starts, ends string) generated.Recording {
		r := generated.Recording{Id: id, Title: title, StartsAt: instant(t, starts)}
		if ends != "" {
			r.EndsAt = instant(t, ends)
		}
		return r
	}
	breakfast := event(1, "Breakfast", "2026-09-20T13:00:00Z", "2026-09-20T14:00:00Z")
	dinner := event(2, "Dinner", "2026-09-21T00:50:00Z", "2026-09-21T02:40:00Z")
	concert := event(3, "Concert", "2026-09-18T22:00:00Z", "2026-09-19T01:30:00Z")
	flight := event(4, "Flight", "2026-09-20T23:00:00Z", "2026-09-21T05:00:00Z")
	lateShow := event(5, "Late show", "2026-11-02T04:30:00Z", "2026-11-02T04:45:00Z")
	earlyStart := event(6, "Early start", "2026-11-02T05:00:00Z", "2026-11-02T06:00:00Z")
	closing := event(10, "Closing", "2026-09-19T02:00:00Z", "2026-09-19T04:00:00Z")
	midnight := event(7, "Midnight", "2026-09-21T00:00:00Z", "")
	holiday := event(8, "Holiday", "2026-09-20T00:00:00Z", "2026-09-20T00:00:00Z")
	holiday.AllDay = true
	nextHoliday := event(9, "Next holiday", "2026-09-21T00:00:00Z", "2026-09-21T00:00:00Z")
	nextHoliday.AllDay = true
	standup := generated.Recording{OccurrenceId: "7_2026-09-21", Title: "Standup", StartsAt: instant(t, "2026-09-21T14:00:00Z")}

	cases := []struct {
		name, zone, date    string
		main, before, after []generated.Recording
		want                []string
	}{
		{"evening in New York", "America/New_York", "2026-09-20", []generated.Recording{breakfast}, nil, []generated.Recording{dinner}, []string{"Breakfast", "Dinner"}},
		{"next local day", "America/New_York", "2026-09-21", []generated.Recording{dinner}, nil, nil, nil},
		{"ended before the day", "America/New_York", "2026-09-19", []generated.Recording{concert}, nil, nil, nil},
		{"ended at local midnight", "America/New_York", "2026-09-19", []generated.Recording{closing}, nil, nil, nil},
		{"own local day", "America/New_York", "2026-09-18", []generated.Recording{concert}, nil, nil, []string{"Concert"}},
		{"east of UTC", "Asia/Tokyo", "2026-09-21", []generated.Recording{dinner}, nil, nil, []string{"Dinner"}},
		{"east of UTC, day before", "Asia/Tokyo", "2026-09-20", nil, nil, []generated.Recording{dinner}, nil},
		{"only from the day before", "UTC", "2026-09-19", nil, []generated.Recording{concert}, nil, []string{"Concert"}},
		{"clock change", "America/New_York", "2026-11-01", nil, nil, []generated.Recording{lateShow, earlyStart}, []string{"Late show"}},
		{"all-day from main only", "UTC", "2026-09-20", []generated.Recording{holiday}, nil, []generated.Recording{nextHoliday}, []string{"Holiday"}},
		{"same id twice", "UTC", "2026-09-21", []generated.Recording{flight}, []generated.Recording{flight}, nil, []string{"Flight"}},
		{"same occurrence twice", "UTC", "2026-09-21", []generated.Recording{standup}, []generated.Recording{standup}, nil, []string{"Standup"}},
		{"instant at the start", "UTC", "2026-09-21", nil, nil, []generated.Recording{midnight}, []string{"Midnight"}},
		{"instant at the end", "UTC", "2026-09-20", nil, nil, []generated.Recording{midnight}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc, _ := time.LoadLocation(tc.zone)
			main := period(t, tc.date, tc.date, tc.main...)
			neighbours := []*generated.CalendarPeriod{period(t, "2026-09-01", "2026-09-01", tc.before...), period(t, "2026-09-02", "2026-09-02", tc.after...)}
			var got []string
			for _, e := range LocalEvents(main, neighbours, loc) {
				got = append(got, e.Title)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInDays(t *testing.T) {
	day := func(date string) time.Time { return instant(t, date+"T00:00:00Z") }
	newYork, _ := time.LoadLocation("America/New_York")
	evening := generated.Recording{StartsAt: instant(t, "2026-09-21T00:50:00Z"), EndsAt: instant(t, "2026-09-21T02:40:00Z")}
	cases := []struct {
		name  string
		event generated.Recording
		first string
		last  string
		want  bool
	}{
		{"evening on its local day", evening, "2026-09-20", "2026-09-20", true},
		{"evening not on the next day", evening, "2026-09-21", "2026-09-21", false},
		{"series from a year before", generated.Recording{Recurring: true, StartsAt: instant(t, "2025-09-15T14:00:00Z")}, "2026-09-20", "2026-09-20", true},
		{"series from the day after", generated.Recording{Recurring: true, StartsAt: instant(t, "2026-09-21T14:00:00Z")}, "2026-09-20", "2026-09-20", false},
		{"series from the evening of the last day", generated.Recording{Recurring: true, StartsAt: instant(t, "2026-09-21T02:00:00Z")}, "2026-09-20", "2026-09-20", true},
		{"no start", generated.Recording{}, "2026-09-20", "2026-09-20", true},
		{"all-day on its date", generated.Recording{AllDay: true, StartsAt: day("2026-09-20"), EndsAt: day("2026-09-20")}, "2026-09-20", "2026-09-20", true},
		{"all-day outside", generated.Recording{AllDay: true, StartsAt: day("2026-09-20")}, "2026-09-21", "2026-09-22", false},
		{"multi-day all-day", generated.Recording{AllDay: true, StartsAt: day("2026-09-18"), EndsAt: day("2026-09-22")}, "2026-09-20", "2026-09-20", true},
	}
	for _, tc := range cases {
		if got := InDays(tc.event, day(tc.first), day(tc.last), newYork); got != tc.want {
			t.Errorf("%s: InDays = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestInDaysComparesAnAllDaySeriesByItsDate(t *testing.T) {
	newYork, _ := time.LoadLocation("America/New_York")
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	cases := []struct {
		name     string
		loc      *time.Location
		startsOn string
		last     string
		want     bool
	}{
		{"New York, series from the day after", newYork, "2026-09-21", "2026-09-20", false},
		{"New York, series from the last day", newYork, "2026-09-20", "2026-09-20", true},
		{"Tokyo, series from the day after", tokyo, "2026-09-21", "2026-09-20", false},
		{"Tokyo, series from the last day", tokyo, "2026-09-20", "2026-09-20", true},
	}
	for _, tc := range cases {
		event := generated.Recording{Recurring: true, AllDay: true, StartsAt: instant(t, tc.startsOn+"T00:00:00Z")}
		last := instant(t, tc.last+"T00:00:00Z")
		if got := InDays(event, last, last, tc.loc); got != tc.want {
			t.Errorf("%s: InDays = %v, want %v", tc.name, got, tc.want)
		}
	}
}
