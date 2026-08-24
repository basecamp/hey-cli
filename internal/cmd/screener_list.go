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

const maxScreenerPages = 100

type screenerListCommand struct {
	cmd  *cobra.Command
	page int
	all  bool
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
			"agent_notes": "Returns clearance IDs with the sender and the subject of what they sent. Feed the ID to `hey screener approve` or `hey screener deny`, and topic_id to `hey thread read` to read the thread first. Use --count for the number alone, which is a much cheaper request.",
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
	return listCommand
}

func (c *screenerListCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if c.page < 1 {
		return apierr.ErrUsage("--page must be at least 1")
	}
	if writer.EffectiveFormat() == output.FormatCount {
		return c.runCount(cmd)
	}

	first, err := readPendingClearances(cmd.Context(), strconv.Itoa(c.page))
	if err != nil {
		return err
	}
	collected, err := collectPages(cmd.Context(), first, pageRequest{All: c.all, MaxPages: maxScreenerPages}, readPendingClearances)
	if err != nil {
		return err
	}
	pending := collected.Items
	notice := screenerTruncationNotice(c.page, collected.Read, collected.Truncated)

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
		output.WithMeta("pages_fetched", collected.Read),
		output.WithBreadcrumbs(
			output.Breadcrumb{Action: "approve", Command: "hey screener approve <id>", Description: "Let a sender through"},
			output.Breadcrumb{Action: "deny", Command: "hey screener deny <id>", Description: "Turn a sender away"},
		),
	)
}

// runCount answers the global --count with the number alone, which is a much cheaper
// request than the queue. It writes the number itself: --count is a bare count on every
// other command, and `n=$(hey screener list --count)` has to be that number.
func (c *screenerListCommand) runCount(cmd *cobra.Command) error {
	count, err := sdk.Clearances().PendingCount(cmd.Context())
	if err != nil {
		return apierr.FromSDK(err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), count)
	return nil
}

// readPendingClearances reads one page of the queue. The Screener numbers its pages, so
// the cursor is the next page's number.
func readPendingClearances(ctx context.Context, cursor string) (pageResult[pendingClearance], error) {
	page, err := nextScreenerPage(cursor)
	if err != nil {
		return pageResult[pendingClearance]{}, err
	}

	summary, err := sdk.Clearances().Pending(ctx, cursor)
	if err != nil {
		return pageResult[pendingClearance]{}, apierr.FromSDK(err)
	}
	if summary == nil {
		return pageResult[pendingClearance]{}, nil
	}

	var pending []pendingClearance
	for _, clearance := range summary.Clearances {
		pending = append(pending, pendingClearanceFor(clearance))
	}
	return pageResult[pendingClearance]{Items: pending, Cursor: page}, nil
}

func nextScreenerPage(cursor string) (string, error) {
	page, err := strconv.Atoi(cursor)
	if err != nil {
		return "", fmt.Errorf("unreadable screener page %q: %w", cursor, err)
	}
	return strconv.Itoa(page + 1), nil
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
