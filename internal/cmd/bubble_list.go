package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type bubbleListCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

type bubbleListOutput struct {
	BubbledUp         []sourcePostingOutput `json:"bubbled_up"`
	Scheduled         []sourcePostingOutput `json:"scheduled"`
	ScheduledNextPage string                `json:"scheduled_next_page,omitempty"`
}

type bubbleListRow struct {
	ID          int64  `json:"id"`
	TopicID     int64  `json:"topic_id,omitempty"`
	From        string `json:"from,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Status      string `json:"status"`
	BubblesUpAt string `json:"bubbles_up_at,omitempty"`
}

func newBubbleListCommand() *bubbleListCommand {
	bubbleListCommand := &bubbleListCommand{}
	bubbleListCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List bubbled-up and scheduled email threads",
		Long:  "List the email threads that have bubbled up in the Imbox and the ones scheduled to bubble up, with when each is due.",
		Example: `  hey bubble list
  hey bubble list --json`,
		Annotations: map[string]string{
			"agent_notes": "Lists two buckets: bubbled_up, the threads back in the Imbox, and scheduled, the threads waiting in Bubble Up with bubble_up_schedule.bubble_up_at saying when. Use id with hey bubble pop; use topic_id with hey thread read. --limit caps each bucket.",
		},
		RunE: bubbleListCommand.run,
		Args: cobra.NoArgs,
	}

	bubbleListCommand.cmd.Flags().IntVar(&bubbleListCommand.limit, "limit", 0, "Maximum number of threads to show per bucket")
	bubbleListCommand.cmd.Flags().BoolVar(&bubbleListCommand.all, "all", false, "Fetch all results (override --limit)")

	return bubbleListCommand
}

func (c *bubbleListCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	imbox, bubblebox, err := bubbleSources(cmd.Context())
	if err != nil {
		return err
	}

	request := pageRequest{Limit: c.limit, All: c.all, MaxPages: maxPostingPages}

	bubbled, err := readBubbledUp(cmd.Context(), imbox, request)
	if err != nil {
		return err
	}

	scheduled, err := readScheduled(cmd.Context(), bubblebox, request)
	if err != nil {
		return err
	}

	if request.Limit > 0 && !request.All {
		if len(bubbled) > request.Limit {
			bubbled = bubbled[:request.Limit]
		}
		if len(scheduled.Items) > request.Limit {
			scheduled.Items = scheduled.Items[:request.Limit]
			scheduled.Cursor = ""
		}
	}

	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		return c.writeStyled(cmd, bubbled, scheduled.Items)
	case output.FormatIDs, output.FormatCount:
		return writeOK(append(bubbled, scheduled.Items...))
	case output.FormatMarkdown:
		return c.writeMarkdown(cmd, bubbled, scheduled.Items)
	default:
		return writeOK(bubbleListOutput{
			BubbledUp:         makeSourcePostings(bubbled),
			Scheduled:         makeSourcePostings(scheduled.Items),
			ScheduledNextPage: scheduled.Cursor,
		}, output.WithSummary(fmt.Sprintf("%d bubbled up, %d scheduled", len(bubbled), len(scheduled.Items))))
	}
}

func (c *bubbleListCommand) writeStyled(cmd *cobra.Command, bubbled, scheduled []generated.Posting) error {
	fmt.Fprintln(cmd.OutOrStdout(), "Bubbled up:")
	fmt.Fprintln(cmd.OutOrStdout())
	table := newTable(cmd.OutOrStdout())
	table.addRow([]string{"ID", "Thread", "From", "Summary", "Date"})
	for _, posting := range bubbled {
		table.addRow([]string{
			fmt.Sprintf("%d", posting.Id),
			postingTopicIDCell(posting),
			terminal.SanitizeLine(posting.Creator.Name),
			truncate(terminal.SanitizeLine(posting.Summary), 60),
			formatDate(posting.CreatedAt),
		})
	}
	table.print()

	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Scheduled to bubble up:")
	fmt.Fprintln(cmd.OutOrStdout())
	table = newTable(cmd.OutOrStdout())
	table.addRow([]string{"ID", "Thread", "From", "Summary", "Bubbles up"})
	for _, posting := range scheduled {
		table.addRow([]string{
			fmt.Sprintf("%d", posting.Id),
			postingTopicIDCell(posting),
			terminal.SanitizeLine(posting.Creator.Name),
			truncate(terminal.SanitizeLine(posting.Summary), 60),
			bubblesUpAt(posting),
		})
	}
	table.print()
	return nil
}

func (c *bubbleListCommand) writeMarkdown(cmd *cobra.Command, bubbled, scheduled []generated.Posting) error {
	fmt.Fprintf(cmd.OutOrStdout(), "# Bubble Up\n\n")
	rows := make([]bubbleListRow, 0, len(bubbled)+len(scheduled))
	for _, posting := range bubbled {
		rows = append(rows, bubbleListRow{
			ID:      posting.Id,
			TopicID: resolvePostingTopicID(posting),
			From:    posting.Creator.Name,
			Summary: posting.Summary,
			Status:  "bubbled_up",
		})
	}
	for _, posting := range scheduled {
		rows = append(rows, bubbleListRow{
			ID:          posting.Id,
			TopicID:     resolvePostingTopicID(posting),
			From:        posting.Creator.Name,
			Summary:     posting.Summary,
			Status:      "scheduled",
			BubblesUpAt: bubblesUpAt(posting),
		})
	}
	return writeOK(rows)
}

// bubbleSources names the two boxes the listing reads: the Imbox, whose ordering puts
// bubbled-up threads first, and Bubble Up, which holds the scheduled ones.
func bubbleSources(ctx context.Context) (imbox, bubblebox mail.Source, err error) {
	boxes, err := sdk.Boxes().List(ctx)
	if err != nil {
		return mail.Source{}, mail.Source{}, apierr.FromSDK(err)
	}

	for _, box := range *boxes {
		switch box.Kind {
		case hey.BoxKindImbox:
			imbox = mail.ListedBoxSource(box)
		case hey.BoxKindBubbleUp:
			bubblebox = mail.ListedBoxSource(box)
		}
	}

	if imbox.ID == 0 {
		return mail.Source{}, mail.Source{}, apierr.ErrNotFound("box", "imbox")
	}
	if bubblebox.ID == 0 {
		return mail.Source{}, mail.Source{}, apierr.ErrNotFound("box", "bubblebox")
	}
	return imbox, bubblebox, nil
}

// readBubbledUp reads the Imbox from the top and keeps its bubbled-up prefix. HEY orders
// the Imbox bubbled-up first — that ordering is what draws the web app's Bubbled Up
// section — so the first row that is not bubbled up ends the read.
func readBubbledUp(ctx context.Context, imbox mail.Source, request pageRequest) ([]generated.Posting, error) {
	var rows []generated.Posting
	cursor := ""

	for read := 0; read < request.MaxPages; read++ {
		page, err := mail.ReadPage(ctx, sdk, imbox, cursor)
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		for _, posting := range page.Postings {
			if !posting.BubbledUp {
				return rows, nil
			}
			rows = append(rows, posting)
		}
		if len(page.Postings) == 0 || page.Cursor == "" {
			return rows, nil
		}
		if request.Limit > 0 && !request.All && len(rows) >= request.Limit {
			return rows, nil
		}
		cursor = page.Cursor
	}
	return rows, nil
}

func readScheduled(ctx context.Context, bubblebox mail.Source, request pageRequest) (collectedPages[generated.Posting], error) {
	first, err := mail.ReadPage(ctx, sdk, bubblebox, "")
	if err != nil {
		return collectedPages[generated.Posting]{}, apierr.FromSDK(err)
	}
	seed := pageResult[generated.Posting]{Items: first.Postings, Cursor: first.Cursor, Total: first.Total}
	return collectPages(ctx, seed, request, readSourcePage(bubblebox))
}

// bubblesUpAt is when a scheduled posting resurfaces, in the reader's zone. A surprise
// schedule shows the web app's "???" — its time is HEY's secret to keep.
func bubblesUpAt(posting generated.Posting) string {
	schedule := posting.BubbleUpSchedule
	if schedule.SurpriseMe {
		return "???"
	}
	if schedule.BubbleUpAt.IsZero() {
		return ""
	}
	return schedule.BubbleUpAt.Local().Format("2006-01-02 15:04")
}

func postingTopicIDCell(posting generated.Posting) string {
	if id := resolvePostingTopicID(posting); id != 0 {
		return fmt.Sprintf("%d", id)
	}
	return ""
}
