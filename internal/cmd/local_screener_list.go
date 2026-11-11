package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type localScreenerListCommand struct {
	cmd *cobra.Command
}

type localSender struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	ScreenedIn *bool  `json:"screened_in,omitempty"`
}

type localScreener struct {
	Waiting      []localSender `json:"waiting"`
	WaitingCount int           `json:"waiting_count"`
	Decided      []localSender `json:"decided"`
}

func newLocalScreenerListCommand() *localScreenerListCommand {
	listCommand := &localScreenerListCommand{}
	listCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List who waits in The Screener, and who was decided",
		Annotations: map[string]string{
			"agent_notes": "waiting are first-time senders; decided are senders already screened in (screened_in true) or out. Every id is a sender ID for `hey local screener approve|deny`. waiting_count counts everyone waiting, which may be more than the app lists.",
		},
		Example: `  hey local screener list
  hey local screener list --json`,
		RunE: listCommand.run,
		Args: cobra.NoArgs,
	}
	return listCommand
}

func (c *localScreenerListCommand) run(cmd *cobra.Command, _ []string) error {
	result, err := callLocal(cmd, "screener.list", nil)
	if err != nil {
		return err
	}
	screener, err := decodeLocalAnswerAs("screener.list", result, func(screener localScreener) error {
		if screener.Waiting == nil || screener.Decided == nil {
			return errors.New("it lists neither who waits nor who was decided")
		}
		return nil
	})
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		if len(screener.Waiting) == 0 && len(screener.Decided) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Nobody waits in The Screener, and nobody was decided")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Sender", "Name", "Email", "Status"})
		for _, sender := range screener.Waiting {
			table.addRow([]string{fmt.Sprintf("%d", sender.ID), truncate(sender.Name, 24), truncate(sender.Address, 32), "waiting"})
		}
		for _, sender := range screener.Decided {
			status := "screened out"
			if sender.ScreenedIn != nil && *sender.ScreenedIn {
				status = "screened in"
			}
			table.addRow([]string{fmt.Sprintf("%d", sender.ID), truncate(sender.Name, 24), truncate(sender.Address, 32), status})
		}
		table.print()
		return nil
	}
	return writeOK(screener,
		output.WithSummary(fmt.Sprintf("%d %s waiting", screener.WaitingCount, senderNoun(screener.WaitingCount))),
		output.WithBreadcrumbs(
			output.Breadcrumb{Action: "approve", Command: "hey local screener approve <sender>", Description: "Screen a sender in"},
			output.Breadcrumb{Action: "deny", Command: "hey local screener deny <sender>", Description: "Screen a sender out"},
		),
	)
}
