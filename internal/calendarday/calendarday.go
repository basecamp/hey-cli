// Package calendarday finds the events of the reader's local days. HEY reads a date as a
// UTC day, so a timed event can sit on the day before or after the one the reader sees.
// The package finds those events from the read of the days and from its neighbour days.
package calendarday

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

const (
	// EventType is the key HEY files a calendar's events under in a period's recordings.
	EventType = "Calendar::Event"

	dateLayout = "2006-01-02"
)

// Span is the run of UTC days a period covers, from First to Last inclusive. It is how a
// read of HEY is told apart from the local days the reader asked for.
type Span struct {
	First time.Time
	Last  time.Time
}

// DayReader reads one UTC day by its date. The CLI and the TUI each hand the package their
// own, so it never sees the SDK client or how a calendar is chosen.
type DayReader func(ctx context.Context, date string) (*generated.CalendarPeriod, error)

// ReadNeighbours reads the UTC days outside a period that its local days reach into, at the
// same time. West of UTC that is the day after, east of it the day before, and in UTC none,
// so no request is made that cannot find an event. A failed read is an error because a day
// with a missing neighbour would silently lose events.
func ReadNeighbours(ctx context.Context, main *generated.CalendarPeriod, loc *time.Location, readDay DayReader) ([]*generated.CalendarPeriod, error) {
	if main == nil || main.StartsAt.IsZero() {
		return nil, nil
	}
	dates := SpanOf(main).Neighbours(loc)
	if len(dates) == 0 {
		return nil, nil
	}
	periods := make([]*generated.CalendarPeriod, len(dates))
	failures := make([]error, len(dates))

	var group sync.WaitGroup
	for i, date := range dates {
		group.Add(1)
		go func() {
			defer group.Done()
			periods[i], failures[i] = readDay(ctx, date)
		}()
	}
	group.Wait()

	for _, err := range failures {
		if err != nil {
			return nil, err
		}
	}
	return periods, nil
}

// WithLocalEvents is a copy of a period whose events are the ones on its local days. The
// period read from HEY stays as it was, because other readers share it.
func WithLocalEvents(main *generated.CalendarPeriod, neighbours []*generated.CalendarPeriod, loc *time.Location) *generated.CalendarPeriod {
	if main == nil || main.StartsAt.IsZero() {
		return main
	}
	local := *main
	local.Recordings = make(generated.CalendarRecordingsResponse, len(main.Recordings))
	for kind, recordings := range main.Recordings {
		local.Recordings[kind] = recordings
	}
	local.Recordings[EventType] = LocalEvents(main, neighbours, loc)
	return &local
}

// LocalEvents are the events that overlap the local days of a period, once each. An
// all-day event is a date rather than an instant, so only the period's own read decides it.
// A timed event that spans two local days is on both.
func LocalEvents(main *generated.CalendarPeriod, neighbours []*generated.CalendarPeriod, loc *time.Location) []generated.Recording {
	if main == nil {
		return nil
	}
	from, to := SpanOf(main).Local(loc)
	events := []generated.Recording{}
	seen := map[string]bool{}
	keep := func(event generated.Recording) {
		key := identity(event)
		if seen[key] {
			return
		}
		seen[key] = true
		events = append(events, event)
	}

	for _, event := range main.Recordings[EventType] {
		if event.AllDay || Overlaps(event, from, to) {
			keep(event)
		}
	}
	for _, neighbour := range neighbours {
		if neighbour == nil {
			continue
		}
		for _, event := range neighbour.Recordings[EventType] {
			if !event.AllDay && Overlaps(event, from, to) {
				keep(event)
			}
		}
	}
	return events
}

// InDays reports whether a recording belongs to the local days first through last. It
// answers for a list read, which asks for a run of days and has no period to answer from.
// HEY's series rows carry no end date, so a repeating row is kept unless it starts later. An
// all-day series is a date, so it is compared by its date, and a timed series by its instant.
func InDays(r generated.Recording, first, last time.Time, loc *time.Location) bool {
	if r.StartsAt.IsZero() {
		return true
	}
	if r.Recurring {
		if r.AllDay {
			return !utcDate(r.StartsAt).After(last)
		}
		return r.StartsAt.Before(civilMidnight(last.AddDate(0, 0, 1), loc))
	}
	if r.AllDay {
		startsOn := utcDate(r.StartsAt)
		endsOn := startsOn
		if r.EndsAt.After(r.StartsAt) {
			endsOn = utcDate(r.EndsAt)
		}
		return !startsOn.After(last) && !endsOn.Before(first)
	}
	return Overlaps(r, civilMidnight(first, loc), civilMidnight(last.AddDate(0, 0, 1), loc))
}

// SpanOf is the UTC days a period covers. HEY ends a period at midnight, which belongs to
// the day after, so that midnight does not count as a day of its own.
func SpanOf(period *generated.CalendarPeriod) Span {
	first := utcDate(period.StartsAt)
	last := utcDate(period.EndsAt)
	endsAtMidnight := period.EndsAt.UTC().Equal(last)
	if endsAtMidnight && period.EndsAt.After(period.StartsAt) {
		last = last.AddDate(0, 0, -1)
	}
	if last.Before(first) {
		last = first
	}
	return Span{First: first, Last: last}
}

// Overlaps reports whether a recording is inside [from, to). An instant belongs to the day
// it starts on, and a run belongs to every day it touches, but a run that ends at the
// start of a day is not on that day.
func Overlaps(r generated.Recording, from, to time.Time) bool {
	if r.EndsAt.IsZero() || !r.EndsAt.After(r.StartsAt) {
		return !r.StartsAt.Before(from) && r.StartsAt.Before(to)
	}
	return r.StartsAt.Before(to) && r.EndsAt.After(from)
}

// ReachesDayBefore reports whether the local day of the UTC date first starts before that
// date does, so HEY files some of its events under the UTC day before. That is the case east
// of UTC. Each edge uses its own offset, so a clock change is right.
func ReachesDayBefore(first time.Time, loc *time.Location) bool {
	return civilMidnight(first, loc).Before(first)
}

// ReachesDayAfter reports whether the local day after the UTC date last starts after that
// date's day ends, so HEY files some of the events of last under the UTC day after. That is
// the case west of UTC.
func ReachesDayAfter(last time.Time, loc *time.Location) bool {
	next := last.AddDate(0, 0, 1)
	return civilMidnight(next, loc).After(next)
}

// utcDate is the UTC day an instant falls on, as a midnight in UTC. HEY files an event under
// that day, whatever zone the reader keeps.
func utcDate(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

// civilMidnight is the start of a date in the reader's zone. The date is read off day as it
// is, so a UTC day maps to the same calendar date there.
func civilMidnight(day time.Time, loc *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
}

// identity names an event so that the same one read under two days counts once. A recording
// with no id has none to go by, so its type, start and title stand in.
func identity(event generated.Recording) string {
	if event.Id != 0 || event.OccurrenceId != "" {
		return fmt.Sprintf("%d|%s", event.Id, event.OccurrenceId)
	}
	return fmt.Sprintf("%s|%d|%s", event.Type, event.StartsAt.UnixNano(), event.Title)
}

// Neighbours are the dates of the UTC days just outside the span that its local days reach
// into, the day before first and then the day after. HEY can file an event of the span's
// local days under either of them.
func (s Span) Neighbours(loc *time.Location) []string {
	var dates []string
	if ReachesDayBefore(s.First, loc) {
		dates = append(dates, s.First.AddDate(0, 0, -1).Format(dateLayout))
	}
	if ReachesDayAfter(s.Last, loc) {
		dates = append(dates, s.Last.AddDate(0, 0, 1).Format(dateLayout))
	}
	return dates
}

// Local is the span as a range of instants in the reader's zone, from the start of its
// first day up to the start of the day after its last.
func (s Span) Local(loc *time.Location) (from, to time.Time) {
	return civilMidnight(s.First, loc), civilMidnight(s.Last.AddDate(0, 0, 1), loc)
}
