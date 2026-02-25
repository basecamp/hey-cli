package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

type habitCommand struct {
	cmd *cobra.Command
}

func newHabitCommand() *habitCommand {
	habitCommand := &habitCommand{}
	habitCommand.cmd = &cobra.Command{
		Use:   "habit",
		Short: "Manage habit completions",
	}

	habitCommand.cmd.AddCommand(newHabitCompleteCommand().cmd)
	habitCommand.cmd.AddCommand(newHabitUncompleteCommand().cmd)

	return habitCommand
}

// complete

type habitCompleteCommand struct {
	cmd  *cobra.Command
	date string
}

func newHabitCompleteCommand() *habitCompleteCommand {
	habitCompleteCommand := &habitCompleteCommand{}
	habitCompleteCommand.cmd = &cobra.Command{
		Use:   "complete <id>",
		Short: "Mark a habit as complete for a date",
		RunE:  habitCompleteCommand.run,
		Args:  cobra.ExactArgs(1),
	}

	habitCompleteCommand.cmd.Flags().StringVar(&habitCompleteCommand.date, "date", "", "Date (YYYY-MM-DD, default: today)")

	return habitCompleteCommand
}

func (c *habitCompleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	date := c.date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	path := fmt.Sprintf("/calendar/days/%s/habits/%s/completions.json", date, args[0])
	data, err := apiClient.PostJSON(path, nil)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Habit %s completed for %s.\n", args[0], date)
	return nil
}

// uncomplete

type habitUncompleteCommand struct {
	cmd  *cobra.Command
	date string
}

func newHabitUncompleteCommand() *habitUncompleteCommand {
	habitUncompleteCommand := &habitUncompleteCommand{}
	habitUncompleteCommand.cmd = &cobra.Command{
		Use:   "uncomplete <id>",
		Short: "Remove a habit completion for a date",
		RunE:  habitUncompleteCommand.run,
		Args:  cobra.ExactArgs(1),
	}

	habitUncompleteCommand.cmd.Flags().StringVar(&habitUncompleteCommand.date, "date", "", "Date (YYYY-MM-DD, default: today)")

	return habitUncompleteCommand
}

func (c *habitUncompleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	date := c.date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	path := fmt.Sprintf("/calendar/days/%s/habits/%s/completions.json", date, args[0])
	data, err := apiClient.Delete(path)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Habit %s uncompleted for %s.\n", args[0], date)
	return nil
}
