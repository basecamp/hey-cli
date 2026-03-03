package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"hey-cli/internal/editor"
	"hey-cli/internal/models"
)

type journalCommand struct {
	cmd *cobra.Command
}

func newJournalCommand() *journalCommand {
	journalCommand := &journalCommand{}
	journalCommand.cmd = &cobra.Command{
		Use:   "journal",
		Short: "Manage journal entries",
	}

	journalCommand.cmd.AddCommand(newJournalListCommand().cmd)
	journalCommand.cmd.AddCommand(newJournalReadCommand().cmd)
	journalCommand.cmd.AddCommand(newJournalWriteCommand().cmd)

	return journalCommand
}

// list

type journalListCommand struct {
	cmd   *cobra.Command
	limit int
}

func newJournalListCommand() *journalListCommand {
	journalListCommand := &journalListCommand{}
	journalListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List journal entries",
		Example: `  hey journal list
  hey journal list --limit 10
  hey journal list --json`,
		RunE: journalListCommand.run,
	}

	journalListCommand.cmd.Flags().IntVar(&journalListCommand.limit, "limit", 0, "Maximum number of entries to show")

	return journalListCommand
}

func (c *journalListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	if jsonOutput {
		data, err := apiClient.Get("/calendar/journal_entries.json")
		if err != nil {
			return err
		}
		return printRawJSON(limitJSONArray(data, c.limit))
	}

	var entries []models.JournalEntry
	if err := apiClient.GetJSON("/calendar/journal_entries.json", &entries); err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Println("No journal entries.")
		return nil
	}

	if c.limit > 0 && len(entries) > c.limit {
		entries = entries[:c.limit]
	}

	table := newTable()
	table.addRow([]string{"ID", "Date", "Preview"})
	for _, e := range entries {
		table.addRow([]string{fmt.Sprintf("%d", e.ID), e.Date, truncate(e.Body, 60)})
	}
	table.print()
	return nil
}

// read

type journalReadCommand struct {
	cmd *cobra.Command
}

func newJournalReadCommand() *journalReadCommand {
	journalReadCommand := &journalReadCommand{}
	journalReadCommand.cmd = &cobra.Command{
		Use:   "read [date]",
		Short: "Read a journal entry (default: today)",
		Example: `  hey journal read
  hey journal read 2024-01-15
  hey journal read --json`,
		RunE: journalReadCommand.run,
		Args: cobra.MaximumNArgs(1),
	}

	return journalReadCommand
}

func (c *journalReadCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	date := time.Now().Format("2006-01-02")
	if len(args) > 0 {
		date = args[0]
	}

	path := fmt.Sprintf("/calendar/days/%s/journal_entry.json", date)

	if jsonOutput {
		data, err := apiClient.Get(path)
		if err != nil {
			return err
		}
		return printRawJSON(data)
	}

	var entry models.JournalEntry
	if err := apiClient.GetJSON(path, &entry); err != nil {
		return err
	}

	fmt.Printf("Journal — %s\n\n", date)
	if entry.Body != "" {
		fmt.Println(entry.Body)
	} else {
		fmt.Println("(empty)")
	}
	return nil
}

// write

type journalWriteCommand struct {
	cmd     *cobra.Command
	content string
}

func newJournalWriteCommand() *journalWriteCommand {
	journalWriteCommand := &journalWriteCommand{}
	journalWriteCommand.cmd = &cobra.Command{
		Use:   "write [date]",
		Short: "Write or edit a journal entry (default: today)",
		Example: `  hey journal write --content "Today was great"
  hey journal write 2024-01-15 --content "Retrospective"
  echo "Journal content" | hey journal write`,
		RunE: journalWriteCommand.run,
		Args: cobra.MaximumNArgs(1),
	}

	journalWriteCommand.cmd.Flags().StringVar(&journalWriteCommand.content, "content", "", "Journal content (or opens $EDITOR)")

	return journalWriteCommand
}

func (c *journalWriteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	date := time.Now().Format("2006-01-02")
	if len(args) > 0 {
		date = args[0]
	}

	content := c.content
	if content == "" {
		if !stdinIsTerminal() {
			var err error
			content, err = readStdin()
			if err != nil {
				return err
			}
			if content == "" {
				return fmt.Errorf("no content provided (use --content to provide inline, or pipe to stdin)")
			}
		} else {
			existing := ""
			path := fmt.Sprintf("/calendar/days/%s/journal_entry.json", date)
			var entry models.JournalEntry
			if err := apiClient.GetJSON(path, &entry); err == nil {
				existing = entry.Body
			}

			var err error
			content, err = editor.Open(existing)
			if err != nil {
				return fmt.Errorf("could not open editor: %w", err)
			}
		}
	}

	path := fmt.Sprintf("/calendar/days/%s/journal_entry.json", date)
	body := map[string]interface{}{"body": content}

	data, err := apiClient.PatchJSON(path, body)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printRawJSON(data)
	}

	fmt.Printf("Journal entry for %s saved.%s\n", date, extractMutationInfo(data))
	return nil
}
