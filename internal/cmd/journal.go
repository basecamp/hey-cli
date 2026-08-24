package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/output"
)

type journalCommand struct {
	cmd *cobra.Command
}

func newJournalCommand() *journalCommand {
	journalCommand := &journalCommand{}
	journalCommand.cmd = &cobra.Command{
		Use:   "journal",
		Short: "Read and write journal entries",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: list, read, write. Read defaults to today. Write accepts --content, stdin, or opens $EDITOR; content is Markdown, or raw HTML via --content-html.",
		},
	}

	journalCommand.cmd.AddCommand(newJournalListCommand().cmd)
	journalCommand.cmd.AddCommand(newJournalReadCommand().cmd)
	journalCommand.cmd.AddCommand(newJournalWriteCommand().cmd)

	return journalCommand
}

// list

type journalListCommand struct {
	cmd    *cobra.Command
	filter recordingFilter
}

func newJournalListCommand() *journalListCommand {
	journalListCommand := &journalListCommand{
		filter: recordingFilter{defaultWindow: personalWindow, defaultCalendars: personalCalendarIDs},
	}
	journalListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List journal entries",
		Example: `  hey journal list
  hey journal list --limit 10
  hey journal list --starts-on 2026-01-01 --ends-on 2026-01-31 --json`,
		RunE: journalListCommand.run,
		Args: cobra.NoArgs,
	}

	journalListCommand.filter.registerFlags(journalListCommand.cmd, "entries", "Calendar ID to read (defaults to the personal calendar)")

	return journalListCommand
}

func (c *journalListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	window, err := c.filter.resolve(ctx)
	if err != nil {
		return err
	}

	entries, err := window.read(ctx, "Calendar::JournalEntry")
	if err != nil {
		return err
	}

	total := len(entries)
	if c.filter.limit > 0 && !c.filter.all && len(entries) > c.filter.limit {
		entries = entries[:c.filter.limit]
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
  hey journal read 2026-03-15
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

	date := time.Now().Format(dateLayout)
	if len(args) > 0 {
		if _, err := parseDateArg("date", args[0]); err != nil {
			return err
		}
		date = args[0]
	}

	ctx := cmd.Context()
	content, err := sdk.Journal().GetContent(ctx, date)
	if err != nil {
		return apierr.FromSDK(err)
	}

	// --html writes the entry's HTML and, for a day without one, nothing at all.
	if writer.EffectiveFormat() == output.FormatHTML {
		if content == "" {
			return nil
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), content)
		return err
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
		fmt.Fprintf(w, "Journal — %s\n\n", date)
		fmt.Fprintln(w, markdown.Render(htmlutil.ToMarkdown(content), stdoutWidth()))
		return nil
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
	cmd         *cobra.Command
	content     string
	contentHTML string
}

func newJournalWriteCommand() *journalWriteCommand {
	journalWriteCommand := &journalWriteCommand{}
	journalWriteCommand.cmd = &cobra.Command{
		Use:   "write [date] [content]",
		Short: "Write or edit a journal entry (default: today)",
		Long: `Write or edit a journal entry, today's by default.

Writing empty content removes the day's entry, and the command says "removed" rather than
"saved". Omitting content opens $EDITOR on the day's existing entry; if that entry cannot
be read the command stops rather than opening a blank buffer over it.`,
		Example: `  hey journal write "Shipped the pagination fix and paired with Jane on the cover art."
  hey journal write 2026-03-15 "Retrospective: the migration took two days longer than planned."
  hey journal write -c "Reviewed the Q3 numbers with Alice."
  echo "Notes from the offsite" | hey journal write`,
		RunE: journalWriteCommand.run,
		Args: cobra.MaximumNArgs(2),
	}

	journalWriteCommand.cmd.Flags().StringVarP(&journalWriteCommand.content, "content", "c", "", "Journal content as Markdown (or opens $EDITOR)")
	journalWriteCommand.cmd.Flags().StringVar(&journalWriteCommand.contentHTML, "content-html", "", "Journal content as raw HTML instead of Markdown")
	journalWriteCommand.cmd.MarkFlagsMutuallyExclusive("content", "content-html")

	return journalWriteCommand
}

func (c *journalWriteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	date := ""
	content := c.content

	switch len(args) {
	case 2:
		if content != "" {
			return apierr.ErrUsage("--content and positional content are mutually exclusive")
		}
		if c.contentHTML != "" {
			return apierr.ErrUsage("--content-html and positional content are mutually exclusive")
		}
		if !isDateArg(args[0]) {
			return apierr.ErrUsageHint(
				"first argument must be a date (YYYY-MM-DD) when two positional arguments are given",
				"hey journal write 2026-03-15 \"Retrospective\"  or  hey journal write \"Retrospective\"")
		}
		date = args[0]
		content = args[1]
	case 1:
		if isDateArg(args[0]) {
			date = args[0]
		} else {
			if content != "" {
				return apierr.ErrUsage("--content and positional content are mutually exclusive")
			}
			if c.contentHTML != "" {
				return apierr.ErrUsage("--content-html and positional content are mutually exclusive")
			}
			content = args[0]
		}
	}
	ctx := cmd.Context()

	if date == "" {
		date = time.Now().Format(dateLayout)
	}

	if c.contentHTML != "" {
		content = strings.TrimSpace(c.contentHTML)
	} else {
		if content == "" && !stdinIsTerminal() {
			piped, err := readStdin()
			if err != nil {
				return err
			}
			if piped == "" {
				return apierr.ErrUsage("no content provided (use --content to provide inline, or pipe to stdin)")
			}
			content = piped
		}
		if content == "" {
			var err error
			content, err = journalEntryFromEditor(ctx, date, sdk.Journal().GetContent, editor.Open)
			if err != nil {
				return err
			}
		}
		content = strings.TrimSpace(content)
		if content != "" {
			content = htmlutil.FromMarkdown(content)
		}
	}

	if _, err := sdk.Journal().Update(ctx, date, content); err != nil {
		return apierr.FromSDK(err)
	}

	verb := "saved"
	if content == "" {
		verb = "removed"
	}

	return writeMutation(cmd, fmt.Sprintf("Journal entry for %s %s", date, verb), nil,
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "read",
			Command:     fmt.Sprintf("hey journal read %s", date),
			Description: "Read the journal entry",
		}),
	)
}

type journalContentFetcher func(context.Context, string) (string, error)

// journalEntryFromEditor opens $EDITOR on the day's entry. A read that fails is fatal:
// an empty day answers 204 as an empty string, so anything else means we do not know
// what the day holds -- and saving an empty editor over it would replace the entry.
// journalEntryFromEditor prefills $EDITOR with the day's entry as Markdown — the same
// form the edited result is saved in.
func journalEntryFromEditor(ctx context.Context, date string, fetch journalContentFetcher, open func(string) (string, error)) (string, error) {
	existing, err := fetch(ctx, date)
	if err != nil {
		return "", apierr.FromSDK(err)
	}
	edited, err := open(htmlutil.ToMarkdown(existing).String())
	if err != nil {
		return "", apierr.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
	}
	return edited, nil
}
