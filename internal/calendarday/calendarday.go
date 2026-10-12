package calendarday

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

const (
	EventType  = "Calendar::Event"
	dateLayout = "2006-01-02"
)

type Span struct {
	First time.Time
	Last  time.Time
}

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

func (s Span) Neighbours() (before, after string) {
	return s.First.AddDate(0, 0, -1).Format(dateLayout), s.Last.AddDate(0, 0, 1).Format(dateLayout)
}

func (s Span) Local(loc *time.Location) (from, to time.Time) {
	return civilMidnight(s.First, loc), civilMidnight(s.Last.AddDate(0, 0, 1), loc)
}

func Overlaps(r generated.Recording, from, to time.Time) bool {
	if r.EndsAt.IsZero() || !r.EndsAt.After(r.StartsAt) {
		return !r.StartsAt.Before(from) && r.StartsAt.Before(to)
	}
	return r.StartsAt.Before(to) && r.EndsAt.After(from)
}

type DayReader func(ctx context.Context, date string) (*generated.CalendarPeriod, error)

func ReadNeighbours(ctx context.Context, main *generated.CalendarPeriod, readDay DayReader) ([]*generated.CalendarPeriod, error) {
	if main == nil || main.StartsAt.IsZero() {
		return nil, nil
	}
	before, after := SpanOf(main).Neighbours()
	dates := []string{before, after}
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

func InDays(r generated.Recording, first, last time.Time, loc *time.Location) bool {
	if r.StartsAt.IsZero() {
		return true
	}
	if r.Recurring {
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

func utcDate(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

func civilMidnight(day time.Time, loc *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
}

func identity(event generated.Recording) string {
	if event.Id != 0 || event.OccurrenceId != "" {
		return fmt.Sprintf("%d|%s", event.Id, event.OccurrenceId)
	}
	return fmt.Sprintf("%s|%d|%s", event.Type, event.StartsAt.UnixNano(), event.Title)
}
