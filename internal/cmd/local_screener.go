package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type localScreenerCommand struct {
	cmd *cobra.Command
}

func newLocalScreenerCommand() *localScreenerCommand {
	screenerCommand := &localScreenerCommand{}
	screenerCommand.cmd = &cobra.Command{
		Use:   "screener",
		Short: "Screen senders in or out through the HEY app",
		Long:  "List the senders waiting in The Screener and the ones already decided, and screen a sender in or out, through the HEY app on this computer.",
		Annotations: map[string]string{
			"agent_notes": "Sender IDs come from `hey local screener list`: they are the app's contact IDs, not the clearance IDs `hey screener` takes. A decision runs at once, contacts nobody, and is undone by the opposite one; screening out a sender already screened in is how HEY stops a newsletter. Say which senders you screened out and how to undo it.",
		},
	}
	screenerCommand.cmd.AddCommand(newLocalScreenerListCommand().cmd)
	screenerCommand.cmd.AddCommand(newLocalScreenerApproveCommand().cmd)
	screenerCommand.cmd.AddCommand(newLocalScreenerDenyCommand().cmd)
	return screenerCommand
}

// localDecision is what screener.decide answers.
type localDecision struct {
	Sender   int64  `json:"sender"`
	Screened string `json:"screened"`
}

// decideLocally screens one sender in or out, and says how to undo it.
func decideLocally(cmd *cobra.Command, value, decision string) error {
	sender, err := strconv.ParseInt(value, 10, 64)
	if err != nil || sender <= 0 {
		return apierr.ErrUsageHint(fmt.Sprintf("invalid sender ID: %s", value),
			"Sender IDs come from: hey local screener list")
	}
	result, err := callLocal(cmd, "screener.decide", map[string]any{"sender": sender, "decision": decision})
	if err != nil {
		return err
	}
	decided, err := decodeLocalAnswerAs[localDecision]("screener.decide", result)
	if err != nil {
		return err
	}

	opposite, undo := "approve", "Screen them back in"
	verb := "screened out"
	if decided.Screened == "in" {
		opposite, undo, verb = "deny", "Screen them out again", "screened in"
	}
	reverse := fmt.Sprintf("hey local screener %s %d", opposite, sender)
	return writeMutationLine(cmd,
		fmt.Sprintf("Sender %d %s. Undo with: %s", sender, verb, reverse),
		fmt.Sprintf("Sender %d %s", sender, verb),
		decided,
		output.WithBreadcrumbs(output.Breadcrumb{Action: opposite, Command: reverse, Description: undo}),
	)
}
