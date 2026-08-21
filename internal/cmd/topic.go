package cmd

import (
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
	"github.com/basecamp/hey-cli/internal/threadload"
)

const threadEntrySeparatorWidth = 60

// threadContact is whoever wrote an entry.
type threadContact struct {
	ID           int64  `json:"id,omitempty"`
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
}

// threadEntry is one message in a thread. Body is Markdown, converted once here from
// HEY's Trix HTML; BodyHTML keeps that HTML for --html. BodyState says what became of
// the body: hydrated, bodyless (HEY served none), over_limit or failed (it was not
// read), or not_requested (the format did not need it).
type threadEntry struct {
	ID                    int64         `json:"id"`
	CreatedAt             string        `json:"created_at"`
	UpdatedAt             string        `json:"updated_at"`
	Creator               threadContact `json:"creator"`
	AlternativeSenderName string        `json:"alternative_sender_name"`
	Summary               string        `json:"summary"`
	Kind                  string        `json:"kind"`
	AppURL                string        `json:"app_url"`
	Body                  string        `json:"body,omitempty"`
	BodyState             string        `json:"body_state,omitempty"`
	BodyHTML              string        `json:"-"`
}

type topicCommand struct {
	cmd          *cobra.Command
	allowPartial bool
}

func newThreadsCommand() *topicCommand {
	threadsCommand := &topicCommand{}
	threadsCommand.cmd = &cobra.Command{
		Use:   "threads <id>",
		Short: "Read a thread",
		Annotations: map[string]string{
			"agent_notes": "Returns a thread with all entries, oldest first. Entry bodies are Markdown; --html returns HEY's original HTML instead. A thread that could only be read in part is refused unless --allow-partial is passed, in which case each entry's body_state says what was read. Use the topic ID with hey reply or hey forward.",
		},
		Example: `  hey threads 12345
  hey threads 12345 --json
  hey threads 12345 --count
  hey threads 12345 --allow-partial`,
		RunE: threadsCommand.run,
		Args: usageExactOneArg(),
	}
	threadsCommand.cmd.Flags().BoolVar(&threadsCommand.allowPartial, "allow-partial", false,
		"Take a thread that could only be read in part, with a notice saying what is missing")

	return threadsCommand
}

func (c *topicCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return apierr.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	// A count or a list of IDs needs the index and nothing else; every other format
	// shows bodies, so it reads them.
	format := writer.EffectiveFormat()
	hydrate := format != output.FormatCount && format != output.FormatIDs
	thread, err := loadThread(cmd.Context(), threadID, hydrate)
	if err != nil {
		return err
	}
	notice := threadNotice(thread)
	if notice != "" && !c.allowPartial {
		return errPartialThread(threadID, notice)
	}
	entries := threadEntries(thread)

	switch format {
	case output.FormatHTML:
		return writeThreadHTML(cmd.OutOrStdout(), entries)
	case output.FormatStyled:
		printThreadStyled(cmd.OutOrStdout(), entries, notice)
		return nil
	case output.FormatCount, output.FormatIDs:
		if stderrNotice := paginationNoticeForStderr(format, notice); stderrNotice != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
		}
		return writeOK(entries)
	case output.FormatAuto, output.FormatJSON, output.FormatQuiet, output.FormatMarkdown:
	}

	return writeOK(entries,
		output.WithSummary(fmt.Sprintf("%d entries in thread %d", len(entries), threadID)),
		output.WithNotice(notice),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "reply",
				Command:     fmt.Sprintf("hey reply %d", threadID),
				Description: "Reply to this thread",
			},
			output.Breadcrumb{
				Action:      "forward",
				Command:     fmt.Sprintf("hey forward %d --to <email>", threadID),
				Description: "Forward the latest message",
			},
		),
	)
}

// printThreadStyled writes a thread for a terminal. A body is rendered; an entry HEY
// served no body for shows its summary, which is all HEY has for it; an entry whose body
// was not read says so rather than passing a preview off as the message.
func printThreadStyled(w io.Writer, entries []threadEntry, notice string) {
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(w, strings.Repeat("─", threadEntrySeparatorWidth))
		}
		fmt.Fprintf(w, "From: %s  [%s]  #%d\n", terminal.SanitizeLine(threadEntrySender(e)), e.CreatedAt, e.ID)
		fmt.Fprintln(w)
		switch {
		case e.Body != "":
			fmt.Fprintln(w, markdown.Render(e.Body, stdoutWidth()))
		case e.BodyState == string(threadload.StateBodyless) && e.Summary != "":
			fmt.Fprintln(w, terminal.SanitizeLine(e.Summary))
		case e.BodyState == string(threadload.StateBodyless):
			fmt.Fprintln(w, "(no body)")
		default:
			fmt.Fprintf(w, "(body not read: %s)\n", e.BodyState)
		}
		fmt.Fprintln(w)
	}
	if notice != "" {
		fmt.Fprintf(w, "notice: %s\n", notice)
	}
}

// writeThreadHTML is what --html writes for a thread: each entry's original HTML, oldest
// first, each introduced by a comment naming the entry, its sender and its date, with a
// blank line between entries. An entry without a body says why in its comment. A write
// that fails is the command's error.
func writeThreadHTML(w io.Writer, entries []threadEntry) error {
	for i, e := range entries {
		if i > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return fmt.Errorf("write thread HTML: %w", err)
			}
		}
		body := e.BodyHTML
		if body == "" {
			body = "<!-- no body: " + htmlCommentSafe(e.BodyState) + " -->"
		}
		_, err := fmt.Fprintf(w, "<!-- hey entry %d from %s at %s -->\n%s\n",
			e.ID, html.EscapeString(htmlCommentSafe(threadEntrySender(e))), e.CreatedAt, body)
		if err != nil {
			return fmt.Errorf("write thread HTML: %w", err)
		}
	}
	return nil
}

// htmlCommentSafe keeps a value from ending the comment it is written into.
func htmlCommentSafe(value string) string {
	return strings.ReplaceAll(terminal.SanitizeLine(value), "--", "- -")
}

func threadEntrySender(entry threadEntry) string {
	switch {
	case entry.AlternativeSenderName != "":
		return entry.AlternativeSenderName
	case entry.Creator.Name != "":
		return entry.Creator.Name
	default:
		return entry.Creator.EmailAddress
	}
}

// threadEntries is a loaded thread in the CLI's shape, oldest first, each body
// converted to Markdown once.
func threadEntries(thread *threadload.Thread) []threadEntry {
	entries := make([]threadEntry, len(thread.Entries))
	for i, loaded := range thread.Entries {
		entries[i] = newThreadEntry(loaded)
	}
	return entries
}

func newThreadEntry(loaded threadload.Entry) threadEntry {
	entry := loaded.Entry
	creator := entry.Creator
	createdAt := entry.CreatedAt
	updatedAt := entry.UpdatedAt
	summary := entry.Summary
	appURL := entry.AppUrl
	body, bodyHTML := "", ""

	if message := loaded.Message; message != nil {
		if creator.Id == 0 {
			creator = message.Creator
		}
		if createdAt.IsZero() {
			createdAt = message.CreatedAt
		}
		if updatedAt.IsZero() {
			updatedAt = message.UpdatedAt
		}
		if summary == "" {
			summary = message.Subject
		}
		if appURL == "" {
			appURL = message.Url
		}
		body = htmlutil.ToMarkdown(message.Content)
		bodyHTML = message.Content
	}

	return threadEntry{
		ID:                    entry.Id,
		CreatedAt:             formatTimestamp(createdAt),
		UpdatedAt:             formatTimestamp(updatedAt),
		AlternativeSenderName: entry.AlternativeSenderName,
		Summary:               summary,
		Kind:                  entry.Kind,
		AppURL:                appURL,
		Body:                  body,
		BodyState:             string(loaded.State),
		BodyHTML:              bodyHTML,
		Creator: threadContact{
			ID:           creator.Id,
			Name:         creator.Name,
			EmailAddress: creator.EmailAddress,
		},
	}
}
