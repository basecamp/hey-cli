package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
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
			"agent_notes": "Subcommands: start, stop, current, list, categories, category. Use current to check if tracking is active before start/stop.",
		},
	}

	timetrackCommand.cmd.AddCommand(newTimetrackStartCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackStopCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackCurrentCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackListCommand().cmd)
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
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Time tracking started.")
		return nil
	}

	normalized, nerr := normalizeAny(result)
	if nerr != nil {
		return writeOK(nil, output.WithSummary("Time tracking started"))
	}
	return writeOK(normalized,
		output.WithSummary("Time tracking started"),
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
		return convertSDKError(err)
	}

	if track == nil {
		return output.ErrNotFound("time track", "active")
	}

	if err = sdk.TimeTracks().Stop(ctx, track.Id); err != nil {
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), "Time tracking stopped.")
		return nil
	}

	return writeOK(nil, output.WithSummary("Time tracking stopped"))
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
		return convertSDKError(err)
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
			fmt.Fprintf(w, "Title:   %s\n", track.Title)
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
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		if len(categories) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No time track categories.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Title"})
		for _, category := range categories {
			table.addRow([]string{fmt.Sprintf("%d", category.Id), category.Title})
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
		return output.ErrUsage("category title is required")
	}
	if err := sdk.TimeTracks().CreateCategory(cmd.Context(), title); err != nil {
		return convertSDKError(err)
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
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				return nil
			}
			if len(args) == 0 {
				return usageErrorf("%s", cleanUseLine(cmd.UseLine()))
			}
			return fmt.Errorf("expected 2 arguments, got %d", len(args))
		},
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
		return output.ErrUsage("category title is required")
	}
	if err := sdk.TimeTracks().UpdateCategory(cmd.Context(), categoryID, title); err != nil {
		return convertSDKError(err)
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
		return convertSDKError(err)
	}
	return writeTimetrackCategoryMutation(cmd, fmt.Sprintf("Time track category %d deleted", categoryID))
}

func writeTimetrackCategoryMutation(cmd *cobra.Command, summary string) error {
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}
	return writeOK(nil,
		output.WithSummary(summary),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "list",
			Command:     "hey timetrack categories",
			Description: "List time track categories",
		}),
	)
}
