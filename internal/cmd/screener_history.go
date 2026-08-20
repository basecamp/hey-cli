package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type screenerHistoryCommand struct {
	cmd  *cobra.Command
	page int
	all  bool
}

type screenedClearance struct {
	ID      int64  `json:"id"`
	Status  string `json:"status"`
	Name    string `json:"name,omitempty"`
	Email   string `json:"email_address,omitempty"`
	Decided string `json:"decided_at,omitempty"`
}

func newScreenerHistoryCommand() *screenerHistoryCommand {
	historyCommand := &screenerHistoryCommand{}
	historyCommand.cmd = &cobra.Command{
		Use:   "history",
		Short: "List the senders already screened",
		Long:  "Review who has been approved or denied, most recent decision first.",
		Annotations: map[string]string{
			"agent_notes": "Only senders already decided on — the pending queue is `hey screener list`. Change a decision with `hey screener approve <id>` or `hey screener deny <id>` using the ID from here.",
		},
		Example: `  hey screener history
  hey screener history --json
  hey screener history --all --json`,
		RunE: historyCommand.run,
		Args: cobra.NoArgs,
	}
	historyCommand.cmd.Flags().IntVar(&historyCommand.page, "page", 1, "Results page")
	historyCommand.cmd.Flags().BoolVar(&historyCommand.all, "all", false, "Fetch up to 100 results pages from --page onward")
	return historyCommand
}

func (c *screenerHistoryCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if c.page < 1 {
		return output.ErrUsage("--page must be at least 1")
	}

	screened, pages, truncated, err := collectScreenedClearances(cmd.Context(), c.page, c.all,
		func(ctx context.Context, page string) ([]generated.Clearance, error) {
			clearances, listErr := sdk.Clearances().Screened(ctx, page)
			if listErr != nil {
				return nil, convertSDKError(listErr)
			}
			return clearances, nil
		})
	if err != nil {
		return err
	}
	notice := screenerTruncationNotice(c.page, pages, truncated)

	if writer.IsStyled() {
		if len(screened) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Nobody has been screened yet")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Status", "Sender", "Email", "Decided"})
		for _, clearance := range screened {
			table.addRow([]string{
				fmt.Sprintf("%d", clearance.ID),
				clearance.Status,
				truncate(clearance.Name, 24),
				truncate(clearance.Email, 32),
				clearance.Decided,
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
	return writeOK(screened,
		output.WithSummary(fmt.Sprintf("%d %s screened", len(screened), senderNoun(len(screened)))),
		output.WithNotice(notice),
		output.WithMeta("page", c.page),
		output.WithMeta("pages_fetched", pages),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "rescreen",
			Command:     "hey screener approve <id>",
			Description: "Change a decision",
		}),
	)
}

type screenedPageFetcher func(context.Context, string) ([]generated.Clearance, error)

func collectScreenedClearances(ctx context.Context, startPage int, all bool, fetch screenedPageFetcher) ([]screenedClearance, int, bool, error) {
	if fetch == nil {
		return nil, 0, false, fmt.Errorf("collectScreenedClearances: fetch function is nil")
	}
	page := max(startPage, 1)
	var screened []screenedClearance
	for pages := 1; pages <= maxScreenerPages; pages++ {
		clearances, err := fetch(ctx, strconv.Itoa(page))
		if err != nil {
			return nil, pages - 1, false, err
		}
		if len(clearances) == 0 {
			return screened, pages, false, nil
		}
		for _, clearance := range clearances {
			screened = append(screened, screenedClearanceFor(clearance))
		}
		if !all {
			return screened, 1, false, nil
		}
		if pages == maxScreenerPages {
			return screened, pages, true, nil
		}
		page++
	}
	return screened, maxScreenerPages, true, nil
}

func screenedClearanceFor(clearance generated.Clearance) screenedClearance {
	return screenedClearance{
		ID:      clearance.Id,
		Status:  clearance.Status,
		Name:    clearance.Petitioner.Name,
		Email:   clearance.Petitioner.EmailAddress,
		Decided: formatDate(clearance.UpdatedAt),
	}
}
