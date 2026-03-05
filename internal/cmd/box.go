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
	all   bool
}

func newBoxCommand() *boxCommand {
	boxCommand := &boxCommand{}
	boxCommand.cmd = &cobra.Command{
		Use:   "box <name|id>",
		Short: "List postings in a mailbox",
		Long:  "List postings in a mailbox. Accepts a box name (imbox, feedbox, etc.) or numeric ID.",
		Annotations: map[string]string{
			"agent_notes": "Accepts box name or numeric ID. Returns postings (threads). Use thread IDs with hey threads.",
		},
		Example: `  hey box imbox
  hey box imbox --limit 10
  hey box 123 --json`,
		RunE: boxCommand.run,
		Args: validateBoxArgs,
	}

	boxCommand.cmd.Flags().IntVar(&boxCommand.limit, "limit", 0, "Maximum number of postings to show")
	boxCommand.cmd.Flags().BoolVar(&boxCommand.all, "all", false, "Fetch all results (override --limit)")

	return boxCommand
}

func validateBoxArgs(cmd *cobra.Command, args []string) error {
	switch len(args) {
	case 1:
		return nil
	case 0:
		return usageErrorf("%s <name|id> (example: hey box imbox)", cmd.CommandPath())
	default:
		return fmt.Errorf("expected 1 mailbox argument, got %d", len(args))
	}
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
	total := len(postings)
	if c.limit > 0 && !c.all && len(postings) > c.limit {
		postings = postings[:c.limit]
	}
	notice := output.TruncationNotice(len(postings), total)

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Box: %s (%s)\n\n", resp.Box.Name, resp.Box.Kind)

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Thread", "From", "Summary", "Date"})
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
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	resp.Postings = postings
	return writeOK(resp,
		output.WithSummary(fmt.Sprintf("%d postings in %s", len(postings), resp.Box.Name)),
		output.WithNotice(notice),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "read",
				Command:     "hey threads <id>",
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
