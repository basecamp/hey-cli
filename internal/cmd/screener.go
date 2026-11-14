package cmd

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

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
			"agent_notes": "The Screener is where first-time senders wait until they are approved or denied. Clearance IDs come from `hey screener list`, and are not contact IDs. Approving delivers everything the sender has waiting; denying hides it. Both are reversible with the opposite command. `hey screener history` shows what was already decided.",
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

// isScreenerAddress reports a screening argument that names a sender by email address
// rather than by id.
func isScreenerAddress(value string) bool {
	return strings.Contains(value, "@")
}

// checkClearanceArgs refuses an argument that is neither a clearance id nor an address,
// before anything is asked of HEY.
func checkClearanceArgs(values []string) error {
	for _, value := range values {
		if isScreenerAddress(value) {
			continue
		}
		if _, err := parseClearanceID(value); err != nil {
			return err
		}
	}
	return nil
}

// resolveClearanceIDs is the clearance each argument names: an id as given, or an address,
// looked up among the senders waiting, whatever its case, and then among those already
// decided, so a decision can be changed by address too. Each list is read at most once,
// and only when an address needs it.
func resolveClearanceIDs(ctx context.Context, values []string) ([]int64, error) {
	ids := make([]int64, len(values))
	var addresses []int
	for i, value := range values {
		if isScreenerAddress(value) {
			addresses = append(addresses, i)
			continue
		}
		id, err := parseClearanceID(value)
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	if len(addresses) == 0 {
		return ids, nil
	}

	pending, err := clearancesByAddress(ctx, readPendingClearances, func(c pendingClearance) (string, int64) { return c.Email, c.ID })
	if err != nil {
		return nil, err
	}
	var decided map[string][]int64
	for _, i := range addresses {
		address := strings.ToLower(strings.TrimSpace(values[i]))
		found := pending[address]
		if len(found) == 0 {
			if decided == nil {
				if decided, err = clearancesByAddress(ctx, readScreenedClearances, func(c screenedClearance) (string, int64) { return c.Email, c.ID }); err != nil {
					return nil, err
				}
			}
			found = decided[address]
		}
		switch len(found) {
		case 0:
			return nil, apierr.ErrNotFoundHint("sender", values[i],
				"Nobody waiting in The Screener or already screened sends from that address: hey screener list, hey screener history")
		case 1:
			ids[i] = found[0]
		default:
			matches := make([]string, 0, len(found))
			for _, id := range found {
				matches = append(matches, strconv.FormatInt(id, 10))
			}
			return nil, apierr.ErrAmbiguous("sender", matches)
		}
	}
	return ids, nil
}

// clearancesByAddress reads a whole clearance listing, up to maxScreenerPages, and indexes
// its clearance ids by the sender's address, lowercased.
func clearancesByAddress[T any](ctx context.Context, read pageReader[T], key func(T) (string, int64)) (map[string][]int64, error) {
	first, err := read(ctx, "")
	if err != nil {
		return nil, err
	}
	collected, err := collectPages(ctx, first, pageRequest{All: true, MaxPages: maxScreenerPages}, read)
	if err != nil {
		return nil, err
	}
	if collected.Truncated {
		// Stopped at the cap with pages left: an address missing from what was read was
		// not searched for, which is no answer that nobody sends from it; and one found
		// there may be found again past the cap, so none is taken.
		return nil, &apierr.Error{
			Code:    apierr.CodeAPI,
			Message: fmt.Sprintf("The Screener holds more than %d pages, so an address could not be looked up in all of it", maxScreenerPages),
			Hint:    "Use the clearance ID from hey screener list instead",
		}
	}
	index := make(map[string][]int64)
	for _, item := range collected.Items {
		address, id := key(item)
		address = strings.ToLower(strings.TrimSpace(address))
		if address != "" && !slices.Contains(index[address], id) {
			index[address] = append(index[address], id)
		}
	}
	return index, nil
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
