package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

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
	path  string
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
	"sent":       {title: "Sent", path: "/topics/sent.json"},
	"spam":       {title: "Spam", path: "/topics/spam.json"},
	"trash":      {title: "Trash", path: "/topics/trash.json"},
	"everything": {title: "Everything", path: "/topics/everything.json"},
}

func newThreadListCommand() *threadListCommand {
	command := &threadListCommand{}
	command.cmd = &cobra.Command{
		Use:   "list",
		Short: "List threads in a system view",
		Long:  "List thread IDs from HEY's Sent, Spam, Trash, or Everything view.",
		Annotations: map[string]string{
			"agent_notes": "Returns thread IDs, not box item IDs. Use topic_id with hey thread read, reply, forward, share, and attachment list. --page continues from the opaque next_page cursor of an earlier listing of the same view.",
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
	read := func(ctx context.Context, cursor string) (pageResult[generated.Topic], error) {
		page, pageErr := readThreadListPage(ctx, view, cursor)
		return page.page, pageErr
	}
	collected, err := collectPages(cmd.Context(), first.page, pageRequest{
		Limit:    c.limit,
		All:      c.all,
		MaxPages: maxThreadListPages,
	}, read)
	if err != nil {
		return err
	}

	topics := collected.Items
	nextPage := collected.Cursor
	if c.limit > 0 && !c.all && len(topics) > c.limit {
		topics = topics[:c.limit]
		nextPage = ""
	}
	title := first.title
	if title == "" {
		title = view.title
	}
	notice := threadListNotice(len(topics), collected.Total, nextPage != "", c.all, c.page != "")

	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		return writeThreadListStyled(cmd, title, topics, notice)
	case output.FormatIDs, output.FormatCount, output.FormatQuiet:
		writeThreadListPagination(cmd, notice, nextPage)
		return writeOK(makeThreadListTopics(topics))
	case output.FormatMarkdown:
		return writeThreadListMarkdown(cmd, title, topics, nextPage, collected.Total, notice)
	default:
		opts := []output.ResponseOption{
			output.WithSummary(fmt.Sprintf("%d %s in %s", len(topics), threadNoun(len(topics)), title)),
			output.WithNotice(notice),
			output.WithMeta("pages_fetched", collected.Read),
			output.WithMeta("total_count", collected.Total),
			output.WithBreadcrumbs(output.Breadcrumb{
				Action:      "read",
				Command:     "hey thread read <thread-id>",
				Description: "Read an email thread",
			}),
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

func writeThreadListStyled(cmd *cobra.Command, title string, topics []generated.Topic, notice string) error {
	if len(topics) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No threads in %s.\n", terminal.SanitizeLine(title))
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
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
	return nil
}

func writeThreadListMarkdown(cmd *cobra.Command, title string, topics []generated.Topic, nextPage string, total int, notice string) error {
	fmt.Fprintf(cmd.OutOrStdout(), "# %s\n\n", markdownSafeText(title))
	if err := writeOK(makeThreadListRows(topics)); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\n**Total threads:** %d\n", total)
	if nextPage != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "**Next page:** `%s`\n", terminal.SanitizeLine(nextPage))
	}
	if notice != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", markdownSafeText(notice))
	}
	return nil
}

// The typed topic-list helpers return the JSON body but not the geared_pagination Link
// header. Read the same fixed route through the SDK document client so the command can
// carry its opaque cursor forward, then decode the generated response shape.
func readThreadListPage(ctx context.Context, view threadListView, cursor string) (threadListPage, error) {
	response, err := sdk.Get(ctx, threadListPagePath(view.path, cursor))
	if err != nil {
		return threadListPage{}, apierr.FromSDK(err)
	}
	if response == nil {
		return threadListPage{}, apierr.ErrAPI(0, fmt.Sprintf("HEY returned no %s thread list", view.title))
	}

	var result generated.TopicListResponse
	if err := response.UnmarshalData(&result); err != nil {
		return threadListPage{}, apierr.ErrAPI(response.StatusCode, fmt.Sprintf("could not read the %s thread list: %v", view.title, err))
	}
	topics := result.Topics
	if topics == nil {
		topics = []generated.Topic{}
	}
	total, _ := strconv.Atoi(response.Headers.Get("X-Total-Count"))
	return threadListPage{
		page: pageResult[generated.Topic]{
			Items:  topics,
			Cursor: threadListNextPage(response.Headers),
			Total:  total,
		},
		title: result.Title,
	}, nil
}

func threadListPagePath(path, cursor string) string {
	if cursor == "" {
		return path
	}
	query := url.Values{"page": {cursor}}
	return path + "?" + query.Encode()
}

func threadListNextPage(headers http.Header) string {
	for _, value := range headers.Values("Link") {
		for part := range strings.SplitSeq(value, ",") {
			start := strings.IndexByte(part, '<')
			end := strings.IndexByte(part, '>')
			if start < 0 || end <= start+1 || !threadListLinkIsNext(part[end+1:]) {
				continue
			}
			target, err := url.Parse(part[start+1 : end])
			if err == nil {
				return target.Query().Get("page")
			}
		}
	}
	return ""
}

func threadListLinkIsNext(parameters string) bool {
	for _, parameter := range strings.Split(parameters, ";") {
		parts := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "rel") {
			continue
		}
		for _, relation := range strings.Fields(strings.Trim(parts[1], `"`)) {
			if strings.EqualFold(relation, "next") {
				return true
			}
		}
	}
	return false
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

func threadListNotice(shown, total int, hasMore, all, fromCursor bool) string {
	if all {
		if hasMore {
			return fmt.Sprintf("Showing %d results. Pagination limit reached; continue with --page using next_page.", shown)
		}
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
