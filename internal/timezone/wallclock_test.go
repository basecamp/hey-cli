package timezone

import (
	"testing"
	"time"
)

// A clock time is placed where HEY places it. These are what ActiveSupport answers for
// Time.zone.parse(clock).change(zone:), the way HEY reads a typed time: a time the clocks
// skip moves an hour on, and a time they repeat is the daylight-saving one of the two, or
// the later where neither keeps daylight saving — Almaty, Volgograd and Moscow going back an
// hour for good. Go's time.Date picks by rules of its own and gets some of each wrong.
func TestWallClockPlacesAClockTimeAsHEYDoes(t *testing.T) {
	tests := []struct{ name, zone, day, clock, want string }{
		{name: "Zagreb repeats 02:30", zone: "Europe/Zagreb", day: "2026-10-25", clock: "02:30", want: "2026-10-25T00:30:00Z"},
		{name: "New York repeats 01:30", zone: "America/New_York", day: "2026-11-01", clock: "01:30", want: "2026-11-01T05:30:00Z"},
		{name: "New York skips 02:30", zone: "America/New_York", day: "2026-03-08", clock: "02:30", want: "2026-03-08T07:30:00Z"},
		{name: "Lord Howe repeats 01:45", zone: "Australia/Lord_Howe", day: "2026-04-05", clock: "01:45", want: "2026-04-04T14:45:00Z"},
		{name: "Lord Howe skips 02:15", zone: "Australia/Lord_Howe", day: "2026-10-04", clock: "02:15", want: "2026-10-03T16:15:00Z"},
		{name: "Samoa skips 30 December 2011, noon", zone: "Pacific/Apia", day: "2011-12-30", clock: "12:00", want: "2011-12-30T10:00:00Z"},
		{name: "Samoa skips 30 December 2011, midnight", zone: "Pacific/Apia", day: "2011-12-30", clock: "00:00", want: "2011-12-30T10:00:00Z"},
		{name: "Samoa skips 30 December 2011, 23:59", zone: "Pacific/Apia", day: "2011-12-30", clock: "23:59", want: "2011-12-30T10:59:00Z"},
		{name: "an ordinary day", zone: "America/New_York", day: "2026-06-10", clock: "10:00", want: "2026-06-10T14:00:00Z"},
		{name: "Almaty repeats 23:30 for good", zone: "Asia/Almaty", day: "2024-02-29", clock: "23:30", want: "2024-02-29T18:30:00Z"},
		{name: "Volgograd repeats 01:30 for good", zone: "Europe/Volgograd", day: "2020-12-27", clock: "01:30", want: "2020-12-26T22:30:00Z"},
		{name: "Monrovia before its half minute went", zone: "Africa/Monrovia", day: "1972-01-06", clock: "23:00", want: "1972-01-06T23:44:30Z"},
		{name: "Monrovia skips 00:44", zone: "Africa/Monrovia", day: "1972-01-07", clock: "00:44", want: "1972-01-07T01:44:00Z"},
		{name: "Monrovia's first 00:45", zone: "Africa/Monrovia", day: "1972-01-07", clock: "00:45", want: "1972-01-07T00:45:00Z"},
		{name: "Moscow repeats 01:30 for good", zone: "Europe/Moscow", day: "2014-10-26", clock: "01:30", want: "2014-10-25T22:30:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			day, _ := time.Parse("2006-01-02", tt.day)
			clock, _ := time.Parse("15:04", tt.clock)
			if got := WallClock(day, clock, loc).UTC().Format(time.RFC3339); got != tt.want {
				t.Errorf("WallClock(%s %s) = %s, want %s", tt.day, tt.clock, got, tt.want)
			}
			if got, ok := Placed(tt.day, tt.clock, loc); !ok || got.UTC().Format(time.RFC3339) != tt.want {
				t.Errorf("Placed(%s %s) = %s, want %s", tt.day, tt.clock, got.UTC(), tt.want)
			}
		})
	}
}

// With no zone, HEY reads the clock as UTC; a date or a time that does not parse places
// nothing.
func TestPlacedWithoutAZoneIsUTC(t *testing.T) {
	if got, ok := Placed("2026-11-01", "06:30", nil); !ok || !got.Equal(time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("Placed = %s, want 06:30 UTC", got)
	}
	if _, ok := Placed("1 November", "06:30", nil); ok {
		t.Error("Placed read a date that does not parse")
	}
	if _, ok := Placed("2026-11-01", "half six", nil); ok {
		t.Error("Placed read a time that does not parse")
	}
}

// The first clock time HEY places no earlier than an instant, and where it reads. Each
// expectation is checked against Time.zone.parse(clock).change(zone:) with Time.zone UTC.
func TestFirstClockFrom(t *testing.T) {
	for _, tt := range []struct {
		name string
		zone string
		at   time.Time
		want time.Time
	}{
		// 2026-11-01 01:00 America/New_York => 05:00 UTC; an hour on is 06:00 UTC, the second
		// 01:00, which HEY reads as the first. 02:00 => 07:00 UTC is the first after it.
		{"New York's repeated hour", "America/New_York", time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC)},
		// 2026-04-05 01:00 Australia/Lord_Howe => 2026-04-04 14:00 UTC; 15:00 UTC is the second
		// 01:30. 02:00 => 15:30 UTC.
		{"Lord Howe's repeated half hour", "Australia/Lord_Howe", time.Date(2026, 4, 4, 15, 0, 0, 0, time.UTC), time.Date(2026, 4, 4, 15, 30, 0, 0, time.UTC)},
		// 1972-01-06 23:00 Africa/Monrovia => 23:44:30 UTC; an hour on is 00:44:30, and the
		// clock had just dropped its half minute. 00:44 => 01:44 UTC (a skipped time moved an
		// hour on); 00:45 => 00:45 UTC.
		{"Monrovia's half minute", "Africa/Monrovia", time.Date(1972, 1, 7, 0, 44, 30, 0, time.UTC), time.Date(1972, 1, 7, 0, 45, 0, 0, time.UTC)},
		// 2026-03-08 03:00 America/New_York => 07:00 UTC, the first clock time after the gap.
		{"New York's skipped hour", "America/New_York", time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC), time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)},
		{"an ordinary time", "America/New_York", time.Date(2026, 6, 10, 14, 0, 0, 0, time.UTC), time.Date(2026, 6, 10, 14, 0, 0, 0, time.UTC)},
	} {
		loc, err := time.LoadLocation(tt.zone)
		if err != nil {
			t.Fatal(err)
		}
		if got := FirstClockFrom(tt.at, loc); !got.Equal(tt.want) {
			t.Errorf("%s: FirstClockFrom(%s) = %s, want %s", tt.name, tt.at, got.UTC(), tt.want)
		}
	}
}

func TestMovedBy(t *testing.T) {
	at := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	for _, tt := range []struct {
		sent time.Time
		want string
	}{
		{at.Add(-time.Hour), "an hour earlier"},
		{at.Add(2 * time.Hour), "2 hours later"},
		{at.Add(-30 * time.Minute), "30 minutes earlier"},
		{at.Add(30 * time.Second), "30 seconds later"},
	} {
		if got := MovedBy(at, tt.sent); got != tt.want {
			t.Errorf("MovedBy(%s) = %q, want %q", tt.sent.Sub(at), got, tt.want)
		}
	}
}
