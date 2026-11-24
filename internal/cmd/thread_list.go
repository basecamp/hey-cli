package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

const maxThreadListPages = 100

type threadListCommand struct {
	cmd   *cobra.Command
	in    string
	limit int
	all   bool
	page  string
}

type threadListView struct {
	title string
	read  func(context.Context, string) (*hey.TopicListPage, error)
	// followsEmptyPages is set for Sent alone: HEY takes each page of Sent and only then
	// drops the threads that are not active, so a page can come back short or empty with
	// more after it.
	followsEmptyPages bool
	// uncounted is set for Sent alone, the one view HEY sends no X-Total-Count for.
	uncounted bool
}

type threadListPage struct {
	page  pageResult[generated.Topic]
	title string
}

type threadListTopic struct {
	generated.Topic
	TopicID int64 `json:"topic_id"`
}

type threadListRow struct {
	TopicID int64  `json:"topic_id"`
	Subject string `json:"subject,omitempty"`
	From    string `json:"from,omitempty"`
	Date    string `json:"date,omitempty"`
}

var threadListViews = map[string]threadListView{
	"sent": {title: "Sent", read: readSentPage, followsEmptyPages: true, uncounted: true},
	"spam": {title: "Spam", read: func(ctx context.Context, cursor string) (*hey.TopicListPage, error) {
		return sdk.Topics().GetSpamPage(ctx, cursor)
	}},
	"trash": {title: "Trash", read: func(ctx context.Context, cursor string) (*hey.TopicListPage, error) {
		return sdk.Topics().GetTrashPage(ctx, cursor)
	}},
	"everything": {title: "Everything", read: func(ctx context.Context, cursor string) (*hey.TopicListPage, error) {
		return sdk.Topics().GetEverythingPage(ctx, cursor)
	}},
}

func newThreadListCommand() *threadListCommand {
	command := &threadListCommand{}
	command.cmd = &cobra.Command{
		Use:   "list",
		Short: "List threads in a system view",
		Long:  "List thread IDs from HEY's Sent, Spam, Trash, or Everything view.",
		Annotations: map[string]string{
			"agent_notes": "Returns thread IDs, not box item IDs. Use topic_id with hey thread read, reply, forward, share, attachment list, and restore (a trashed thread has no box item ID). --page continues from the opaque next_page cursor of an earlier listing of the same view.",
		},
		Example: `  hey thread list --in sent
  hey thread list --in trash --limit 10
  hey thread list --in everything --all --json
  hey thread list --in spam --page next-cursor`,
		RunE: command.run,
		Args: cobra.NoArgs,
	}

	command.cmd.Flags().StringVar(&command.in, "in", "", "View: sent, spam, trash, or everything")
	command.cmd.Flags().IntVar(&command.limit, "limit", 0, "Maximum number of threads to show")
	command.cmd.Flags().BoolVar(&command.all, "all", false, "Fetch all results (override --limit)")
	command.cmd.Flags().StringVar(&command.page, "page", "", "Continue from a next_page cursor")

	return command
}

func (c *threadListCommand) run(cmd *cobra.Command, _ []string) error {
	view, viewErr := c.selectedView()
	if viewErr != nil {
		return viewErr
	}
	if c.limit < 0 {
		return apierr.ErrUsage("--limit must be at least 0")
	}
	if err := requireAuth(); err != nil {
		return err
	}

	first, err := readThreadListPage(cmd.Context(), view, c.page)
	if err != nil {
		return err
	}
	if len(first.page.Items) == 0 && !view.followsEmptyPages {
		// An empty page ends a keyset-paged view whatever cursor came with it.
		first.page.Cursor = ""
	}
	read := func(ctx context.Context, cursor string) (pageResult[generated.Topic], error) {
		page, pageErr := readThreadListPage(ctx, view, cursor)
		return page.page, pageErr
	}
	collected, err := collectPages(cmd.Context(), first.page, pageRequest{
		Limit:            c.limit,
		All:              c.all,
		MaxPages:         maxThreadListPages,
		FollowEmptyPages: view.followsEmptyPages,
		StartCursor:      c.page,
	}, read)
	if err != nil {
		return err
	}

	topics := collected.Items
	nextPage := collected.Cursor
	wantedMore := c.all || (c.limit > 0 && len(topics) < c.limit)
	if wantedMore && nextPage != "" && !collected.Truncated {
		// The read stopped at an empty page of a view that does not follow one, which
		// ends the list: its cursor is not a continuation.
		nextPage = ""
	}
	clipped := false
	if c.limit > 0 && !c.all && len(topics) > c.limit {
		topics = topics[:c.limit]
		nextPage = ""
		clipped = true
	}
	// ended is a read that wanted more and found the list's end: --all would stop at the
	// same place, so the notice must not send the reader there.
	ended := wantedMore && nextPage == "" && !collected.Truncated
	total := collected.Total
	if view.uncounted {
		// HEY counts no total for Sent: only what was shown, and whether more was read.
		total = len(topics)
	}
	title := first.title
	if title == "" {
		title = view.title
	}
	notice := threadListNotice(len(topics), total, nextPage != "" || clipped, collected.Truncated, c.all || ended, c.page != "")

	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		return writeThreadListStyled(cmd, title, topics, nextPage, notice)
	case output.FormatIDs, output.FormatCount, output.FormatQuiet:
		writeThreadListPagination(cmd, notice, nextPage)
		return writeOK(makeThreadListTopics(topics))
	case output.FormatMarkdown:
		return writeThreadListMarkdown(cmd, title, topics, nextPage, total, !view.uncounted, notice)
	default:
		opts := []output.ResponseOption{
			output.WithSummary(fmt.Sprintf("%d %s in %s", len(topics), threadNoun(len(topics)), title)),
			output.WithNotice(notice),
			output.WithMeta("pages_fetched", collected.Read),
			output.WithBreadcrumbs(output.Breadcrumb{
				Action:      "read",
				Command:     "hey thread read <thread-id>",
				Description: "Read an email thread",
			}),
		}
		if !view.uncounted {
			opts = append(opts, output.WithMeta("total_count", total))
		}
		if nextPage != "" {
			opts = append(opts, output.WithMeta("next_page", nextPage))
		}
		return writeOK(makeThreadListTopics(topics), opts...)
	}
}

func (c *threadListCommand) selectedView() (threadListView, error) {
	view, ok := threadListViews[c.in]
	if !ok {
		return threadListView{}, apierr.ErrUsage("--in must be sent, spam, trash, or everything")
	}
	return view, nil
}

func writeThreadListStyled(cmd *cobra.Command, title string, topics []generated.Topic, nextPage, notice string) error {
	if len(topics) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No threads in %s.\n", terminal.SanitizeLine(title))
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		writeThreadListStyledNextPage(cmd, nextPage)
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s:\n\n", terminal.SanitizeLine(title))
	table := newTable(cmd.OutOrStdout())
	table.addRow([]string{"Thread", "Subject", "From", "Date"})
	for _, topic := range topics {
		table.addRow([]string{
			fmt.Sprintf("%d", topic.Id),
			truncate(terminal.SanitizeLine(topic.Name), 48),
			truncate(terminal.SanitizeLine(threadListSender(topic)), 32),
			formatDate(threadListDate(topic)),
		})
	}
	table.print()
	if notice != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", notice)
	}
	writeThreadListStyledNextPage(cmd, nextPage)
	return nil
}

func writeThreadListStyledNextPage(cmd *cobra.Command, nextPage string) {
	if nextPage != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Next page: %s\n", terminal.SanitizeLine(nextPage))
	}
}

func writeThreadListMarkdown(cmd *cobra.Command, title string, topics []generated.Topic, nextPage string, total int, counted bool, notice string) error {
	fmt.Fprintf(cmd.OutOrStdout(), "# %s\n\n", markdownSafeText(title))
	if err := writeOK(makeThreadListRows(topics)); err != nil {
		return err
	}
	if counted {
		fmt.Fprintf(cmd.OutOrStdout(), "\n**Total threads:** %d\n", total)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "\n**Threads listed:** %d\n", total)
	}
	if nextPage != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "**Next page:** `%s`\n", terminal.SanitizeLine(nextPage))
	}
	if notice != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", markdownSafeText(notice))
	}
	return nil
}

func readThreadListPage(ctx context.Context, view threadListView, cursor string) (threadListPage, error) {
	page, err := view.read(ctx, cursor)
	if err != nil {
		return threadListPage{}, apierr.FromSDK(err)
	}
	topics := page.Topics
	if topics == nil {
		topics = []generated.Topic{}
	}
	return threadListPage{
		page: pageResult[generated.Topic]{
			Items:  topics,
			Cursor: page.NextPage,
			Total:  page.TotalCount,
		},
		title: page.Title,
	}, nil
}

// readSentPage reads Sent into the shape of the other views. Sent pages by offset in HEY
// and sends no total, so its TotalCount stays zero.
func readSentPage(ctx context.Context, cursor string) (*hey.TopicListPage, error) {
	page, err := sdk.Topics().GetSentPage(ctx, cursor)
	if err != nil || page == nil {
		return nil, err
	}
	return &hey.TopicListPage{
		Title:       page.Title,
		Description: page.Description,
		Topics:      page.Topics,
		NextPage:    page.NextPage,
	}, nil
}

func makeThreadListTopics(topics []generated.Topic) []threadListTopic {
	rows := make([]threadListTopic, len(topics))
	for i, topic := range topics {
		rows[i] = threadListTopic{Topic: topic, TopicID: topic.Id}
	}
	return rows
}

func makeThreadListRows(topics []generated.Topic) []threadListRow {
	rows := make([]threadListRow, len(topics))
	for i, topic := range topics {
		rows[i] = threadListRow{
			TopicID: topic.Id,
			Subject: topic.Name,
			From:    threadListSender(topic),
			Date:    formatDate(threadListDate(topic)),
		}
	}
	return rows
}

func writeThreadListPagination(cmd *cobra.Command, notice, nextPage string) {
	if notice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "notice: "+notice)
	}
	if nextPage != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "next_page: "+terminal.SanitizeLine(nextPage))
	}
}

func threadListNotice(shown, total int, hasMore, truncated, all, fromCursor bool) string {
	if truncated && hasMore {
		return fmt.Sprintf("Showing %d results. Pagination limit reached; continue with --page using next_page.", shown)
	}
	if all {
		if shown < total {
			if fromCursor {
				return fmt.Sprintf("Showing %d remaining results from this cursor (%d threads in the view).", shown, total)
			}
			return fmt.Sprintf("Showing %d of %d results; HEY returned no additional page cursor.", shown, total)
		}
		return ""
	}
	if shown < total {
		return output.TruncationNotice(shown, total)
	}
	if hasMore {
		return fmt.Sprintf("Showing %d results. More available; use --all to fetch all.", shown)
	}
	return ""
}

func threadListSender(topic generated.Topic) string {
	if topic.Creator.Name != "" {
		return topic.Creator.Name
	}
	return topic.Creator.EmailAddress
}

func threadListDate(topic generated.Topic) time.Time {
	if !topic.ActiveAt.IsZero() {
		return topic.ActiveAt
	}
	if !topic.UpdatedAt.IsZero() {
		return topic.UpdatedAt
	}
	return topic.CreatedAt
}
