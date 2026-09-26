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
		})
	}
}
