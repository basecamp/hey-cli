package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type todoCommand struct {
	cmd *cobra.Command
}

func newTodoCommand() *todoCommand {
	todoCommand := &todoCommand{}
	todoCommand.cmd = &cobra.Command{
		Use:   "todo",
		Short: "Manage todos",
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
	cmd   *cobra.Command
	limit int
	all   bool
}

func newTodoListCommand() *todoListCommand {
	todoListCommand := &todoListCommand{}
	todoListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List todos",
		Example: `  hey todo list
  hey todo list --limit 10
  hey todo list --json`,
		RunE: todoListCommand.run,
	}

	todoListCommand.cmd.Flags().IntVar(&todoListCommand.limit, "limit", 0, "Maximum number of todos to show")
	todoListCommand.cmd.Flags().BoolVar(&todoListCommand.all, "all", false, "Fetch all results (override --limit)")

	return todoListCommand
}

func (c *todoListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	todos, err := apiClient.ListTodos()
	if err != nil {
		return err
	}

	total := len(todos)
	if c.limit > 0 && !c.all && len(todos) > c.limit {
		todos = todos[:c.limit]
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
			date := ""
			if len(t.StartsAt) >= 10 {
				date = t.StartsAt[:10]
			}
			done := ""
			if t.CompletedAt != "" {
				done = "yes"
			}
			table.addRow([]string{fmt.Sprintf("%d", t.ID), t.Title, date, done})
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
  hey todo add -t "Meeting prep" --date 2024-01-20
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

	title := c.title
	if title != "" && len(args) > 0 {
		return output.ErrUsage("--title and positional argument are mutually exclusive")
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
		return output.ErrUsageHint("title is required",
			"hey todo add \"Buy milk\"  or  hey todo add --title \"Buy milk\"")
	}

	body := map[string]any{"title": title}
	if c.date != "" {
		body["starts_at"] = c.date
	}

	data, err := apiClient.CreateTodo(body)
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Todo created.%s\n", extractMutationInfo(data))
		return nil
	}

	normalized, err := output.NormalizeJSONNumbers(data)
	if err != nil {
		return writeOK(nil, output.WithSummary("Todo created"))
	}
	return writeOK(normalized, output.WithSummary("Todo created"))
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
		Args:    cobra.ExactArgs(1),
	}

	return todoCompleteCommand
}

func (c *todoCompleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	data, err := apiClient.CompleteTodo(args[0])
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Todo completed.%s\n", extractMutationInfo(data))
		return nil
	}

	normalized, err := output.NormalizeJSONNumbers(data)
	if err != nil {
		return writeOK(nil, output.WithSummary("Todo completed"))
	}
	return writeOK(normalized, output.WithSummary("Todo completed"))
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
		Args:    cobra.ExactArgs(1),
	}

	return todoUncompleteCommand
}

func (c *todoUncompleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	data, err := apiClient.UncompleteTodo(args[0])
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Todo marked incomplete.%s\n", extractMutationInfo(data))
		return nil
	}

	normalized, err := output.NormalizeJSONNumbers(data)
	if err != nil {
		return writeOK(nil, output.WithSummary("Todo marked incomplete"))
	}
	return writeOK(normalized, output.WithSummary("Todo marked incomplete"))
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
		Args:    cobra.ExactArgs(1),
	}

	return todoDeleteCommand
}

func (c *todoDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	data, err := apiClient.DeleteTodo(args[0])
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Todo deleted.%s\n", extractMutationInfo(data))
		return nil
	}

	normalized, err := output.NormalizeJSONNumbers(data)
	if err != nil {
		return writeOK(nil, output.WithSummary("Todo deleted"))
	}
	return writeOK(normalized, output.WithSummary("Todo deleted"))
}
