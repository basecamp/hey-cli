package cmd

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
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
			"agent_notes": "Subcommands: list, create, edit, delete. Defaults to the personal calendar; pass --calendar (ID or owned calendar name, case-insensitive) to target another. Use list --ids-only to pipe IDs to edit/delete.",
		},
	}

	eventCommand.cmd.AddCommand(newEventListCommand().cmd)
	eventCommand.cmd.AddCommand(newEventCreateCommand().cmd)
	eventCommand.cmd.AddCommand(newEventEditCommand().cmd)
	eventCommand.cmd.AddCommand(newEventDeleteCommand().cmd)

	return eventCommand
}

// list

type eventListCommand struct {
	cmd      *cobra.Command
	limit    int
	all      bool
	calendar string
}

func newEventListCommand() *eventListCommand {
	c := &eventListCommand{}
	c.cmd = &cobra.Command{
		Use:   "list",
		Short: "List calendar events",
		Example: `  hey event list
  hey event list --limit 10
  hey event list --calendar Work
  hey event list --calendar 123
  hey event list --ids-only`,
		RunE: c.run,
	}

	c.cmd.Flags().IntVar(&c.limit, "limit", 0, "Maximum number of events to show")
	c.cmd.Flags().BoolVar(&c.all, "all", false, "Fetch all results (override --limit)")
	c.cmd.Flags().StringVar(&c.calendar, "calendar", "", "Calendar ID or name (defaults to personal calendar; names matched case-insensitively against owned calendars)")

	return c
}

func (c *eventListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()

	var resp *generated.CalendarRecordingsResponse
	if c.calendar != "" {
		calID, err := resolveCalendarID(ctx, c.calendar)
		if err != nil {
			return err
		}
		now := time.Now()
		resp, err = sdk.Calendars().GetRecordings(ctx, calID, &generated.GetCalendarRecordingsParams{
			StartsOn: now.AddDate(-personalRecordingsLookbackYears, 0, 0).Format("2006-01-02"),
			EndsOn:   now.AddDate(personalRecordingsLookaheadYears, 0, 0).Format("2006-01-02"),
		})
		if err != nil {
			return convertSDKError(err)
		}
	} else {
		var err error
		resp, err = listPersonalRecordings(ctx)
		if err != nil {
			return err
		}
	}
	events := filterRecordingsByType(resp, "Calendar::Event")
	if events == nil {
		events = []generated.CalendarEvent{}
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
	cmd       *cobra.Command
	title     string
	date      string
	allDay    bool
	start     string
	end       string
	calendar  string
	timezone  string
	reminders []string
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
	c.cmd.Flags().StringVar(&c.calendar, "calendar", "", "Calendar ID or name (defaults to personal calendar; names matched case-insensitively against owned calendars)")
	c.cmd.Flags().StringVar(&c.timezone, "timezone", "", "IANA timezone name (defaults to local)")
	c.cmd.Flags().StringArrayVar(&c.reminders, "reminder", nil, "Reminder duration (e.g. 30m, 1h, 2d, 1w); repeatable")

	return c
}

func (c *eventCreateCommand) run(cmd *cobra.Command, args []string) error {
	c.title = strings.TrimSpace(c.title)
	c.date = strings.TrimSpace(c.date)
	c.start = strings.TrimSpace(c.start)
	c.end = strings.TrimSpace(c.end)
	c.timezone = strings.TrimSpace(c.timezone)

	if c.title == "" {
		return output.ErrUsage("--title is required")
	}
	if c.date == "" {
		return output.ErrUsage("--date is required (YYYY-MM-DD)")
	}
	if _, err := time.Parse("2006-01-02", c.date); err != nil {
		return output.ErrUsage("--date must be in YYYY-MM-DD format")
	}
	if c.allDay {
		if c.start != "" || c.end != "" {
			return output.ErrUsage("--start/--end cannot be combined with --all-day")
		}
		if cmd.Flags().Changed("timezone") {
			return output.ErrUsage("--timezone cannot be combined with --all-day")
		}
	} else {
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

	if cmd.Flags().Changed("timezone") {
		if err := validateTimezone(c.timezone); err != nil {
			return err
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

	var defaultCalendars []generated.Calendar // populated only when taking the default branch

	var calID int64
	if c.calendar != "" {
		id, err := resolveCalendarID(ctx, c.calendar)
		if err != nil {
			return err
		}
		calID = id
	} else {
		payload, err := sdk.Calendars().List(ctx)
		if err != nil {
			return convertSDKError(err)
		}
		defaultCalendars = unwrapCalendars(payload)
		id, err := findPersonalCalendarID(defaultCalendars)
		if err != nil {
			msg := "Couldn't determine default calendar. Pass --calendar <id-or-name>."
			if list := formatOwnedCalendarList(defaultCalendars); list != "" {
				msg += " Available:\n" + list
			}
			return output.ErrUsage(msg)
		}
		calID = id
	}

	tz := c.timezone
	if tz == "" && !c.allDay {
		tz = localTimezoneName()
		if tz == "" {
			return output.ErrUsageHint(
				"could not determine local timezone",
				"pass --timezone explicitly (e.g. --timezone America/New_York)",
			)
		}
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
		if c.calendar == "" && hey.AsError(err).HTTPStatus == 404 {
			msg := fmt.Sprintf("Couldn't create event in default calendar (id=%d). Pass --calendar <id-or-name>.", calID)
			if list := formatOwnedCalendarList(defaultCalendars); list != "" {
				msg += " Available:\n" + list
			}
			return output.ErrUsage(msg)
		}
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

	c.title = strings.TrimSpace(c.title)
	c.date = strings.TrimSpace(c.date)
	c.start = strings.TrimSpace(c.start)
	c.end = strings.TrimSpace(c.end)
	c.timezone = strings.TrimSpace(c.timezone)

	editable := []string{"title", "date", "start", "end", "all-day", "timezone", "reminder"}
	anyChanged := false
	for _, name := range editable {
		if flags.Changed(name) {
			anyChanged = true
			break
		}
	}
	if !anyChanged {
		return output.ErrUsageHint(
			"no fields to update",
			"pass at least one of --title, --date, --start, --end, --all-day, --timezone, --reminder",
		)
	}

	if flags.Changed("all-day") && c.allDay {
		if flags.Changed("start") || flags.Changed("end") {
			return output.ErrUsage("--start/--end cannot be combined with --all-day")
		}
		if flags.Changed("timezone") {
			return output.ErrUsage("--timezone cannot be combined with --all-day")
		}
	}

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
	if flags.Changed("timezone") {
		if err := validateTimezone(c.timezone); err != nil {
			return err
		}
	}

	var reminders []time.Duration
	if flags.Changed("reminder") {
		reminders, err = parseReminders(c.reminders)
		if err != nil {
			return err
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

// delete

type eventDeleteCommand struct {
	cmd *cobra.Command
}

func newEventDeleteCommand() *eventDeleteCommand {
	c := &eventDeleteCommand{}
	c.cmd = &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a calendar event",
		Example: `  hey event delete 123`,
		Args:    usageExactOneArg(),
		RunE:    c.run,
	}
	return c
}

func (c *eventDeleteCommand) run(cmd *cobra.Command, args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid event ID: %s", args[0]))
	}

	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	if err := sdk.CalendarEvents().Delete(ctx, id); err != nil {
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Event deleted.")
		return nil
	}

	return writeOK(nil, output.WithSummary("Event deleted"))
}

// validateTimezone returns a usage error when tz isn't a resolvable IANA
// timezone name. Empty input is also rejected so callers don't need a
// separate check.
func validateTimezone(tz string) error {
	if tz == "" {
		return output.ErrUsage("--timezone cannot be empty")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return output.ErrUsageHint(
			fmt.Sprintf("invalid --timezone %q", tz),
			"use an IANA timezone name (e.g. America/New_York)",
		)
	}
	return nil
}

// parseReminderDuration parses reminder durations like "30m", "1h", "2d", "1w"
// into time.Duration. Supports minutes, hours, days, and weeks. Rejects
// magnitudes that would overflow time.Duration.
func parseReminderDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid reminder %q: expected a number followed by m, h, d, or w", s)
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid reminder %q: expected a non-negative number followed by m, h, d, or w", s)
	}
	var perUnit time.Duration
	switch unit {
	case 'm':
		perUnit = time.Minute
	case 'h':
		perUnit = time.Hour
	case 'd':
		perUnit = 24 * time.Hour
	case 'w':
		perUnit = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid reminder %q: unit must be m, h, d, or w", s)
	}
	if n > int64(time.Duration(math.MaxInt64)/perUnit) {
		return 0, fmt.Errorf("invalid reminder %q: value is too large", s)
	}
	return time.Duration(n) * perUnit, nil
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

// resolveCalendarID maps user input (numeric ID or calendar name) to a
// calendar ID. Numeric input is returned as-is with no SDK call. Otherwise the
// calendar list is fetched and filtered to Owned == true, matching Name
// case-insensitively. Zero matches or multiple matches yield a usage error.
func resolveCalendarID(ctx context.Context, input string) (int64, error) {
	trimmed := strings.TrimSpace(input)
	if id, err := strconv.ParseInt(trimmed, 10, 64); err == nil && id > 0 {
		return id, nil
	}

	payload, err := sdk.Calendars().List(ctx)
	if err != nil {
		return 0, convertSDKError(err)
	}
	calendars := unwrapCalendars(payload)

	var matches []generated.Calendar
	for _, cal := range calendars {
		if !cal.Owned {
			continue
		}
		if strings.EqualFold(cal.Name, trimmed) {
			matches = append(matches, cal)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0].Id, nil
	case 0:
		return 0, output.ErrUsageHint(
			fmt.Sprintf("no owned calendar named %q", trimmed),
			"run 'hey calendars' to list your calendars, then use --calendar <id-or-name>",
		)
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "multiple owned calendars named %q; pick one by ID:\n", trimmed)
		sort.Slice(matches, func(i, j int) bool { return matches[i].Id < matches[j].Id })
		for _, cal := range matches {
			fmt.Fprintf(&b, "  %d\t%s\n", cal.Id, cal.Name)
		}
		return 0, output.ErrUsage(strings.TrimRight(b.String(), "\n"))
	}
}

// formatOwnedCalendarList renders owned calendars as "  ID\tName" lines sorted
// by ID. Returns an empty string when there are no owned calendars.
func formatOwnedCalendarList(calendars []generated.Calendar) string {
	owned := make([]generated.Calendar, 0, len(calendars))
	for _, cal := range calendars {
		if cal.Owned {
			owned = append(owned, cal)
		}
	}
	if len(owned) == 0 {
		return ""
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].Id < owned[j].Id })
	var b strings.Builder
	for _, cal := range owned {
		fmt.Fprintf(&b, "  %d\t%s\n", cal.Id, cal.Name)
	}
	return strings.TrimRight(b.String(), "\n")
}

// systemTimezonePath is the path consulted by localTimezoneName after
// time.Local and $TZ fail. Overridable for tests.
var systemTimezonePath = "/etc/localtime"

// localTimezoneName returns the local IANA timezone name, or "" when no
// reasonable candidate can be determined. Silently defaulting to UTC would
// shift event times, so callers should treat "" as "ask the user".
//
// On Linux/macOS, time.Local.String() typically returns "Local" when the
// zone was loaded from /etc/localtime; we fall back to $TZ and to the
// /etc/localtime symlink target to recover an IANA name.
func localTimezoneName() string {
	if name := time.Local.String(); name != "" && name != "Local" {
		return name
	}
	if tz := os.Getenv("TZ"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			if name := loc.String(); name != "" && name != "Local" {
				return name
			}
		}
	}
	if name := readSystemTimezoneFrom(systemTimezonePath); name != "" {
		if _, err := time.LoadLocation(name); err == nil {
			return name
		}
	}
	return ""
}

// readSystemTimezoneFrom resolves a symlink like /etc/localtime →
// /usr/share/zoneinfo/America/Sao_Paulo and returns the IANA suffix
// ("America/Sao_Paulo"). Returns "" on any failure.
func readSystemTimezoneFrom(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	const marker = "zoneinfo/"
	idx := strings.Index(resolved, marker)
	if idx < 0 {
		return ""
	}
	return resolved[idx+len(marker):]
}
