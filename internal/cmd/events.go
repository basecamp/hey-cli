package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

// recordingTypeEvent is how HEY names an event among the recordings a calendar holds.
const recordingTypeEvent = "Calendar::Event"

type eventsCommand struct {
	cmd *cobra.Command
}

func newEventsCommand() *eventsCommand {
	eventsCommand := &eventsCommand{}
	eventsCommand.cmd = &cobra.Command{
		Use:   "event",
		Short: "Read and manage calendar events",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: list, day, week, add, edit, delete. \"What's on the schedule today?\" is answered by day, not list: day and week read the span as HEY draws it, with a repeating event expanded into the occurrences inside it, over the calendars switched on in HEY. list reads what calendars hold — every calendar unless --calendar names one — and a repeating event is one row, its series, on the day the series began. An edit is not a patch on HEY's side: it resends the notes, location, link, attached email, reminders and time zones the event already carries, so notes lose their formatting and a countdown is removed unless --countdown names one again.",
		},
	}

	eventsCommand.cmd.AddCommand(newEventsListCommand().cmd)
	eventsCommand.cmd.AddCommand(newEventsDayCommand().cmd)
	eventsCommand.cmd.AddCommand(newEventsWeekCommand().cmd)
	eventsCommand.cmd.AddCommand(newEventsAddCommand().cmd)
	eventsCommand.cmd.AddCommand(newEventsEditCommand().cmd)
	eventsCommand.cmd.AddCommand(newEventsDeleteCommand().cmd)

	return eventsCommand
}

// list

type eventsListCommand struct {
	cmd    *cobra.Command
	filter recordingFilter
}

func newEventsListCommand() *eventsListCommand {
	eventsListCommand := &eventsListCommand{
		filter: recordingFilter{defaultWindow: eventWindow, defaultCalendars: allCalendarIDs},
	}
	eventsListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List calendar events",
		Long: `List the events calendars hold over a date window.

A repeating event is stored once, so it lists once, as its series, on the day the series
began. For the events of a day or a week as HEY draws them — occurrences of a repeating
series expanded into the days they fall on — read 'hey event day' or 'hey event week'.`,
		Example: `  hey event list
  hey event list --starts-on 2026-01-01 --ends-on 2026-01-31
  hey event list --calendar 123 --limit 5 --json`,
		RunE: eventsListCommand.run,
		Args: cobra.NoArgs,
	}

	eventsListCommand.filter.registerFlags(eventsListCommand.cmd, "events", "Calendar ID to read (defaults to every calendar)")

	return eventsListCommand
}

func (c *eventsListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	window, err := c.filter.resolve(ctx)
	if err != nil {
		return err
	}

	events, err := window.read(ctx, recordingTypeEvent)
	if err != nil {
		return err
	}

	total := len(events)
	if c.filter.limit > 0 && !c.filter.all && len(events) > c.filter.limit {
		events = events[:c.filter.limit]
	}
	notice := output.TruncationNotice(len(events), total)

	return writeEventRows(cmd, events, window.describe(), notice)
}

// eventBoundary writes one end of an event: a day for an all-day event, a day and a clock
// time for a timed one. An all-day event has no time of day, and printing it midnight reads
// as an event that starts at midnight.
func eventBoundary(at time.Time, allDay bool) string {
	if allDay {
		return formatDate(at)
	}
	return formatTimestamp(at)
}

// add

type eventsAddCommand struct {
	cmd    *cobra.Command
	fields eventFields
}

func newEventsAddCommand() *eventsAddCommand {
	eventsAddCommand := &eventsAddCommand{}
	eventsAddCommand.cmd = &cobra.Command{
		Use:   "add [title]",
		Short: "Create an event",
		Long: `Create an event.

An event with no --start-time is an all-day event. A --start-time with no --end-time runs
for an hour. Clock times are read in --time-zone, which defaults to this machine's zone.`,
		Example: `  hey event add "Design review" --starts-on 2026-09-02 --start-time 14:00 --end-time 15:00
  hey event add "Sarah's birthday" --starts-on 2026-09-02
  hey event add "Standup" --start-time 09:15 --repeat every_weekday --calendar 123
  hey event add "Quarterly planning" --starts-on 2026-09-14 --ends-on 2026-09-15 --remind 1d --remind 1h`,
		RunE: eventsAddCommand.run,
		Args: cobra.MaximumNArgs(1),
	}

	eventsAddCommand.fields.registerFlags(eventsAddCommand.cmd)

	return eventsAddCommand
}

func (c *eventsAddCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	title, err := eventTitleFrom(c.fields.title, args)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	calendarID, err := c.fields.resolveCalendar(ctx)
	if err != nil {
		return err
	}

	schedule, err := c.fields.newSchedule()
	if err != nil {
		return err
	}
	reminders, err := c.fields.parseReminders()
	if err != nil {
		return err
	}
	repeat, err := c.fields.parseRepeat()
	if err != nil {
		return err
	}
	countdown, err := c.fields.parseCountdown()
	if err != nil {
		return err
	}

	params := hey.CreateCalendarEventParams{
		CalendarID:    calendarID,
		Title:         title,
		StartsAt:      schedule.startsAt,
		EndsAt:        schedule.endsAt,
		AllDay:        schedule.allDay,
		StartTime:     schedule.startTime,
		EndTime:       schedule.endTime,
		StartTimeZone: schedule.zone,
		EndTimeZone:   schedule.zone,
		Reminders:     reminders,
		Content: hey.EventContentParams{
			Notes:    c.fields.notes,
			Location: c.fields.location,
			Link:     c.fields.link,
		},
		Attendees: c.fields.invitesOrNil(cmd),
		Countdown: countdown,
		Repeat:    repeat,
	}
	if cmd.Flags().Changed("circle") {
		params.Highlighted = &c.fields.circle
	}

	result, err := sdk.CalendarEvents().Create(ctx, params)
	if err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutationLine(cmd,
		fmt.Sprintf("Event created.%s", extractMutationInfoFromResult(result)),
		"Event created",
		result)
}

// edit

type eventsEditCommand struct {
	cmd    *cobra.Command
	fields eventFields
}

func newEventsEditCommand() *eventsEditCommand {
	eventsEditCommand := &eventsEditCommand{}
	eventsEditCommand.cmd = &cobra.Command{
		Use:   "edit <id> [date]",
		Short: "Change an event",
		Long: `Change an event. Only the flags you name change; the rest of the event is read and
sent back, because HEY clears whatever a write leaves out.

Two things cannot survive that round trip, and both are HEY's doing rather than this
command's. Notes are served back as plain text, so saving flattens their formatting. A
countdown is not served at all, so an edit removes one unless --countdown names it again.

The event is found by reading the calendars it might be on, which is one request each and
covers the pages HEY answers with. Give the day it starts as [date] to look on that day
alone, or --calendar to look on one calendar.`,
		Example: `  hey event edit 4821 --title "Design review (moved)"
  hey event edit 4821 --starts-on 2026-09-04 --start-time 15:00
  hey event edit 4821 2026-09-02 --location "Studio, 3rd floor"
  hey event edit 4821 --circle=false`,
		RunE: eventsEditCommand.run,
		Args: cobra.RangeArgs(1, 2),
	}

	eventsEditCommand.fields.registerFlags(eventsEditCommand.cmd)

	return eventsEditCommand
}

func (c *eventsEditCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	id, err := parsePositiveID(args[0], "event")
	if err != nil {
		return err
	}

	on := ""
	if len(args) > 1 {
		if _, err = parseDateArg("date", args[1]); err != nil {
			return err
		}
		on = args[1]
	}

	ctx := cmd.Context()
	event, err := c.findEvent(ctx, id, on)
	if err != nil {
		return err
	}

	schedule, err := c.fields.scheduleFrom(cmd, event)
	if err != nil {
		return err
	}
	repeat, err := c.fields.parseRepeat()
	if err != nil {
		return err
	}
	countdown, err := c.fields.parseCountdown()
	if err != nil {
		return err
	}
	reminders, err := c.fields.remindersFrom(cmd, event)
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
	if cmd.Flags().Changed("circle") {
		changes.Highlighted = &c.fields.circle
	}

	result, err := sdk.CalendarEvents().Update(ctx, id, changes)
	if err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutationLine(cmd,
		fmt.Sprintf("Event updated.%s", extractMutationInfoFromResult(result)),
		"Event updated",
		result)
}

// findEvent reads the event so the update can send back what it is not changing. HEY serves
// no event on its own, so it is looked for among the recordings of the calendars it could be
// on — every one of them, or the one --calendar names, over the day given or a window wide
// enough to cover an event somebody is editing.
//
// The day is read as the window [day, day+1): HEY's recordings window is a pair of
// instants, and the degenerate same-day window — midnight to the same midnight — can
// never contain a timed event, which is how editing an event by its own day used to
// answer not-found. Reading a day too many is harmless here: the event is matched by id.
func (c *eventsEditCommand) findEvent(ctx context.Context, id int64, on string) (generated.Recording, error) {
	endsOn := on
	if day, err := time.Parse("2006-01-02", on); err == nil {
		endsOn = day.AddDate(0, 0, 1).Format("2006-01-02")
	}
	filter := recordingFilter{
		calendar:         c.fields.calendar,
		startsOn:         on,
		endsOn:           endsOn,
		defaultWindow:    func(now time.Time) (time.Time, time.Time) { return now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0) },
		defaultCalendars: allCalendarIDs,
	}
	window, err := filter.resolve(ctx)
	if err != nil {
		return generated.Recording{}, err
	}

	events, err := window.read(ctx, recordingTypeEvent)
	if err != nil {
		return generated.Recording{}, err
	}
	for _, event := range events {
		if event.Id == id {
			return event, nil
		}
	}

	return generated.Recording{}, apierr.ErrNotFoundHint("event", strconv.FormatInt(id, 10),
		fmt.Sprintf("hey event edit %d <YYYY-MM-DD>  reads the day it starts on", id))
}

// delete

type eventsDeleteCommand struct {
	cmd *cobra.Command
}

func newEventsDeleteCommand() *eventsDeleteCommand {
	eventsDeleteCommand := &eventsDeleteCommand{}
	eventsDeleteCommand.cmd = &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an event",
		Annotations: map[string]string{
			"agent_notes": "Deletes the whole event, recurring series included. An event on a shared calendar is deleted for everybody on it.",
		},
		Example: `  hey event delete 4821`,
		RunE:    eventsDeleteCommand.run,
		Args:    usageExactOneArg(),
	}

	return eventsDeleteCommand
}

func (c *eventsDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	id, err := parsePositiveID(args[0], "event")
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	if err := sdk.CalendarEvents().Delete(ctx, id); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, "Event deleted", nil)
}

// --- Shared write flags ---

// eventFields is the flag set `events add` and `events edit` share. They take the same event
// apart, and the only difference is that a create starts from nothing while an edit starts
// from what HEY already holds.
type eventFields struct {
	calendar     int64
	title        string
	startsOn     string
	endsOn       string
	allDay       bool
	startTime    string
	endTime      string
	timeZone     string
	notes        string
	location     string
	link         string
	invites      []string
	circle       bool
	repeat       string
	repeatUntil  string
	repeatTimes  int
	countdown    int
	countdownFor string
	reminders    []string
}

func (f *eventFields) registerFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.Int64Var(&f.calendar, "calendar", 0, "Calendar ID to file the event on")
	flags.StringVarP(&f.title, "title", "t", "", "Event title")
	flags.StringVar(&f.startsOn, "starts-on", "", "Start date (YYYY-MM-DD, defaults to today)")
	flags.StringVar(&f.endsOn, "ends-on", "", "End date (YYYY-MM-DD, defaults to the start date)")
	flags.BoolVar(&f.allDay, "all-day", false, "Make it an all-day event")
	flags.StringVar(&f.startTime, "start-time", "", "Start time (HH:MM)")
	flags.StringVar(&f.endTime, "end-time", "", "End time (HH:MM, defaults to an hour after the start)")
	flags.StringVar(&f.timeZone, "time-zone", "", "IANA zone the times are written in (defaults to this machine's)")
	flags.StringVar(&f.notes, "notes", "", "Event notes")
	flags.StringVar(&f.location, "location", "", "Event location")
	flags.StringVar(&f.link, "link", "", "Meeting or reference URL")
	flags.StringArrayVar(&f.invites, "invite", nil, "Email address to invite (repeatable, replaces the guest list)")
	flags.BoolVar(&f.circle, "circle", false, "Circle the event")
	flags.StringVar(&f.repeat, "repeat", "", "Repeat: every_day, every_weekday, every_week, every_other_week, every_day_of_month, every_year")
	flags.StringVar(&f.repeatUntil, "repeat-until", "", "Stop repeating on this date (YYYY-MM-DD)")
	flags.IntVar(&f.repeatTimes, "repeat-times", 0, "Stop repeating after this many occurrences")
	flags.IntVar(&f.countdown, "countdown", 0, "Count down this many units to the event")
	flags.StringVar(&f.countdownFor, "countdown-unit", "days", "Countdown unit: days, weeks or months")
	flags.StringArrayVar(&f.reminders, "remind", nil, "Notice before the event, e.g. 10m, 1h, 1d (repeatable)")
}

// resolveCalendar is which calendar a new event is filed on. Without --calendar it is the
// first one HEY lists that the identity can file on, the way the TUI's form opens on one.
func (f *eventFields) resolveCalendar(ctx context.Context) (int64, error) {
	if f.calendar != 0 {
		return f.calendar, nil
	}

	payload, err := sdk.Calendars().List(ctx)
	if err != nil {
		return 0, apierr.FromSDK(err)
	}
	for _, calendar := range unwrapCalendars(payload) {
		// The personal calendar is in the list and answers 404 when filed on, and a
		// calendar somebody else owns or a subscription cannot take an event either.
		if calendar.Owned && !calendar.Personal && !calendar.External {
			return calendar.Id, nil
		}
	}
	return 0, apierr.ErrUsageHint("no calendar to file the event on",
		"hey calendar list  lists them; pass one with --calendar")
}

// eventSchedule is when an event happens, in the shape HEY writes it: dates, clock times and
// the zone those times are written in.
type eventSchedule struct {
	startsAt  string
	endsAt    string
	allDay    bool
	startTime string
	endTime   string
	zone      string
	endZone   string
}

// newSchedule is when a new event happens. Saying nothing about the time of day makes it an
// all-day event, which is what a bare `hey event add "Sarah's birthday"` means.
func (f *eventFields) newSchedule() (eventSchedule, error) {
	startsOn := f.startsOn
	if startsOn == "" {
		startsOn = time.Now().Format(dateLayout)
	}
	if _, err := parseDateArg("starts-on date", startsOn); err != nil {
		return eventSchedule{}, err
	}

	endsOn := f.endsOn
	if endsOn == "" {
		endsOn = startsOn
	}
	if err := checkEventDates(startsOn, endsOn); err != nil {
		return eventSchedule{}, err
	}

	if f.allDay || f.startTime == "" {
		return eventSchedule{startsAt: startsOn, endsAt: endsOn, allDay: true}, nil
	}

	startTime, endTime, err := f.clockTimes(f.startTime, f.endTime)
	if err != nil {
		return eventSchedule{}, err
	}
	zone := f.zoneOrLocal()
	return eventSchedule{
		startsAt: startsOn, endsAt: endsOn,
		startTime: startTime, endTime: endTime,
		zone: zone, endZone: zone,
	}, nil
}

// scheduleFrom is when an edited event happens: whatever the flags name, and the event's own
// answer for everything they do not.
func (f *eventFields) scheduleFrom(cmd *cobra.Command, event generated.Recording) (eventSchedule, error) {
	startsOn, startTime := eventClock(event.StartsAt, event.StartsAtTimeZone)
	endsOn, endTime := eventClock(event.EndsAt, event.EndsAtTimeZone)

	schedule := eventSchedule{
		startsAt:  stringOr(cmd, "starts-on", f.startsOn, startsOn),
		endsAt:    stringOr(cmd, "ends-on", f.endsOn, endsOn),
		allDay:    event.AllDay,
		startTime: startTime,
		endTime:   endTime,
		zone:      stringOr(cmd, "time-zone", f.timeZone, event.StartsAtTimeZone),
		endZone:   stringOr(cmd, "time-zone", f.timeZone, event.EndsAtTimeZone),
	}
	if cmd.Flags().Changed("all-day") {
		schedule.allDay = f.allDay
	}

	if _, err := parseDateArg("starts-on date", schedule.startsAt); err != nil {
		return eventSchedule{}, err
	}
	if _, err := parseDateArg("ends-on date", schedule.endsAt); err != nil {
		return eventSchedule{}, err
	}
	if err := checkEventDates(schedule.startsAt, schedule.endsAt); err != nil {
		return eventSchedule{}, err
	}

	// A time given to an all-day event is what turns it into a timed one — asking for 14:00
	// and being answered with a day would read as the flag being ignored.
	if cmd.Flags().Changed("start-time") || cmd.Flags().Changed("end-time") {
		schedule.allDay = false
	}
	if schedule.allDay {
		return eventSchedule{startsAt: schedule.startsAt, endsAt: schedule.endsAt, allDay: true}, nil
	}

	start := stringOr(cmd, "start-time", f.startTime, schedule.startTime)
	end := stringOr(cmd, "end-time", f.endTime, schedule.endTime)
	if start == "" {
		start = defaultEventStartTime
	}
	// An all-day event given only a start has no end to keep, so it takes the default hour.
	if event.AllDay && cmd.Flags().Changed("start-time") && !cmd.Flags().Changed("end-time") {
		end = ""
	}
	var err error
	if schedule.startTime, schedule.endTime, err = f.clockTimes(start, end); err != nil {
		return eventSchedule{}, err
	}
	return schedule, nil
}

// defaultEventStartTime is when an all-day event starts once it is given a time but not one of
// its own. It is HEY's own default for a new event.
const defaultEventStartTime = "09:00"

// eventDuration is how long a timed event runs when only its start was named.
const eventDuration = time.Hour

// clockTimes reads the pair of HH:MM times, defaulting the end to an hour after the start.
func (f *eventFields) clockTimes(startTime, endTime string) (string, string, error) {
	start, err := time.Parse(clockLayout, startTime)
	if err != nil {
		return "", "", apierr.ErrUsageHint(fmt.Sprintf("invalid start-time: %s", startTime),
			"times are HH:MM on a 24-hour clock, for example 14:30")
	}
	if endTime == "" {
		return startTime, start.Add(eventDuration).Format(clockLayout), nil
	}
	if _, err := time.Parse(clockLayout, endTime); err != nil {
		return "", "", apierr.ErrUsageHint(fmt.Sprintf("invalid end-time: %s", endTime),
			"times are HH:MM on a 24-hour clock, for example 15:30")
	}
	return startTime, endTime, nil
}

// clockLayout is the time of day HEY's form takes, and the one a reader types.
const clockLayout = "15:04"

// zoneOrLocal is the zone the clock times are written in. Naming none would have HEY read
// them as UTC, so 14:00 typed in Lisbon would be stored as 14:00 in Tokyo's morning.
func (f *eventFields) zoneOrLocal() string {
	if f.timeZone != "" {
		return f.timeZone
	}
	// A machine with no TZ set has a location called "Local", which names no zone HEY could
	// look up; UTC is what it already assumes.
	if name := time.Now().Location().String(); name != "Local" && name != "UTC" {
		return name
	}
	return ""
}

// eventClock takes an end of an event apart into the day and the clock time it was written
// in. HEY stores a wall-clock time and the zone it belongs to, and serves the instant in UTC,
// so reading one back for a resend means putting it into its own zone first.
func eventClock(at time.Time, zone string) (string, string) {
	if at.IsZero() {
		return "", ""
	}
	if zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			at = at.In(loc)
		}
	}
	return at.Format(dateLayout), at.Format(clockLayout)
}

func checkEventDates(startsOn, endsOn string) error {
	start, err := parseDateArg("starts-on date", startsOn)
	if err != nil {
		return err
	}
	end, err := parseDateArg("ends-on date", endsOn)
	if err != nil {
		return err
	}
	if end.Before(start) {
		return apierr.ErrUsage(fmt.Sprintf("ends-on %s is before starts-on %s", endsOn, startsOn))
	}
	return nil
}

// parseRepeat reads the recurrence flags into the three fields HEY takes. Nil is no change,
// which on a whole-event update leaves the recurrence as it was.
func (f *eventFields) parseRepeat() (*hey.RepeatParams, error) {
	if f.repeat == "" {
		if f.repeatUntil == "" && f.repeatTimes == 0 {
			return nil, nil
		}
		return nil, apierr.ErrUsageHint("repeat-until and repeat-times need --repeat",
			"hey event add \"Standup\" --repeat every_weekday --repeat-times 20")
	}

	frequencies := map[string]hey.RepeatFrequency{
		"every_day":          hey.RepeatEveryDay,
		"every_weekday":      hey.RepeatEveryWeekday,
		"every_week":         hey.RepeatEveryWeek,
		"every_other_week":   hey.RepeatEveryOtherWeek,
		"every_day_of_month": hey.RepeatEveryDayOfMonth,
		"every_year":         hey.RepeatEveryYear,
	}
	frequency, ok := frequencies[f.repeat]
	if !ok {
		return nil, apierr.ErrUsageHint(fmt.Sprintf("invalid repeat: %s", f.repeat),
			"one of every_day, every_weekday, every_week, every_other_week, every_day_of_month, every_year")
	}

	if f.repeatUntil != "" && f.repeatTimes > 0 {
		return nil, apierr.ErrUsage("--repeat-until and --repeat-times are mutually exclusive")
	}

	repeat := &hey.RepeatParams{Frequency: frequency, Until: hey.RepeatUntilForever}
	if f.repeatUntil != "" {
		if _, err := parseDateArg("repeat-until date", f.repeatUntil); err != nil {
			return nil, err
		}
		repeat.Until = hey.RepeatUntilDate
		repeat.UntilDate = f.repeatUntil
	}
	if f.repeatTimes > 0 {
		repeat.Until = hey.RepeatUntilCount
		repeat.Count = f.repeatTimes
	}
	return repeat, nil
}

// parseCountdown reads the countdown flags. The zero value is no countdown, and on an update
// that removes the one the event had — a countdown is a recording of its own that HEY does
// not serve, so there is nothing to read and send back.
func (f *eventFields) parseCountdown() (hey.CountdownParams, error) {
	if f.countdown == 0 {
		return hey.CountdownParams{}, nil
	}
	if f.countdown < 1 || f.countdown > 30 {
		return hey.CountdownParams{}, apierr.ErrUsage(fmt.Sprintf("countdown %d is outside 1 to 30", f.countdown))
	}

	units := map[string]hey.CountdownUnit{
		"days":   hey.CountdownUnitDays,
		"weeks":  hey.CountdownUnitWeeks,
		"months": hey.CountdownUnitMonths,
	}
	unit, ok := units[f.countdownFor]
	if !ok {
		return hey.CountdownParams{}, apierr.ErrUsageHint(fmt.Sprintf("invalid countdown-unit: %s", f.countdownFor),
			"one of days, weeks or months")
	}
	return hey.CountdownParams{Value: f.countdown, Unit: unit}, nil
}

// parseReminders reads the --remind flags into the durations HEY schedules.
func (f *eventFields) parseReminders() ([]time.Duration, error) {
	durations := make([]time.Duration, 0, len(f.reminders))
	for _, notice := range f.reminders {
		duration, err := parseNotice(notice)
		if err != nil {
			return nil, err
		}
		durations = append(durations, duration)
	}
	return durations, nil
}

// remindersFrom keeps the reminders an edited event already has. HEY unschedules every one it
// is not sent, so an update that said nothing about them would quietly remove them all.
func (f *eventFields) remindersFrom(cmd *cobra.Command, event generated.Recording) ([]time.Duration, error) {
	if cmd.Flags().Changed("remind") {
		return f.parseReminders()
	}
	durations := make([]time.Duration, 0, len(event.Reminders))
	for _, reminder := range event.Reminders {
		durations = append(durations, time.Duration(reminder.Duration)*time.Second)
	}
	return durations, nil
}

// parseNotice reads a notice period. Go's own parser stops at hours, and a day is the notice
// somebody most often wants for an event.
func parseNotice(notice string) (time.Duration, error) {
	invalid := apierr.ErrUsageHint(fmt.Sprintf("invalid remind: %s", notice),
		"a notice period like 10m, 1h or 2d")

	if days, found := strings.CutSuffix(notice, "d"); found {
		count, err := strconv.Atoi(days)
		if err != nil || count <= 0 {
			return 0, invalid
		}
		return time.Duration(count) * 24 * time.Hour, nil
	}

	duration, err := time.ParseDuration(notice)
	if err != nil || duration <= 0 {
		return 0, invalid
	}
	return duration, nil
}

// invitesOrNil is the guest list, and nil unless --invite was given. Submitting a list makes
// the caller the event's organizer and sends invitations, so an edit that says nothing about
// guests must say nothing on the wire either.
func (f *eventFields) invitesOrNil(cmd *cobra.Command) []string {
	if !cmd.Flags().Changed("invite") {
		return nil
	}
	invites := make([]string, 0, len(f.invites))
	for _, invite := range f.invites {
		if trimmed := strings.TrimSpace(invite); trimmed != "" {
			invites = append(invites, trimmed)
		}
	}
	return invites
}

// stringOr is the flag when the reader named it and the event's own value when they did not.
// Every string on an event is replace-or-lose-it on HEY's side, so "unchanged" has to be sent
// as what is already there.
func stringOr(cmd *cobra.Command, flag, given, existing string) string {
	if cmd.Flags().Changed(flag) {
		return given
	}
	return existing
}

// eventTitleFrom reads the title from --title, the positional argument or stdin, the way
// `hey todo add` does.
func eventTitleFrom(flagTitle string, args []string) (string, error) {
	if flagTitle != "" && len(args) > 0 {
		return "", apierr.ErrUsage("--title and positional argument are mutually exclusive")
	}
	if flagTitle != "" {
		return flagTitle, nil
	}
	if len(args) > 0 {
		return args[0], nil
	}
	if !stdinIsTerminal() {
		title, err := readStdin()
		if err != nil {
			return "", err
		}
		if title != "" {
			return title, nil
		}
	}
	return "", apierr.ErrUsageHint("title is required",
		"hey event add \"Design review\"  or  hey event add --title \"Design review\"")
}
