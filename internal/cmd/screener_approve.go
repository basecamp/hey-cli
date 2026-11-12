package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type screenerApproveCommand struct {
	cmd  *cobra.Command
	box  string
	seen bool
}

func newScreenerApproveCommand() *screenerApproveCommand {
	approveCommand := &screenerApproveCommand{}
	approveCommand.cmd = &cobra.Command{
		Use:   "approve <clearance-id or email>...",
		Short: "Let a sender through",
		Long: `Approve a sender, so everything they have waiting is delivered and their future email arrives.

A sender is named by the clearance ID hey screener list gives, or by their email address,
looked up among the senders waiting and then among those already screened.`,
		Annotations: map[string]string{
			"agent_notes": "Clearance IDs come from `hey screener list`, not contact IDs; the sender's email address works too, looked up among those waiting and then those already screened (not found is exit 2). --box and --seen apply to one sender at a time; several IDs go through the bulk endpoint, which takes neither. Reverse with `hey screener deny <id>`.",
		},
		Example: `  hey screener approve 12345
  hey screener approve priya@example.com
  hey screener approve 12345 --box "The Feed"
  hey screener approve 12345 --seen
  hey screener approve 12345 67890 --json`,
		RunE: approveCommand.run,
		Args: usageMinOneArg(),
	}
	approveCommand.cmd.Flags().StringVar(&approveCommand.box, "box", "", "Deliver their email to this box instead of the Imbox")
	approveCommand.cmd.Flags().BoolVar(&approveCommand.seen, "seen", false, "Mark what they already sent as seen (reliable only for a sender with one waiting thread)")
	return approveCommand
}

func (c *screenerApproveCommand) run(cmd *cobra.Command, args []string) error {
	if appEndpoint() {
		return screenInApp(cmd, args, "in")
	}
	if err := checkClearanceArgs(args); err != nil {
		return err
	}
	if err := requireAuth(); err != nil {
		return err
	}
	if len(args) > 1 && c.box != "" {
		return apierr.ErrUsage("--box approves one sender at a time")
	}
	if len(args) > 1 && c.seen {
		return apierr.ErrUsage("--seen approves one sender at a time")
	}

	ids, err := resolveClearanceIDs(cmd.Context(), args)
	if err != nil {
		return err
	}

	opts := hey.ScreenOptions{MarkTopicsAsSeen: c.seen}
	if c.box != "" {
		box, resolveErr := resolveBox(cmd.Context(), c.box, "")
		if resolveErr != nil {
			return resolveErr
		}
		opts.DesignationBoxID = box.Id
	}

	reverse := output.Breadcrumb{
		Action:      "deny",
		Command:     fmt.Sprintf("hey screener deny %d", ids[0]),
		Description: "Turn this sender away instead",
	}
	return screenSenders(cmd, ids, hey.ClearanceApproved, opts, reverse)
}
