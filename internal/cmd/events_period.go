package cmd

import (
	"context"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

// eventsPeriodCommand lists events the way HEY draws a day or a week rather than the way a
// calendar stores them. A repeating event is stored once, so `hey event list` answers a
// standing Monday standup on the day the series began; a period expands it into the
// occurrences that fall inside the window, which is what "what's on my schedule today?"
// is asking. `hey habit list` already reads its week this way.
//
// A period is scoped by HEY to the calendars the identity has switched on in the app — the
// same set the app draws — which is why there is no --calendar here: it would read as a
// filter and change nothing.
type eventsPeriodCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool

	// read fetches the period a date falls in: a day or a week.
	read func(ctx context.Context, date string) (*generated.CalendarPeriod, error)

	// describe names the span read, in words that follow "No events" and sit inside the
	// summary's parentheses: "on 2026-09-02", "in the week of 2026-09-02".
	describe func(date string) string
}

func newEventsDayCommand() *eventsPeriodCommand {
	eventsDayCommand := &eventsPeriodCommand{
		read: func(ctx context.Context, date string) (*generated.CalendarPeriod, error) {
			return sdk.CalendarPeriods().Day(ctx, date)
		},
		describe: func(date string) string {
			if date == periodNow {
				return "today"
			}
			return "on " + date
		},
	}
	eventsDayCommand.cmd = &cobra.Command{
		Use:   "day [date]",
		Short: "List the events of one day, as HEY draws it",
		Long: `List the events of one day, as HEY's Day View draws it.

A calendar stores a repeating event once, so 'hey event list' answers a standing Monday
standup on the day the series began and on no other. A day is HEY's own expansion: every
event that falls on it, occurrences of a repeating series included, and nothing from
outside it.

The day covers the calendars switched on in HEY, the same set the app draws, so there is
no --calendar to narrow it. Every occurrence carries the series in id and parent_id, which
'hey event edit' and 'hey event delete' take for the whole series. A day HEY has written
out on its own — after an edit of that day alone, or a reminder — also carries its own
recording_id, which those two commands act on for that day alone. Either way occurrence_id
is what 'hey event edit --occurrence' takes, with the series id before it.`,
		Example: `  hey event day
  hey event day 2026-09-02
  hey event day --json`,
		RunE: eventsDayCommand.run,
		Args: cobra.MaximumNArgs(1),
	}

	eventsDayCommand.registerFlags()
	return eventsDayCommand
}

func newEventsWeekCommand() *eventsPeriodCommand {
	eventsWeekCommand := &eventsPeriodCommand{
		read: func(ctx context.Context, date string) (*generated.CalendarPeriod, error) {
			return sdk.CalendarPeriods().Week(ctx, date)
		},
		describe: func(date string) string {
			if date == periodNow {
				return "this week"
			}
			return "in the week of " + date
		},
	}
	eventsWeekCommand.cmd = &cobra.Command{
		Use:   "week [date]",
		Short: "List the events of one week, as HEY draws it",
		Long: `List the events of the week a date falls in, as HEY's Week View draws it: every event
inside the week, occurrences of a repeating series included. Any day names its week.

The week covers the calendars switched on in HEY, the same set the app draws, so there is
no --calendar to narrow it. Every occurrence carries the series in id and parent_id, which
'hey event edit' and 'hey event delete' take for the whole series. A day HEY has written
out on its own — after an edit of that day alone, or a reminder — also carries its own
recording_id, which those two commands act on for that day alone. Either way occurrence_id
is what 'hey event edit --occurrence' takes, with the series id before it.`,
		Example: `  hey event week
  hey event week 2026-09-02
  hey event week --json`,
		RunE: eventsWeekCommand.run,
		Args: cobra.MaximumNArgs(1),
	}

	eventsWeekCommand.registerFlags()
	return eventsWeekCommand
}

func (c *eventsPeriodCommand) registerFlags() {
	c.cmd.Flags().IntVar(&c.limit, "limit", 0, "Maximum number of events to show")
	c.cmd.Flags().BoolVar(&c.all, "all", false, "Fetch all results (override --limit)")
}

func (c *eventsPeriodCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	// With no date the read asks for "now" and HEY resolves today in the account's own
	// time zone, so a host in another zone does not fetch yesterday's schedule at midnight.
	date := periodNow
	if len(args) > 0 {
		if _, err := parseDateArg("date", args[0]); err != nil {
			return err
		}
		date = args[0]
	}

	ctx := cmd.Context()
	period, err := c.read(ctx, date)
	if err != nil {
		return apierr.FromSDK(err)
	}

	events := []generated.Recording{}
	if period != nil {
		events = filterRecordingsByType(&period.Recordings, recordingTypeEvent)
	}
	sortEventsByStart(events)
	rows := occurrenceEventRows(events)

	total := len(rows)
	if c.limit > 0 && !c.all && len(rows) > c.limit {
		rows = rows[:c.limit]
	}
	notice := output.TruncationNotice(len(rows), total)

	return writeEventRows(cmd, rows, c.describe(date), notice)
}

// periodNow is the date the period reads accept for today: HEY resolves it in the
// account's own time zone, which the CLI process's clock cannot.
const periodNow = "now"

// eventRow is the event shape the CLI publishes. An occurrence's id names its series,
// while RecordingID names a realized day's own event for an edit or delete of that day.
type eventRow struct {
	generated.Recording
	RecordingID int64 `json:"recording_id,omitempty"`
}

func eventRows(events []generated.Recording) []eventRow {
	rows := make([]eventRow, len(events))
	for i, event := range events {
		rows[i] = eventRow{Recording: event}
	}
	return rows
}

// occurrenceEventRows gives every occurrence the series id used for whole-series actions.
// A virtual occurrence has no id of its own. A realized occurrence publishes that own id
// separately as recording_id for actions on that day alone.
func occurrenceEventRows(events []generated.Recording) []eventRow {
	rows := eventRows(events)
	for i := range rows {
		if rows[i].ParentId == 0 {
			continue
		}
		if rows[i].Id != 0 {
			rows[i].RecordingID = rows[i].Id
		}
		rows[i].Id = rows[i].ParentId
	}
	return rows
}

// sortEventsByStart puts a period's events in the order HEY draws the span: day by day,
// each day's all-day band first, then the timed events by clock. HEY serves a period
// grouped by type with no promise about order, and instants alone would not do — an
// all-day event is stamped midnight UTC, so a timed event late the same local evening
// can be the earlier instant even though the app draws it below the band.
func sortEventsByStart(events []generated.Recording) {
	sort.SliceStable(events, func(i, j int) bool {
		if di, dj := eventDay(events[i]), eventDay(events[j]); di != dj {
			return di < dj
		}
		if events[i].AllDay != events[j].AllDay {
			return events[i].AllDay
		}
		if !events[i].StartsAt.Equal(events[j].StartsAt) {
			return events[i].StartsAt.Before(events[j].StartsAt)
		}
		return events[i].Title < events[j].Title
	})
}

// eventDay names the day a row sits on, the day HEY draws it: the stamped date for an
// all-day event, the reader's own day for a timed one. HEY's JSON is always UTC, so a
// 23:30Z event belongs to the next day east of Greenwich.
func eventDay(event generated.Recording) string {
	if event.AllDay {
		return event.StartsAt.UTC().Format(dateLayout)
	}
	return event.StartsAt.Local().Format(dateLayout)
}

// writeEventRows renders one listing of events: the table when styled, the JSON envelope
// with the add/edit/delete breadcrumbs otherwise. described names the span read, in
// whatever words the command reads it — a window, a day, a week.
func writeEventRows(cmd *cobra.Command, events []eventRow, described, notice string) error {
	if writer.IsStyled() {
		if len(events) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "No events %s.\n", described)
			return nil
		}

		showsOccurrenceIDs := false
		for _, event := range events {
			if event.OccurrenceId != "" || event.RecordingID != 0 {
				showsOccurrenceIDs = true
				break
			}
		}

		table := newTable(cmd.OutOrStdout())
		header := []string{"ID"}
		if showsOccurrenceIDs {
			header = append(header, "Occurrence ID", "Recording ID")
		}
		table.addRow(append(header, "Title", "Starts", "Ends", "Calendar"))
		for _, event := range events {
			row := []string{fmt.Sprintf("%d", event.Id)}
			if showsOccurrenceIDs {
				recordingID := ""
				if event.RecordingID != 0 {
					recordingID = fmt.Sprintf("%d", event.RecordingID)
				}
				row = append(row, event.OccurrenceId, recordingID)
			}
			table.addRow(append(row,
				event.Title,
				eventBoundary(event.StartsAt, event.AllDay),
				eventBoundary(event.EndsAt, event.AllDay),
				event.Calendar.Name,
			))
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(events,
		output.WithSummary(fmt.Sprintf("%d events (%s)", len(events), described)),
		output.WithNotice(notice),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "add",
				Command:     "hey event add '...'",
				Description: "Create an event",
			},
			output.Breadcrumb{
				Action:      "edit",
				Command:     "hey event edit <id>",
				Description: "Change an event",
			},
			output.Breadcrumb{
				Action:      "delete",
				Command:     "hey event delete <id>",
				Description: "Delete an event",
			},
		),
	)
}
