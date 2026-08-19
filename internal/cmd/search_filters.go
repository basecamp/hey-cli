package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type searchFiltersCommand struct {
	cmd *cobra.Command
}

type searchFilters struct {
	Boxes       []generated.SearchFilterItem `json:"boxes"`
	Dates       []generated.SearchFilterItem `json:"dates"`
	Labels      []generated.SearchFilterItem `json:"labels"`
	Attachments []generated.SearchFilterItem `json:"attachments"`
}

func newSearchFiltersCommand() *searchFiltersCommand {
	filtersCommand := &searchFiltersCommand{}
	filtersCommand.cmd = &cobra.Command{
		Use:   "filters",
		Short: "List available search refinement values",
		Annotations: map[string]string{
			"agent_notes": "Returns the current values accepted by --in, --date, --label, and --attachment.",
		},
		RunE: filtersCommand.run,
		Args: cobra.NoArgs,
	}
	return filtersCommand
}

func (c *searchFiltersCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	result, err := sdk.Search().Filters(cmd.Context())
	if err != nil {
		return convertSDKError(err)
	}
	filters := makeSearchFilters(result)

	if writer.IsStyled() {
		printSearchFilterGroup(cmd, "Boxes", filters.Boxes)
		printSearchFilterGroup(cmd, "Dates", filters.Dates)
		printSearchFilterGroup(cmd, "Labels", filters.Labels)
		printSearchFilterGroup(cmd, "Attachments", filters.Attachments)
		return nil
	}

	return writeOK(filters,
		output.WithSummary("Available search filters"),
	)
}

func makeSearchFilters(result *generated.AdvancedSearchFilters) searchFilters {
	if result == nil {
		return searchFilters{}
	}
	return searchFilters{
		Boxes:       result.RefineIn,
		Dates:       result.RefineDates,
		Labels:      result.RefineLabels,
		Attachments: result.RefineAttachments,
	}
}

func printSearchFilterGroup(cmd *cobra.Command, title string, items []generated.SearchFilterItem) {
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", title)
	if len(items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  (none)")
		fmt.Fprintln(cmd.OutOrStdout())
		return
	}
	table := newTable(cmd.OutOrStdout())
	table.addRow([]string{"Value", "Description"})
	for _, item := range items {
		table.addRow([]string{item.Value, item.Title})
	}
	table.print()
	fmt.Fprintln(cmd.OutOrStdout())
}
