package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	safefiles "github.com/basecamp/hey-cli/internal/attachments"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type timetrackCommand struct {
	cmd *cobra.Command
}

func newTimetrackCommand() *timetrackCommand {
	timetrackCommand := &timetrackCommand{}
	timetrackCommand.cmd = &cobra.Command{
		Use:   "timetrack",
		Short: "Track time",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: start, stop, current, list, export, categories, category. Use current to check if tracking is active before start/stop. Export writes CSV to stdout or safely saves it with --output.",
		},
	}

	timetrackCommand.cmd.AddCommand(newTimetrackStartCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackStopCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackCurrentCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackListCommand().cmd)
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
	cmd *cobra.Command
}

func newTimetrackStopCommand() *timetrackStopCommand {
	timetrackStopCommand := &timetrackStopCommand{}
	timetrackStopCommand.cmd = &cobra.Command{
		Use:   "stop",
		Short: "Stop time tracking",
		Example: `  hey timetrack stop
  hey timetrack stop --json`,
		RunE: timetrackStopCommand.run,
	}

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

	if err = sdk.TimeTracks().Stop(ctx, track.Id); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, "Time tracking stopped", nil)
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
	cmd   *cobra.Command
	limit int
	all   bool
}

func newTimetrackListCommand() *timetrackListCommand {
	timetrackListCommand := &timetrackListCommand{}
	timetrackListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List time tracks",
		Example: `  hey timetrack list
  hey timetrack list --limit 10
  hey timetrack list --json`,
		RunE: timetrackListCommand.run,
	}

	timetrackListCommand.cmd.Flags().IntVar(&timetrackListCommand.limit, "limit", 0, "Maximum number of time tracks to show")
	timetrackListCommand.cmd.Flags().BoolVar(&timetrackListCommand.all, "all", false, "Fetch all results (override --limit)")

	return timetrackListCommand
}

func (c *timetrackListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	resp, err := listPersonalRecordings(ctx)
	if err != nil {
		return err
	}

	tracks := filterRecordingsByType(resp, "Calendar::TimeTrack")

	total := len(tracks)
	if c.limit > 0 && !c.all && len(tracks) > c.limit {
		tracks = tracks[:c.limit]
	}
	notice := output.TruncationNotice(len(tracks), total)

	if writer.IsStyled() {
		if len(tracks) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No time tracks.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Title", "Start", "End"})
		for _, t := range tracks {
			table.addRow([]string{fmt.Sprintf("%d", t.Id), t.Title, formatTimestamp(t.StartsAt), formatTimestamp(t.EndsAt)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(tracks,
		output.WithSummary(fmt.Sprintf("%d time tracks", len(tracks))),
		output.WithNotice(notice),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "start",
			Command:     "hey timetrack start",
			Description: "Start time tracking",
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
