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
		Use:   "approve <clearance-id>...",
		Short: "Let a sender through",
		Long:  "Approve a sender, so everything they have waiting is delivered and their future email arrives.",
		Annotations: map[string]string{
			"agent_notes": "Clearance IDs come from `hey screener list`, not contact IDs. --box and --seen apply to one sender at a time; several IDs go through the bulk endpoint, which takes neither. Reverse with `hey screener deny <id>`.",
		},
		Example: `  hey screener approve 12345
  hey screener approve 12345 --box "The Feed"
  hey screener approve 12345 --seen
  hey screener approve 12345 67890 --json`,
		RunE: approveCommand.run,
		Args: usageMinOneArg(),
	}
	approveCommand.cmd.Flags().StringVar(&approveCommand.box, "box", "", "Deliver their email to this box instead of the Imbox")
	approveCommand.cmd.Flags().BoolVar(&approveCommand.seen, "seen", false, "Mark what they already sent as seen")
	return approveCommand
}

func (c *screenerApproveCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	ids, err := parseClearanceIDs(args)
	if err != nil {
		return err
	}
	if len(ids) > 1 && c.box != "" {
		return apierr.ErrUsage("--box approves one sender at a time")
	}
	if len(ids) > 1 && c.seen {
		return apierr.ErrUsage("--seen approves one sender at a time")
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
