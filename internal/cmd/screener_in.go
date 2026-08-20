package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/output"
)

type screenerInCommand struct {
	cmd  *cobra.Command
	box  string
	seen bool
}

func newScreenerInCommand() *screenerInCommand {
	inCommand := &screenerInCommand{}
	inCommand.cmd = &cobra.Command{
		Use:   "in <clearance-id>...",
		Short: "Let a sender through",
		Long:  "Screen a sender in, so everything they have waiting is delivered and their future email arrives.",
		Annotations: map[string]string{
			"agent_notes": "Clearance IDs come from `hey screener list`, not contact IDs. --box and --seen apply to one sender at a time; several IDs go through the bulk endpoint, which takes neither. Reverse with `hey screener out <id>`.",
		},
		Example: `  hey screener in 12345
  hey screener in 12345 --box "The Feed"
  hey screener in 12345 --seen
  hey screener in 12345 67890 --json`,
		RunE: inCommand.run,
		Args: usageMinOneArg(),
	}
	inCommand.cmd.Flags().StringVar(&inCommand.box, "box", "", "Deliver their email to this box instead of the Imbox")
	inCommand.cmd.Flags().BoolVar(&inCommand.seen, "seen", false, "Mark what they already sent as seen")
	return inCommand
}

func (c *screenerInCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	ids, err := parseClearanceIDs(args)
	if err != nil {
		return err
	}
	if len(ids) > 1 && c.box != "" {
		return output.ErrUsage("--box screens in one sender at a time")
	}
	if len(ids) > 1 && c.seen {
		return output.ErrUsage("--seen screens in one sender at a time")
	}

	opts := hey.ScreenOptions{MarkTopicsAsSeen: c.seen}
	if c.box != "" {
		box, resolveErr := resolveBox(cmd.Context(), c.box)
		if resolveErr != nil {
			return resolveErr
		}
		opts.DesignationBoxID = box.Id
	}

	reverse := output.Breadcrumb{
		Action:      "screen_out",
		Command:     fmt.Sprintf("hey screener out %d", ids[0]),
		Description: "Turn this sender away instead",
	}
	return screenSenders(cmd, ids, hey.ClearanceApproved, opts, reverse)
}
