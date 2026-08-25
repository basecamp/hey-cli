package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type screenerHistoryCommand struct {
	cmd  *cobra.Command
	page string
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
			"agent_notes": "Only senders already decided on — the pending queue is `hey screener list`. Change a decision with `hey screener approve <id>` or `hey screener deny <id>` using the ID from here. --page continues from the next_page cursor of an earlier listing.",
		},
		Example: `  hey screener history
  hey screener history --json
  hey screener history --all --json`,
		RunE: historyCommand.run,
		Args: cobra.NoArgs,
	}
	historyCommand.cmd.Flags().StringVar(&historyCommand.page, "page", "", "Continue from a next_page cursor")
	historyCommand.cmd.Flags().BoolVar(&historyCommand.all, "all", false, "Fetch all results (up to 100 pages)")
	return historyCommand
}

func (c *screenerHistoryCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	first, err := readScreenedClearances(cmd.Context(), c.page)
	if err != nil {
		return err
	}
	collected, err := collectPages(cmd.Context(), first, pageRequest{All: c.all, MaxPages: maxScreenerPages}, readScreenedClearances)
	if err != nil {
		return err
	}
	screened := collected.Items
	notice := screenerTruncationNotice(collected.Read, collected.Truncated)

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
	opts := []output.ResponseOption{
		output.WithSummary(fmt.Sprintf("%d %s screened", len(screened), senderNoun(len(screened)))),
		output.WithNotice(notice),
		output.WithMeta("pages_fetched", collected.Read),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "rescreen",
			Command:     "hey screener approve <id>",
			Description: "Change a decision",
		}),
	}
	if collected.Cursor != "" {
		opts = append(opts, output.WithMeta("next_page", collected.Cursor))
	}
	return writeOK(screened, opts...)
}

// readScreenedClearances reads one page of decisions, following the opaque
// geared_pagination cursor the way readPendingClearances does.
func readScreenedClearances(ctx context.Context, cursor string) (pageResult[screenedClearance], error) {
	page, err := sdk.Clearances().ScreenedPage(ctx, cursor)
	if err != nil {
		return pageResult[screenedClearance]{}, apierr.FromSDK(err)
	}
	if page == nil {
		return pageResult[screenedClearance]{}, nil
	}

	var screened []screenedClearance
	for _, clearance := range page.Clearances {
		screened = append(screened, screenedClearanceFor(clearance))
	}
	return pageResult[screenedClearance]{Items: screened, Cursor: page.NextPage}, nil
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
