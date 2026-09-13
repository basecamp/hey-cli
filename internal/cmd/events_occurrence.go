package cmd

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// recordingTypeCountdown is how HEY names an event's countdown among a calendar's
// recordings. A countdown is not a field on the event but a recording of its own kind
// under it, spanning from the countdown's start to the moment the event begins.
const recordingTypeCountdown = "Calendar::Countdown"

// occurrenceEdit is one day of a repeating event and how much of the series a change to it
// reaches, as --occurrence and --apply-to name them.
type occurrenceEdit struct {
	occurrence hey.EventOccurrence
	scope      hey.OccurrenceScope
}

// parseOccurrence reads --occurrence and --apply-to, and answers nil for an edit of the
// whole event. Everything here is refused before a request is made: the occurrence_id has
// to be the one a day or a week listing served, byte for byte, naming the series the
// positional id names; --apply-to has to say current or future; and a change to the
// schedule cannot apply to one day, which is HEY's rule as much as this command's.
func (c *eventsEditCommand) parseOccurrence(cmd *cobra.Command, id int64, on string) (*occurrenceEdit, error) {
	flags := cmd.Flags()
	if !flags.Changed("occurrence") {
		if flags.Changed("apply-to") {
			return nil, apierr.ErrUsageHint("--apply-to needs --occurrence",
				"hey event edit 4821 --occurrence 4821_2026-09-15 --apply-to current")
		}
		return nil, nil
	}
	// An --occurrence given empty — a script's unset variable — must not quietly become an
	// edit of the whole series, which is the one thing the flag was there to avoid.
	if c.occurrence == "" {
		return nil, apierr.ErrUsageHint("--occurrence needs an occurrence_id",
			"an occurrence_id as hey event day serves it, <series id>_<YYYY-MM-DD>, for example 4821_2026-09-15")
	}

	occurrence, err := hey.ParseOccurrenceID(c.occurrence)
	if err != nil || occurrence.String() != c.occurrence {
		return nil, apierr.ErrUsageHint(fmt.Sprintf("invalid occurrence: %s", c.occurrence),
			"an occurrence_id as hey event day serves it, <series id>_<YYYY-MM-DD>, for example 4821_2026-09-15")
	}
	if occurrence.EventID != id {
		return nil, apierr.ErrUsageHint(
			fmt.Sprintf("occurrence %s belongs to series %d, not %d", c.occurrence, occurrence.EventID, id),
			fmt.Sprintf("hey event edit %d --occurrence %s", occurrence.EventID, c.occurrence))
	}
	if on != "" {
		day, dateErr := parseDateArg("date", on)
		if dateErr != nil {
			return nil, dateErr
		}
		if day.Format(dateLayout) != occurrence.DateParam() {
			return nil, apierr.ErrUsageHint(fmt.Sprintf("date %s is not the day of occurrence %s", on, c.occurrence),
				"an occurrence is read on its own day, so leave the date out or name that day")
		}
	}

	scope, err := parseApplyTo(c.applyTo, flags.Changed("apply-to"))
	if err != nil {
		return nil, err
	}
	if scope == hey.OccurrenceScopeThisEvent {
		for _, flag := range []string{"repeat", "repeat-until", "repeat-times"} {
			if flags.Changed(flag) {
				return nil, apierr.ErrUsageHint(fmt.Sprintf("--%s cannot apply to one day of a series", flag),
					"a change to the schedule reaches this day and every one after it with --apply-to future, or the whole series when the id is edited alone")
			}
		}
	}

	return &occurrenceEdit{occurrence: occurrence, scope: scope}, nil
}

// parseApplyTo reads the scope. There is no default: a caller who does not say how much of
// the series to change has not said what they want, and the narrower answer is not safer
// when the wider one was meant.
func parseApplyTo(value string, given bool) (hey.OccurrenceScope, error) {
	if !given {
		return "", apierr.ErrUsageHint("--apply-to is required with --occurrence",
			"--apply-to current changes that day alone; --apply-to future changes it and every day after it")
	}
	switch value {
	case "current":
		return hey.OccurrenceScopeThisEvent, nil
	case "future":
		return hey.OccurrenceScopeThisAndFollowing, nil
	}
	return "", apierr.ErrUsageHint(fmt.Sprintf("invalid apply-to: %s", value), "one of current or future")
}

// editOccurrence changes one day of a repeating event, or that day and every one after it.
//
// The day is read on its own date, as the window [day, day+1) over the calendars the event
// could be on: HEY answers that window with every series still recurring through it, so the
// series is found without a scan for where it began, and with the day itself if an earlier
// edit has already written it out as a recording of its own — whose title, notes and times
// are then its own rather than the series', and are what an edit has to keep.
//
// The write is a replacement like every event write, so what the flags do not name is read
// back and sent again: the schedule of that day, its zones, notes, location, link, attached
// email, reminders, circle and countdown. Two things the whole-event edit loses are handled
// here rather than lost. The countdown is read from the recording HEY keeps for it and sent
// again, so only --countdown 0 removes it. Notes are served only as plain text, so an edit
// that would send formatted notes back as text is refused unless --allow-plain-notes says
// that is acceptable or --notes replaces them.
//
// One thing cannot be kept and cannot be refused either: an attached email the editor
// cannot read is left out of what HEY serves, indistinguishable from none, and HEY clears
// the attachment whether the write sends an empty entry id or no key at all. That is the
// server's to fix; the docs say so.
func (c *eventsEditCommand) editOccurrence(ctx context.Context, cmd *cobra.Command, edit occurrenceEdit) error {
	// The flags that need no read are refused first, so a bad one costs no request.
	repeat, err := c.fields.parseRepeat()
	if err != nil {
		return err
	}
	if _, err = c.fields.parseCountdown(); err != nil {
		return err
	}

	// The day is read over every calendar, whatever --calendar says: here the flag names the
	// calendar the day is moved to, and the series being moved is on some other one.
	window, err := occurrenceDayWindow(ctx, edit.occurrence.Date)
	if err != nil {
		return err
	}
	rows, err := window.readTypes(ctx, recordingTypeEvent, recordingTypeCountdown)
	if err != nil {
		return err
	}
	day, err := locateOccurrence(rows, edit.occurrence)
	if err != nil {
		return err
	}
	event := day.event()

	if event.Description != "" && !cmd.Flags().Changed("notes") && !c.allowPlainNotes {
		return errPlainNotes(edit.occurrence)
	}

	schedule, err := c.fields.scheduleFrom(cmd, event)
	if err != nil {
		return err
	}
	reminders, err := c.fields.remindersFrom(cmd, event)
	if err != nil {
		return err
	}
	countdown, err := c.occurrenceCountdown(ctx, cmd, window, day)
	if err != nil {
		return err
	}

	changes := hey.UpdateCalendarEventParams{
		StartsAt:      &schedule.startsAt,
		EndsAt:        &schedule.endsAt,
		AllDay:        &schedule.allDay,
		StartTime:     &schedule.startTime,
		EndTime:       &schedule.endTime,
		StartTimeZone: &schedule.zone,
		EndTimeZone:   &schedule.endZone,
		Reminders:     reminders,
		Content: hey.EventContentParams{
			Notes:    stringOr(cmd, "notes", c.fields.notes, event.Description),
			Location: stringOr(cmd, "location", c.fields.location, event.Location),
			Link:     stringOr(cmd, "link", c.fields.link, event.Url),
			EntryID:  event.AttachedEntry.Id,
		},
		Attendees: c.fields.invitesOrNil(cmd),
		Countdown: countdown,
		Repeat:    repeat,
	}
	if title := stringOr(cmd, "title", c.fields.title, event.Title); title != "" {
		changes.Title = &title
	}
	if cmd.Flags().Changed("calendar") {
		changes.CalendarID = &c.fields.calendar
	}
	// The circle is sent back whether or not it changes. A future edit records a new series
	// for the days from this one on, and HEY circles that one only when told to; the
	// whole-event edit can leave the flag out because there the event stays the same record.
	circled := event.Highlighted
	if cmd.Flags().Changed("circle") {
		circled = c.fields.circle
	}
	changes.Highlighted = &circled

	result, err := sdk.CalendarEvents().UpdateOccurrence(ctx, edit.occurrence, edit.scope,
		hey.UpdateCalendarEventOccurrenceParams{UpdateCalendarEventParams: changes})
	if err != nil {
		return occurrenceWriteError(err, edit.occurrence)
	}

	summary := "Occurrence updated"
	if edit.scope == hey.OccurrenceScopeThisAndFollowing {
		summary = "Occurrence and the following updated"
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("%s.%s", summary, extractMutationInfoFromResult(result)),
		summary,
		result)
}

// occurrenceWriteError says what HEY's refusal of an occurrence write means. The occurrence
// route answers not-found for a date that is not a day of the series and for a series the
// caller may not edit alike, and the SDK reports a form route's status as a bare API
// error, so the status is read here into the not-found it is, with both meanings named.
func occurrenceWriteError(err error, occurrence hey.EventOccurrence) error {
	if hey.AsError(err).HTTPStatus == http.StatusNotFound {
		return apierr.ErrNotFoundHint("occurrence", occurrence.String(),
			fmt.Sprintf("HEY refuses a date that is not a day of the series and a series you cannot edit alike; hey event day %s  lists that day's occurrences", occurrence.DateParam()))
	}
	return apierr.FromSDK(err)
}

// occurrenceDayWindow is the one day an occurrence is read on, [day, day+1), over every
// calendar the identity has. HEY answers that window with every series still recurring
// through it and with the day itself where an earlier edit has written it out, and it is
// one request per calendar, so there is nothing to narrow.
func occurrenceDayWindow(ctx context.Context, day time.Time) (recordingWindow, error) {
	calendars, err := allCalendarIDs(ctx)
	if err != nil {
		return recordingWindow{}, err
	}
	return recordingWindow{
		calendars: calendars,
		startsOn:  day.Format(dateLayout),
		endsOn:    day.AddDate(0, 0, 1).Format(dateLayout),
	}, nil
}

// occurrenceDay is what the day of an occurrence holds for it: the series it belongs to,
// the day itself where HEY has already written it out as a recording of its own, and the
// countdowns ending that day.
type occurrenceDay struct {
	occurrence hey.EventOccurrence
	series     generated.Recording
	realized   *generated.Recording
	countdowns []generated.Recording
}

// locateOccurrence picks the occurrence's rows out of the day's. The series is matched by
// id and has to be one: an id that names a day of some other series, or an event that does
// not repeat, is refused rather than written through the occurrence route to be answered
// not-found. The written-out day is matched by its occurrence_id alone, which names the
// series and the date together.
func locateOccurrence(rows []generated.Recording, occurrence hey.EventOccurrence) (occurrenceDay, error) {
	day := occurrenceDay{occurrence: occurrence}
	found := false
	for i := range rows {
		row := rows[i]
		switch {
		case row.Type == recordingTypeCountdown:
			day.countdowns = append(day.countdowns, row)
		case row.Id == occurrence.EventID:
			day.series = row
			found = true
		case row.OccurrenceId == occurrence.String():
			day.realized = &row
		}
	}

	id := strconv.FormatInt(occurrence.EventID, 10)
	switch {
	case !found:
		return occurrenceDay{}, apierr.ErrNotFoundHint("occurrence", occurrence.String(),
			fmt.Sprintf("hey event day %s  lists that day's occurrences with their ids", occurrence.DateParam()))
	case day.series.OccurrenceId != "":
		return occurrenceDay{}, apierr.ErrUsageHint(
			fmt.Sprintf("event %s is one day of series %d, not a series", id, day.series.ParentId),
			fmt.Sprintf("hey event edit %d --occurrence %s", day.series.ParentId, day.series.OccurrenceId))
	case !day.series.Recurring:
		return occurrenceDay{}, apierr.ErrUsageHint(
			fmt.Sprintf("event %s does not repeat, so it has no occurrences", id),
			fmt.Sprintf("hey event edit %s  changes it", id))
	}
	return day, nil
}

// event is the day as an event: the recording HEY wrote out for it, or the series with the
// day's own times in place of the series' first.
func (d occurrenceDay) event() generated.Recording {
	if d.realized != nil {
		return *d.realized
	}
	return virtualOccurrence(d.series, d.occurrence.Date)
}

// virtualOccurrence is one day of a series the way HEY builds it: the series' own fields,
// with the day's start and end in place of the first day's. It is what the write has to
// send, since a date the series began on would move the day there.
func virtualOccurrence(series generated.Recording, day time.Time) generated.Recording {
	occurrence := series
	occurrence.Id = 0
	occurrence.ParentId = series.Id
	occurrence.OccurrenceId = hey.EventOccurrence{EventID: series.Id, Date: day}.String()
	occurrence.StartsAt, occurrence.EndsAt = occurrenceInstants(series, day)
	return occurrence
}

// occurrenceInstants is when a day of the series starts and ends. HEY names the day by the
// UTC date of its start and gives it the series' wall-clock time in the series' own zone,
// so a series that starts late in the evening west of Greenwich, or early in the morning
// east of it, has its wall-clock day on the day either side of the one named. The end
// follows the start by the series' own length.
func occurrenceInstants(series generated.Recording, day time.Time) (time.Time, time.Time) {
	duration := series.EndsAt.Sub(series.StartsAt)
	if series.AllDay {
		start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		return start, start.Add(duration)
	}

	loc := time.UTC
	if series.StartsAtTimeZone != "" {
		if zone, err := time.LoadLocation(series.StartsAtTimeZone); err == nil {
			loc = zone
		}
	}
	wall := series.StartsAt.In(loc)
	named := day.Format(dateLayout)
	start := wallClockOn(day, wall, loc)
	for _, delta := range []int{0, -1, 1} {
		candidate := wallClockOn(day.AddDate(0, 0, delta), wall, loc)
		if candidate.UTC().Format(dateLayout) == named {
			start = candidate
			break
		}
	}
	return start, start.Add(duration)
}

// wallClockOn is the series' clock time on a day, resolved the way HEY resolves it. A clock
// time that does not exist on that day — the hour a zone springs forward over — is moved
// an hour later and tried again, which is what ActiveSupport does when it changes the day
// of a time; Go's time.Date picks the earlier zone instead and would land the day an hour
// before HEY's, so a title-only edit would move it.
func wallClockOn(day, wall time.Time, loc *time.Location) time.Time {
	hour, minute, second := wall.Clock()
	for step := range 24 {
		at := time.Date(day.Year(), day.Month(), day.Day(), hour+step, minute, second, 0, loc)
		if h, m, _ := at.Clock(); h == (hour+step)%24 && m == minute {
			return at
		}
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0, loc)
}

// occurrenceCountdown is the countdown the write sends: the one --countdown names, or the
// one the event already has, read back so that a write that says nothing about it does
// not remove it. --countdown 0 is how it is removed on purpose.
//
// A countdown is a recording of its own under the event, ending when the event starts. A
// day written out on its own may carry one, ending on this day; the series' ends on the
// day the series began, which is this day only for the first occurrence and otherwise one
// more read of that one day, on the series' own calendar.
func (c *eventsEditCommand) occurrenceCountdown(ctx context.Context, cmd *cobra.Command, window recordingWindow, day occurrenceDay) (hey.CountdownParams, error) {
	if cmd.Flags().Changed("countdown") {
		return c.fields.parseCountdown()
	}
	if day.realized != nil {
		if countdown, ok := countdownOf(day.countdowns, day.realized.Id); ok {
			return countdownFromRecording(countdown)
		}
		// A day that was moved is still listed under the date it stands for, but its
		// countdown ends where it now starts, so that day is where its own countdown is.
		if day.realized.StartsAt.UTC().Format(dateLayout) != day.occurrence.DateParam() {
			countdown, found, err := countdownEnding(ctx, window, *day.realized)
			if err != nil || found {
				return countdown, err
			}
		}
	}
	if countdown, ok := countdownOf(day.countdowns, day.series.Id); ok {
		return countdownFromRecording(countdown)
	}
	if day.series.StartsAt.UTC().Format(dateLayout) == day.occurrence.DateParam() {
		return hey.CountdownParams{}, nil
	}
	countdown, _, err := countdownEnding(ctx, window, day.series)
	return countdown, err
}

// countdownEnding reads the day an event starts on, over its own calendar, for the
// countdown ending there. A countdown ends at the moment its event starts, so that one
// day is the whole window it can be found in.
func countdownEnding(ctx context.Context, window recordingWindow, event generated.Recording) (hey.CountdownParams, bool, error) {
	if event.StartsAt.IsZero() {
		return hey.CountdownParams{}, false, nil
	}
	calendars := window.calendars
	if event.Calendar.Id != 0 {
		calendars = []int64{event.Calendar.Id}
	}
	starts := event.StartsAt.UTC()
	startDay := recordingWindow{
		calendars: calendars,
		startsOn:  starts.Format(dateLayout),
		endsOn:    starts.AddDate(0, 0, 1).Format(dateLayout),
	}
	countdowns, err := startDay.readTypes(ctx, recordingTypeCountdown)
	if err != nil {
		return hey.CountdownParams{}, false, err
	}
	countdown, ok := countdownOf(countdowns, event.Id)
	if !ok {
		return hey.CountdownParams{}, false, nil
	}
	params, err := countdownFromRecording(countdown)
	return params, true, err
}

// countdownOf is the countdown recording under the event with that id, if the rows hold one.
func countdownOf(rows []generated.Recording, eventID int64) (generated.Recording, bool) {
	for _, row := range rows {
		if row.Type == recordingTypeCountdown && row.ParentId == eventID {
			return row, true
		}
	}
	return generated.Recording{}, false
}

// countdownLabel is the one thing HEY serves about a countdown's length: its label, "3 weeks
// before", from which the value and the unit its own form would submit are read back.
var countdownLabel = regexp.MustCompile(`^(\d+) (day|week|month)s? before$`)

// countdownFromRecording reads the countdown back out of the recording HEY keeps for it.
// The label is the length as HEY's own form would show it, and it is trusted first. HEY
// tries months before weeks before days and takes a remainder of up to a day as a match,
// so a countdown that is exactly one day long — a day before an event at midnight, which
// is any all-day event's — comes out as "0 months before"; the recording's own span says
// what that is. Anything else this cannot read is refused rather than guessed at or
// dropped, since a countdown the write does not name is a countdown removed: the caller
// can still name it with --countdown, or remove it with --countdown 0.
func countdownFromRecording(countdown generated.Recording) (hey.CountdownParams, error) {
	unreadable := &apierr.Error{
		Code:    apierr.CodeAPI,
		Message: fmt.Sprintf("the event's countdown could not be read back: %q", terminal.SanitizeLine(countdown.Label)),
		Hint:    "pass --countdown with --countdown-unit to keep it, or --countdown 0 to remove it",
	}

	match := countdownLabel.FindStringSubmatch(countdown.Label)
	if match == nil {
		return hey.CountdownParams{}, unreadable
	}
	value, _ := strconv.Atoi(match[1])
	if value >= 1 {
		units := map[string]hey.CountdownUnit{
			"day":   hey.CountdownUnitDays,
			"week":  hey.CountdownUnitWeeks,
			"month": hey.CountdownUnitMonths,
		}
		return hey.CountdownParams{Value: value, Unit: units[match[2]]}, nil
	}
	if !countdown.StartsAt.IsZero() && countdown.EndsAt.Sub(countdown.StartsAt) == 24*time.Hour {
		return hey.CountdownParams{Value: 1, Unit: hey.CountdownUnitDays}, nil
	}
	return hey.CountdownParams{}, unreadable
}

// errPlainNotes is how an occurrence edit refuses to flatten notes it was not asked to
// change. HEY serves them as plain text and takes back whatever it is sent, so nothing here
// can tell formatted notes from plain ones; the caller has to say the loss is acceptable.
func errPlainNotes(occurrence hey.EventOccurrence) error {
	return &apierr.Error{
		Code:    apierr.CodeUsage,
		Message: fmt.Sprintf("occurrence %s has notes, which HEY serves only as plain text, so sending them back would lose their formatting", occurrence),
		Hint:    "pass --allow-plain-notes to send them back as text, or --notes to replace them",
	}
}
