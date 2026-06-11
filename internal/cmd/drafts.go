package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type draftsCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newDraftsCommand() *draftsCommand {
	draftsCommand := &draftsCommand{}
	draftsCommand.cmd = &cobra.Command{
		Use:   "drafts",
		Short: "List drafts",
		Annotations: map[string]string{
			"agent_notes": "Returns saved draft messages with IDs, summaries, and subjects.",
		},
		Example: `  hey drafts
  hey drafts --limit 10
  hey drafts --json`,
		RunE: draftsCommand.run,
	}

	draftsCommand.cmd.Flags().IntVar(&draftsCommand.limit, "limit", 0, "Maximum number of drafts to show")
	draftsCommand.cmd.Flags().BoolVar(&draftsCommand.all, "all", false, "Fetch all results (override --limit)")

	return draftsCommand
}

func (c *draftsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	drafts, total, hasMore, err := paginateDrafts(ctx, c.limit, c.all, fetchDraftsPage)
	if err != nil {
		return err
	}
	notice := draftsTruncationNotice(len(drafts), total, hasMore, c.all)

	if writer.IsStyled() {
		if len(drafts) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No drafts.")
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Summary", "Subject", "Date"})
		for _, d := range drafts {
			table.addRow([]string{fmt.Sprintf("%d", d.Id), truncate(d.Summary, 60), d.Subject, formatDate(d.UpdatedAt)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(drafts,
		output.WithSummary(fmt.Sprintf("%d drafts", len(drafts))),
		output.WithNotice(notice),
	)
}

type draftPageFetcher func(ctx context.Context, pageURL string) ([]generated.DraftMessage, string, int, error)

func fetchDraftsPage(ctx context.Context, pageURL string) ([]generated.DraftMessage, string, int, error) {
	if strings.HasPrefix(pageURL, "http://") || strings.HasPrefix(pageURL, "https://") {
		if err := validateSameOrigin(sdk.Config().BaseURL, pageURL); err != nil {
			return nil, "", 0, err
		}
	}

	resp, err := sdk.Get(ctx, pageURL)
	if err != nil {
		return nil, "", 0, convertSDKError(err)
	}

	var drafts []generated.DraftMessage
	if err := resp.UnmarshalData(&drafts); err != nil {
		return nil, "", 0, fmt.Errorf("failed to parse drafts response: %w", err)
	}

	total := len(drafts)
	if totalHeader := resp.Headers.Get("X-Total-Count"); totalHeader != "" {
		if parsed, err := strconv.Atoi(totalHeader); err == nil {
			total = parsed
		}
	}

	return drafts, parseNextLinkHeader(resp.Headers.Get("Link")), total, nil
}

func paginateDrafts(ctx context.Context, limit int, all bool, fetch draftPageFetcher) ([]generated.DraftMessage, int, bool, error) {
	if fetch == nil {
		return nil, 0, false, fmt.Errorf("paginateDrafts: fetch function is nil")
	}

	pageDrafts, nextURL, total, err := fetch(ctx, "/entries/drafts.json")
	if err != nil {
		return nil, 0, false, err
	}

	drafts := append([]generated.DraftMessage(nil), pageDrafts...)
	if total == 0 {
		total = len(drafts)
	}

	needMore := all || (limit > 0 && len(drafts) < limit)
	for page := 1; needMore && nextURL != "" && page <= maxAdditionalPages; page++ {
		var pageTotal int
		pageDrafts, nextURL, pageTotal, err = fetch(ctx, nextURL)
		if err != nil {
			return nil, 0, false, err
		}
		if pageTotal > total {
			total = pageTotal
		}
		if len(pageDrafts) == 0 {
			nextURL = ""
			break
		}

		drafts = append(drafts, pageDrafts...)
		needMore = all || (limit > 0 && len(drafts) < limit)
	}

	hasMore := nextURL != ""
	if limit > 0 && !all && len(drafts) > limit {
		drafts = drafts[:limit]
		hasMore = true
	}
	if total < len(drafts) {
		total = len(drafts)
	}

	return drafts, total, hasMore, nil
}

func draftsTruncationNotice(shown, total int, hasMore, all bool) string {
	if hasMore && all {
		return fmt.Sprintf("Showing %d of at least %d drafts. Pagination limit reached; not all drafts could be fetched.", shown, total)
	}
	if hasMore {
		return fmt.Sprintf("Showing %d of %d drafts. Use --all to fetch all.", shown, total)
	}
	return output.TruncationNotice(shown, total)
}

func parseNextLinkHeader(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			return part[start+1 : end]
		}
	}
	return ""
}
