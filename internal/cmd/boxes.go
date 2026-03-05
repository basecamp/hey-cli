package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type boxesCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newBoxesCommand() *boxesCommand {
	boxesCommand := &boxesCommand{}
	boxesCommand.cmd = &cobra.Command{
		Use:   "boxes",
		Short: "List mailboxes",
		Annotations: map[string]string{
			"agent_notes": "Returns all mailbox types. Use --ids-only to pipe IDs to hey box.",
		},
		Example: `  hey boxes
  hey boxes --limit 5
  hey boxes --json`,
		RunE: boxesCommand.run,
	}

	boxesCommand.cmd.Flags().IntVar(&boxesCommand.limit, "limit", 0, "Maximum number of boxes to show")
	boxesCommand.cmd.Flags().BoolVar(&boxesCommand.all, "all", false, "Fetch all results (override --limit)")

	return boxesCommand
}

func (c *boxesCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	boxes, err := apiClient.ListBoxes()
	if err != nil {
		return err
	}

	total := len(boxes)
	if c.limit > 0 && !c.all && len(boxes) > c.limit {
		boxes = boxes[:c.limit]
	}
	notice := output.TruncationNotice(len(boxes), total)

	if writer.IsStyled() {
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Kind", "Name"})
		for _, b := range boxes {
			table.addRow([]string{fmt.Sprintf("%d", b.ID), b.Kind, b.Name})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writer.OK(boxes, statsOption(),
		output.WithSummary(fmt.Sprintf("%d mailboxes", len(boxes))),
		output.WithNotice(notice),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey box <name>",
			Description: "View postings in a box",
		}),
	)
}
