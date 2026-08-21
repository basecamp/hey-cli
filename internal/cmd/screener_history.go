package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
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
		return apierr.ErrUsage("--page must be at least 1")
	}

	first, err := readScreenedClearances(cmd.Context(), strconv.Itoa(c.page))
	if err != nil {
		return err
	}
	collected, err := collectPages(cmd.Context(), first, pageRequest{All: c.all, MaxPages: maxScreenerPages}, readScreenedClearances)
	if err != nil {
		return err
	}
	screened := collected.Items
	notice := screenerTruncationNotice(c.page, collected.Read, collected.Truncated)

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
		output.WithMeta("pages_fetched", collected.Read),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "rescreen",
			Command:     "hey screener approve <id>",
			Description: "Change a decision",
		}),
	)
}

func readScreenedClearances(ctx context.Context, cursor string) (pageResult[screenedClearance], error) {
	page, err := nextScreenerPage(cursor)
	if err != nil {
		return pageResult[screenedClearance]{}, err
	}

	clearances, err := sdk.Clearances().Screened(ctx, cursor)
	if err != nil {
		return pageResult[screenedClearance]{}, apierr.FromSDK(err)
	}

	var screened []screenedClearance
	for _, clearance := range clearances {
		screened = append(screened, screenedClearanceFor(clearance))
	}
	return pageResult[screenedClearance]{Items: screened, Cursor: page}, nil
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
