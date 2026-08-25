package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

// maxDraftPages is how many pages of drafts one command reads, counting the first.
const maxDraftPages = 100

type draftsCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
	page  string
}

func newDraftCommand() *cobra.Command {
	draft := &cobra.Command{
		Use:   "draft",
		Short: "Manage unsent drafts",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: list, show, edit, send, delete. Draft IDs come from `hey draft list` or from `hey compose --draft`/`hey reply --draft`. list's --page continues from the next_page cursor of an earlier listing.",
		},
	}
	draft.AddCommand(newDraftsCommand().cmd)
	draft.AddCommand(newDraftShowCommand().cmd)
	draft.AddCommand(newDraftEditCommand().cmd)
	draft.AddCommand(newDraftSendCommand().cmd)
	draft.AddCommand(newDraftDeleteCommand().cmd)
	return draft
}

func newDraftsCommand() *draftsCommand {
	draftsCommand := &draftsCommand{}
	draftsCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List draft emails",
		Example: `  hey draft list
  hey draft list --limit 10
  hey draft list --all --json`,
		RunE: draftsCommand.run,
		Args: cobra.NoArgs,
	}

	draftsCommand.cmd.Flags().IntVar(&draftsCommand.limit, "limit", 0, "Maximum number of drafts to show")
	draftsCommand.cmd.Flags().BoolVar(&draftsCommand.all, "all", false, "Fetch all results (override --limit)")
	draftsCommand.cmd.Flags().StringVar(&draftsCommand.page, "page", "", "Continue from a next_page cursor")

	return draftsCommand
}

func (c *draftsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	first, err := readDraftsPage(cmd.Context(), c.page)
	if err != nil {
		return err
	}
	collected, err := collectPages(cmd.Context(), first, pageRequest{Limit: c.limit, All: c.all, MaxPages: maxDraftPages}, readDraftsPage)
	if err != nil {
		return err
	}

	drafts := collected.Items
	nextPage := collected.Cursor
	notice := draftsTruncationNotice(collected.Read, collected.Truncated)
	if c.limit > 0 && !c.all && len(drafts) > c.limit {
		drafts = drafts[:c.limit]
		nextPage = ""
		notice = output.TruncationNotice(len(drafts), len(collected.Items))
	}

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
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", notice)
		}
		return nil
	}
	if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}

	opts := []output.ResponseOption{
		output.WithSummary(fmt.Sprintf("%d drafts", len(drafts))),
		output.WithNotice(notice),
		output.WithMeta("pages_fetched", collected.Read),
	}
	if nextPage != "" {
		opts = append(opts, output.WithMeta("next_page", nextPage))
	}
	return writeOK(drafts, opts...)
}

// readDraftsPage reads one page of drafts. The index pages by geared_pagination's opaque
// cursor out of the Link header — a page number is answered with the first page forever
// — which is what ListDraftsPage exists for; an empty cursor is the first page.
func readDraftsPage(ctx context.Context, cursor string) (pageResult[generated.DraftMessage], error) {
	page, err := sdk.Entries().ListDraftsPage(ctx, cursor)
	if err != nil {
		return pageResult[generated.DraftMessage]{}, apierr.FromSDK(err)
	}
	if page == nil {
		return pageResult[generated.DraftMessage]{}, nil
	}
	return pageResult[generated.DraftMessage]{Items: page.Drafts, Cursor: page.NextPage}, nil
}

func draftsTruncationNotice(pages int, truncated bool) string {
	if !truncated {
		return ""
	}
	return fmt.Sprintf("Draft listing stopped after %d pages. Continue with --page using next_page.", pages)
}
