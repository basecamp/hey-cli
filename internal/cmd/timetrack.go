package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	safefiles "github.com/basecamp/hey-cli/internal/attachments"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

const maxTimeTrackPages = 100

type timetrackCommand struct {
	cmd *cobra.Command
}

func newTimetrackCommand() *timetrackCommand {
	timetrackCommand := &timetrackCommand{}
	timetrackCommand.cmd = &cobra.Command{
		Use:   "timetrack",
		Short: "Track time",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: start, stop, current, list, edit, delete, export, categories, category. Use current to check if tracking is active before start/stop. Stop takes --category to file the track as it stops it. List, edit and delete work on completed tracks and take the IDs list reports. Export writes CSV to stdout or safely saves it with --output.",
		},
	}

	timetrackCommand.cmd.AddCommand(newTimetrackStartCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackStopCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackCurrentCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackListCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackEditCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackDeleteCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackExportCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackCategoriesCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackCategoryCommand().cmd)

	return timetrackCommand
}

// start

type timetrackStartCommand struct {
	cmd *cobra.Command
}

func newTimetrackStartCommand() *timetrackStartCommand {
	timetrackStartCommand := &timetrackStartCommand{}
	timetrackStartCommand.cmd = &cobra.Command{
		Use:   "start",
		Short: "Start time tracking",
		Example: `  hey timetrack start
  hey timetrack start --json`,
		RunE: timetrackStartCommand.run,
	}

	return timetrackStartCommand
}

func (c *timetrackStartCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	result, err := sdk.TimeTracks().Start(ctx)
	if err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, "Time tracking started", result,
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "stop",
			Command:     "hey timetrack stop",
			Description: "Stop time tracking",
		}),
	)
}

// stop

type timetrackStopCommand struct {
	cmd      *cobra.Command
	category string
}

func newTimetrackStopCommand() *timetrackStopCommand {
	timetrackStopCommand := &timetrackStopCommand{}
	timetrackStopCommand.cmd = &cobra.Command{
		Use:   "stop",
		Short: "Stop time tracking",
		Long: `Stop the running time track.

--category files the track under a category as it stops it, in the one request, and HEY
creates the category if it has none by that title. There is no clearing a category this
way: a blank title leaves the track filed where it was.`,
		Example: `  hey timetrack stop
  hey timetrack stop --category "Client work"
  hey timetrack stop --json`,
		RunE: timetrackStopCommand.run,
	}

	timetrackStopCommand.cmd.Flags().StringVar(&timetrackStopCommand.category, "category", "", "Category title to file the stopped track under, created if HEY has none by that title")

	return timetrackStopCommand
}

func (c *timetrackStopCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	track, err := sdk.TimeTracks().GetOngoing(ctx)
	if err != nil {
		return apierr.FromSDK(err)
	}

	if track == nil {
		return apierr.ErrNotFound("time track", "active")
	}

	category := strings.TrimSpace(c.category)
	if cmd.Flags().Changed("category") && category == "" {
		return apierr.ErrUsage("--category needs a title; a blank one does not clear a category")
	}

	if err = sdk.TimeTracks().StopAndFile(ctx, track.Id, category); err != nil {
		return apierr.FromSDK(err)
	}

	summary := "Time tracking stopped"
	if category != "" {
		summary = fmt.Sprintf("Time tracking stopped and filed under %q", category)
	}
	return writeMutation(cmd, summary, nil)
}

// current

type timetrackCurrentCommand struct {
	cmd *cobra.Command
}

func newTimetrackCurrentCommand() *timetrackCurrentCommand {
	timetrackCurrentCommand := &timetrackCurrentCommand{}
	timetrackCurrentCommand.cmd = &cobra.Command{
		Use:   "current",
		Short: "Show current time tracking status",
		Example: `  hey timetrack current
  hey timetrack current --json`,
		RunE: timetrackCurrentCommand.run,
	}

	return timetrackCurrentCommand
}

func (c *timetrackCurrentCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	track, err := sdk.TimeTracks().GetOngoing(ctx)
	if err != nil {
		return apierr.FromSDK(err)
	}

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		if track == nil {
			fmt.Fprintln(w, "No active time track.")
			return nil
		}

		fmt.Fprintf(w, "Active time track #%d\n", track.Id)
		fmt.Fprintf(w, "Started: %s\n", formatTimestamp(track.StartsAt))
		if track.Title != "" {
			fmt.Fprintf(w, "Title:   %s\n", terminal.SanitizeLine(track.Title))
		}
		return nil
	}

	if track == nil {
		return writeOK(nil, output.WithSummary("No active time track"))
	}

	return writeOK(track,
		output.WithSummary(fmt.Sprintf("Active time track #%d", track.Id)),
	)
}

// list

type timetrackListCommand struct {
	cmd      *cobra.Command
	limit    int
	all      bool
	category int64
}

func newTimetrackListCommand() *timetrackListCommand {
	timetrackListCommand := &timetrackListCommand{}
	timetrackListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List completed time tracks",
		Long: `List completed time tracks, newest first, with how long each one took.

The track that is running is not here — read that with hey timetrack current. One page
arrives by default; --limit reads on until it has that many, and --all reads the lot.`,
		Annotations: map[string]string{
			"agent_notes": "Reads HEY's tracked time index, newest-ended first. Category is a title and is empty for an unfiled track. --category takes a category ID from hey timetrack categories.",
		},
		Example: `  hey timetrack list
  hey timetrack list --limit 10
  hey timetrack list --all --json
  hey timetrack list --category 42`,
		RunE: timetrackListCommand.run,
		Args: cobra.NoArgs,
	}

	timetrackListCommand.cmd.Flags().IntVar(&timetrackListCommand.limit, "limit", 0, "Maximum number of time tracks to show")
	timetrackListCommand.cmd.Flags().BoolVar(&timetrackListCommand.all, "all", false, "Fetch all results (override --limit)")
	timetrackListCommand.cmd.Flags().Int64Var(&timetrackListCommand.category, "category", 0, "Only tracks filed under this category ID")

	return timetrackListCommand
}

func (c *timetrackListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if cmd.Flags().Changed("category") && c.category <= 0 {
		return apierr.ErrUsage("--category takes a category ID; list them with hey timetrack categories")
	}

	ctx := cmd.Context()
	first, err := c.readPage(ctx, "")
	if err != nil {
		return err
	}
	collected, err := collectPages(ctx, first, pageRequest{Limit: c.limit, All: c.all, MaxPages: maxTimeTrackPages}, c.readPage)
	if err != nil {
		return err
	}

	tracks := collected.Items
	more := collected.Cursor != ""
	if c.limit > 0 && !c.all && len(tracks) > c.limit {
		tracks = tracks[:c.limit]
		more = true
	}
	notice := timetrackListNotice(len(tracks), collected.Read, more, collected.Truncated)

	if writer.IsStyled() {
		if len(tracks) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No time tracks.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Day", "Start", "End", "Length", "Category", "Notes"})
		for _, track := range tracks {
			table.addRow([]string{
				strconv.FormatInt(track.Id, 10),
				track.StartsAt.Local().Format(dateLayout),
				formatClock(track.StartsAt),
				timetrackEndClock(track.StartsAt, track.EndsAt),
				formatTrackedLength(trackedLength(track)),
				truncate(track.Category, 24),
				truncate(track.Notes, 40),
			})
		}
		table.print()
		if notice != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", notice)
		}
		return nil
	}
	if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}

	return writeOK(tracks,
		output.WithSummary(fmt.Sprintf("%d %s", len(tracks), timeTrackNoun(len(tracks)))),
		output.WithNotice(notice),
		output.WithMeta("pages_fetched", collected.Read),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "edit",
			Command:     "hey timetrack edit <id>",
			Description: "Edit a time track",
		}),
	)
}

func (c *timetrackListCommand) readPage(ctx context.Context, cursor string) (pageResult[generated.Recording], error) {
	params := &generated.ListTimeTracksParams{}
	if cursor != "" {
		params.Page = &cursor
	}
	if c.category > 0 {
		params.CategoryId = &c.category
	}

	page, err := sdk.TimeTracks().ListPage(ctx, params)
	if err != nil {
		return pageResult[generated.Recording]{}, c.readError(err)
	}
	if page == nil {
		return pageResult[generated.Recording]{}, nil
	}
	return pageResult[generated.Recording]{Items: page.TimeTracks, Cursor: page.NextPage}, nil
}

// readError names the category HEY could not find. An id no category has answers 404 for
// the whole list, which otherwise reads as somebody's tracked time having gone missing.
func (c *timetrackListCommand) readError(err error) error {
	converted := apierr.FromSDK(err)
	if c.category > 0 && apierr.AsError(converted).Code == apierr.CodeNotFound {
		return apierr.ErrNotFound("time track category", strconv.FormatInt(c.category, 10))
	}
	return converted
}

// timetrackListNotice says why the list stopped, because a list that quietly ended looks
// like the whole of somebody's tracked time.
func timetrackListNotice(shown, pages int, more, truncated bool) string {
	switch {
	case truncated:
		return fmt.Sprintf("Stopped after %d pages, at %d %s. Narrow the list with --category.", pages, shown, timeTrackNoun(shown))
	case more:
		return fmt.Sprintf("Showing %d %s. Use --all to see everything.", shown, timeTrackNoun(shown))
	default:
		return ""
	}
}

func timeTrackNoun(count int) string {
	if count == 1 {
		return "time track"
	}
	return "time tracks"
}

// trackedLength is how long a track took, computed from the two instants HEY serves
// rather than read off a field: the index carries no duration of its own.
func trackedLength(track generated.Recording) time.Duration {
	return max(track.EndsAt.Sub(track.StartsAt), 0)
}

// formatTrackedLength reads a stretch of time the way a stopwatch does, hours and all.
// Dropping the hours until there is one makes 0:45 either three quarters of a minute or
// three quarters of an hour, and moves the columns after it.
func formatTrackedLength(length time.Duration) string {
	seconds := int(max(length, 0).Seconds())
	return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

// formatClock is a time of day on the reader's own clock. HEY serves every JSON timestamp in
// UTC, so a track kept at noon in Zagreb arrives as 11:00 and reads as an hour it was not — and
// with the day beside it, a late enough track lands on the wrong date entirely.
func formatClock(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Local().Format("15:04")
}

// timetrackEndClock is the end of a track next to the day it started on. A track that ran
// past midnight ended on another day, so that one carries its date rather than reading as
// an end before its own start.
func timetrackEndClock(startsAt, endsAt time.Time) string {
	// Compared on the reader's clock, since that is the day the Day column shows: a track can
	// cross midnight in one zone and not in another.
	if !endsAt.IsZero() && endsAt.Local().Format(dateLayout) != startsAt.Local().Format(dateLayout) {
		return endsAt.Local().Format("2006-01-02T15:04")
	}
	return formatClock(endsAt)
}

// edit

type timetrackEditCommand struct {
	cmd      *cobra.Command
	start    string
	end      string
	category string
	notes    string
}

func newTimetrackEditCommand() *timetrackEditCommand {
	timetrackEditCommand := &timetrackEditCommand{}
	timetrackEditCommand.cmd = &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a completed time track",
		Long: `Edit a time track. Only the fields whose flags are given change.

Timestamps are YYYY-MM-DDTHH:MM in your own time zone, or a full RFC 3339 instant when
the zone matters. --category files the track under a category title, which HEY creates if
it has none by that title; a blank title does not clear one. Editing a track completes it,
so this is for tracks that have already finished.`,
		Example: `  hey timetrack edit 1042 --end 2026-08-22T17:30
  hey timetrack edit 1042 --category "Client work" --notes "Invoice review"
  hey timetrack edit 1042 --start 2026-08-22T09:00 --end 2026-08-22T11:15`,
		RunE: timetrackEditCommand.run,
		Args: usageExactOneArg(),
	}

	timetrackEditCommand.cmd.Flags().StringVar(&timetrackEditCommand.start, "start", "", "New start, as YYYY-MM-DDTHH:MM in your time zone")
	timetrackEditCommand.cmd.Flags().StringVar(&timetrackEditCommand.end, "end", "", "New end, as YYYY-MM-DDTHH:MM in your time zone")
	timetrackEditCommand.cmd.Flags().StringVar(&timetrackEditCommand.category, "category", "", "Category title to file the track under, created if HEY has none by that title")
	timetrackEditCommand.cmd.Flags().StringVar(&timetrackEditCommand.notes, "notes", "", "New notes")

	return timetrackEditCommand
}

func (c *timetrackEditCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	trackID, err := parsePositiveID(args[0], "time track")
	if err != nil {
		return err
	}
	payload, err := c.payload(cmd)
	if err != nil {
		return err
	}

	track, err := sdk.TimeTracks().Update(cmd.Context(), trackID, generated.UpdateTimeTrackJSONRequestBody{CalendarTimeTrack: payload})
	if err != nil {
		return timetrackUpdateError(err)
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Time track %d updated.", trackID),
		"Time track updated",
		track,
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "list",
			Command:     "hey timetrack list",
			Description: "List completed time tracks",
		}),
	)
}

func (c *timetrackEditCommand) payload(cmd *cobra.Command) (generated.UpdateTimeTrackPayload, error) {
	payload := generated.UpdateTimeTrackPayload{}
	changed := false

	if cmd.Flags().Changed("start") {
		startsAt, err := parseTimestampArg("--start", c.start)
		if err != nil {
			return payload, err
		}
		payload.StartsAt = &startsAt
		changed = true
	}
	if cmd.Flags().Changed("end") {
		endsAt, err := parseTimestampArg("--end", c.end)
		if err != nil {
			return payload, err
		}
		payload.EndsAt = &endsAt
		changed = true
	}
	if cmd.Flags().Changed("category") {
		payload.CategoryTitle = strings.TrimSpace(c.category)
		if payload.CategoryTitle == "" {
			return payload, apierr.ErrUsage("--category needs a title; a blank one does not clear a category")
		}
		changed = true
	}
	if cmd.Flags().Changed("notes") {
		payload.Notes = c.notes
		if strings.TrimSpace(payload.Notes) == "" {
			return payload, apierr.ErrUsage("--notes needs text; a blank one does not clear the notes")
		}
		changed = true
	}
	if !changed {
		return payload, apierr.ErrUsage("provide at least one of --start, --end, --category, or --notes")
	}
	return payload, nil
}

// parseTimestampArg reads a moment typed on the command line, on the caller's own clock
// unless they spelled out an offset, and hands it over in UTC — which needs no zone name
// and is exact.
func parseTimestampArg(label, value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04", time.RFC3339} {
		if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(value), time.Local); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, apierr.ErrUsageHint(
		fmt.Sprintf("invalid %s: %s", label, value),
		"timestamps are YYYY-MM-DDTHH:MM in your time zone, for example 2026-08-22T09:00, or a full RFC 3339 instant")
}

// timetrackUpdateError reads a 400 back as what it is. HEY answers one when it cannot
// parse a timestamp it was sent, which is somebody's typing rather than a failure here.
func timetrackUpdateError(err error) error {
	converted := apierr.FromSDK(err)
	if apierr.AsError(converted).HTTPStatus == http.StatusBadRequest {
		return apierr.ErrUsageHint("HEY could not read the time track update",
			"check --start and --end: timestamps are YYYY-MM-DDTHH:MM, for example 2026-08-22T09:00")
	}
	return converted
}

// delete

type timetrackDeleteCommand struct {
	cmd *cobra.Command
}

func newTimetrackDeleteCommand() *timetrackDeleteCommand {
	timetrackDeleteCommand := &timetrackDeleteCommand{}
	timetrackDeleteCommand.cmd = &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a time track",
		Example: `  hey timetrack delete 1042`,
		RunE:    timetrackDeleteCommand.run,
		Args:    usageExactOneArg(),
	}
	return timetrackDeleteCommand
}

func (c *timetrackDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	trackID, err := parsePositiveID(args[0], "time track")
	if err != nil {
		return err
	}
	if err = sdk.TimeTracks().Delete(cmd.Context(), trackID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Time track %d deleted.", trackID),
		"Time track deleted",
		nil,
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "list",
			Command:     "hey timetrack list",
			Description: "List completed time tracks",
		}),
	)
}

// export

type timetrackExportCommand struct {
	cmd    *cobra.Command
	output string
	force  bool
}

type timeTrackExportResult struct {
	Path     string `json:"path"`
	ByteSize int64  `json:"byte_size"`
}

func newTimetrackExportCommand() *timetrackExportCommand {
	timetrackExportCommand := &timetrackExportCommand{}
	timetrackExportCommand.cmd = &cobra.Command{
		Use:   "export",
		Short: "Export completed time tracks as CSV",
		Long: `Export every completed time track as CSV, newest first.

Without --output the CSV goes straight to stdout, so redirecting it to a file is the whole
recipe. The output formatting flags have nothing to reshape there and are refused rather
than ignored: --json, --quiet, --markdown, --ids-only, --count and --html all need
--output, which returns file metadata they can format.`,
		Example: `  hey timetrack export > time-tracking.csv
  hey timetrack export --output time-tracking.csv
  hey timetrack export --output time-tracking.csv --force`,
		Annotations: map[string]string{
			"agent_notes": "Writes HEY's CSV unchanged to stdout. Use --output to save it to a file; existing paths are preserved unless --force is set. The columns are Start, End, Duration, Category, and Notes.",
		},
		RunE: timetrackExportCommand.run,
		Args: cobra.NoArgs,
	}

	timetrackExportCommand.cmd.Flags().StringVarP(&timetrackExportCommand.output, "output", "o", "", "Save CSV to this file instead of stdout")
	timetrackExportCommand.cmd.Flags().BoolVar(&timetrackExportCommand.force, "force", false, "Replace an existing output file")

	return timetrackExportCommand
}

func (c *timetrackExportCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if c.output == "" {
		if c.force {
			return apierr.ErrUsage("--force requires --output")
		}
		if timetrackExportStructuredOutputRequested(cmd) {
			return apierr.ErrUsage("output formatting flags require --output for time track exports")
		}
	} else if err := ensureTimetrackExportDestination(c.output, c.force); err != nil {
		return err
	}

	data, err := sdk.TimeTracks().Export(cmd.Context())
	if err != nil {
		return apierr.FromSDK(err)
	}

	if c.output == "" {
		if _, writeErr := cmd.OutOrStdout().Write(data); writeErr != nil {
			return apierr.ErrAPI(0, fmt.Sprintf("could not write time track export: %v", writeErr))
		}
		return nil
	}

	destination := filepath.Clean(c.output)
	written, err := safefiles.SaveBytes(destination, data, c.force)
	if err != nil {
		return err
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Exported time tracks to %s (%s)", destination, formatByteSize(written)),
		fmt.Sprintf("Time tracks exported to %s", destination),
		timeTrackExportResult{Path: destination, ByteSize: written})
}

// timetrackExportStructuredOutputRequested reports whether the caller asked for
// formatted output, which the export cannot give them without a file to report on:
// stdout carries the CSV itself. Asking the writer covers every output flag,
// including the two — --quiet and --html — the hand-written list used to miss.
func timetrackExportStructuredOutputRequested(cmd *cobra.Command) bool {
	return writer.RequestedFormat() != output.FormatAuto || htmlOutput || statsFlag || cmd.Flags().Changed("jq")
}

func ensureTimetrackExportDestination(destination string, force bool) error {
	if force {
		return nil
	}
	if _, err := os.Lstat(destination); err == nil {
		return apierr.ErrUsage(fmt.Sprintf("destination already exists: %s (use --force to replace it)", destination))
	} else if !os.IsNotExist(err) {
		return apierr.ErrAPI(0, fmt.Sprintf("could not inspect destination: %v", err))
	}
	return nil
}

// categories

type timetrackCategoriesCommand struct {
	cmd *cobra.Command
}

func newTimetrackCategoriesCommand() *timetrackCategoriesCommand {
	timetrackCategoriesCommand := &timetrackCategoriesCommand{}
	timetrackCategoriesCommand.cmd = &cobra.Command{
		Use:   "categories",
		Short: "List time track categories",
		Example: `  hey timetrack categories
  hey timetrack categories --json`,
		RunE: timetrackCategoriesCommand.run,
	}
	return timetrackCategoriesCommand
}

func (c *timetrackCategoriesCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	categories, err := sdk.TimeTracks().Categories(cmd.Context())
	if err != nil {
		return apierr.FromSDK(err)
	}

	if writer.IsStyled() {
		if len(categories) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No time track categories.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Title"})
		for _, category := range categories {
			table.addRow([]string{fmt.Sprintf("%d", category.Id), terminal.SanitizeLine(category.Title)})
		}
		table.print()
		return nil
	}

	return writeOK(categories,
		output.WithSummary(fmt.Sprintf("%d time track categories", len(categories))),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "create",
			Command:     "hey timetrack category create <title>",
			Description: "Create a time track category",
		}),
	)
}

// category

type timetrackCategoryCommand struct {
	cmd *cobra.Command
}

func newTimetrackCategoryCommand() *timetrackCategoryCommand {
	timetrackCategoryCommand := &timetrackCategoryCommand{}
	timetrackCategoryCommand.cmd = &cobra.Command{
		Use:   "category",
		Short: "Manage time track categories",
	}
	timetrackCategoryCommand.cmd.AddCommand(newTimetrackCategoryCreateCommand().cmd)
	timetrackCategoryCommand.cmd.AddCommand(newTimetrackCategoryRenameCommand().cmd)
	timetrackCategoryCommand.cmd.AddCommand(newTimetrackCategoryDeleteCommand().cmd)
	return timetrackCategoryCommand
}

type timetrackCategoryCreateCommand struct {
	cmd *cobra.Command
}

func newTimetrackCategoryCreateCommand() *timetrackCategoryCreateCommand {
	timetrackCategoryCreateCommand := &timetrackCategoryCreateCommand{}
	timetrackCategoryCreateCommand.cmd = &cobra.Command{
		Use:     "create <title>",
		Short:   "Create a time track category",
		Example: `  hey timetrack category create "Client work"`,
		RunE:    timetrackCategoryCreateCommand.run,
		Args:    usageExactOneArg(),
	}
	return timetrackCategoryCreateCommand
}

func (c *timetrackCategoryCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	title := strings.TrimSpace(args[0])
	if title == "" {
		return apierr.ErrUsage("category title is required")
	}
	if err := sdk.TimeTracks().CreateCategory(cmd.Context(), title); err != nil {
		return apierr.FromSDK(err)
	}
	return writeTimetrackCategoryMutation(cmd, fmt.Sprintf("Time track category %q created", title))
}

type timetrackCategoryRenameCommand struct {
	cmd *cobra.Command
}

func newTimetrackCategoryRenameCommand() *timetrackCategoryRenameCommand {
	timetrackCategoryRenameCommand := &timetrackCategoryRenameCommand{}
	timetrackCategoryRenameCommand.cmd = &cobra.Command{
		Use:     "rename <id> <title>",
		Short:   "Rename a time track category",
		Example: `  hey timetrack category rename 123 "Planning"`,
		RunE:    timetrackCategoryRenameCommand.run,
		Args:    usageExactArgs(2),
	}
	return timetrackCategoryRenameCommand
}

func (c *timetrackCategoryRenameCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	categoryID, err := parsePositiveID(args[0], "category")
	if err != nil {
		return err
	}
	title := strings.TrimSpace(args[1])
	if title == "" {
		return apierr.ErrUsage("category title is required")
	}
	if err := sdk.TimeTracks().UpdateCategory(cmd.Context(), categoryID, title); err != nil {
		return apierr.FromSDK(err)
	}
	return writeTimetrackCategoryMutation(cmd, fmt.Sprintf("Time track category %d renamed to %q", categoryID, title))
}

type timetrackCategoryDeleteCommand struct {
	cmd *cobra.Command
}

func newTimetrackCategoryDeleteCommand() *timetrackCategoryDeleteCommand {
	timetrackCategoryDeleteCommand := &timetrackCategoryDeleteCommand{}
	timetrackCategoryDeleteCommand.cmd = &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a time track category",
		Example: `  hey timetrack category delete 123`,
		RunE:    timetrackCategoryDeleteCommand.run,
		Args:    usageExactOneArg(),
	}
	return timetrackCategoryDeleteCommand
}

func (c *timetrackCategoryDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	categoryID, err := parsePositiveID(args[0], "category")
	if err != nil {
		return err
	}
	if err := sdk.TimeTracks().DeleteCategory(cmd.Context(), categoryID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeTimetrackCategoryMutation(cmd, fmt.Sprintf("Time track category %d deleted", categoryID))
}

func writeTimetrackCategoryMutation(cmd *cobra.Command, summary string) error {
	return writeMutation(cmd, summary, nil,
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "list",
			Command:     "hey timetrack categories",
			Description: "List time track categories",
		}),
	)
}
