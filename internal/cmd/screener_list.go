package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

const maxScreenerPages = 100

type screenerListCommand struct {
	cmd   *cobra.Command
	page  int
	all   bool
	count bool
}

type pendingClearance struct {
	ID      int64  `json:"id"`
	Name    string `json:"name,omitempty"`
	Email   string `json:"email_address,omitempty"`
	Subject string `json:"subject,omitempty"`
	Summary string `json:"summary,omitempty"`
	TopicID int64  `json:"topic_id,omitempty"`
}

func newScreenerListCommand() *screenerListCommand {
	listCommand := &screenerListCommand{}
	listCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List the senders waiting to be screened",
		Annotations: map[string]string{
			"agent_notes": "Returns clearance IDs with the sender and the subject of what they sent. Feed the ID to `hey screener approve` or `hey screener deny`, and topic_id to `hey topic` to read the thread first. Use --count for the number alone, which is a much cheaper request.",
		},
		Example: `  hey screener list
  hey screener list --json
  hey screener list --count
  hey screener list --all --json`,
		RunE: listCommand.run,
		Args: cobra.NoArgs,
	}
	listCommand.cmd.Flags().IntVar(&listCommand.page, "page", 1, "Results page")
	listCommand.cmd.Flags().BoolVar(&listCommand.all, "all", false, "Fetch up to 100 results pages from --page onward")
	listCommand.cmd.Flags().BoolVar(&listCommand.count, "count", false, "Print how many senders are waiting, without listing them")
	return listCommand
}

func (c *screenerListCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if c.page < 1 {
		return output.ErrUsage("--page must be at least 1")
	}
	if c.count {
		return c.runCount(cmd)
	}

	pending, pages, truncated, err := collectPendingClearances(cmd.Context(), c.page, c.all,
		func(ctx context.Context, page string) (*generated.ClearanceSummary, error) {
			summary, listErr := sdk.Clearances().Pending(ctx, page)
			if listErr != nil {
				return nil, convertSDKError(listErr)
			}
			return summary, nil
		})
	if err != nil {
		return err
	}
	notice := screenerTruncationNotice(c.page, pages, truncated)

	if writer.IsStyled() {
		if len(pending) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Nobody is waiting to be screened")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Sender", "Email", "Subject"})
		for _, clearance := range pending {
			table.addRow([]string{
				fmt.Sprintf("%d", clearance.ID),
				truncate(clearance.Name, 24),
				truncate(clearance.Email, 32),
				truncate(clearance.Subject, 40),
			})
		}
		table.print()
		if notice != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", notice)
		}
		return nil
	}
	if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}
	return writeOK(pending,
		output.WithSummary(fmt.Sprintf("%d %s waiting", len(pending), senderNoun(len(pending)))),
		output.WithNotice(notice),
		output.WithMeta("page", c.page),
		output.WithMeta("pages_fetched", pages),
		output.WithBreadcrumbs(
			output.Breadcrumb{Action: "approve", Command: "hey screener approve <id>", Description: "Let a sender through"},
			output.Breadcrumb{Action: "deny", Command: "hey screener deny <id>", Description: "Turn a sender away"},
		),
	)
}

func (c *screenerListCommand) runCount(cmd *cobra.Command) error {
	count, err := sdk.Clearances().PendingCount(cmd.Context())
	if err != nil {
		return convertSDKError(err)
	}
	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "%d %s waiting to be screened\n", count, senderNoun(count))
		return nil
	}
	return writeOK(map[string]any{"pending_count": count},
		output.WithSummary(fmt.Sprintf("%d %s waiting", count, senderNoun(count))),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "list", Command: "hey screener list", Description: "See who is waiting"}),
	)
}

type pendingPageFetcher func(context.Context, string) (*generated.ClearanceSummary, error)

func collectPendingClearances(ctx context.Context, startPage int, all bool, fetch pendingPageFetcher) ([]pendingClearance, int, bool, error) {
	if fetch == nil {
		return nil, 0, false, fmt.Errorf("collectPendingClearances: fetch function is nil")
	}
	page := max(startPage, 1)
	var pending []pendingClearance
	for pages := 1; pages <= maxScreenerPages; pages++ {
		summary, err := fetch(ctx, strconv.Itoa(page))
		if err != nil {
			return nil, pages - 1, false, err
		}
		if summary == nil || len(summary.Clearances) == 0 {
			return pending, pages, false, nil
		}
		for _, clearance := range summary.Clearances {
			pending = append(pending, pendingClearanceFor(clearance))
		}
		if !all {
			return pending, 1, false, nil
		}
		if pages == maxScreenerPages {
			return pending, pages, true, nil
		}
		page++
	}
	return pending, maxScreenerPages, true, nil
}

func pendingClearanceFor(clearance generated.Clearance) pendingClearance {
	return pendingClearance{
		ID:      clearance.Id,
		Name:    clearance.Petitioner.Name,
		Email:   clearance.Petitioner.EmailAddress,
		Subject: clearance.MostRecentEntry.Subject,
		Summary: clearance.MostRecentEntry.Summary,
		TopicID: clearance.MostRecentEntry.TopicId,
	}
}

func screenerTruncationNotice(startPage, pages int, truncated bool) string {
	if !truncated {
		return ""
	}
	return fmt.Sprintf("Screener listing stopped after %d pages. Continue with --page %d.", pages, startPage+pages)
}
