package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"hey-cli/internal/models"
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
		RunE:  timetrackStartCommand.run,
	}

	return timetrackStartCommand
}

func (c *timetrackStartCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	data, err := apiClient.PostJSON("/calendar/ongoing_time_track.json", nil)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Println("Time tracking started.")
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
		RunE:  timetrackStopCommand.run,
	}

	return timetrackStopCommand
}

func (c *timetrackStopCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	data, err := apiClient.Get("/calendar/ongoing_time_track.json")
	if err != nil {
		return fmt.Errorf("could not get current time track: %w", err)
	}

	var track models.TimeTrack
	if err := json.Unmarshal(data, &track); err != nil {
		return fmt.Errorf("could not parse time track: %w", err)
	}

	if track.ID == 0 {
		return fmt.Errorf("no active time track")
	}

	path := fmt.Sprintf("/calendar/time_tracks/%d.json", track.ID)
	result, err := apiClient.PutJSON(path, map[string]interface{}{"stopped": true})
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(result)
	}

	fmt.Println("Time tracking stopped.")
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
		RunE:  timetrackCurrentCommand.run,
	}

	return timetrackCurrentCommand
}

func (c *timetrackCurrentCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	data, err := apiClient.Get("/calendar/ongoing_time_track.json")
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	var track models.TimeTrack
	if err := json.Unmarshal(data, &track); err != nil {
		return fmt.Errorf("could not parse time track: %w", err)
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
		RunE:  timetrackListCommand.run,
	}

	return timetrackListCommand
}

func (c *timetrackListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if jsonOutput {
		data, err := apiClient.Get("/calendar/time_tracks.json")
		if err != nil {
			return err
		}
		return printRawJSON(data)
	}

	var tracks []models.TimeTrack
	if err := apiClient.GetJSON("/calendar/time_tracks.json", &tracks); err != nil {
		return err
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
