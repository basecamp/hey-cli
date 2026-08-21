package cmd

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/output"
)

// maxThreadEntryPages is how many pages of a thread's entries one command reads, counting
// the page it already has: that one plus a hundred cursors beyond it.
const (
	maxThreadEntryPages         = 101
	maxConcurrentEntryBodyReads = 8
	threadEntrySeparatorWidth   = 60
)

// threadContact is whoever wrote an entry.
type threadContact struct {
	ID           int64  `json:"id,omitempty"`
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
}

// threadEntry is one message in a thread. Body is Markdown, converted once here from
// HEY's Trix HTML; BodyHTML keeps that HTML for --html.
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
	BodyHTML              string        `json:"-"`
}

type topicCommand struct {
	cmd *cobra.Command
}

func newThreadsCommand() *topicCommand {
	threadsCommand := &topicCommand{}
	threadsCommand.cmd = &cobra.Command{
		Use:   "threads <id>",
		Short: "Read a thread",
		Annotations: map[string]string{
			"agent_notes": "Returns a thread with all entries, oldest first. Entry bodies are Markdown; --html returns HEY's original HTML instead. Use the topic ID with hey reply or hey forward.",
		},
		Example: `  hey threads 12345
  hey threads 12345 --json`,
		RunE: threadsCommand.run,
		Args: usageExactOneArg(),
	}

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

	entries, err := entriesInThread(cmd.Context(), threadID)
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		for i, e := range entries {
			if i > 0 {
				fmt.Fprintln(w, strings.Repeat("─", threadEntrySeparatorWidth))
			}
			fmt.Fprintf(w, "From: %s  [%s]  #%d\n", threadEntrySender(e), e.CreatedAt, e.ID)
			switch {
			case htmlOutput && e.BodyHTML != "":
				fmt.Fprintln(w)
				fmt.Fprintln(w, e.BodyHTML)
			case e.Body != "":
				fmt.Fprintln(w)
				fmt.Fprintln(w, markdown.Render(e.Body, stdoutWidth()))
			case e.Summary != "":
				fmt.Fprintln(w)
				fmt.Fprintln(w, e.Summary)
			}
			fmt.Fprintln(w)
		}
		return nil
	}

	return writeOK(entries,
		output.WithSummary(fmt.Sprintf("%d entries in thread %d", len(entries), threadID)),
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

// entriesInThread reads a thread's entries and then each entry's body. HEY serves the
// entry list newest first, a page at a time, and carries the body on the message rather
// than the entry, so the pages are gathered and reversed into reading order.
func entriesInThread(ctx context.Context, threadID int64) ([]threadEntry, error) {
	collected, err := threadEntryPages(ctx, threadID)
	if err != nil {
		return nil, err
	}

	entries := collected.Items
	if len(entries) == 0 {
		return nil, apierr.ErrNotFound("entries for thread", strconv.FormatInt(threadID, 10))
	}
	slices.Reverse(entries)

	messages := make([]generated.Message, len(entries))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxConcurrentEntryBodyReads)
	for index, entry := range entries {
		group.Go(func() error {
			message, err := sdk.Messages().Get(groupCtx, entry.Id)
			if err != nil {
				return err
			}
			if message == nil {
				return fmt.Errorf("message %d returned no data", entry.Id)
			}
			messages[index] = *message
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, apierr.FromSDK(err)
	}

	threadEntries := make([]threadEntry, len(entries))
	for index, entry := range entries {
		threadEntries[index] = messageToThreadEntry(entry, messages[index])
	}
	return threadEntries, nil
}

// threadEntryPages reads a thread's entries a page at a time, newest first, following the
// cursor HEY answers each page with. `hey attachments` walks the same list.
func threadEntryPages(ctx context.Context, threadID int64) (collectedPages[generated.Entry], error) {
	read := readThreadEntryPage(threadID)
	first, err := read(ctx, "")
	if err != nil {
		return collectedPages[generated.Entry]{}, err
	}
	return collectPages(ctx, first, pageRequest{All: true, MaxPages: maxThreadEntryPages}, read)
}

func readThreadEntryPage(threadID int64) pageReader[generated.Entry] {
	return func(ctx context.Context, cursor string) (pageResult[generated.Entry], error) {
		page, err := sdk.Topics().GetEntriesPage(ctx, threadID, cursor)
		if err != nil {
			return pageResult[generated.Entry]{}, apierr.FromSDK(err)
		}
		if page == nil {
			return pageResult[generated.Entry]{}, fmt.Errorf("thread %d answered no entry page at cursor %q", threadID, cursor)
		}
		return pageResult[generated.Entry]{Items: page.Entries, Cursor: page.NextPage}, nil
	}
}

func messageToThreadEntry(entry generated.Entry, message generated.Message) threadEntry {
	creator := entry.Creator
	if creator.Id == 0 {
		creator = message.Creator
	}
	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = message.CreatedAt
	}
	updatedAt := entry.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = message.UpdatedAt
	}
	summary := entry.Summary
	if summary == "" {
		summary = message.Subject
	}
	appURL := entry.AppUrl
	if appURL == "" {
		appURL = message.Url
	}

	return threadEntry{
		ID:                    entry.Id,
		CreatedAt:             formatTimestamp(createdAt),
		UpdatedAt:             formatTimestamp(updatedAt),
		AlternativeSenderName: entry.AlternativeSenderName,
		Summary:               summary,
		Kind:                  entry.Kind,
		AppURL:                appURL,
		Body:                  htmlutil.ToMarkdown(message.Content),
		BodyHTML:              message.Content,
		Creator: threadContact{
			ID:           creator.Id,
			Name:         creator.Name,
			EmailAddress: creator.EmailAddress,
		},
	}
}
