package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type boxesCommand struct {
	cmd   *cobra.Command
	limit int
}

func newBoxesCommand() *boxesCommand {
	boxesCommand := &boxesCommand{}
	boxesCommand.cmd = &cobra.Command{
		Use:   "boxes",
		Short: "List mailboxes",
		Example: `  hey boxes
  hey boxes --limit 5
  hey boxes --json`,
		RunE: boxesCommand.run,
	}

	boxesCommand.cmd.Flags().IntVar(&boxesCommand.limit, "limit", 0, "Maximum number of boxes to show")

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

	if c.limit > 0 && len(boxes) > c.limit {
		boxes = boxes[:c.limit]
	}

	if writer.IsStyled() {
		table := newTable()
		table.addRow([]string{"ID", "Kind", "Name"})
		for _, b := range boxes {
			table.addRow([]string{fmt.Sprintf("%d", b.ID), b.Kind, b.Name})
		}
		table.print()
		return nil
	}

	return writer.OK(boxes,
		output.WithSummary(fmt.Sprintf("%d mailboxes", len(boxes))),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey box <name>",
			Description: "View postings in a box",
		}),
	)
}
