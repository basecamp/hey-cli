package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/output"
)

type screenerDenyCommand struct {
	cmd  *cobra.Command
	spam bool
}

func newScreenerDenyCommand() *screenerDenyCommand {
	denyCommand := &screenerDenyCommand{}
	denyCommand.cmd = &cobra.Command{
		Use:   "deny <clearance-id or email>...",
		Short: "Turn a sender away",
		Long: `Deny a sender, so what they sent is hidden and their future email never arrives.

A sender is named by the clearance ID hey screener list gives, or by their email address,
looked up among the senders waiting and then among those already screened.`,
		Annotations: map[string]string{
			"agent_notes": "Clearance IDs come from `hey screener list`, not contact IDs; the sender's email address works too, looked up among those waiting and then those already screened (not found is exit 2). --spam also marks what they already sent as spam and, for a single ID, trains HEY's filter on it, which is harder to undo than denying; several IDs go through HEY's bulk endpoint, which does not train. Reverse with `hey screener approve <id>`.",
		},
		Example: `  hey screener deny 12345
  hey screener deny offers@example.com
  hey screener deny 12345 67890
  hey screener deny 12345 --spam
  hey screener deny 12345 --json`,
		RunE: denyCommand.run,
		Args: usageMinOneArg(),
	}
	denyCommand.cmd.Flags().BoolVar(&denyCommand.spam, "spam", false, "Also mark what they sent as spam (and train the filter, for a single ID)")
	return denyCommand
}

func (c *screenerDenyCommand) run(cmd *cobra.Command, args []string) error {
	if appEndpoint() {
		return screenInApp(cmd, args, "out")
	}
	if err := checkClearanceArgs(args); err != nil {
		return err
	}
	if err := requireAuth(); err != nil {
		return err
	}
	ids, err := resolveClearanceIDs(cmd.Context(), args)
	if err != nil {
		return err
	}

	reverse := output.Breadcrumb{
		Action:      "approve",
		Command:     fmt.Sprintf("hey screener approve %d", ids[0]),
		Description: "Let this sender through after all",
	}
	return screenSenders(cmd, ids, hey.ClearanceDenied, hey.ScreenOptions{Spam: c.spam}, reverse)
}
