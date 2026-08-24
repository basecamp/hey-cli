package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type todoCommand struct {
	cmd *cobra.Command
}

func newTodoCommand() *todoCommand {
	todoCommand := &todoCommand{}
	todoCommand.cmd = &cobra.Command{
		Use:   "todo",
		Short: "Create and manage to-dos",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: list, add, complete, uncomplete, delete. Use list --ids-only to pipe IDs to complete/delete.",
		},
	}

	todoCommand.cmd.AddCommand(newTodoListCommand().cmd)
	todoCommand.cmd.AddCommand(newTodoAddCommand().cmd)
	todoCommand.cmd.AddCommand(newTodoCompleteCommand().cmd)
	todoCommand.cmd.AddCommand(newTodoUncompleteCommand().cmd)
	todoCommand.cmd.AddCommand(newTodoDeleteCommand().cmd)

	return todoCommand
}

// list

type todoListCommand struct {
	cmd    *cobra.Command
	filter recordingFilter
}

func newTodoListCommand() *todoListCommand {
	todoListCommand := &todoListCommand{
		filter: recordingFilter{defaultWindow: personalWindow, defaultCalendars: personalCalendarIDs},
	}
	todoListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List todos",
		Example: `  hey todo list
  hey todo list --limit 10
  hey todo list --starts-on 2026-01-01 --ends-on 2026-01-31 --json`,
		RunE: todoListCommand.run,
		Args: cobra.NoArgs,
	}

	todoListCommand.filter.registerFlags(todoListCommand.cmd, "todos", "Calendar ID to read (defaults to the personal calendar)")

	return todoListCommand
}

func (c *todoListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	window, err := c.filter.resolve(ctx)
	if err != nil {
		return err
	}

	todos, err := window.read(ctx, "Calendar::Todo")
	if err != nil {
		return err
	}

	total := len(todos)
	if c.filter.limit > 0 && !c.filter.all && len(todos) > c.filter.limit {
		todos = todos[:c.filter.limit]
	}
	notice := output.TruncationNotice(len(todos), total)

	if writer.IsStyled() {
		if len(todos) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No todos.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Title", "Date", "Done"})
		for _, t := range todos {
			done := ""
			if !t.CompletedAt.IsZero() {
				done = "yes"
			}
			table.addRow([]string{fmt.Sprintf("%d", t.Id), t.Title, formatDate(t.StartsAt), done})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(todos,
		output.WithSummary(fmt.Sprintf("%d todos", len(todos))),
		output.WithNotice(notice),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "add",
				Command:     "hey todo add '...'",
				Description: "Add a new todo",
			},
			output.Breadcrumb{
				Action:      "complete",
				Command:     "hey todo complete <id>",
				Description: "Mark a todo as complete",
			},
		),
	)
}

// add

type todoAddCommand struct {
	cmd   *cobra.Command
	title string
	date  string
}

func newTodoAddCommand() *todoAddCommand {
	todoAddCommand := &todoAddCommand{}
	todoAddCommand.cmd = &cobra.Command{
		Use:   "add [title]",
		Short: "Create a new todo",
		Example: `  hey todo add "Buy groceries"
  hey todo add -t "Meeting prep" --date 2026-01-20
  hey todo add --title "Review PR" --json
  echo "Buy milk" | hey todo add`,
		RunE: todoAddCommand.run,
		Args: cobra.MaximumNArgs(1),
	}

	todoAddCommand.cmd.Flags().StringVarP(&todoAddCommand.title, "title", "t", "", "Todo title")
	todoAddCommand.cmd.Flags().StringVar(&todoAddCommand.date, "date", "", "Due date (YYYY-MM-DD)")

	return todoAddCommand
}

func (c *todoAddCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if c.date != "" {
		if _, err := parseDateArg("date", c.date); err != nil {
			return err
		}
	}

	title := c.title
	if title != "" && len(args) > 0 {
		return apierr.ErrUsage("--title and positional argument are mutually exclusive")
	}
	if title == "" && len(args) > 0 {
		title = args[0]
	}
	if title == "" && !stdinIsTerminal() {
		var err error
		title, err = readStdin()
		if err != nil {
			return err
		}
	}
	if title == "" {
		return apierr.ErrUsageHint("title is required",
			"hey todo add \"Buy milk\"  or  hey todo add --title \"Buy milk\"")
	}

	ctx := cmd.Context()
	result, err := sdk.CalendarTodos().Create(ctx, title, c.date)
	if err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, "Todo created", result)
}

// complete

type todoCompleteCommand struct {
	cmd *cobra.Command
}

func newTodoCompleteCommand() *todoCompleteCommand {
	todoCompleteCommand := &todoCompleteCommand{}
	todoCompleteCommand.cmd = &cobra.Command{
		Use:     "complete <id>",
		Short:   "Mark a todo as complete",
		Example: `  hey todo complete 456`,
		RunE:    todoCompleteCommand.run,
		Args:    usageExactOneArg(),
	}

	return todoCompleteCommand
}

func (c *todoCompleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	id, err := parsePositiveID(args[0], "todo")
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	result, err := sdk.CalendarTodos().Complete(ctx, id)
	if err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutationLine(cmd,
		fmt.Sprintf("Todo completed.%s", extractMutationInfoFromResult(result)),
		"Todo completed",
		result)
}

// uncomplete

type todoUncompleteCommand struct {
	cmd *cobra.Command
}

func newTodoUncompleteCommand() *todoUncompleteCommand {
	todoUncompleteCommand := &todoUncompleteCommand{}
	todoUncompleteCommand.cmd = &cobra.Command{
		Use:     "uncomplete <id>",
		Short:   "Mark a todo as incomplete",
		Example: `  hey todo uncomplete 456`,
		RunE:    todoUncompleteCommand.run,
		Args:    usageExactOneArg(),
	}

	return todoUncompleteCommand
}

func (c *todoUncompleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	id, err := parsePositiveID(args[0], "todo")
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	result, err := sdk.CalendarTodos().Uncomplete(ctx, id)
	if err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutationLine(cmd,
		fmt.Sprintf("Todo marked incomplete.%s", extractMutationInfoFromResult(result)),
		"Todo marked incomplete",
		result)
}

// delete

type todoDeleteCommand struct {
	cmd *cobra.Command
}

func newTodoDeleteCommand() *todoDeleteCommand {
	todoDeleteCommand := &todoDeleteCommand{}
	todoDeleteCommand.cmd = &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a todo",
		Example: `  hey todo delete 456`,
		RunE:    todoDeleteCommand.run,
		Args:    usageExactOneArg(),
	}

	return todoDeleteCommand
}

func (c *todoDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	id, err := parsePositiveID(args[0], "todo")
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	if err := sdk.CalendarTodos().Delete(ctx, id); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, "Todo deleted", nil)
}
