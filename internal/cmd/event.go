package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/output"
)

type eventCommand struct {
	cmd *cobra.Command
}

func newEventCommand() *eventCommand {
	eventCommand := &eventCommand{}
	eventCommand.cmd = &cobra.Command{
		Use:   "event",
		Short: "Manage calendar events",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: list, create, edit. Lists events from the personal calendar by default, or from --calendar ID.",
		},
	}

	eventCommand.cmd.AddCommand(newEventListCommand().cmd)
	eventCommand.cmd.AddCommand(newEventCreateCommand().cmd)
	eventCommand.cmd.AddCommand(newEventEditCommand().cmd)

	return eventCommand
}

// list

type eventListCommand struct {
	cmd        *cobra.Command
	limit      int
	all        bool
	calendarID int64
}

func newEventListCommand() *eventListCommand {
	eventListCommand := &eventListCommand{}
	eventListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List calendar events",
		Example: `  hey event list
  hey event list --limit 10
  hey event list --calendar 123
  hey event list --ids-only`,
		RunE: eventListCommand.run,
	}

	eventListCommand.cmd.Flags().IntVar(&eventListCommand.limit, "limit", 0, "Maximum number of events to show")
	eventListCommand.cmd.Flags().BoolVar(&eventListCommand.all, "all", false, "Fetch all results (override --limit)")
	eventListCommand.cmd.Flags().Int64Var(&eventListCommand.calendarID, "calendar", 0, "Calendar ID (defaults to personal calendar)")

	return eventListCommand
}

func (c *eventListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()

	var events []generated.Recording
	if c.calendarID != 0 {
		resp, err := sdk.Calendars().GetRecordings(ctx, c.calendarID, nil)
		if err != nil {
			return convertSDKError(err)
		}
		events = filterRecordingsByType(resp, "Calendar::Event")
	} else {
		resp, err := listPersonalRecordings(ctx)
		if err != nil {
			return err
		}
		events = filterRecordingsByType(resp, "Calendar::Event")
	}

	total := len(events)
	if c.limit > 0 && !c.all && len(events) > c.limit {
		events = events[:c.limit]
	}
	notice := output.TruncationNotice(len(events), total)

	if writer.IsStyled() {
		if len(events) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No events.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Title", "Starts", "Ends"})
		for _, e := range events {
			table.addRow([]string{fmt.Sprintf("%d", e.Id), e.Title, formatTimestamp(e.StartsAt), formatTimestamp(e.EndsAt)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(events,
		output.WithSummary(fmt.Sprintf("%d events", len(events))),
		output.WithNotice(notice),
	)
}

// create

type eventCreateCommand struct {
	cmd        *cobra.Command
	title      string
	date       string
	allDay     bool
	start      string
	end        string
	calendarID int64
	timezone   string
	reminders  []string
}

func newEventCreateCommand() *eventCreateCommand {
	c := &eventCreateCommand{}
	c.cmd = &cobra.Command{
		Use:   "create",
		Short: "Create a calendar event",
		Example: `  hey event create --title "Team sync" --date 2024-06-15 --start 09:00 --end 10:00
  hey event create --title "Holiday" --date 2024-06-15 --all-day
  hey event create --title "Review" --date 2024-06-15 --start 14:00 --end 15:00 --reminder 30m --reminder 1h`,
		RunE: c.run,
	}

	c.cmd.Flags().StringVar(&c.title, "title", "", "Event title (required)")
	c.cmd.Flags().StringVar(&c.date, "date", "", "Event date YYYY-MM-DD (required)")
	c.cmd.Flags().BoolVar(&c.allDay, "all-day", false, "Create as all-day event")
	c.cmd.Flags().StringVar(&c.start, "start", "", "Start time HH:MM (required unless --all-day)")
	c.cmd.Flags().StringVar(&c.end, "end", "", "End time HH:MM (required unless --all-day)")
	c.cmd.Flags().Int64Var(&c.calendarID, "calendar", 0, "Calendar ID (defaults to personal calendar)")
	c.cmd.Flags().StringVar(&c.timezone, "timezone", "", "IANA timezone name (defaults to local)")
	c.cmd.Flags().StringArrayVar(&c.reminders, "reminder", nil, "Reminder duration (e.g. 30m, 1h, 2d, 1w); repeatable")

	return c
}

func (c *eventCreateCommand) run(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(c.title) == "" {
		return output.ErrUsage("--title is required")
	}
	if strings.TrimSpace(c.date) == "" {
		return output.ErrUsage("--date is required (YYYY-MM-DD)")
	}
	if _, err := time.Parse("2006-01-02", c.date); err != nil {
		return output.ErrUsage("--date must be in YYYY-MM-DD format")
	}
	if !c.allDay {
		if c.start == "" || c.end == "" {
			return output.ErrUsageHint(
				"must supply either --all-day or both --start and --end",
				"Use --all-day for all-day events, or --start HH:MM --end HH:MM for timed events",
			)
		}
		if _, err := time.Parse("15:04", c.start); err != nil {
			return output.ErrUsage("--start must be in HH:MM format")
		}
		if _, err := time.Parse("15:04", c.end); err != nil {
			return output.ErrUsage("--end must be in HH:MM format")
		}
	}

	reminders, err := parseReminders(c.reminders)
	if err != nil {
		return err
	}

	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()

	calID := c.calendarID
	if calID == 0 {
		payload, err := sdk.Calendars().List(ctx)
		if err != nil {
			return convertSDKError(err)
		}
		id, err := findPersonalCalendarID(unwrapCalendars(payload))
		if err != nil {
			return output.ErrNotFound("calendar", "personal")
		}
		calID = id
	}

	tz := c.timezone
	if tz == "" && !c.allDay {
		tz = localTimezoneName()
	}

	params := hey.CreateCalendarEventParams{
		CalendarID: calID,
		Title:      c.title,
		StartsAt:   c.date,
		EndsAt:     c.date,
		AllDay:     c.allDay,
		Reminders:  reminders,
	}
	if !c.allDay {
		params.StartTime = c.start
		params.EndTime = c.end
		params.TimeZone = tz
	}

	id, err := sdk.CalendarEvents().Create(ctx, params)
	if err != nil {
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Event created (id=%d).\n", id)
		return nil
	}

	return writeOK(map[string]any{"id": id}, output.WithSummary("Event created"))
}

// edit

type eventEditCommand struct {
	cmd       *cobra.Command
	title     string
	date      string
	start     string
	end       string
	allDay    bool
	timezone  string
	reminders []string
}

func newEventEditCommand() *eventEditCommand {
	c := &eventEditCommand{}
	c.cmd = &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a calendar event",
		Example: `  hey event edit 123 --title "Updated standup"
  hey event edit 123 --date 2024-06-16 --start 10:00 --end 11:00
  hey event edit 123 --all-day
  hey event edit 123 --reminder 30m --reminder 1h`,
		Args: usageExactOneArg(),
		RunE: c.run,
	}

	c.cmd.Flags().StringVar(&c.title, "title", "", "New event title")
	c.cmd.Flags().StringVar(&c.date, "date", "", "New event date YYYY-MM-DD (applies to both start and end)")
	c.cmd.Flags().StringVar(&c.start, "start", "", "New start time HH:MM")
	c.cmd.Flags().StringVar(&c.end, "end", "", "New end time HH:MM")
	c.cmd.Flags().BoolVar(&c.allDay, "all-day", false, "Set as all-day event")
	c.cmd.Flags().StringVar(&c.timezone, "timezone", "", "IANA timezone name")
	c.cmd.Flags().StringArrayVar(&c.reminders, "reminder", nil, "Reminder duration (e.g. 30m, 1h, 2d, 1w); repeatable")

	return c
}

func (c *eventEditCommand) run(cmd *cobra.Command, args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid event ID: %s", args[0]))
	}

	flags := cmd.Flags()

	if flags.Changed("date") {
		if _, err := time.Parse("2006-01-02", c.date); err != nil {
			return output.ErrUsage("--date must be in YYYY-MM-DD format")
		}
	}
	if flags.Changed("start") {
		if _, err := time.Parse("15:04", c.start); err != nil {
			return output.ErrUsage("--start must be in HH:MM format")
		}
	}
	if flags.Changed("end") {
		if _, err := time.Parse("15:04", c.end); err != nil {
			return output.ErrUsage("--end must be in HH:MM format")
		}
	}

	var reminders []time.Duration
	if flags.Changed("reminder") {
		reminders, err = parseReminders(c.reminders)
		if err != nil {
			return err
		}
		if reminders == nil {
			reminders = []time.Duration{}
		}
	}

	if err := requireAuth(); err != nil {
		return err
	}

	params := hey.UpdateCalendarEventParams{}
	if flags.Changed("title") {
		v := c.title
		params.Title = &v
	}
	if flags.Changed("date") {
		v := c.date
		params.StartsAt = &v
		params.EndsAt = &v
	}
	if flags.Changed("start") {
		v := c.start
		params.StartTime = &v
	}
	if flags.Changed("end") {
		v := c.end
		params.EndTime = &v
	}
	if flags.Changed("all-day") {
		v := c.allDay
		params.AllDay = &v
	}
	if flags.Changed("timezone") {
		v := c.timezone
		params.TimeZone = &v
	}
	if flags.Changed("reminder") {
		params.Reminders = reminders
	}

	ctx := cmd.Context()
	if err := sdk.CalendarEvents().Update(ctx, id, params); err != nil {
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Event updated.")
		return nil
	}

	return writeOK(nil, output.WithSummary("Event updated"))
}

// parseReminderDuration parses reminder durations like "30m", "1h", "2d", "1w"
// into time.Duration. Supports minutes, hours, days, and weeks.
func parseReminderDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid reminder %q: expected a number followed by m, h, d, or w", s)
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid reminder %q: expected a non-negative number followed by m, h, d, or w", s)
	}
	switch unit {
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid reminder %q: unit must be m, h, d, or w", s)
	}
}

// parseReminders converts a list of reminder strings to durations, returning
// a usage error on the first failure.
func parseReminders(in []string) ([]time.Duration, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]time.Duration, 0, len(in))
	for _, s := range in {
		d, err := parseReminderDuration(s)
		if err != nil {
			return nil, output.ErrUsage(err.Error())
		}
		out = append(out, d)
	}
	return out, nil
}

// localTimezoneName returns the local IANA timezone name, falling back to
// "UTC" if the runtime didn't resolve one.
func localTimezoneName() string {
	name := time.Local.String()
	if name == "" || name == "Local" {
		return "UTC"
	}
	return name
}
