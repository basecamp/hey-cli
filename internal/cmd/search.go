package cmd

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

type searchCommand struct {
	cmd  *cobra.Command
	page int
}

func newSearchCommand() *searchCommand {
	command := &searchCommand{}
	command.cmd = &cobra.Command{
		Use:   "search <query...>",
		Short: "Search email",
		Long:  "Search email topics by keyword or phrase using HEY's advanced search.",
		Annotations: map[string]string{
			"agent_notes": "Returns matching email topics. Use a result ID with hey threads to read the full conversation.",
		},
		Example: `  hey search "quarterly planning"
  hey search invoice --page 2
  hey search "project update" --json`,
		RunE: command.run,
		Args: usageMinOneArg(),
	}

	command.cmd.Flags().IntVar(&command.page, "page", 1, "Result page (starting at 1)")

	return command
}

func (c *searchCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return output.ErrUsage("search query cannot be empty")
	}
	if c.page < 1 {
		return output.ErrUsage("--page must be at least 1")
	}

	params := url.Values{"q": []string{query}}
	if c.page > 1 {
		params.Set("page", strconv.Itoa(c.page))
	}

	resp, err := sdk.GetHTML(cmd.Context(), "/advanced_search?"+params.Encode())
	if err != nil {
		return convertSDKError(err)
	}
	page := htmlutil.ParseSearchResultsHTML(string(resp.Data))
	results := page.Results
	notice := searchPageNotice(page.NextPage)

	if writer.IsStyled() {
		if len(results) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "No results for %q.\n", query)
			return nil
		}

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Thread", "Subject", "Summary", "Date"})
		for _, result := range results {
			table.addRow([]string{
				fmt.Sprintf("%d", result.ID),
				truncate(result.Subject, 48),
				truncate(result.Summary, 60),
				searchResultDate(result.ActiveAt),
			})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(results,
		output.WithSummary(searchSummary(len(results), query)),
		output.WithNotice(notice),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "read",
			Command:     "hey threads <id>",
			Description: "Read a matching email thread",
		}),
	)
}

func searchSummary(count int, query string) string {
	noun := "results"
	if count == 1 {
		noun = "result"
	}
	return fmt.Sprintf("%d search %s for %q", count, noun, query)
}

func searchPageNotice(nextPage int) string {
	if nextPage < 1 {
		return ""
	}
	return fmt.Sprintf("More results available. Use --page %d.", nextPage)
}

func searchResultDate(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return formatDate(parsed)
}
