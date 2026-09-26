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
	"github.com/basecamp/hey-cli/internal/terminal"
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
			"agent_notes": "Subcommands: list, day, week, add, edit, delete. \"What's on the schedule today?\" is answered by day, not list: day and week read the span as HEY draws it, with a repeating event expanded into the occurrences inside it, over the calendars switched on in HEY. list reads what calendars hold — every calendar unless --calendar names one — and a repeating event is one row, its series, on the day the series began. An edit is not a patch on HEY's side: it resends the notes, location, link, attached email, reminders and time zones the event already carries, so notes lose their formatting and a countdown is removed unless --countdown names one again. edit <series id> changes a whole series; one day of it is edit <series id> --occurrence <occurrence_id from day/week> --apply-to current|future, which keeps the countdown and refuses to flatten notes unless --allow-plain-notes or --notes is given. --apply-to future starts a new series and requires --repeat with its complete schedule; a preset alone means forever and custom copies an existing opaque schedule (a count-based rule can restart its full count). A virtual day of an opaque custom schedule is read from HEY's Day view and refused if that view no longer serves it. A realized custom day cannot be split safely; use a virtual occurrence, edit that day alone, or edit the whole series. A realized preset occurrence moved away from its series time must be moved back with --apply-to current before a future split. Read the day again afterward for the new series id. delete <series id> deletes the whole series; one day of it is delete <series id> --occurrence <occurrence_id> --apply-to current|future, whether or not HEY has written that day out. delete refuses a written-out day's own id, because HEY would draw the day again from the series. add and edit read clock times in the HEY account's time zone unless --time-zone names one, and refuse a timed write when the account has none; an edit keeps a zoned event's zone and leaves a zoneless event zoneless.",
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

	return writeEventRows(cmd, eventRows(events), window.describe(), notice)
}

// eventBoundary writes one end of an event: a day for an all-day event, a day and a clock
// time for a timed one. An all-day event has no time of day, and printing it midnight reads
// as an event that starts at midnight. HEY's JSON is always UTC, so a timed boundary
// converts to the reader's clock the way HEY's own views draw it, while an all-day date is
// the day it names and does not shift.
func eventBoundary(at time.Time, allDay bool) string {
	if allDay {
		return formatDate(at)
	}
	return formatTimestamp(at.Local())
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
for an hour. Clock times are read in your HEY account's time zone, the one HEY's web app
uses, and so is today when --starts-on is left out; --time-zone names another. If the
account has no time zone the command refuses and asks for --time-zone.

Without --calendar the event goes where HEY puts one by default: the first ordinary calendar
you own that is not a subscription — never Maybe or the personal calendar.`,
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

	repeat, err := c.fields.parseRepeat(cmd, false)
	if err != nil {
		return err
	}
	countdown, err := c.fields.parseCountdown()
	if err != nil {
		return err
	}
	reminders, err := c.fields.parseReminders()
	if err != nil {
		return err
	}
	if err = c.fields.validateExplicitScheduleFlags(cmd); err != nil {
		return err
	}
	ctx := cmd.Context()
	schedule, err := c.fields.newSchedule(ctx)
	if err != nil {
		return err
	}
	err = checkRepeatStarts(repeat, schedule.startsAt)
	if err != nil {
		return err
	}

	calendarID, err := c.fields.resolveCalendar(ctx)
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

	// occurrence and applyTo turn the edit into one of a repeating event's days: the
	// occurrence_id a day or a week listing serves, and how much of the series the change
	// reaches. allowPlainNotes accepts what that write cannot keep; see editOccurrence.
	occurrence      string
	applyTo         string
	allowPlainNotes bool
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
countdown is a recording of its own that this edit does not read back, so an edit removes
one unless --countdown names it again.

Clock times you type are read in the event's own time zone. An event saved without one
stays without one: typed times are read in your HEY account's zone and sent as the moment
they name, and the times you do not type keep theirs. --time-zone gives the event that
zone, keeping the moment of every time you do not type. An all-day event given a time
takes the account's zone. If a zone is needed and the account has none, the edit refuses
and asks for --time-zone. All of this holds for an --occurrence edit too.

The event is found by reading the calendars it might be on, which is one request each and
covers the pages HEY answers with. Give the day it starts as [date] to look on that day
alone, or --calendar to look on one calendar.

An id alone changes the whole event, a repeating series included. One day of a series is
changed with --occurrence, which takes the occurrence_id 'hey event day' and 'hey event
week' serve (<series id>_<YYYY-MM-DD>, and the series must be the id given), and
--apply-to, which is required with it: 'current' changes that day alone and 'future'
changes it and every day after it, the two choices HEY's own form offers. The day is read
on its own date, so [date] can be left out or must name it. A change to --repeat,
--repeat-until or --repeat-times cannot apply to one day, so 'current' refuses those
flags. 'future' starts a new series and requires --repeat to state its complete schedule:
combine a preset with --repeat-times or --repeat-until for a finite series, use a preset
alone for one that continues forever, or use --repeat custom to copy an existing opaque
schedule. A custom count-based rule can restart its full count on the replacement. HEY
accepts the replacement's submitted start even when it overlaps an earlier occurrence, so
choose its date and time deliberately. A virtual day of an opaque custom schedule takes its exact time from HEY's Day view and is
refused if that view no longer serves it. A realized day of a custom schedule cannot be split
safely, because HEY does not serve the rule's occurrence boundary; use a virtual occurrence,
edit that day alone, or edit the whole series. A realized preset occurrence moved
away from its series time must be moved back with a current-only edit before it can be split
safely. The days from this one on
get a new series id.

An occurrence edit keeps more than a whole-event edit does, and refuses what it cannot
keep. A countdown owned by the day is read back and sent again; an inherited series
countdown is left inherited on a current-only edit and copied to the replacement series on
a future edit. It survives unless --countdown 0 removes it — and one day of a series with
a countdown cannot lose it alone, since HEY shows a day the series' countdown whenever it
has none of its own, so that is refused. Notes are still only served as plain text, so an
occurrence edit that would send
formatted notes back as text refuses unless --allow-plain-notes accepts that or --notes
replaces them. A 'future' edit records the new series from the series' own guest list,
so a day that had come to have guests of its own is refused until --invite names the new
series' list. The day is read over every calendar, so here --calendar is only where the
day is moved to; a day already moved elsewhere stays there. A virtual occurrence lists
its series in id and parent_id; a day HEY has written out on its own keeps its own event id
in id and recording_id, with the series in parent_id. That own id edits the day alone; it
is deleted with 'hey event delete <series id> --occurrence <occurrence_id> --apply-to
current'. One thing no edit can keep: an attached email you cannot read is not served, so
it is detached by any edit, whole event
or one day.`,
		Example: `  hey event edit 4821 --title "Design review (moved)"
  hey event edit 4821 --starts-on 2026-09-04 --start-time 15:00
  hey event edit 4821 2026-09-02 --location "Studio, 3rd floor"
  hey event edit 4821 --circle=false
  hey event edit 4821 --occurrence 4821_2026-09-15 --apply-to current --start-time 15:00 --json
  hey event edit 4821 --occurrence 4821_2026-09-15 --apply-to future --repeat every_week --repeat-times 8 --location "Studio, 3rd floor" --allow-plain-notes`,
		RunE: eventsEditCommand.run,
		Args: cobra.RangeArgs(1, 2),
	}

	eventsEditCommand.fields.registerFlags(eventsEditCommand.cmd)
	flags := eventsEditCommand.cmd.Flags()
	flags.StringVar(&eventsEditCommand.occurrence, "occurrence", "", "One day of a repeating event, by the occurrence_id 'hey event day' serves (<series id>_<YYYY-MM-DD>)")
	flags.StringVar(&eventsEditCommand.applyTo, "apply-to", "", "How much of the series an --occurrence edit reaches: current (that day alone) or future (that day and every one after it)")
	flags.BoolVar(&eventsEditCommand.allowPlainNotes, "allow-plain-notes", false, "Let an --occurrence edit send notes it is not changing back as plain text, losing their formatting")

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

	occurrence, err := c.parseOccurrence(cmd, id, on)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if occurrence != nil {
		return c.editOccurrence(ctx, cmd, *occurrence)
	}

	repeat, err := c.fields.parseRepeat(cmd, false)
	if err != nil {
		return err
	}
	countdown, err := c.fields.parseCountdown()
	if err != nil {
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

	event, err := c.findEvent(ctx, id, on)
	if err != nil {
		return err
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
	window, err := c.searchWindow(ctx, on)
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

// searchWindow is where an edit looks for its event: the day given, read as [day, day+1),
// or a window wide enough to cover an event somebody is editing, over the calendar
// --calendar names or every one of them.
func (c *eventsEditCommand) searchWindow(ctx context.Context, on string) (recordingWindow, error) {
	return eventSearchWindow(ctx, c.fields.calendar, on)
}

// eventSearchWindow is where an event given by id is looked for: the day given, read as
// [day, day+1), or a year either side of today, over one calendar or every one of them.
func eventSearchWindow(ctx context.Context, calendar int64, on string) (recordingWindow, error) {
	endsOn := on
	if day, err := time.Parse(dateLayout, on); err == nil {
		endsOn = day.AddDate(0, 0, 1).Format(dateLayout)
	}
	filter := recordingFilter{
		calendar:         calendar,
		startsOn:         on,
		endsOn:           endsOn,
		defaultWindow:    func(now time.Time) (time.Time, time.Time) { return now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0) },
		defaultCalendars: allCalendarIDs,
	}
	return filter.resolve(ctx)
}

// delete

type eventsDeleteCommand struct {
	cmd *cobra.Command
	// occurrence and applyTo name one day of a repeating event and how much of the series
	// goes with it, as they do for an edit.
	occurrence string
	applyTo    string
}

func newEventsDeleteCommand() *eventsDeleteCommand {
	eventsDeleteCommand := &eventsDeleteCommand{}
	eventsDeleteCommand.cmd = &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an event",
		Long: `Delete an event. An id alone deletes the whole event, a repeating series included, and
an event on a shared calendar is deleted for everybody on it.

One day of a series is deleted with --occurrence, which takes the occurrence_id 'hey event
day' and 'hey event week' serve (<series id>_<YYYY-MM-DD>, and the series must be the id
given), and --apply-to, which is required with it: 'current' deletes that day alone and
HEY keeps the rest of the series by writing the day into its exceptions; 'future' deletes
it and every day after it, stopping the series the day before — from the series' first
day, that is the whole series. A day HEY has written out on its own and a day it has not
are deleted the same way.

A day HEY has written out on its own — after an edit of that day alone, or a reminder —
has an id of its own, but that id is refused here: deleting it would remove only HEY's
copy of the day, and HEY would draw the day again from the series. The refusal names the
command that deletes the day. To tell, the event is read first, over every calendar and a
year either side of today; an id that read does not find is deleted as given.

A 'future' delete from a written-out day is refused where its boundary cannot be trusted,
as a 'future' edit is: a day moved off its series time, or any written-out day of an
opaque custom schedule. Delete that day alone first, then from the next day.`,
		Annotations: map[string]string{
			"agent_notes": "delete <id> deletes the whole event, recurring series included, for everybody on a shared calendar. One day of a series is delete <series id> --occurrence <occurrence_id from day/week> --apply-to current|future: current deletes that day alone; future deletes it and every day after, and from the series' first day that is the whole series. A day HEY wrote out on its own (own id in id/recording_id, series in parent_id) is refused by its own id, because deleting that id lets HEY draw the day again from the series; use the occurrence form. future is refused from a written-out day moved off its series time or on an opaque custom schedule.",
		},
		Example: `  hey event delete 4821
  hey event delete 4821 --occurrence 4821_2026-09-15 --apply-to current
  hey event delete 4821 --occurrence 4821_2026-09-15 --apply-to future`,
		RunE: eventsDeleteCommand.run,
		Args: usageExactOneArg(),
	}

	flags := eventsDeleteCommand.cmd.Flags()
	flags.StringVar(&eventsDeleteCommand.occurrence, "occurrence", "", "One day of a repeating event, by the occurrence_id 'hey event day' serves (<series id>_<YYYY-MM-DD>)")
	flags.StringVar(&eventsDeleteCommand.applyTo, "apply-to", "", "How much of the series an --occurrence delete reaches: current (that day alone) or future (that day and every one after it)")

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

	occurrence, err := parseOccurrenceFlags(cmd, id, c.occurrence, c.applyTo)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if occurrence != nil {
		return c.deleteOccurrence(ctx, cmd, *occurrence)
	}

	if err = refuseADayOfASeries(ctx, cmd, id); err != nil {
		return err
	}
	if err = sdk.CalendarEvents().Delete(ctx, id); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, "Event deleted", nil)
}

// deleteOccurrence deletes one day of a repeating event, or that day and every one after it,
// through the occurrence route: that is the one that writes a day into the series'
// exceptions, so the day stays gone. A future delete reads the day first, for the boundary
// check a future edit makes; a current one has nothing to check that HEY does not.
func (c *eventsDeleteCommand) deleteOccurrence(ctx context.Context, cmd *cobra.Command, write occurrenceWrite) error {
	if write.scope == hey.OccurrenceScopeThisAndFollowing {
		window, err := occurrenceDayWindow(ctx, write.occurrence.Date)
		if err != nil {
			return err
		}
		rows, err := window.read(ctx, recordingTypeEvent)
		if err != nil {
			return err
		}
		day, err := locateOccurrence(cmd, rows, write.occurrence, write.scope)
		if err != nil {
			return err
		}
		if err = day.refuseAnUnsafeFutureBoundary("deleted",
			"delete that day alone with --apply-to current, then delete from a later virtual occurrence with --apply-to future",
			"delete that day alone with --apply-to current, then delete from the next day with --apply-to future"); err != nil {
			return err
		}
	}

	if err := sdk.CalendarEvents().DeleteOccurrence(ctx, write.occurrence, write.scope); err != nil {
		return occurrenceWriteError(err, write.occurrence)
	}

	summary := "Occurrence deleted"
	if write.scope == hey.OccurrenceScopeThisAndFollowing {
		summary = "Occurrence and the following deleted"
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("%s: %s.", summary, write.occurrence),
		summary,
		map[string]string{"occurrence_id": write.occurrence.String(), "apply_to": applyToFlag(write.scope)})
}

// refuseADayOfASeries stops a delete by id from reaching a day HEY has written out on its
// own. HEY deletes that recording and nothing else — the day is not written into the
// series' exceptions — so the series draws the day again, in the series' own version, and
// the delete would be reported done having undone an edit instead. There is no read of one
// event, so it is looked for where an edit looks, and an id that is not found there is
// deleted as it always was: whatever it is, it is not a day of a series within a year.
func refuseADayOfASeries(ctx context.Context, cmd *cobra.Command, id int64) error {
	window, err := eventSearchWindow(ctx, 0, "")
	if err != nil {
		return err
	}
	events, err := window.read(ctx, recordingTypeEvent)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Id != id || event.OccurrenceId == "" {
			continue
		}
		occurrence, err := hey.ParseOccurrenceID(event.OccurrenceId)
		if err != nil {
			return apierr.ErrUsageHint(
				fmt.Sprintf("event %d is one day of a series, and deleting it by its own id would bring the series' version of that day back", id),
				fmt.Sprintf("hey event day  lists the day's occurrence_id for %s --occurrence", cmd.CommandPath()))
		}
		return apierr.ErrUsageHint(
			fmt.Sprintf("event %d is one day of series %d, and deleting it by its own id would bring the series' version of that day back", id, occurrence.EventID),
			fmt.Sprintf("%s %d --occurrence %s --apply-to current", cmd.CommandPath(), occurrence.EventID, occurrence))
	}
	return nil
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

	account accountZone
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
	flags.StringVar(&f.timeZone, "time-zone", "", "IANA zone the times are written in, such as America/New_York (defaults to your HEY account's)")
	flags.StringVar(&f.notes, "notes", "", "Event notes")
	flags.StringVar(&f.location, "location", "", "Event location")
	flags.StringVar(&f.link, "link", "", "Meeting or reference URL")
	flags.StringArrayVar(&f.invites, "invite", nil, "Email address to invite (repeatable, replaces the guest list)")
	flags.BoolVar(&f.circle, "circle", false, "Circle the event")
	flags.StringVar(&f.repeat, "repeat", "", "Repeat: every_day, every_weekday, every_week, every_other_week, every_day_of_month, every_year; custom only copies an opaque schedule in a future occurrence edit")
	flags.StringVar(&f.repeatUntil, "repeat-until", "", "Stop repeating on this date (YYYY-MM-DD)")
	flags.IntVar(&f.repeatTimes, "repeat-times", 0, "Stop repeating after this many occurrences")
	flags.IntVar(&f.countdown, "countdown", 0, "Count down this many units to the event")
	flags.StringVar(&f.countdownFor, "countdown-unit", "days", "Countdown unit: days, weeks or months")
	flags.StringArrayVar(&f.reminders, "remind", nil, "Notice before the event, e.g. 10m, 1h, 1d (repeatable)")
}

// calendarKindNormal is the kind of an ordinary calendar. The other kind HEY has is "maybe",
// the tentative calendar every account is given right after the personal one.
const calendarKindNormal = "normal"

// resolveCalendar is which calendar a new event is filed on. Without --calendar it is the one
// HEY itself files on when no calendar is named — `owned_calendars.internal.normal.first!` —
// which the TUI's form opens on as well.
func (f *eventFields) resolveCalendar(ctx context.Context) (int64, error) {
	if f.calendar != 0 {
		return f.calendar, nil
	}

	payload, err := sdk.Calendars().List(ctx)
	if err != nil {
		return 0, apierr.FromSDK(err)
	}
	for _, calendar := range unwrapCalendars(payload) {
		if isDefaultEventCalendar(calendar) {
			return calendar.Id, nil
		}
	}
	return 0, apierr.ErrUsageHint("no calendar to file the event on",
		"hey calendar list  lists them; pass one with --calendar")
}

// isDefaultEventCalendar is whether HEY would file a new event here when none is named. HEY
// lists calendars by when they were made, and every account has Maybe before the calendar
// it files on, so the first owned calendar is the wrong one: it has to be a normal one too.
// The personal calendar is in the list and answers 404 when filed on, and a subscription —
// external, the other side of HEY's `internal` — cannot take an event either.
func isDefaultEventCalendar(calendar generated.Calendar) bool {
	return calendar.Owned && !calendar.Personal && !calendar.External && calendar.Kind == calendarKindNormal
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
//
// A timed event's clock times are read in writeZone's zone, and so is today when no date is
// named: at 02:00 UTC it is still the evening before in New York. An all-day event has no
// zone and asks for none, so its today is this machine's.
func (f *eventFields) newSchedule(ctx context.Context) (eventSchedule, error) {
	allDay := f.allDay || f.startTime == ""
	var startTime, endTime, zone string
	today := eventNow().Local()
	if !allDay {
		var err error
		if startTime, endTime, err = f.clockTimes(f.startTime, f.endTime); err != nil {
			return eventSchedule{}, err
		}
		var loc *time.Location
		if zone, loc, err = f.writeZone(ctx); err != nil {
			return eventSchedule{}, err
		}
		today = today.In(loc)
	}

	startsOn := f.startsOn
	if startsOn == "" {
		startsOn = today.Format(dateLayout)
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

	if allDay {
		return eventSchedule{startsAt: startsOn, endsAt: endsOn, allDay: true}, nil
	}
	return eventSchedule{
		startsAt: startsOn, endsAt: endsOn,
		startTime: startTime, endTime: endTime,
		zone: zone, endZone: zone,
	}, nil
}

// validateExplicitScheduleFlags checks every schedule value that needs no stored event.
// Relationships involving an omitted half are checked later, after scheduleFrom fills it in.
func (f *eventFields) validateExplicitScheduleFlags(cmd *cobra.Command) error {
	flags := cmd.Flags()
	if flags.Changed("starts-on") {
		if _, err := parseDateArg("starts-on date", f.startsOn); err != nil {
			return err
		}
	}
	if flags.Changed("ends-on") {
		if _, err := parseDateArg("ends-on date", f.endsOn); err != nil {
			return err
		}
	}
	if flags.Changed("starts-on") && flags.Changed("ends-on") {
		if err := checkEventDates(f.startsOn, f.endsOn); err != nil {
			return err
		}
	}
	if flags.Changed("start-time") {
		if _, err := parseEventClock("start-time", f.startTime, "14:30"); err != nil {
			return err
		}
	}
	if flags.Changed("end-time") {
		if _, err := parseEventClock("end-time", f.endTime, "15:30"); err != nil {
			return err
		}
	}
	if flags.Changed("time-zone") {
		if f.timeZone == "" {
			return apierr.ErrUsageHint("--time-zone needs a time zone", "an IANA time zone name, for example America/New_York")
		}
		if _, err := loadEventZone(f.timeZone); err != nil {
			return errInvalidTimeZone(f.timeZone)
		}
	}
	return nil
}

// scheduleFrom is when an edited event happens: whatever the flags name, and the event's own
// answer for everything they do not, each end read and written in the zone editZones gives it.
func (f *eventFields) scheduleFrom(ctx context.Context, cmd *cobra.Command, event generated.Recording) (eventSchedule, error) {
	flags := cmd.Flags()
	allDay := event.AllDay
	if flags.Changed("all-day") {
		allDay = f.allDay
	}
	// A time given to an all-day event is what turns it into a timed one — asking for 14:00
	// and being answered with a day would read as the flag being ignored.
	if flags.Changed("start-time") || flags.Changed("end-time") {
		allDay = false
	}

	startZone, endZone, err := f.editZones(ctx, cmd, event, allDay)
	if err != nil {
		return eventSchedule{}, err
	}
	startsOn, startTime := eventClock(event.StartsAt, startZone.readIn(event))
	endsOn, endTime := eventClock(event.EndsAt, endZone.readIn(event))

	schedule := eventSchedule{
		startsAt:  stringOr(cmd, "starts-on", f.startsOn, startsOn),
		endsAt:    stringOr(cmd, "ends-on", f.endsOn, endsOn),
		allDay:    allDay,
		startTime: startTime,
		endTime:   endTime,
		zone:      startZone.name,
		endZone:   endZone.name,
	}

	if _, err = parseDateArg("starts-on date", schedule.startsAt); err != nil {
		return eventSchedule{}, err
	}
	if _, err = parseDateArg("ends-on date", schedule.endsAt); err != nil {
		return eventSchedule{}, err
	}
	if err = checkEventDates(schedule.startsAt, schedule.endsAt); err != nil {
		return eventSchedule{}, err
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
	if event.AllDay && flags.Changed("start-time") && !flags.Changed("end-time") {
		end = ""
	}
	if schedule.startTime, schedule.endTime, err = f.clockTimes(start, end); err != nil {
		return eventSchedule{}, err
	}

	if startZone.name == "" {
		retyped := flags.Changed("starts-on") || flags.Changed("start-time")
		schedule.startsAt, schedule.startTime = zonelessEnd(event.StartsAt, schedule.startsAt, schedule.startTime, startZone.loc, retyped)
	}
	if endZone.name == "" {
		retyped := flags.Changed("ends-on") || flags.Changed("end-time")
		schedule.endsAt, schedule.endTime = zonelessEnd(event.EndsAt, schedule.endsAt, schedule.endTime, endZone.loc, retyped)
	}
	return schedule, nil
}

// clockZone is the zone one end of an edited event is read and written in. An end with no
// name is zoneless: its clock times are read in loc and go back to HEY as UTC, zone and all.
type clockZone struct {
	name string
	loc  *time.Location
}

// readIn is where the event's own end is read from. An all-day event's end is the date it
// names, whatever zone it is about to be written in.
func (z clockZone) readIn(event generated.Recording) *time.Location {
	if event.AllDay {
		return time.UTC
	}
	return z.loc
}

// editZones is the zone each end of an edited event is read and written in.
//
// --time-zone names it outright, and the times nobody retyped are read in it, so they keep
// their instant. Otherwise a zoned event keeps its own zones, and one this build cannot load
// is refused rather than read as UTC. A zoneless event stays zoneless — HEY expands it, a
// repeating series across a change of daylight saving included, as it was saved — so only
// the times and dates somebody typed are read in the account's zone. An all-day event given
// a time takes the account's zone, as a new event does. The account is read only when a
// typed time or date needs it.
func (f *eventFields) editZones(ctx context.Context, cmd *cobra.Command, event generated.Recording, allDay bool) (clockZone, clockZone, error) {
	if f.timeZone != "" {
		name, loc, err := f.writeZone(ctx)
		return clockZone{name, loc}, clockZone{name, loc}, err
	}

	if !event.AllDay && event.StartsAtTimeZone != "" {
		start, err := storedZone(event.StartsAtTimeZone)
		if err != nil {
			return clockZone{}, clockZone{}, err
		}
		if event.EndsAtTimeZone == "" {
			return start, start, nil
		}
		end, err := storedZone(event.EndsAtTimeZone)
		return start, end, err
	}

	flags := cmd.Flags()
	retyped := flags.Changed("starts-on") || flags.Changed("ends-on") || flags.Changed("start-time") || flags.Changed("end-time")
	if allDay == event.AllDay && (allDay || !retyped) {
		return clockZone{loc: time.UTC}, clockZone{loc: time.UTC}, nil
	}
	name, loc, err := f.writeZone(ctx)
	if err != nil {
		return clockZone{}, clockZone{}, err
	}
	if !event.AllDay {
		name = ""
	}
	return clockZone{name, loc}, clockZone{name, loc}, nil
}

// storedZone loads a zone an event was saved in.
func storedZone(name string) (clockZone, error) {
	loc, err := loadEventZone(name)
	if err != nil {
		return clockZone{}, &apierr.Error{
			Code:    apierr.CodeUsage,
			Message: fmt.Sprintf("the event is in time zone %s, which this build of hey does not know, so its times cannot be read", terminal.SanitizeLine(name)),
			Hint:    "pass --time-zone to write the event in a zone this build knows",
			Cause:   err,
		}
	}
	return clockZone{name, loc}, nil
}

// zonelessEnd is one end of a zoneless event as HEY takes it back. An end nobody retyped keeps
// the instant it had; a retyped one is read in loc and sent as the UTC time it names.
func zonelessEnd(had time.Time, date, clock string, loc *time.Location, retyped bool) (string, string) {
	if !retyped {
		return eventClock(had, time.UTC)
	}
	// Both halves were checked before they got here.
	wall, _ := time.ParseInLocation(dateLayout+" "+clockLayout, date+" "+clock, loc)
	return eventClock(wall, time.UTC)
}

// defaultEventStartTime is when an all-day event starts once it is given a time but not one of
// its own. It is HEY's own default for a new event.
const defaultEventStartTime = "09:00"

// eventDuration is how long a timed event runs when only its start was named.
const eventDuration = time.Hour

// clockTimes reads the pair of HH:MM times, defaulting the end to an hour after the start.
func (f *eventFields) clockTimes(startTime, endTime string) (string, string, error) {
	start, err := parseEventClock("start-time", startTime, "14:30")
	if err != nil {
		return "", "", err
	}
	if endTime == "" {
		return startTime, start.Add(eventDuration).Format(clockLayout), nil
	}
	if _, err := parseEventClock("end-time", endTime, "15:30"); err != nil {
		return "", "", err
	}
	return startTime, endTime, nil
}

func parseEventClock(name, value, example string) (time.Time, error) {
	clock, err := time.Parse(clockLayout, value)
	if err != nil {
		return time.Time{}, apierr.ErrUsageHint(fmt.Sprintf("invalid %s: %s", name, value),
			fmt.Sprintf("times are HH:MM on a 24-hour clock, for example %s", example))
	}
	return clock, nil
}

// clockLayout is the time of day HEY's form takes, and the one a reader types.
const clockLayout = "15:04"

// eventClock takes an end of an event apart into the day and the clock time it reads as in
// loc. HEY serves every instant in UTC, so an end is put into the zone it is written in first.
func eventClock(at time.Time, loc *time.Location) (string, string) {
	if at.IsZero() {
		return "", ""
	}
	at = at.In(loc)
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
func (f *eventFields) parseRepeat(cmd *cobra.Command, allowCustom bool) (*hey.RepeatParams, error) {
	flags := cmd.Flags()
	repeatGiven := flags.Changed("repeat")
	untilGiven := flags.Changed("repeat-until")
	timesGiven := flags.Changed("repeat-times")
	frequencyHint := "one of every_day, every_weekday, every_week, every_other_week, every_day_of_month, every_year"
	if allowCustom {
		frequencyHint += ", or custom to copy an existing opaque schedule"
	}
	if repeatGiven && f.repeat == "" {
		return nil, apierr.ErrUsageHint("--repeat needs a frequency", frequencyHint)
	}
	if untilGiven && f.repeatUntil == "" {
		return nil, apierr.ErrUsageHint("--repeat-until needs a date", "a date in YYYY-MM-DD form")
	}
	if f.repeat == "" {
		if !untilGiven && !timesGiven {
			return nil, nil
		}
		return nil, apierr.ErrUsageHint("repeat-until and repeat-times need --repeat",
			"hey event add \"Standup\" --repeat every_weekday --repeat-times 20")
	}
	// A count of nothing is not "forever", which is what the zero value would have meant.
	if timesGiven && f.repeatTimes < 1 {
		return nil, apierr.ErrUsageHint(fmt.Sprintf("repeat-times %d is not a number of occurrences", f.repeatTimes),
			"a count of at least 1, or --repeat-until for a last day")
	}

	frequencies := map[string]hey.RepeatFrequency{
		"every_day":          hey.RepeatEveryDay,
		"every_weekday":      hey.RepeatEveryWeekday,
		"every_week":         hey.RepeatEveryWeek,
		"every_other_week":   hey.RepeatEveryOtherWeek,
		"every_day_of_month": hey.RepeatEveryDayOfMonth,
		"every_year":         hey.RepeatEveryYear,
	}
	if allowCustom {
		frequencies["custom"] = hey.RepeatCustom
	}
	frequency, ok := frequencies[f.repeat]
	if !ok {
		return nil, apierr.ErrUsageHint(fmt.Sprintf("invalid repeat: %s", f.repeat), frequencyHint)
	}

	if f.repeatUntil != "" && f.repeatTimes > 0 {
		return nil, apierr.ErrUsage("--repeat-until and --repeat-times are mutually exclusive")
	}
	if frequency == hey.RepeatCustom {
		if untilGiven || timesGiven {
			return nil, apierr.ErrUsage("--repeat custom copies the existing schedule, so it cannot be combined with --repeat-until or --repeat-times")
		}
		return &hey.RepeatParams{Frequency: hey.RepeatCustom}, nil
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

// checkRepeatStarts keeps a recurrence's last day at or after its first. HEY turns a
// schedule that has no occurrence after its start into a one-off event, so accepting an
// earlier --repeat-until would silently stop a series while reporting a recurring write.
func checkRepeatStarts(repeat *hey.RepeatParams, startsAt string) error {
	if repeat == nil || repeat.Until != hey.RepeatUntilDate {
		return nil
	}
	start, err := parseDateArg("starts-on date", startsAt)
	if err != nil {
		return err
	}
	until, err := parseDateArg("repeat-until date", repeat.UntilDate)
	if err != nil {
		return err
	}
	if until.Before(start) {
		return apierr.ErrUsage(fmt.Sprintf("repeat-until %s is before starts-on %s", repeat.UntilDate, startsAt))
	}
	return nil
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
