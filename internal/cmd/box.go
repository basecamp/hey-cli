package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"hey-cli/internal/models"
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

	path := fmt.Sprintf("/boxes/%d.json", boxID)

	if jsonOutput {
		data, err := apiClient.Get(path)
		if err != nil {
			return err
		}
		if c.limit > 0 {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(data, &obj); err == nil {
				if postings, ok := obj["postings"]; ok {
					obj["postings"] = json.RawMessage(limitJSONArray([]byte(postings), c.limit))
					if limited, err := json.Marshal(obj); err == nil {
						data = limited
					}
				}
			}
		}
		return printRawJSON(data)
	}

	var resp models.BoxShowResponse
	if err := apiClient.GetJSON(path, &resp); err != nil {
		return err
	}

	fmt.Printf("Box: %s (%s)\n\n", resp.Box.Name, resp.Box.Kind)

	postings := resp.Postings
	if c.limit > 0 && len(postings) > c.limit {
		postings = postings[:c.limit]
	}

	table := newTable()
	table.addRow([]string{"ID", "From", "Summary", "Date"})
	for _, raw := range postings {
		var p models.Posting
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		date := ""
		if len(p.CreatedAt) >= 10 {
			date = p.CreatedAt[:10]
		}
		table.addRow([]string{fmt.Sprintf("%d", p.ID), p.Creator.Name, truncate(p.Summary, 60), date})
	}
	table.print()
	return nil
}

func (c *boxCommand) resolveBoxID(nameOrID string) (int, error) {
	if id, err := strconv.Atoi(nameOrID); err == nil {
		return id, nil
	}

	var boxes []models.Box
	if err := apiClient.GetJSON("/boxes.json", &boxes); err != nil {
		return 0, fmt.Errorf("could not list boxes: %w", err)
	}

	nameOrID = strings.ToLower(nameOrID)
	for _, b := range boxes {
		if strings.ToLower(b.Kind) == nameOrID || strings.ToLower(b.Name) == nameOrID {
			return b.ID, nil
		}
	}

	return 0, fmt.Errorf("box %q not found", nameOrID)
}
