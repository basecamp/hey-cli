package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// recordingFilter is the calendar, the date window and the paging that every listing of one
// kind of recording takes. An event, a to-do and a journal entry are all recordings on a
// calendar inside a window, so `hey event list`, `hey todo list` and `hey journal list`
// select alike and differ only in the type they keep and where they look when the reader
// names nothing.
type recordingFilter struct {
	calendar int64
	startsOn string
	endsOn   string
	limit    int
	all      bool

	// defaultWindow is the span to read when no dates are given. An event looks ahead from
	// today, the way a calendar is read; a to-do or a journal entry looks back over years,
	// because that is where the ones worth listing already are.
	defaultWindow func(now time.Time) (time.Time, time.Time)

	// defaultCalendars is where to look when --calendar is not given. Events are spread over
	// every calendar the identity has; to-dos and journal entries live on the personal one.
	defaultCalendars func(ctx context.Context) ([]int64, error)
}

// eventWindow reads from today forward, which is what somebody asking what is on their
// calendar means.
func eventWindow(now time.Time) (time.Time, time.Time) {
	return now, now.AddDate(0, 0, 30)
}

// personalWindow reads years back and a year forward. A to-do or a journal entry is looked up
// by having been written rather than by coming up, so its window is wide.
func personalWindow(now time.Time) (time.Time, time.Time) {
	return now.AddDate(-4, 0, 0), now.AddDate(1, 0, 0)
}

// registerFlags puts the window on a command. calendarUsage says where the listing looks
// without it, since that differs per recording type and is the one thing a reader cannot
// guess.
func (f *recordingFilter) registerFlags(cmd *cobra.Command, noun, calendarUsage string) {
	cmd.Flags().Int64Var(&f.calendar, "calendar", 0, calendarUsage)
	cmd.Flags().StringVar(&f.startsOn, "starts-on", "", "Start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&f.endsOn, "ends-on", "", "End date (YYYY-MM-DD)")
	cmd.Flags().IntVar(&f.limit, "limit", 0, fmt.Sprintf("Maximum number of %s to show", noun))
	cmd.Flags().BoolVar(&f.all, "all", false, "Fetch all results (override --limit)")
}

// recordingWindow is a filter with its dates parsed and its calendars looked up — what is
// actually read.
type recordingWindow struct {
	calendars []int64
	startsOn  string
	endsOn    string
}

func (f *recordingFilter) resolve(ctx context.Context) (recordingWindow, error) {
	defaultStart, defaultEnd := f.defaultWindow(time.Now())

	startsOn := f.startsOn
	if startsOn == "" {
		startsOn = defaultStart.Format(dateLayout)
	}
	start, err := parseDateArg("starts-on date", startsOn)
	if err != nil {
		return recordingWindow{}, err
	}

	endsOn := f.endsOn
	if endsOn == "" {
		// A named start moves the end along with it, so `--starts-on` on its own reads the
		// same span from a different day rather than an empty one behind the default end.
		if f.startsOn == "" {
			endsOn = defaultEnd.Format(dateLayout)
		} else {
			endsOn = start.Add(defaultEnd.Sub(defaultStart)).Format(dateLayout)
		}
	}
	end, err := parseDateArg("ends-on date", endsOn)
	if err != nil {
		return recordingWindow{}, err
	}
	if end.Before(start) {
		return recordingWindow{}, apierr.ErrUsage(fmt.Sprintf("ends-on %s is before starts-on %s", endsOn, startsOn))
	}

	calendars := []int64{f.calendar}
	if f.calendar == 0 {
		if calendars, err = f.defaultCalendars(ctx); err != nil {
			return recordingWindow{}, err
		}
	}

	return recordingWindow{calendars: calendars, startsOn: startsOn, endsOn: endsOn}, nil
}

// read lists the recordings of one type over the window, calendar by calendar in the order
// HEY listed the calendars.
//
// A recurring event is one recording here rather than one per day it falls on: a calendar
// lists what it holds, and only the day, week and year reads expand a recurrence into the
// occurrences inside them.
func (w recordingWindow) read(ctx context.Context, recType string) ([]generated.Recording, error) {
	recordings := []generated.Recording{}
	for _, calendarID := range w.calendars {
		resp, err := sdk.Calendars().GetRecordings(ctx, calendarID, &generated.GetCalendarRecordingsParams{
			StartsOn: &w.startsOn,
			EndsOn:   &w.endsOn,
		})
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		recordings = append(recordings, filterRecordingsByType(resp, recType)...)
	}
	return recordings, nil
}

// describe names the window for a summary line.
func (w recordingWindow) describe() string {
	return fmt.Sprintf("%s to %s", w.startsOn, w.endsOn)
}

// allCalendarIDs is every calendar the identity has, which is where an event can be.
func allCalendarIDs(ctx context.Context) ([]int64, error) {
	payload, err := sdk.Calendars().List(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	calendars := unwrapCalendars(payload)
	ids := make([]int64, 0, len(calendars))
	for _, calendar := range calendars {
		ids = append(ids, calendar.Id)
	}
	return ids, nil
}

// personalCalendarIDs is the one calendar a to-do, a journal entry, a habit and a time track
// are all filed on.
func personalCalendarIDs(ctx context.Context) ([]int64, error) {
	payload, err := sdk.Calendars().List(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	id, err := findPersonalCalendarID(unwrapCalendars(payload))
	if err != nil {
		return nil, apierr.ErrNotFound("calendar", "personal")
	}
	return []int64{id}, nil
}
