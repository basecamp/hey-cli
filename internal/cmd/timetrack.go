package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type timetrackCommand struct {
	cmd *cobra.Command
}

func newTimetrackCommand() *timetrackCommand {
	timetrackCommand := &timetrackCommand{}
	timetrackCommand.cmd = &cobra.Command{
		Use:   "timetrack",
		Short: "Manage time tracking",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: start, stop, current, list. Use current to check if tracking is active before start/stop.",
		},
	}

	timetrackCommand.cmd.AddCommand(newTimetrackStartCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackStopCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackCurrentCommand().cmd)
	timetrackCommand.cmd.AddCommand(newTimetrackListCommand().cmd)

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
	result, err := sdk.TimeTracks().Start(ctx, generated.StartTimeTrackJSONRequestBody{})
	if err != nil {
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Time tracking started.%s\n", extractMutationInfoFromResult(result))
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

	result, err := sdk.TimeTracks().Stop(ctx, track.Id)
	if err != nil {
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Time tracking stopped.%s\n", extractMutationInfoFromResult(result))
		return nil
	}

	normalized, nerr := normalizeAny(result)
	if nerr != nil {
		return writeOK(nil, output.WithSummary("Time tracking stopped"))
	}
	return writeOK(normalized, output.WithSummary("Time tracking stopped"))
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
