package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type screenerCommand struct {
	cmd *cobra.Command
}

func newScreenerCommand() *screenerCommand {
	screenerCommand := &screenerCommand{}
	screenerCommand.cmd = &cobra.Command{
		Use:   "screener",
		Short: "Decide who gets to email you",
		Long:  "List the senders waiting in The Screener, screen them in or out, and review the ones already decided.",
		Annotations: map[string]string{
			"agent_notes": "Clearance IDs come from `hey screener list`, and are not contact IDs. Approving delivers everything the sender has waiting; denying hides it. Both are reversible with the opposite command. `hey screener history` shows what was already decided.",
		},
	}

	screenerCommand.cmd.AddCommand(newScreenerListCommand().cmd)
	screenerCommand.cmd.AddCommand(newScreenerApproveCommand().cmd)
	screenerCommand.cmd.AddCommand(newScreenerDenyCommand().cmd)
	screenerCommand.cmd.AddCommand(newScreenerClearCommand().cmd)
	screenerCommand.cmd.AddCommand(newScreenerHistoryCommand().cmd)
	return screenerCommand
}

func parseClearanceID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, apierr.ErrUsage(fmt.Sprintf("invalid clearance ID: %s", value))
	}
	return id, nil
}

func parseClearanceIDs(values []string) ([]int64, error) {
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := parseClearanceID(value)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func senderNoun(count int) string {
	if count == 1 {
		return "sender"
	}
	return "senders"
}

// screenedResult is what the screening commands answer with. The clearance the API
// returns carries the petitioner, so a caller can confirm who was decided on without
// holding the list it started from.
type screenedResult struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Name   string `json:"name,omitempty"`
	Email  string `json:"email_address,omitempty"`
}

func screenedResultFor(clearance generated.Clearance) screenedResult {
	return screenedResult{
		ID:     clearance.Id,
		Status: clearance.Status,
		Name:   clearance.Petitioner.Name,
		Email:  clearance.Petitioner.EmailAddress,
	}
}

// screenSenders answers the Screener for one sender or many. A single sender goes through
// the clearance itself, which is what carries the delivery box and the seen and spam
// options; several go through the bulk endpoint in one request.
func screenSenders(cmd *cobra.Command, ids []int64, status string, opts hey.ScreenOptions, reverse output.Breadcrumb) error {
	if len(ids) == 1 {
		clearance, err := sdk.Clearances().Screen(cmd.Context(), ids[0], status, opts)
		if err != nil {
			return apierr.FromSDK(err)
		}
		return reportScreened(cmd, []generated.Clearance{*clearance}, status, reverse)
	}

	clearances, err := sdk.Clearances().ScreenMany(cmd.Context(), ids, status, opts.Spam)
	if err != nil {
		return apierr.FromSDK(err)
	}
	return reportScreened(cmd, clearances, status, reverse)
}

func reportScreened(cmd *cobra.Command, clearances []generated.Clearance, status string, reverse output.Breadcrumb) error {
	results := make([]screenedResult, 0, len(clearances))
	for _, clearance := range clearances {
		results = append(results, screenedResultFor(clearance))
	}

	verb := "approved"
	if status == hey.ClearanceDenied {
		verb = "denied"
	}

	if writer.IsStyled() {
		for _, clearance := range clearances {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s.\n", describeSender(clearance), verb)
		}
		if len(clearances) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Nothing to screen.")
		}
		return nil
	}
	return writeOK(results,
		output.WithSummary(fmt.Sprintf("%d %s %s", len(results), senderNoun(len(results)), verb)),
		output.WithBreadcrumbs(reverse),
	)
}

func describeSender(clearance generated.Clearance) string {
	if clearance.Petitioner.Name != "" {
		return clearance.Petitioner.Name
	}
	if clearance.Petitioner.EmailAddress != "" {
		return clearance.Petitioner.EmailAddress
	}
	return fmt.Sprintf("clearance %d", clearance.Id)
}
