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
	cmd *cobra.Command
}

func newBoxCommand() *boxCommand {
	boxCommand := &boxCommand{}
	boxCommand.cmd = &cobra.Command{
		Use:   "box <name|id>",
		Short: "List postings in a mailbox",
		Long:  "List postings in a mailbox. Accepts a box name (imbox, feedbox, etc.) or numeric ID.",
		RunE:  boxCommand.run,
		Args:  cobra.ExactArgs(1),
	}

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
		return printRawJSON(data)
	}

	var resp models.BoxShowResponse
	if err := apiClient.GetJSON(path, &resp); err != nil {
		return err
	}

	fmt.Printf("Box: %s (%s)\n\n", resp.Box.Name, resp.Box.Kind)

	table := newTable()
	table.addRow([]string{"ID", "From", "Summary", "Date"})
	for _, raw := range resp.Postings {
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
