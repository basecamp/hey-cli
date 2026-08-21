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

const maxContactPages = 100

type contactsListCommand struct {
	cmd  *cobra.Command
	page int
	all  bool
}

func newContactsListCommand() *contactsListCommand {
	listCommand := &contactsListCommand{}
	listCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List contacts",
		Annotations: map[string]string{
			"agent_notes": "Returns contact IDs, names, and email addresses. Use --all to fetch up to 100 pages.",
		},
		Example: `  hey contacts list
  hey contacts list --page 2 --json
  hey contacts list --all --json`,
		RunE: listCommand.run,
		Args: cobra.NoArgs,
	}
	listCommand.cmd.Flags().IntVar(&listCommand.page, "page", 1, "Results page")
	listCommand.cmd.Flags().BoolVar(&listCommand.all, "all", false, "Fetch up to 100 results pages from --page onward")
	return listCommand
}

func (c *contactsListCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	if c.page < 1 {
		return apierr.ErrUsage("--page must be at least 1")
	}

	first, err := readContactsPage(cmd.Context(), strconv.Itoa(c.page))
	if err != nil {
		return err
	}
	collected, err := collectPages(cmd.Context(), first, pageRequest{All: c.all, MaxPages: maxContactPages}, readContactsPage)
	if err != nil {
		return err
	}
	contacts := collected.Items
	notice := contactTruncationNotice(c.page, collected.Read, collected.Truncated)

	if writer.IsStyled() {
		if len(contacts) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No contacts found")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Name", "Email", "Updated"})
		for _, contact := range contacts {
			table.addRow([]string{
				fmt.Sprintf("%d", contact.Id),
				truncate(contact.Name, 32),
				truncate(contact.EmailAddress, 42),
				formatDate(contact.UpdatedAt),
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
	return writeOK(contacts,
		output.WithSummary(fmt.Sprintf("%d %s", len(contacts), contactNoun(len(contacts)))),
		output.WithNotice(notice),
		output.WithMeta("page", c.page),
		output.WithMeta("pages_fetched", collected.Read),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey contacts show <id>",
			Description: "View a contact",
		}),
	)
}

func readContactsPage(ctx context.Context, cursor string) (pageResult[generated.Contact], error) {
	page, err := strconv.Atoi(cursor)
	if err != nil {
		return pageResult[generated.Contact]{}, fmt.Errorf("unreadable contacts page %q: %w", cursor, err)
	}

	result, err := sdk.Contacts().List(ctx, &generated.ListContactsParams{Page: &cursor})
	if err != nil {
		return pageResult[generated.Contact]{}, apierr.FromSDK(err)
	}
	if result == nil {
		return pageResult[generated.Contact]{}, nil
	}
	return pageResult[generated.Contact]{Items: *result, Cursor: strconv.Itoa(page + 1)}, nil
}

func contactTruncationNotice(startPage, pages int, truncated bool) string {
	if !truncated {
		return ""
	}
	return fmt.Sprintf("Contact listing stopped after %d pages. Continue with --page %d.", pages, startPage+pages)
}
