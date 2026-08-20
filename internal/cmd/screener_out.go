package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/output"
)

type screenerOutCommand struct {
	cmd  *cobra.Command
	spam bool
}

func newScreenerOutCommand() *screenerOutCommand {
	outCommand := &screenerOutCommand{}
	outCommand.cmd = &cobra.Command{
		Use:   "out <clearance-id>...",
		Short: "Turn a sender away",
		Long:  "Screen a sender out, so what they sent is hidden and their future email never arrives.",
		Annotations: map[string]string{
			"agent_notes": "Clearance IDs come from `hey screener list`, not contact IDs. --spam also marks what they already sent as spam and trains HEY's filter on it, which is harder to undo than screening out. Reverse with `hey screener in <id>`.",
		},
		Example: `  hey screener out 12345
  hey screener out 12345 67890
  hey screener out 12345 --spam
  hey screener out 12345 --json`,
		RunE: outCommand.run,
		Args: usageMinOneArg(),
	}
	outCommand.cmd.Flags().BoolVar(&outCommand.spam, "spam", false, "Also mark what they sent as spam and train the filter")
	return outCommand
}

func (c *screenerOutCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	ids, err := parseClearanceIDs(args)
	if err != nil {
		return err
	}

	reverse := output.Breadcrumb{
		Action:      "screen_in",
		Command:     fmt.Sprintf("hey screener in %d", ids[0]),
		Description: "Let this sender through after all",
	}
	return screenSenders(cmd, ids, hey.ClearanceDenied, hey.ScreenOptions{Spam: c.spam}, reverse)
}
