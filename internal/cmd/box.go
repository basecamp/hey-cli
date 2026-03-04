package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/models"
	"github.com/basecamp/hey-cli/internal/output"
)

type boxCommand struct {
	cmd   *cobra.Command
	limit int
}

func newBoxCommand() *boxCommand {
	boxCommand := &boxCommand{}
	boxCommand.cmd = &cobra.Command{
		Use:   "box <name|id>",
		Short: "List postings in a mailbox",
		Long:  "List postings in a mailbox. Accepts a box name (imbox, feedbox, etc.) or numeric ID.",
		Example: `  hey box imbox
  hey box imbox --limit 10
  hey box 123 --json`,
		RunE: boxCommand.run,
		Args: cobra.ExactArgs(1),
	}

	boxCommand.cmd.Flags().IntVar(&boxCommand.limit, "limit", 0, "Maximum number of postings to show")

	return boxCommand
}

func (c *boxCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	boxID, err := c.resolveBoxID(args[0])
	if err != nil {
		return err
	}

	resp, err := apiClient.GetBox(boxID)
	if err != nil {
		return err
	}

	postings := resp.Postings
	if c.limit > 0 && len(postings) > c.limit {
		postings = postings[:c.limit]
	}

	if writer.IsStyled() {
		fmt.Printf("Box: %s (%s)\n\n", resp.Box.Name, resp.Box.Kind)

		table := newTable()
		table.addRow([]string{"Topic", "From", "Summary", "Date"})
		for _, raw := range postings {
			var p models.Posting
			if err := json.Unmarshal(raw, &p); err != nil {
				continue
			}
			date := ""
			if len(p.CreatedAt) >= 10 {
				date = p.CreatedAt[:10]
			}
			displayID := p.ID
			if tid := p.ResolveTopicID(); tid != 0 {
				displayID = tid
			}
			table.addRow([]string{fmt.Sprintf("%d", displayID), p.Creator.Name, truncate(p.Summary, 60), date})
		}
		table.print()
		return nil
	}

	resp.Postings = postings
	return writer.OK(resp,
		output.WithSummary(fmt.Sprintf("%d postings in %s", len(postings), resp.Box.Name)),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "read",
				Command:     "hey topic <id>",
				Description: "Read an email thread",
			},
			output.Breadcrumb{
				Action:      "compose",
				Command:     "hey compose --to <email> --subject <subject>",
				Description: "Compose a new message",
			},
		),
	)
}

func (c *boxCommand) resolveBoxID(nameOrID string) (int, error) {
	if id, err := strconv.Atoi(nameOrID); err == nil {
		return id, nil
	}

	boxes, err := apiClient.ListBoxes()
	if err != nil {
		return 0, err
	}

	nameOrID = strings.ToLower(nameOrID)
	for _, b := range boxes {
		if strings.ToLower(b.Kind) == nameOrID || strings.ToLower(b.Name) == nameOrID {
			return b.ID, nil
		}
	}

	return 0, output.ErrNotFound("box", nameOrID)
}
