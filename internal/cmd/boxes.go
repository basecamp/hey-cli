package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type boxesCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newBoxListCommand() *boxesCommand {
	command := newBoxesListingCommand("list", `  hey box list
  hey box list --limit 5
  hey box list --json`)
	command.cmd.Args = cobra.NoArgs
	return command
}

func newBoxesListingCommand(use, example string) *boxesCommand {
	command := &boxesCommand{}
	command.cmd = &cobra.Command{
		Use:   use,
		Short: "List your HEY boxes",
		Annotations: map[string]string{
			"agent_notes": "Returns all mailbox types. Use --ids-only to pipe IDs to hey box view.",
		},
		Example: example,
		RunE:    command.run,
	}

	command.cmd.Flags().IntVar(&command.limit, "limit", 0, "Maximum number of boxes to show")
	command.cmd.Flags().BoolVar(&command.all, "all", false, "Fetch all results (override --limit)")

	return command
}

func (c *boxesCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	result, err := sdk.Boxes().List(ctx)
	if err != nil {
		return apierr.FromSDK(err)
	}

	var boxes []generated.Box
	if result != nil {
		boxes = *result
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
			table.addRow([]string{fmt.Sprintf("%d", b.Id), b.Kind, b.Name})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(boxes,
		output.WithSummary(fmt.Sprintf("%d mailboxes", len(boxes))),
		output.WithNotice(notice),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "view",
			Command:     "hey box view <name>",
			Description: "View email threads in a box",
		}),
	)
}
