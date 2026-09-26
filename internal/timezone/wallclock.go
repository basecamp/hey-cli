package timezone

import (
	"fmt"
	"slices"
	"strconv"
	"time"
)

// WallClock is a clock time on a day, resolved the way HEY resolves one. A clock time that
// does not exist — the hour a zone springs forward over, or the whole of 30 December 2011 in
// Samoa — is moved an hour later and tried again, date and all, until it does: that is what
// ActiveSupport does with a local time TZInfo cannot find. A clock time that happens twice
// is resolved by heysChoice. Go's time.Date is no guide to either: it picks one side of a gap
// or an overlap by its own rules, not HEY's, so a title-only edit would move the day.
//
// Only day's date and wall's clock are read; their zones are not.
func WallClock(day, wall time.Time, loc *time.Location) time.Time {
	hour, minute, second := wall.Clock()
	local := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0, time.UTC)
	for range 48 {
		if instants := instantsReading(local, loc); len(instants) > 0 {
			return heysChoice(instants)
		}
		local = local.Add(time.Hour)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0, loc)
}

// Placed is the instant HEY stores for a date and a clock time sent with a zone: placed in
// that zone by WallClock, or read as UTC when no zone is sent, since HEY answers a JSON
// request in UTC. It answers false for a date or a time that does not parse.
func Placed(date, clock string, loc *time.Location) (time.Time, bool) {
	day, dayErr := time.Parse("2006-01-02", date)
	at, clockErr := time.Parse("15:04", clock)
	if dayErr != nil || clockErr != nil {
		return time.Time{}, false
	}
	if loc == nil {
		return time.Date(day.Year(), day.Month(), day.Day(), at.Hour(), at.Minute(), 0, 0, time.UTC), true
	}
	return WallClock(day, at, loc), true
}

// Representable is whether an instant can be sent to HEY as a clock time in loc and come
// back as itself: false for the second of two moments a clock time names as the clocks go
// back, when HEY takes the first, and for an instant between whole minutes.
func Representable(at time.Time, loc *time.Location) bool {
	on := at.In(loc)
	return at.Equal(at.Truncate(time.Minute)) && WallClock(on, on, loc).Equal(at)
}

// MovedBy says how far and which way an instant would move, from had to sent.
func MovedBy(had, sent time.Time) string {
	moved, way := sent.Sub(had), "later"
	if moved < 0 {
		moved, way = -moved, "earlier"
	}
	switch {
	case moved == time.Hour:
		return "an hour " + way
	case moved%time.Hour == 0:
		return fmt.Sprintf("%d hours %s", moved/time.Hour, way)
	case moved%time.Minute == 0:
		return fmt.Sprintf("%d minutes %s", moved/time.Minute, way)
	case moved < time.Millisecond:
		return "less than a millisecond " + way
	case moved < time.Second:
		return fmt.Sprintf("%s milliseconds %s", strconv.FormatFloat(float64(moved)/float64(time.Millisecond), 'f', -1, 64), way)
	case moved < time.Minute:
		return fmt.Sprintf("%s seconds %s", strconv.FormatFloat(moved.Seconds(), 'f', -1, 64), way)
	}
	return moved.String() + " " + way
}

// localClock is the clock time an instant reads as, held as the same figures in UTC so it
// can be stepped without a zone getting in the way.
func localClock(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), at.Second(), 0, time.UTC)
}

// instantsReading is every instant whose clock in loc reads local, earliest first: none in a
// gap the clocks skip, two where they go back over the same hour, one anywhere else. Each
// offset the zone has within a day and a half either side is tried in turn.
func instantsReading(local time.Time, loc *time.Location) []time.Time {
	var instants []time.Time
	seen := map[int]bool{}
	for at := local.Add(-36 * time.Hour).In(loc); at.Before(local.Add(36 * time.Hour)); {
		_, offset := at.Zone()
		if !seen[offset] {
			seen[offset] = true
			if candidate := local.Add(-time.Duration(offset) * time.Second).In(loc); localClock(candidate).Equal(local) {
				instants = append(instants, candidate)
			}
		}
		_, end := at.ZoneBounds()
		if end.IsZero() {
			break
		}
		at = end.In(loc)
	}
	slices.SortFunc(instants, func(a, b time.Time) int { return a.Compare(b) })
	return slices.CompactFunc(instants, time.Time.Equal)
}

// heysChoice is the instant HEY takes for a clock time that names more than one:
// ActiveSupport asks TZInfo for the period with daylight saving in force, and takes the
// last of those still left — so the daylight-saving side of a fall-back, and the later of
// two when neither side keeps daylight saving, as when Almaty moved its clocks back an hour
// for good in 2024.
func heysChoice(instants []time.Time) time.Time {
	if len(instants) == 0 {
		return time.Time{}
	}
	var saving []time.Time
	for _, at := range instants {
		if at.IsDST() {
			saving = append(saving, at)
		}
	}
	if len(saving) > 0 {
		instants = saving
	}
	return instants[len(instants)-1]
}
