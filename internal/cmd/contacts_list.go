package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

const maxContactPages = 100

type contactsListCommand struct {
	cmd  *cobra.Command
	page int
	all  bool
}

type contactPageFetcher func(context.Context, *generated.ListContactsParams) (*generated.ListContactsResponseContent, error)

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
		return output.ErrUsage("--page must be at least 1")
	}

	contacts, pages, truncated, err := collectContacts(cmd.Context(), c.page, c.all,
		func(ctx context.Context, params *generated.ListContactsParams) (*generated.ListContactsResponseContent, error) {
			result, listErr := sdk.Contacts().List(ctx, params)
			if listErr != nil {
				return nil, convertSDKError(listErr)
			}
			return result, nil
		})
	if err != nil {
		return err
	}
	notice := contactTruncationNotice(c.page, pages, truncated)

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
		output.WithMeta("pages_fetched", pages),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey contacts show <id>",
			Description: "View a contact",
		}),
	)
}

func collectContacts(ctx context.Context, startPage int, all bool, fetch contactPageFetcher) ([]generated.Contact, int, bool, error) {
	if fetch == nil {
		return nil, 0, false, fmt.Errorf("collectContacts: fetch function is nil")
	}
	page := max(startPage, 1)
	var contacts []generated.Contact
	for pages := 1; pages <= maxContactPages; pages++ {
		pageValue := strconv.Itoa(page)
		params := &generated.ListContactsParams{Page: &pageValue}
		result, err := fetch(ctx, params)
		if err != nil {
			return nil, pages - 1, false, err
		}
		if result == nil || len(*result) == 0 {
			return contacts, pages, false, nil
		}
		contacts = append(contacts, (*result)...)
		if !all {
			return contacts, 1, false, nil
		}
		if pages == maxContactPages {
			return contacts, pages, true, nil
		}
		page++
	}
	return contacts, maxContactPages, true, nil
}

func contactTruncationNotice(startPage, pages int, truncated bool) string {
	if !truncated {
		return ""
	}
	return fmt.Sprintf("Contact listing stopped after %d pages. Continue with --page %d.", pages, startPage+pages)
}
