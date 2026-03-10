package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

type journalCommand struct {
	cmd *cobra.Command
}

func newJournalCommand() *journalCommand {
	journalCommand := &journalCommand{}
	journalCommand.cmd = &cobra.Command{
		Use:   "journal",
		Short: "Manage journal entries",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: list, read, write. Read defaults to today. Write accepts --content, stdin, or opens $EDITOR.",
		},
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
	all   bool
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
	journalListCommand.cmd.Flags().BoolVar(&journalListCommand.all, "all", false, "Fetch all results (override --limit)")

	return journalListCommand
}

func (c *journalListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	resp, err := listPersonalRecordings(ctx)
	if err != nil {
		return err
	}

	entries := filterRecordingsByType(resp, "Calendar::JournalEntry")

	total := len(entries)
	if c.limit > 0 && !c.all && len(entries) > c.limit {
		entries = entries[:c.limit]
	}
	notice := output.TruncationNotice(len(entries), total)

	if writer.IsStyled() {
		if len(entries) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No journal entries.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Date", "Preview"})
		for _, e := range entries {
			table.addRow([]string{fmt.Sprintf("%d", e.Id), formatDate(e.StartsAt), truncate(e.Content, 60)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(entries,
		output.WithSummary(fmt.Sprintf("%d journal entries", len(entries))),
		output.WithNotice(notice),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "read",
				Command:     "hey journal read [date]",
				Description: "Read a journal entry",
			},
			output.Breadcrumb{
				Action:      "write",
				Command:     "hey journal write '...'",
				Description: "Write a journal entry",
			},
		),
	)
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
  hey journal read --html
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

	ctx := cmd.Context()
	entry, err := sdk.Journal().Get(ctx, date)
	if err != nil {
		return convertSDKError(err)
	}

	// SDK returns nil when the server responds 204 (no JSON body).
	// Fall back to the legacy HTML-scrape path which parses the edit page.
	content := ""
	if entry != nil {
		content = entry.Content
	}
	if content == "" && apiClient != nil {
		legacy, legacyErr := apiClient.GetJournalEntry(date)
		if legacyErr == nil && legacy.Body != "" {
			content = legacy.Body
		}
	}

	if content == "" {
		if writer.IsStyled() {
			fmt.Fprintf(cmd.OutOrStdout(), "Journal — %s\n\n(empty)\n", date)
			return nil
		}
		return writeOK(nil, output.WithSummary(fmt.Sprintf("No journal entry for %s", date)))
	}

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		if htmlOutput {
			fmt.Fprintln(w, content)
			return nil
		}

		fmt.Fprintf(w, "Journal — %s\n\n", date)
		fmt.Fprintln(w, htmlutil.ToText(content))
		return nil
	}

	// If SDK returned a full entry, use it; otherwise wrap the scraped content.
	if entry != nil {
		return writeOK(entry,
			output.WithSummary(fmt.Sprintf("Journal entry for %s", date)),
			output.WithBreadcrumbs(output.Breadcrumb{
				Action:      "write",
				Command:     fmt.Sprintf("hey journal write %s '...'", date),
				Description: "Edit this journal entry",
			}),
		)
	}
	return writeOK(map[string]string{"date": date, "content": content},
		output.WithSummary(fmt.Sprintf("Journal entry for %s", date)),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "write",
			Command:     fmt.Sprintf("hey journal write %s '...'", date),
			Description: "Edit this journal entry",
		}),
	)
}

// write

type journalWriteCommand struct {
	cmd     *cobra.Command
	content string
}

func newJournalWriteCommand() *journalWriteCommand {
	journalWriteCommand := &journalWriteCommand{}
	journalWriteCommand.cmd = &cobra.Command{
		Use:   "write [date] [content]",
		Short: "Write or edit a journal entry (default: today)",
		Example: `  hey journal write "Today was great"
  hey journal write 2024-01-15 "Retrospective"
  hey journal write -c "Today was great"
  echo "Journal content" | hey journal write`,
		RunE: journalWriteCommand.run,
		Args: cobra.MaximumNArgs(2),
	}

	journalWriteCommand.cmd.Flags().StringVarP(&journalWriteCommand.content, "content", "c", "", "Journal content (or opens $EDITOR)")

	return journalWriteCommand
}

func (c *journalWriteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	date := time.Now().Format("2006-01-02")
	content := c.content

	switch len(args) {
	case 2:
		if content != "" {
			return output.ErrUsage("--content and positional content are mutually exclusive")
		}
		if !isDateArg(args[0]) {
			return output.ErrUsageHint(
				"first argument must be a date (YYYY-MM-DD) when two positional arguments are given",
				"hey journal write 2024-01-15 \"Content\"  or  hey journal write \"Content\"")
		}
		date = args[0]
		content = args[1]
	case 1:
		if isDateArg(args[0]) {
			date = args[0]
		} else {
			if content != "" {
				return output.ErrUsage("--content and positional content are mutually exclusive")
			}
			content = args[0]
		}
	}
	if content == "" {
		if !stdinIsTerminal() {
			var err error
			content, err = readStdin()
			if err != nil {
				return err
			}
			if content == "" {
				return output.ErrUsage("no content provided (use --content to provide inline, or pipe to stdin)")
			}
		} else {
			ctx := cmd.Context()
			existing := ""
			entry, err := sdk.Journal().Get(ctx, date)
			if err == nil && entry != nil {
				existing = entry.Content
			}
			if existing == "" && apiClient != nil {
				legacy, legacyErr := apiClient.GetJournalEntry(date)
				if legacyErr == nil {
					existing = legacy.Body
				}
			}

			content, err = editor.Open(existing)
			if err != nil {
				return output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
			}
		}
	}

	ctx := cmd.Context()
	result, err := sdk.Journal().Update(ctx, date, generated.UpdateJournalEntryJSONRequestBody{
		Body: content,
	})
	if err != nil {
		return convertSDKError(err)
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Journal entry for %s saved.%s\n", date, extractMutationInfoFromResult(result))
		return nil
	}

	normalized, nerr := normalizeAny(result)
	if nerr != nil {
		return writeOK(nil, output.WithSummary(fmt.Sprintf("Journal entry for %s saved", date)))
	}
	return writeOK(normalized,
		output.WithSummary(fmt.Sprintf("Journal entry for %s saved", date)),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "read",
			Command:     fmt.Sprintf("hey journal read %s", date),
			Description: "Read the journal entry",
		}),
	)
}
