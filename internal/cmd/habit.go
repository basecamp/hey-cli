package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	habitvalues "github.com/basecamp/hey-cli/internal/habit"
)

type habitCommand struct {
	cmd *cobra.Command
}

func newHabitCommand() *habitCommand {
	habitCommand := &habitCommand{}
	habitCommand.cmd = &cobra.Command{
		Use:   "habit",
		Short: "Create and manage habits",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: create, edit, delete, complete, uncomplete. Habit IDs are available in calendar recordings. Days accept weekday names or 0 (Sunday) through 6 (Saturday).",
		},
	}

	habitCommand.cmd.AddCommand(newHabitCreateCommand().cmd)
	habitCommand.cmd.AddCommand(newHabitEditCommand().cmd)
	habitCommand.cmd.AddCommand(newHabitDeleteCommand().cmd)
	habitCommand.cmd.AddCommand(newHabitCompleteCommand().cmd)
	habitCommand.cmd.AddCommand(newHabitUncompleteCommand().cmd)

	return habitCommand
}

// create

type habitCreateCommand struct {
	cmd   *cobra.Command
	name  string
	icon  string
	color string
	days  string
}

func newHabitCreateCommand() *habitCreateCommand {
	habitCreateCommand := &habitCreateCommand{}
	habitCreateCommand.cmd = &cobra.Command{
		Use:   "create [name]",
		Short: "Create a habit",
		Example: `  hey habit create "Morning strength training"
  hey habit create --name "Practice piano" --icon music --color green --days monday,wednesday,friday
  echo "Read for thirty minutes" | hey habit create`,
		RunE: habitCreateCommand.run,
		Args: cobra.MaximumNArgs(1),
	}

	habitCreateCommand.cmd.Flags().StringVar(&habitCreateCommand.name, "name", "", "Habit name")
	habitCreateCommand.cmd.Flags().StringVar(&habitCreateCommand.icon, "icon", habitvalues.DefaultIcon, "Habit icon. Accepted values: "+habitvalues.IconValues)
	habitCreateCommand.cmd.Flags().StringVar(&habitCreateCommand.color, "color", habitvalues.DefaultColor, "Habit color. Accepted values: "+habitvalues.ColorValues)
	habitCreateCommand.cmd.Flags().StringVar(&habitCreateCommand.days, "days", habitvalues.FormatDays(habitvalues.EveryDay), "Weekdays (names or 0-6, comma-separated)")

	return habitCreateCommand
}

func (c *habitCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	name := strings.TrimSpace(c.name)
	nameFlagSet := cmd.Flags().Changed("name")
	if nameFlagSet && len(args) > 0 {
		return apierr.ErrUsage("--name and positional argument are mutually exclusive")
	}
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	if name == "" && !nameFlagSet && len(args) == 0 && !stdinIsTerminal() {
		var err error
		name, err = readStdin()
		if err != nil {
			return err
		}
		name = strings.TrimSpace(name)
	}
	if name == "" {
		return apierr.ErrUsageHint("name is required", "hey habit create \"Morning strength training\"")
	}
	icon := strings.TrimSpace(c.icon)
	color := strings.TrimSpace(c.color)
	if icon == "" || color == "" {
		return apierr.ErrUsage("icon and color cannot be empty")
	}
	if err := habitvalues.ValidateIcon(icon); err != nil {
		return apierr.ErrUsage(err.Error())
	}
	if err := habitvalues.ValidateColor(color); err != nil {
		return apierr.ErrUsage(err.Error())
	}
	days, err := habitvalues.ParseDays(c.days)
	if err != nil {
		return apierr.ErrUsage(err.Error())
	}

	recording, err := sdk.Habits().Create(cmd.Context(), hey.HabitParams{Name: name, Icon: icon, Color: color, Days: days})
	if err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd, fmt.Sprintf("Habit %q created.", name), "Habit created", recording)
}

// edit

type habitEditCommand struct {
	cmd   *cobra.Command
	name  string
	icon  string
	color string
	days  string
}

func newHabitEditCommand() *habitEditCommand {
	habitEditCommand := &habitEditCommand{}
	habitEditCommand.cmd = &cobra.Command{
		Use:     "edit <id> [name]",
		Aliases: []string{"update"},
		Short:   "Edit a habit",
		Example: `  hey habit edit 789 --name "Evening walk"
  hey habit edit 789 --days monday,tuesday,wednesday,thursday,friday
  hey habit update 789 --icon walk --color gold`,
		RunE: habitEditCommand.run,
		Args: cobra.RangeArgs(1, 2),
	}

	habitEditCommand.cmd.Flags().StringVar(&habitEditCommand.name, "name", "", "New habit name")
	habitEditCommand.cmd.Flags().StringVar(&habitEditCommand.icon, "icon", "", "New habit icon. Accepted values: "+habitvalues.IconValues)
	habitEditCommand.cmd.Flags().StringVar(&habitEditCommand.color, "color", "", "New habit color. Accepted values: "+habitvalues.ColorValues)
	habitEditCommand.cmd.Flags().StringVar(&habitEditCommand.days, "days", "", "New weekdays (names or 0-6, comma-separated)")

	return habitEditCommand
}

func (c *habitEditCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	id, err := parseHabitID(args[0])
	if err != nil {
		return err
	}
	nameChanged := cmd.Flags().Changed("name") || len(args) == 2
	if cmd.Flags().Changed("name") && len(args) == 2 {
		return apierr.ErrUsage("--name and positional name are mutually exclusive")
	}
	name := strings.TrimSpace(c.name)
	if len(args) == 2 {
		name = strings.TrimSpace(args[1])
	}
	iconChanged := cmd.Flags().Changed("icon")
	colorChanged := cmd.Flags().Changed("color")
	daysChanged := cmd.Flags().Changed("days")
	if !nameChanged && !iconChanged && !colorChanged && !daysChanged {
		return apierr.ErrUsage("provide at least one of name, --icon, --color, or --days")
	}
	if nameChanged && name == "" {
		return apierr.ErrUsage("name cannot be empty")
	}
	icon := strings.TrimSpace(c.icon)
	color := strings.TrimSpace(c.color)
	if iconChanged && icon == "" {
		return apierr.ErrUsage("icon cannot be empty")
	}
	if iconChanged {
		if validationErr := habitvalues.ValidateIcon(icon); validationErr != nil {
			return apierr.ErrUsage(validationErr.Error())
		}
	}
	if colorChanged && color == "" {
		return apierr.ErrUsage("color cannot be empty")
	}
	if colorChanged {
		if validationErr := habitvalues.ValidateColor(color); validationErr != nil {
			return apierr.ErrUsage(validationErr.Error())
		}
	}
	var days []int32
	if daysChanged {
		days, err = habitvalues.ParseDays(c.days)
		if err != nil {
			return apierr.ErrUsage(err.Error())
		}
	}

	recording, err := sdk.Habits().Update(cmd.Context(), id, hey.HabitParams{Name: name, Icon: icon, Color: color, Days: days})
	if err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd, fmt.Sprintf("Habit %d updated.", id), "Habit updated", recording)
}

// delete

type habitDeleteCommand struct {
	cmd *cobra.Command
}

func newHabitDeleteCommand() *habitDeleteCommand {
	habitDeleteCommand := &habitDeleteCommand{}
	habitDeleteCommand.cmd = &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a habit and its history",
		Example: `  hey habit delete 789`,
		RunE:    habitDeleteCommand.run,
		Args:    usageExactOneArg(),
	}
	return habitDeleteCommand
}

func (c *habitDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	id, err := parseHabitID(args[0])
	if err != nil {
		return err
	}
	if err := sdk.Habits().Delete(cmd.Context(), id); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutationLine(cmd, fmt.Sprintf("Habit %d deleted.", id), "Habit deleted", nil)
}

func parseHabitID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, apierr.ErrUsage(fmt.Sprintf("invalid habit ID: %s", value))
	}
	return id, nil
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
		Example: `  hey habit complete 789
  hey habit complete 789 --date 2026-01-15`,
		RunE: habitCompleteCommand.run,
		Args: usageExactOneArg(),
	}

	habitCompleteCommand.cmd.Flags().StringVar(&habitCompleteCommand.date, "date", "", "Date (YYYY-MM-DD, default: today)")

	return habitCompleteCommand
}

func (c *habitCompleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	id, err := parseHabitID(args[0])
	if err != nil {
		return err
	}

	date, err := habitCompletionDate(c.date)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	result, err := sdk.Habits().Complete(ctx, date, id)
	if err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutationLine(cmd,
		fmt.Sprintf("Habit %s completed for %s.%s", args[0], date, extractMutationInfoFromResult(result)),
		fmt.Sprintf("Habit %s completed for %s", args[0], date),
		result)
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
		Example: `  hey habit uncomplete 789
  hey habit uncomplete 789 --date 2026-01-15`,
		RunE: habitUncompleteCommand.run,
		Args: usageExactOneArg(),
	}

	habitUncompleteCommand.cmd.Flags().StringVar(&habitUncompleteCommand.date, "date", "", "Date (YYYY-MM-DD, default: today)")

	return habitUncompleteCommand
}

func (c *habitUncompleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	id, err := parseHabitID(args[0])
	if err != nil {
		return err
	}

	date, err := habitCompletionDate(c.date)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	result, err := sdk.Habits().Uncomplete(ctx, date, id)
	if err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutationLine(cmd,
		fmt.Sprintf("Habit %s uncompleted for %s.%s", args[0], date, extractMutationInfoFromResult(result)),
		fmt.Sprintf("Habit %s uncompleted for %s", args[0], date),
		result)
}

// habitCompletionDate reads the --date flag, defaulting to today. A date the server
// cannot read would otherwise be sent as a URL segment and answered with a 404.
func habitCompletionDate(flag string) (string, error) {
	if flag == "" {
		return time.Now().Format(dateLayout), nil
	}
	if _, err := parseDateArg("date", flag); err != nil {
		return "", err
	}
	return flag, nil
}
