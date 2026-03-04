package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type timetrackCommand struct {
	cmd *cobra.Command
}

func newTimetrackCommand() *timetrackCommand {
	timetrackCommand := &timetrackCommand{}
	timetrackCommand.cmd = &cobra.Command{
		Use:   "timetrack",
		Short: "Manage time tracking",
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

	data, err := apiClient.StartTimeTrack(nil)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Time tracking started.%s\n", extractMutationInfo(data))
	return nil
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

	track, err := apiClient.GetOngoingTimeTrack()
	if err != nil {
		return fmt.Errorf("could not get current time track: %w", err)
	}

	if track.ID == 0 {
		return fmt.Errorf("no active time track")
	}

	result, err := apiClient.StopTimeTrack(track.ID)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(result)
	}

	fmt.Printf("Time tracking stopped.%s\n", extractMutationInfo(result))
	return nil
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

	track, err := apiClient.GetOngoingTimeTrack()
	if err != nil {
		return err
	}

	if jsonOutput {
		return printJSON(track)
	}

	if track.ID == 0 {
		fmt.Println("No active time track.")
		return nil
	}

	starts := ""
	if len(track.StartsAt) >= 16 {
		starts = track.StartsAt[:16]
	}
	fmt.Printf("Active time track #%d\n", track.ID)
	fmt.Printf("Started: %s\n", starts)
	if track.Title != "" {
		fmt.Printf("Title:   %s\n", track.Title)
	}
	return nil
}

// list

type timetrackListCommand struct {
	cmd *cobra.Command
}

func newTimetrackListCommand() *timetrackListCommand {
	timetrackListCommand := &timetrackListCommand{}
	timetrackListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List time tracks",
		Example: `  hey timetrack list
  hey timetrack list --json`,
		RunE: timetrackListCommand.run,
	}

	return timetrackListCommand
}

func (c *timetrackListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	tracks, err := apiClient.ListTimeTracks()
	if err != nil {
		return err
	}

	if jsonOutput {
		return printJSON(tracks)
	}

	if len(tracks) == 0 {
		fmt.Println("No time tracks.")
		return nil
	}

	table := newTable()
	table.addRow([]string{"ID", "Title", "Start", "End"})
	for _, t := range tracks {
		starts := ""
		if len(t.StartsAt) >= 16 {
			starts = t.StartsAt[:16]
		}
		ends := ""
		if len(t.EndsAt) >= 16 {
			ends = t.EndsAt[:16]
		}
		table.addRow([]string{fmt.Sprintf("%d", t.ID), t.Title, starts, ends})
	}
	table.print()
	return nil
}
