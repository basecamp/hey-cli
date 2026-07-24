package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type collectionsCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newCollectionsCommand() *collectionsCommand {
	collectionsCommand := &collectionsCommand{}
	collectionsCommand.cmd = &cobra.Command{
		Use:   "collections",
		Short: "List collections",
		Annotations: map[string]string{
			"agent_notes": "Returns all email collections (labels). Sorted by name. Use --json for full detail.",
		},
		Example: `  hey collections
  hey collections --limit 5
  hey collections --json`,
		RunE: collectionsCommand.run,
	}

	collectionsCommand.cmd.Flags().IntVar(&collectionsCommand.limit, "limit", 0, "Maximum number of collections to show")
	collectionsCommand.cmd.Flags().BoolVar(&collectionsCommand.all, "all", false, "Fetch all results (override --limit)")

	return collectionsCommand
}

func (c *collectionsCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()

	// Collections have no dedicated SDK service; fetch via the raw JSON endpoint.
	resp, err := sdk.Get(ctx, "/collections.json")
	if err != nil {
		return convertSDKError(err)
	}

	var collections []generated.Collection
	if err := resp.UnmarshalData(&collections); err != nil {
		return fmt.Errorf("decode collections: %w", err)
	}

	sort.Slice(collections, func(i, j int) bool {
		return collections[i].Name < collections[j].Name
	})

	total := len(collections)
	if c.limit > 0 && !c.all && len(collections) > c.limit {
		collections = collections[:c.limit]
	}
	notice := output.TruncationNotice(len(collections), total)

	if writer.IsStyled() {
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Name", "Updated"})
		for _, col := range collections {
			table.addRow([]string{
				fmt.Sprintf("%d", col.Id),
				col.Name,
				col.UpdatedAt.Format("2006-01-02"),
			})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(collections,
		output.WithSummary(fmt.Sprintf("%d collections", len(collections))),
		output.WithNotice(notice),
	)
}
