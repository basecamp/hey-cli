package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// maxPostingPages is how many pages of a box, a label or a collection one command reads,
// counting the page it already has: that one plus a hundred cursors beyond it.
const maxPostingPages = 101

type sourceOutput struct {
	ID         int64                 `json:"id"`
	Name       string                `json:"name,omitempty"`
	AppURL     string                `json:"app_url,omitempty"`
	CreatedAt  *time.Time            `json:"created_at,omitempty"`
	UpdatedAt  *time.Time            `json:"updated_at,omitempty"`
	Postings   []sourcePostingOutput `json:"postings"`
	NextPage   string                `json:"next_page,omitempty"`
	TotalCount int                   `json:"total_count"`
}

type sourcePostingOutput struct {
	generated.Posting
	TopicID int64 `json:"topic_id,omitempty"`
}

type sourcePostingRow struct {
	ID      int64  `json:"id"`
	TopicID int64  `json:"topic_id,omitempty"`
	From    string `json:"from,omitempty"`
	Summary string `json:"summary,omitempty"`
	Date    string `json:"date,omitempty"`
}

// postingsListing is what `hey box view`, `hey label view` and `hey collection view` call the source they
// list. Everything else about the three — the pagination, the notices and the five output
// formats — is the same listing.
//
// payload is the exception, and the reason it is a seam: a label and a collection answer
// `--json` with the same source-and-postings object, while a box answers with HEY's box
// payload, which carries fields (its sync URLs, its stream name) that are not a listing's
// business and that consumers already read.
type postingsListing struct {
	heading      string
	summary      func(count int, name string) string
	cursorNotice func(shown, total int) string
	breadcrumbs  []output.Breadcrumb
	payload      func(source mail.Source, postings []sourcePostingOutput, nextPage string, total int) any
}

func (l postingsListing) write(cmd *cobra.Command, source mail.Source, first pageResult[generated.Posting], request pageRequest, fromCursor bool) error {
	collected, err := collectPages(cmd.Context(), first, request, readSourcePage(source))
	if err != nil {
		return err
	}

	postings := collected.Items
	nextPage := collected.Cursor
	if request.Limit > 0 && !request.All && len(postings) > request.Limit {
		postings = postings[:request.Limit]
		nextPage = ""
	}
	notice := l.notice(len(postings), collected.Total, nextPage != "", request.All, fromCursor)

	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		return l.writeStyled(cmd, source, postings, notice)
	case output.FormatIDs, output.FormatCount:
		if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
		}
		if cursor := sourcePageCursor(source, nextPage); cursor != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "next_page: %s\n", terminal.SanitizeLine(cursor))
		}
		return writeOK(postings)
	case output.FormatMarkdown:
		return l.writeMarkdown(cmd, source, postings, nextPage, collected.Total, notice)
	default:
		return writeOK(l.sourcePayload(source, postings, nextPage, collected.Total),
			output.WithSummary(l.summary(len(postings), source.Name)),
			output.WithNotice(notice),
			output.WithBreadcrumbs(l.breadcrumbs...),
		)
	}
}

// sourcePayload is what `--json` answers with: the listing's own payload where it has one,
// and the source with its postings otherwise. Either way every posting carries the topic_id
// that reads the thread.
func (l postingsListing) sourcePayload(source mail.Source, postings []generated.Posting, nextPage string, total int) any {
	rows := makeSourcePostings(postings)
	if l.payload != nil {
		return l.payload(source, rows, nextPage, total)
	}
	return makeSourceOutput(source, rows, nextPage, total)
}

func (l postingsListing) notice(shown, total int, hasMore, all, fromCursor bool) string {
	if all {
		if hasMore {
			return fmt.Sprintf("Showing %d results. Pagination limit reached; continue with --page using next_page.", shown)
		}
		if shown < total {
			if fromCursor {
				return l.cursorNotice(shown, total)
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

func (l postingsListing) writeStyled(cmd *cobra.Command, source mail.Source, postings []generated.Posting, notice string) error {
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n\n", l.heading, sourceHeading(source))
	table := newTable(cmd.OutOrStdout())
	table.addRow([]string{"ID", "Thread", "From", "Summary", "Date"})
	for _, posting := range postings {
		topicID := ""
		if id := resolvePostingTopicID(posting); id != 0 {
			topicID = fmt.Sprintf("%d", id)
		}
		table.addRow([]string{
			fmt.Sprintf("%d", posting.Id),
			topicID,
			terminal.SanitizeLine(posting.Creator.Name),
			truncate(terminal.SanitizeLine(posting.Summary), 60),
			formatDate(posting.CreatedAt),
		})
	}
	table.print()
	if notice != "" {
		fmt.Fprintln(cmd.OutOrStdout(), notice)
	}
	return nil
}

func (l postingsListing) writeMarkdown(cmd *cobra.Command, source mail.Source, postings []generated.Posting, nextPage string, total int, notice string) error {
	fmt.Fprintf(cmd.OutOrStdout(), "# %s\n\n", markdownSafeText(source.Name))
	rows := make([]sourcePostingRow, len(postings))
	for i, posting := range postings {
		rows[i] = sourcePostingRow{
			ID:      posting.Id,
			TopicID: resolvePostingTopicID(posting),
			From:    posting.Creator.Name,
			Summary: posting.Summary,
			Date:    formatDate(posting.CreatedAt),
		}
	}
	if err := writeOK(rows); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\n**Total threads:** %d\n", total)
	if cursor := sourcePageCursor(source, nextPage); cursor != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "**Next page:** `%s`\n", terminal.SanitizeLine(cursor))
	}
	if notice != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", markdownSafeText(notice))
	}
	return nil
}

// readSourcePage reads a page of any mail source, so the growing loop never has to know
// whether it is walking a box, a label or a collection.
func readSourcePage(source mail.Source) pageReader[generated.Posting] {
	return func(ctx context.Context, cursor string) (pageResult[generated.Posting], error) {
		page, err := mail.ReadPage(ctx, sdk, source, cursor)
		if err != nil {
			return pageResult[generated.Posting]{}, apierr.FromSDK(err)
		}
		return pageResult[generated.Posting]{Items: page.Postings, Cursor: page.Cursor, Total: page.Total}, nil
	}
}

// sourcePageCursor is the cursor a reader hands back to --page. A label and a collection
// are paged by the cursor itself; a box is paged by the cursor inside its next_history_url,
// and the URL around it is not something to continue from.
func sourcePageCursor(source mail.Source, cursor string) string {
	if source.Kind == mail.KindBox {
		return boxPageCursor(cursor)
	}
	return cursor
}

// sourceHeading names a source, and its kind where it has one: a box is asked for by name
// and answers with the kind that name resolved to.
func sourceHeading(source mail.Source) string {
	name := terminal.SanitizeLine(source.Name)
	if source.BoxKind == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, terminal.SanitizeLine(source.BoxKind))
}

func makeSourcePostings(postings []generated.Posting) []sourcePostingOutput {
	rows := make([]sourcePostingOutput, len(postings))
	for i, posting := range postings {
		rows[i] = sourcePostingOutput{Posting: posting, TopicID: resolvePostingTopicID(posting)}
	}
	return rows
}

func makeSourceOutput(source mail.Source, rows []sourcePostingOutput, nextPage string, total int) sourceOutput {
	return sourceOutput{
		ID:         source.ID,
		Name:       source.Name,
		AppURL:     source.AppURL,
		CreatedAt:  nonZeroTime(source.CreatedAt),
		UpdatedAt:  nonZeroTime(source.UpdatedAt),
		Postings:   rows,
		NextPage:   nextPage,
		TotalCount: total,
	}
}

func nonZeroTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
