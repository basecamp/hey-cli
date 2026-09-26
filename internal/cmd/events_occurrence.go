package cmd

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
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

// occurrenceWrite is one day of a repeating event and how much of the series a write to it
// reaches, as --occurrence and --apply-to name them for an edit or a delete.
type occurrenceWrite struct {
	occurrence hey.EventOccurrence
	scope      hey.OccurrenceScope
}

// parseOccurrenceFlags reads --occurrence and --apply-to as edit and delete both take them,
// and answers nil for a write to the whole event. Everything here is refused before a
// request is made: the occurrence_id has to be the one a day or a week listing served, byte
// for byte, naming the series the positional id names, and --apply-to has to say current or
// future. The hints name the command they were given to.
func parseOccurrenceFlags(cmd *cobra.Command, id int64, value, applyTo string) (*occurrenceWrite, error) {
	flags := cmd.Flags()
	command := cmd.CommandPath()
	if !flags.Changed("occurrence") {
		if flags.Changed("apply-to") {
			return nil, apierr.ErrUsageHint("--apply-to needs --occurrence",
				command+" 4821 --occurrence 4821_2026-09-15 --apply-to current")
		}
		return nil, nil
	}
	// An --occurrence given empty — a script's unset variable — must not quietly become a
	// write to the whole series, which is the one thing the flag was there to avoid.
	if value == "" {
		return nil, apierr.ErrUsageHint("--occurrence needs an occurrence_id",
			"an occurrence_id as hey event day serves it, <series id>_<YYYY-MM-DD>, for example 4821_2026-09-15")
	}

	occurrence, err := hey.ParseOccurrenceID(value)
	if err != nil || occurrence.String() != value {
		return nil, apierr.ErrUsageHint(fmt.Sprintf("invalid occurrence: %s", value),
			"an occurrence_id as hey event day serves it, <series id>_<YYYY-MM-DD>, for example 4821_2026-09-15")
	}
	// The scope is read before the series is checked, so the command the hint below names is
	// one that runs.
	scope, err := parseApplyTo(applyTo, flags.Changed("apply-to"))
	if err != nil {
		return nil, err
	}
	if occurrence.EventID != id {
		return nil, apierr.ErrUsageHint(
			fmt.Sprintf("occurrence %s belongs to series %d, not %d", value, occurrence.EventID, id),
			fmt.Sprintf("%s %d --occurrence %s --apply-to %s", command, occurrence.EventID, value, applyToFlag(scope)))
	}
	return &occurrenceWrite{occurrence: occurrence, scope: scope}, nil
}

// applyToFlag is the scope as --apply-to takes it, for a hint that names a command to run.
func applyToFlag(scope hey.OccurrenceScope) string {
	if scope == hey.OccurrenceScopeThisAndFollowing {
		return "future"
	}
	return "current"
}

// parseApplyTo reads the scope. There is no default: a caller who does not say how much of
// the series to change has not said what they want, and the narrower answer is not safer
// when the wider one was meant.
func parseApplyTo(value string, given bool) (hey.OccurrenceScope, error) {
	if !given {
		return "", apierr.ErrUsageHint("--apply-to is required with --occurrence",
			"--apply-to current reaches that day alone; --apply-to future reaches it and every day after it")
	}
	switch value {
	case "current":
		return hey.OccurrenceScopeThisEvent, nil
	case "future":
		return hey.OccurrenceScopeThisAndFollowing, nil
	}
	return "", apierr.ErrUsageHint(fmt.Sprintf("invalid apply-to: %s", value), "one of current or future")
}

// parseOccurrence is parseOccurrenceFlags with what only an edit refuses: a date that is not
// the occurrence's own, a future edit that does not state the schedule of the new series,
// and a schedule change applied to one day, which is HEY's rule as much as this command's.
func (c *eventsEditCommand) parseOccurrence(cmd *cobra.Command, id int64, on string) (*occurrenceWrite, error) {
	edit, err := parseOccurrenceFlags(cmd, id, c.occurrence, c.applyTo)
	if err != nil || edit == nil {
		return nil, err
	}
	if on != "" {
		day, dateErr := parseDateArg("date", on)
		if dateErr != nil {
			return nil, dateErr
		}
		if day.Format(dateLayout) != edit.occurrence.DateParam() {
			return nil, apierr.ErrUsageHint(fmt.Sprintf("date %s is not the day of occurrence %s", on, c.occurrence),
				"an occurrence is read on its own day, so leave the date out or name that day")
		}
	}

	flags := cmd.Flags()
	if edit.scope == hey.OccurrenceScopeThisAndFollowing && !flags.Changed("repeat") {
		return nil, apierr.ErrUsageHint("--apply-to future needs --repeat to define the new series",
			"pass --repeat with --repeat-times or --repeat-until, or pass --repeat alone to repeat forever")
	}
	if edit.scope == hey.OccurrenceScopeThisEvent {
		for _, flag := range []string{"repeat", "repeat-until", "repeat-times"} {
			if flags.Changed(flag) {
				return nil, apierr.ErrUsageHint(fmt.Sprintf("--%s cannot apply to one day of a series", flag),
					"a change to the schedule reaches this day and every one after it with --apply-to future, or the whole series when the id is edited alone")
			}
		}
	}
	return edit, nil
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
// email, reminders and circle. Two things the whole-event edit loses are handled here rather
// than lost. An occurrence-owned countdown is read and sent again; an inherited one is left
// inherited for a current edit and copied for a future split, so only --countdown 0 removes
// it. Notes are served only as plain text, so an edit
// that would send formatted notes back as text is refused unless --allow-plain-notes says
// that is acceptable or --notes replaces them.
//
// One thing cannot be kept and cannot be refused either: an attached email the editor
// cannot read is left out of what HEY serves, indistinguishable from none, and HEY clears
// the attachment whether the write sends an empty entry id or no key at all. That is the
// server's to fix; the docs say so.
func (c *eventsEditCommand) editOccurrence(ctx context.Context, cmd *cobra.Command, edit occurrenceWrite) error {
	// The flags that need no read are refused first, so a bad one costs no request.
	repeat, err := c.fields.parseRepeat(cmd, true)
	if err != nil {
		return err
	}
	if _, err = c.fields.parseCountdown(); err != nil {
		return err
	}
	if err = c.fields.validateExplicitScheduleFlags(cmd); err != nil {
		return err
	}
	var explicitReminders []time.Duration
	if cmd.Flags().Changed("remind") {
		explicitReminders, err = c.fields.parseReminders()
		if err != nil {
			return err
		}
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
	day, err := locateOccurrence(cmd, rows, edit.occurrence, edit.scope)
	if err != nil {
		return err
	}
	event := day.event()
	if day.realized == nil && !day.series.RecurrenceSchedule.Preset {
		event, err = readCustomVirtualOccurrence(ctx, edit.occurrence)
		if err != nil {
			return err
		}
	}

	if edit.scope == hey.OccurrenceScopeThisAndFollowing {
		if err = day.refuseAnUnsafeFutureBoundary("changed",
			"use --apply-to current for that day, edit the whole series, or split from a later virtual occurrence",
			"move that occurrence back to its series date and time with --apply-to current, then retry the future edit"); err != nil {
			return err
		}
	}

	if event.Description != "" && !cmd.Flags().Changed("notes") && !c.allowPlainNotes {
		return errPlainNotes(edit.occurrence)
	}
	// A future edit records the new series from the series' own guest list and invites it,
	// whatever this day's had come to be, and a guest list is only ever sent on purpose.
	if edit.scope == hey.OccurrenceScopeThisAndFollowing && day.realized != nil && !cmd.Flags().Changed("invite") {
		if guests := attendeeAddresses(day.realized.Attendances); !sameAddresses(guests, attendeeAddresses(day.series.Attendances)) {
			return errDayGuests(edit.occurrence, guests)
		}
	}

	schedule, err := c.fields.scheduleFrom(ctx, cmd, event)
	if err != nil {
		return err
	}
	err = checkRepeatStarts(repeat, schedule.startsAt)
	if err != nil {
		return err
	}
	reminders := explicitReminders
	if !cmd.Flags().Changed("remind") {
		reminders, err = c.fields.remindersFrom(cmd, event)
		if err != nil {
			return err
		}
	}
	countdown, err := c.occurrenceCountdown(ctx, cmd, window, day, edit.scope)
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
	} else if edit.scope == hey.OccurrenceScopeThisAndFollowing && day.realized != nil && day.realized.Calendar.Id != 0 {
		// HEY records the new series on the series' calendar unless told otherwise, so a day
		// that had been moved to another calendar would move back with everything after it.
		calendarID := day.realized.Calendar.Id
		changes.CalendarID = &calendarID
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
// caller may not change alike, and the SDK reports a form route's status as a bare API
// error, so the status is read here into the not-found it is, with both meanings named.
func occurrenceWriteError(err error, occurrence hey.EventOccurrence) error {
	if hey.AsError(err).HTTPStatus == http.StatusNotFound {
		return apierr.ErrNotFoundHint("occurrence", occurrence.String(),
			fmt.Sprintf("HEY refuses a date that is not a day of the series and a series you cannot change alike; hey event day %s  lists that day's occurrences", occurrence.DateParam()))
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
// series and the date together. The hints name the command that asked.
func locateOccurrence(cmd *cobra.Command, rows []generated.Recording, occurrence hey.EventOccurrence, scope hey.OccurrenceScope) (occurrenceDay, error) {
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
	command := cmd.CommandPath()
	switch {
	case !found:
		return occurrenceDay{}, apierr.ErrNotFoundHint("occurrence", occurrence.String(),
			fmt.Sprintf("hey event day %s  lists that day's occurrences with their ids", occurrence.DateParam()))
	case day.series.OccurrenceId != "":
		return occurrenceDay{}, apierr.ErrUsageHint(
			fmt.Sprintf("event %s is one day of series %d, not a series", id, day.series.ParentId),
			fmt.Sprintf("%s %d --occurrence %s --apply-to %s", command, day.series.ParentId, day.series.OccurrenceId, applyToFlag(scope)))
	case !day.series.Recurring:
		return occurrenceDay{}, apierr.ErrUsageHint(
			fmt.Sprintf("event %s does not repeat, so it has no occurrences", id),
			fmt.Sprintf("%s %s  acts on the whole event", command, id))
	}
	return day, nil
}

// event is the day as an event: the recording HEY wrote out for it, or a preset series with
// the day's own times in place of the series' first. A custom series needs the authoritative
// virtual occurrence from readCustomVirtualOccurrence instead.
func (d occurrenceDay) event() generated.Recording {
	if d.realized != nil {
		return *d.realized
	}
	return virtualOccurrence(d.series, d.occurrence.Date)
}

// refuseAnUnsafeFutureBoundary refuses a future write from a day HEY has written out when
// the boundary of that write cannot be trusted. HEY truncates the series at the day's
// occurrence date but cancels the written-out days from the selected day's actual start, so
// a day moved off its series time would take an earlier edited day with it, or leave a later
// one behind. A preset puts every day at the series' wall clock, so a moved day shows; a
// custom schedule is opaque in the API — BYHOUR, RDATE and similar rules can put a day at a
// different time — so a written-out day there cannot be checked and is refused outright. A
// virtual day has no start of its own and is always safe.
//
// verb says what the write would do to the days ("changed", "deleted"); the hints are the
// caller's way round each refusal.
func (d occurrenceDay) refuseAnUnsafeFutureBoundary(verb, customHint, movedHint string) error {
	if d.realized == nil {
		return nil
	}
	if !d.series.RecurrenceSchedule.Preset {
		return apierr.ErrUsageHint(
			fmt.Sprintf("occurrence %s belongs to an opaque custom schedule and cannot be %s with --apply-to future after HEY has written that day out", d.occurrence.String(), verb),
			customHint)
	}
	virtual := virtualOccurrence(d.series, d.occurrence.Date)
	if !d.realized.StartsAt.Equal(virtual.StartsAt) {
		return apierr.ErrUsageHint(
			fmt.Sprintf("occurrence %s was moved from %s to %s and cannot be %s with --apply-to future", d.occurrence.String(), virtual.StartsAt.Format(time.RFC3339), d.realized.StartsAt.Format(time.RFC3339), verb),
			movedHint)
	}
	return nil
}

// readCustomVirtualOccurrence finds the exact virtual occurrence HEY serves in its Day view.
// A custom recurrence schedule is opaque in the API: BYHOUR, RDATE and similar rules can put
// the day at a different clock time from its parent, so synthesizing it from the parent would
// move it on an otherwise unrelated edit. An occurrence id uses the UTC start date while the
// Day view uses the identity's calendar date, so that view can be the day on either side.
func readCustomVirtualOccurrence(ctx context.Context, occurrence hey.EventOccurrence) (generated.Recording, error) {
	for _, offset := range []int{0, -1, 1} {
		date := occurrence.Date.AddDate(0, 0, offset).Format(dateLayout)
		period, err := sdk.CalendarPeriods().Day(ctx, date)
		if err != nil {
			return generated.Recording{}, apierr.FromSDK(err)
		}
		if period == nil {
			continue
		}
		for _, event := range filterRecordingsByType(&period.Recordings, recordingTypeEvent) {
			if event.OccurrenceId == occurrence.String() {
				return event, nil
			}
		}
	}
	return generated.Recording{}, apierr.ErrNotFoundHint("occurrence", occurrence.String(),
		"HEY's Day view did not serve this opaque custom occurrence; read it with hey event day or hey event week and retry with the occurrence_id served there")
}

// virtualOccurrence is one day of a preset series the way HEY builds it: the series' own
// fields, with the day's start and end in place of the first day's. It is what the write has
// to send, since a date the series began on would move the day there.
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

// occurrenceCountdown is the countdown the write sends: the one --countdown names, or an
// existing countdown owned by the recording this write updates. An inherited countdown is
// left out of a current-only write so it stays inherited; a future split copies the series'
// countdown because the replacement series needs one of its own. --countdown 0 removes one
// on purpose.
//
// A countdown is a recording of its own under the event, ending when the event starts. A
// day written out on its own may carry one, ending on this day; the series' ends on the
// day the series began, which is this day only for the first occurrence and otherwise one
// more read of that one day, on the series' own calendar.
//
// Removing one is the other way round. HEY shows one day of a series the series' countdown
// whenever the day has none of its own, and a write that names no countdown removes only
// the day's own; so --countdown 0 on one day of a series with a countdown would be
// reported done and change nothing. That is refused: the countdown comes off with the
// series, by --apply-to future or an edit of the series itself.
func (c *eventsEditCommand) occurrenceCountdown(ctx context.Context, cmd *cobra.Command, window recordingWindow, day occurrenceDay, scope hey.OccurrenceScope) (hey.CountdownParams, error) {
	if cmd.Flags().Changed("countdown") {
		countdown, err := c.fields.parseCountdown()
		if err != nil || countdown.Value != 0 || scope != hey.OccurrenceScopeThisEvent {
			return countdown, err
		}
		if _, inherited, err := c.seriesCountdown(ctx, window, day); err != nil || inherited {
			if err != nil {
				return hey.CountdownParams{}, err
			}
			return hey.CountdownParams{}, apierr.ErrUsageHint(
				fmt.Sprintf("the series has a countdown, and HEY shows it on %s whatever the day's own", day.occurrence),
				"--apply-to future removes it from this day and every one after it; an edit of the series id alone removes it everywhere")
		}
		return hey.CountdownParams{}, nil
	}
	if day.realized != nil {
		if countdown, ok := countdownOf(day.countdowns, day.realized.Id); ok {
			return c.countdownFromRecording(ctx, countdown, *day.realized)
		}
		// A day that was moved is still listed under the date it stands for, but its
		// countdown ends where it now starts, so that day is where its own countdown is.
		if day.realized.StartsAt.UTC().Format(dateLayout) != day.occurrence.DateParam() {
			countdown, found, err := c.countdownEnding(ctx, window, *day.realized)
			if err != nil || found {
				return countdown, err
			}
		}
	}
	if scope == hey.OccurrenceScopeThisEvent {
		return hey.CountdownParams{}, nil
	}
	countdown, _, err := c.seriesCountdown(ctx, window, day)
	return countdown, err
}

// seriesCountdown is the series' own countdown: on the day, where the day is the one the
// series began on, and otherwise on that first day in one more read.
func (c *eventsEditCommand) seriesCountdown(ctx context.Context, window recordingWindow, day occurrenceDay) (hey.CountdownParams, bool, error) {
	if countdown, ok := countdownOf(day.countdowns, day.series.Id); ok {
		params, err := c.countdownFromRecording(ctx, countdown, day.series)
		return params, true, err
	}
	if day.series.StartsAt.UTC().Format(dateLayout) == day.occurrence.DateParam() {
		return hey.CountdownParams{}, false, nil
	}
	return c.countdownEnding(ctx, window, day.series)
}

// countdownEnding reads the day an event starts on, over its own calendar, for the
// countdown ending there. A countdown ends at the moment its event starts, so that one
// day is the whole window it can be found in.
func (c *eventsEditCommand) countdownEnding(ctx context.Context, window recordingWindow, event generated.Recording) (hey.CountdownParams, bool, error) {
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
	params, err := c.countdownFromRecording(ctx, countdown, event)
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

// countdownLabel is the human label HEY serves for a countdown, such as "3 weeks before".
// It is lossy, so countdownFromRecording checks it against the recording's timestamps rather
// than treating its displayed value and unit as the interval the form originally submitted.
var countdownLabel = regexp.MustCompile(`^(\d+) (day|week|month)s? before$`)

// countdownFromRecording reads the countdown back out of the recording HEY keeps for it.
//
// HEY starts the countdown at the beginning of the day in the zone handling the write,
// less the requested interval, and ends it when the event starts. The recording's whole
// span therefore also includes however far into that day the event falls. Taking that part
// back out is important on a 25-hour fall-back day: flooring the whole span by 24 hours
// would otherwise add a day to the countdown. The label is not enough on its own either: HEY tries months,
// then weeks, then days, and takes a remainder of up to a whole day as a match, so eight
// days at midnight is "1 weeks before", 29 days is "4 weeks before", and one day is "0
// months before". The full span is used only to verify that label. The exact interval comes
// from the countdown's start relative to a possible beginning of day, and has to be one of
// the value/unit pairs HEY's form can submit.
//
// A recording this cannot read is refused rather than guessed at or dropped, since a
// countdown the write does not name is a countdown removed: the caller can still name it
// with --countdown, or remove it with --countdown 0.
func countdownFromRecording(countdown, event generated.Recording, additionalZones ...string) (hey.CountdownParams, error) {
	unreadable := &apierr.Error{
		Code:    apierr.CodeAPI,
		Message: fmt.Sprintf("the event's countdown could not be read back: %q", terminal.SanitizeLine(countdown.Label)),
		Hint:    "pass --countdown with --countdown-unit to keep it, or --countdown 0 to remove it",
	}

	match := countdownLabel.FindStringSubmatch(countdown.Label)
	if match == nil {
		return hey.CountdownParams{}, unreadable
	}
	labelValue, err := strconv.Atoi(match[1])
	if err != nil {
		return hey.CountdownParams{}, unreadable
	}
	labelUnit := match[2]

	if countdown.StartsAt.IsZero() || countdown.EndsAt.IsZero() || event.StartsAt.IsZero() ||
		!countdown.EndsAt.After(countdown.StartsAt) || !countdown.EndsAt.Equal(event.StartsAt) {
		return hey.CountdownParams{}, unreadable
	}
	if !countdownLabelMatchesDuration(countdown.EndsAt.Sub(countdown.StartsAt), labelValue, labelUnit) {
		return hey.CountdownParams{}, unreadable
	}

	// A JSON write runs in UTC, while the web form runs in the reader's zone. The recording
	// does not say which one created it, so try UTC, the event's zone and any additional
	// identity zone the caller supplies. A candidate is
	// accepted only when it reconstructs one of the exact value/unit pairs the form can send
	// and produces the label HEY served. If both zones give different valid answers, there is
	// no lossless answer and the edit stops.
	locations := []*time.Location{time.UTC}
	seenZones := map[string]bool{"UTC": true}
	zones := append([]string{event.StartsAtTimeZone}, additionalZones...)
	for _, name := range zones {
		if name == "" || seenZones[name] {
			continue
		}
		loc, err := time.LoadLocation(name)
		if err != nil {
			return hey.CountdownParams{}, unreadable
		}
		seenZones[name] = true
		locations = append(locations, loc)
	}

	var answer hey.CountdownParams
	found := false
	for _, loc := range locations {
		localEnd := countdown.EndsAt.In(loc)
		beginning := time.Date(localEnd.Year(), localEnd.Month(), localEnd.Day(), 0, 0, 0, 0, loc)
		candidate, ok := countdownParamsForInterval(beginning.Sub(countdown.StartsAt))
		if !ok {
			continue
		}
		if found && candidate != answer {
			return hey.CountdownParams{}, unreadable
		}
		answer, found = candidate, true
	}
	if !found {
		return hey.CountdownParams{}, unreadable
	}
	return answer, nil
}

// countdownFromRecording includes the identity's zone when decoding a web-created
// countdown. HTML writes use that zone while JSON API writes use UTC, and the event's own
// zone need not be either one. Trying it even when another zone works preserves the pure
// decoder's ambiguity check.
func (c *eventsEditCommand) countdownFromRecording(ctx context.Context, countdown, event generated.Recording) (hey.CountdownParams, error) {
	params, unreadable := countdownFromRecording(countdown, event)

	zone, err := c.fields.accountTimeZone(ctx)
	if err != nil {
		return hey.CountdownParams{}, apierr.FromSDK(err)
	}
	if zone == "" {
		return params, unreadable
	}
	return countdownFromRecording(countdown, event, zone)
}

// countdownLabelMatchesDuration applies the same month, week, then day test HEY uses to
// label a countdown recording. The label describes the recording's whole duration, which
// includes the event's time of day; it does not directly describe the interval the form sent.
func countdownLabelMatchesDuration(duration time.Duration, labelValue int, labelUnit string) bool {
	if duration <= 0 {
		return false
	}
	day := time.Duration(hey.CountdownUnitDays) * time.Second
	for _, candidate := range []struct {
		name     string
		duration time.Duration
	}{
		{name: "month", duration: time.Duration(hey.CountdownUnitMonths) * time.Second},
		{name: "week", duration: time.Duration(hey.CountdownUnitWeeks) * time.Second},
		{name: "day", duration: day},
	} {
		if duration%candidate.duration <= day {
			return int(duration/candidate.duration) == labelValue && candidate.name == labelUnit
		}
	}
	return false
}

// countdownParamsForInterval recognizes an exact countdown interval the form can submit.
// The label's unit does not choose the interval's unit: it describes the recording's longer
// span, and on a 25-hour day can say "31 days" for an exact one-month interval.
func countdownParamsForInterval(interval time.Duration) (hey.CountdownParams, bool) {
	if interval <= 0 {
		return hey.CountdownParams{}, false
	}
	day := time.Duration(hey.CountdownUnitDays) * time.Second
	week := time.Duration(hey.CountdownUnitWeeks) * time.Second
	month := time.Duration(hey.CountdownUnitMonths) * time.Second

	unit := hey.CountdownUnitDays
	var value int
	switch {
	case interval%month == 0:
		unit, value = hey.CountdownUnitMonths, int(interval/month)
	case interval%week == 0:
		unit, value = hey.CountdownUnitWeeks, int(interval/week)
	case interval%day == 0:
		value = int(interval / day)
	default:
		return hey.CountdownParams{}, false
	}
	if value < 1 || value > 30 {
		return hey.CountdownParams{}, false
	}
	return hey.CountdownParams{Value: value, Unit: unit}, true
}

// attendeeAddresses is a guest list as the set of addresses on it, which is how two lists
// are told apart: the same guests in another order or with another status are one list.
func attendeeAddresses(attendances []generated.Attendance) []string {
	addresses := make([]string, 0, len(attendances))
	for _, attendance := range attendances {
		if address := strings.ToLower(strings.TrimSpace(attendance.EmailAddress)); address != "" {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

func sameAddresses(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, address := range a {
		seen[address]++
	}
	for _, address := range b {
		if seen[address] == 0 {
			return false
		}
		seen[address]--
	}
	return true
}

// errDayGuests is how a future edit refuses to invite the series' guests to a day that had
// come to have guests of its own. HEY records the new series from the series' list and
// sends the invitations, so the caller has to say whose list the new series gets.
func errDayGuests(occurrence hey.EventOccurrence, guests []string) error {
	return &apierr.Error{
		Code:    apierr.CodeUsage,
		Message: fmt.Sprintf("occurrence %s has a guest list of its own (%s), and a future edit would give the new series the series' list instead", occurrence, terminal.SanitizeLine(strings.Join(guests, ", "))),
		Hint:    "pass --invite for each address the new series should invite; the list replaces the series' and sends invitations",
	}
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
