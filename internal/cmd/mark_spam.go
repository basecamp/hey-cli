package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type markSpamCommand struct {
	cmd *cobra.Command
}

func newMarkSpamCommand() *markSpamCommand {
	command := &markSpamCommand{}
	command.cmd = &cobra.Command{
		Use:     "mark-spam <entry-id>",
		Short:   "Mark an email entry as spam",
		Long:    "Mark one email entry as spam. This changes mailbox state, so confirm the exact entry ID before running it.",
		Example: "  hey mark-spam 12345",
		Annotations: map[string]string{
			"agent_notes": "State-changing action. Confirm the exact entry ID before marking it as spam.",
		},
		Args: usageExactOneArg(),
		RunE: command.run,
	}
	return command
}

func (c *markSpamCommand) run(cmd *cobra.Command, args []string) error {
	entryID, err := parsePositiveControlID(args[0], "entry")
	if err != nil {
		return err
	}
	if err := rejectControlListFormats(); err != nil {
		return err
	}
	if err := requireAuth(); err != nil {
		return err
	}
	if err := sdk.Entries().MarkSpam(cmd.Context(), entryID); err != nil {
		return convertSDKError(err)
	}

	result := topicControlResult{EntryID: entryID, Action: "marked_spam"}
	return writeTopicControlResult(cmd, result, fmt.Sprintf("Entry %d marked as spam", entryID))
}
