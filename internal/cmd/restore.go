package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type restoreCommand struct {
	cmd *cobra.Command
}

func newRestoreCommand() *restoreCommand {
	command := &restoreCommand{}
	command.cmd = &cobra.Command{
		Use:     "restore <topic-id>",
		Short:   "Restore a topic to active mail",
		Long:    "Restore a topic from Trash to active mail. This changes mailbox state, so confirm the exact topic ID before running it.",
		Example: "  hey restore 12345",
		Annotations: map[string]string{
			"agent_notes": "State-changing action. Confirm the exact topic ID before restoring it from Trash.",
		},
		Args: usageExactOneArg(),
		RunE: command.run,
	}
	return command
}

func (c *restoreCommand) run(cmd *cobra.Command, args []string) error {
	topicID, err := parsePositiveControlID(args[0], "topic")
	if err != nil {
		return err
	}
	if err := rejectControlListFormats(); err != nil {
		return err
	}
	if err := requireAuth(); err != nil {
		return err
	}
	if err := sdk.Topics().Restore(cmd.Context(), topicID); err != nil {
		return convertSDKError(err)
	}

	result := topicControlResult{TopicID: topicID, Action: "restored"}
	return writeTopicControlResult(cmd, result, fmt.Sprintf("Topic %d restored to active mail", topicID))
}
