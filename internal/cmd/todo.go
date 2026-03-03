package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hey-cli/internal/models"
)

type todoCommand struct {
	cmd *cobra.Command
}

func newTodoCommand() *todoCommand {
	todoCommand := &todoCommand{}
	todoCommand.cmd = &cobra.Command{
		Use:   "todo",
		Short: "Manage todos",
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

	return todoListCommand
}

func (c *todoListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if jsonOutput {
		data, err := apiClient.Get("/calendar/todos.json")
		if err != nil {
			return err
		}
		return printRawJSON(limitJSONArray(data, c.limit))
	}

	var todos []models.Todo
	if err := apiClient.GetJSON("/calendar/todos.json", &todos); err != nil {
		return err
	}

	if len(todos) == 0 {
		fmt.Println("No todos.")
		return nil
	}

	if c.limit > 0 && len(todos) > c.limit {
		todos = todos[:c.limit]
	}

	table := newTable()
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
	return nil
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
		Use:   "add",
		Short: "Create a new todo",
		Example: `  hey todo add --title "Buy groceries"
  hey todo add --title "Meeting prep" --date 2024-01-20
  hey todo add --title "Review PR" --json`,
		RunE: todoAddCommand.run,
	}

	todoAddCommand.cmd.Flags().StringVar(&todoAddCommand.title, "title", "", "Todo title (required)")
	todoAddCommand.cmd.Flags().StringVar(&todoAddCommand.date, "date", "", "Due date (YYYY-MM-DD)")

	return todoAddCommand
}

func (c *todoAddCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if c.title == "" {
		return fmt.Errorf("--title is required")
	}

	body := map[string]interface{}{"title": c.title}
	if c.date != "" {
		body["starts_at"] = c.date
	}

	data, err := apiClient.PostJSON("/calendar/todos.json", body)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Todo created.%s\n", extractMutationInfo(data))
	return nil
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

	path := fmt.Sprintf("/calendar/todos/%s/completions.json", args[0])
	data, err := apiClient.PostJSON(path, nil)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Todo completed.%s\n", extractMutationInfo(data))
	return nil
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

	path := fmt.Sprintf("/calendar/todos/%s/completions.json", args[0])
	data, err := apiClient.Delete(path)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Todo marked incomplete.%s\n", extractMutationInfo(data))
	return nil
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

	path := fmt.Sprintf("/calendar/todos/%s.json", args[0])
	data, err := apiClient.Delete(path)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Todo deleted.%s\n", extractMutationInfo(data))
	return nil
}
