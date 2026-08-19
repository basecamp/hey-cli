package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/output"
)

const maxSearchPages = 100

var searchYearPattern = regexp.MustCompile(`^20\d{2}$`)

type searchCommand struct {
	cmd *cobra.Command

	required   string
	any        string
	none       string
	exact      string
	from       string
	to         string
	subject    string
	date       string
	in         string
	label      string
	attachment string
	page       int
	all        bool
}

type searchResult struct {
	ID        int64             `json:"id,omitempty"`
	TopicID   int64             `json:"topic_id"`
	Subject   string            `json:"subject"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Messages  []generated.Entry `json:"messages"`
}

type searchPageFetcher func(context.Context, hey.SearchParams) (*generated.AdvancedSearchResult, error)

func newSearchCommand() *searchCommand {
	searchCommand := &searchCommand{}
	searchCommand.cmd = &cobra.Command{
		Use:   "search [query]",
		Short: "Search email threads and messages",
		Long:  "Search email threads and matching messages. Combine an optional free-text query with advanced refinements.",
		Annotations: map[string]string{
			"agent_notes": "Returns one result per thread. id is the box item ID for organization actions when the thread has an active box item, topic_id opens the thread, and messages contains the matching message summaries. Use `hey search filters` to discover box, date, label, and attachment values.",
		},
		Example: `  hey search "quarterly planning"
  hey search --from jane@example.com --date last_30_days
  hey search --subject invoice --attachment pdf --all
  hey search filters --json`,
		RunE: searchCommand.run,
		Args: cobra.MaximumNArgs(1),
	}

	flags := searchCommand.cmd.Flags()
	flags.StringVar(&searchCommand.required, "required", "", "Words that must all appear")
	flags.StringVar(&searchCommand.any, "any", "", "Words where at least one must appear")
	flags.StringVar(&searchCommand.none, "none", "", "Words that must not appear")
	flags.StringVar(&searchCommand.exact, "exact", "", "Exact phrase that must appear")
	flags.StringVar(&searchCommand.from, "from", "", "Sender name or email address")
	flags.StringVar(&searchCommand.to, "to", "", "Recipient name or email address")
	flags.StringVar(&searchCommand.subject, "subject", "", "Words in the subject")
	flags.StringVar(&searchCommand.date, "date", "", "Date range: last_7_days, last_30_days, last_90_days, or year")
	flags.StringVar(&searchCommand.in, "in", "", "Box: imbox, feed, papertrail, or trash")
	flags.StringVar(&searchCommand.label, "label", "", "Label name")
	flags.StringVar(&searchCommand.attachment, "attachment", "", "Attachment kind, or any")
	flags.IntVar(&searchCommand.page, "page", 1, "Results page")
	flags.BoolVar(&searchCommand.all, "all", false, "Fetch up to 100 results pages from --page onward")

	searchCommand.cmd.AddCommand(newSearchFiltersCommand().cmd)
	return searchCommand
}

func (c *searchCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	params := c.params(args)
	if !hasSearchCriteria(params) {
		return output.ErrUsage("provide a query or at least one search refinement")
	}
	if err := validateSearchParams(params); err != nil {
		return err
	}

	matches, pages, truncated, err := collectSearchMatches(cmd.Context(), params, c.all, func(ctx context.Context, pageParams hey.SearchParams) (*generated.AdvancedSearchResult, error) {
		result, searchErr := sdk.Search().Search(ctx, pageParams)
		if searchErr != nil {
			return nil, convertSDKError(searchErr)
		}
		return result, nil
	})
	if err != nil {
		return err
	}
	results := makeSearchResults(matches)

	notice := searchTruncationNotice(params.Page, pages, truncated)
	if writer.IsStyled() {
		printSearchResults(cmd, results)
		if notice != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", notice)
		}
		return nil
	}
	if stderrNotice := searchNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}
	return writeOK(results,
		output.WithSummary(searchSummary(len(results))),
		output.WithNotice(notice),
		output.WithMeta("page", params.Page),
		output.WithMeta("pages_fetched", pages),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "read",
				Command:     "hey threads <topic_id>",
				Description: "Read a matching email thread",
			},
		),
	)
}

func (c *searchCommand) params(args []string) hey.SearchParams {
	query := ""
	if len(args) == 1 {
		query = strings.TrimSpace(args[0])
	}
	return hey.SearchParams{
		Query:       query,
		Page:        c.page,
		Required:    strings.TrimSpace(c.required),
		Any:         strings.TrimSpace(c.any),
		None:        strings.TrimSpace(c.none),
		ExactPhrase: strings.TrimSpace(c.exact),
		From:        strings.TrimSpace(c.from),
		To:          strings.TrimSpace(c.to),
		Subject:     strings.TrimSpace(c.subject),
		Date:        strings.TrimSpace(c.date),
		In:          strings.TrimSpace(c.in),
		Label:       strings.TrimSpace(c.label),
		Attachment:  strings.TrimSpace(c.attachment),
	}
}

func hasSearchCriteria(params hey.SearchParams) bool {
	return params.Query != "" || params.Required != "" || params.Any != "" || params.None != "" ||
		params.ExactPhrase != "" || params.From != "" || params.To != "" || params.Subject != "" ||
		params.Date != "" || params.In != "" || params.Label != "" || params.Attachment != ""
}

func validateSearchParams(params hey.SearchParams) error {
	if params.Page < 1 {
		return output.ErrUsage("--page must be at least 1")
	}
	if params.Date != "" {
		switch params.Date {
		case "last_7_days", "last_30_days", "last_90_days":
		default:
			if !searchYearPattern.MatchString(params.Date) {
				return output.ErrUsage("--date must be last_7_days, last_30_days, last_90_days, or a four-digit year beginning with 20")
			}
		}
	}
	if params.In != "" {
		switch params.In {
		case "imbox", "feed", "papertrail", "trash":
		default:
			return output.ErrUsage("--in must be imbox, feed, papertrail, or trash")
		}
	}
	return nil
}

func collectSearchMatches(ctx context.Context, params hey.SearchParams, all bool, fetch searchPageFetcher) ([]generated.SearchMatch, int, bool, error) {
	if fetch == nil {
		return nil, 0, false, fmt.Errorf("collectSearchMatches: fetch function is nil")
	}

	page := params.Page
	if page < 1 {
		page = 1
	}
	var matches []generated.SearchMatch
	for pages := 1; pages <= maxSearchPages; pages++ {
		params.Page = page
		result, err := fetch(ctx, params)
		if err != nil {
			return nil, pages - 1, false, err
		}
		if result == nil || len(result.Matches) == 0 {
			return matches, pages, false, nil
		}
		matches = append(matches, result.Matches...)
		if !all {
			return matches, 1, false, nil
		}
		if pages == maxSearchPages {
			return matches, pages, true, nil
		}
		page++
	}
	return matches, maxSearchPages, true, nil
}

func makeSearchResults(matches []generated.SearchMatch) []searchResult {
	results := make([]searchResult, 0, len(matches))
	for _, match := range matches {
		results = append(results, searchResult{
			ID:        match.PostingId,
			TopicID:   match.Topic.Id,
			Subject:   match.Topic.Name,
			UpdatedAt: match.Topic.UpdatedAt,
			Messages:  match.Entries,
		})
	}
	return results
}

func printSearchResults(cmd *cobra.Command, results []searchResult) {
	if len(results) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No matches found")
		return
	}

	table := newTable(cmd.OutOrStdout())
	table.addRow([]string{"ID", "Thread", "Message", "From", "Subject", "Match", "Date"})
	for _, result := range results {
		id := "—"
		if result.ID != 0 {
			id = fmt.Sprintf("%d", result.ID)
		}
		if len(result.Messages) == 0 {
			table.addRow([]string{id, fmt.Sprintf("%d", result.TopicID), "—", "", truncate(result.Subject, 36), "", formatDate(result.UpdatedAt)})
			continue
		}
		for _, message := range result.Messages {
			from := message.Creator.Name
			if from == "" {
				from = message.Creator.EmailAddress
			}
			if message.AlternativeSenderName != "" {
				from = message.AlternativeSenderName
			}
			table.addRow([]string{
				id,
				fmt.Sprintf("%d", result.TopicID),
				fmt.Sprintf("%d", message.Id),
				truncate(from, 24),
				truncate(result.Subject, 36),
				truncate(message.Summary, 50),
				formatDate(message.CreatedAt),
			})
		}
	}
	table.print()
}

func searchSummary(count int) string {
	return fmt.Sprintf("%d matching %s", count, threadNoun(count))
}

func searchTruncationNotice(startPage, pages int, truncated bool) string {
	if !truncated {
		return ""
	}
	return fmt.Sprintf("Search stopped after %d pages. Continue with --page %d.", pages, startPage+pages)
}

func searchNoticeForStderr(format output.Format, notice string) string {
	if notice == "" || format == output.FormatJSON || format == output.FormatStyled {
		return ""
	}
	return "notice: " + notice
}
