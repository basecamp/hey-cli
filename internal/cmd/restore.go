package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type restoreCommand struct {
	cmd *cobra.Command
}

func newRestoreCommand() *restoreCommand {
	restoreCommand := &restoreCommand{}
	restoreCommand.cmd = &cobra.Command{
		Use:   "restore <thread-id>...",
		Short: "Restore email threads from Trash",
		Long:  "Restore one or more email threads from Trash to active mail. This only works for threads currently in Trash. Use the topic_id column from hey search --in trash today; once hey thread list --in trash is available, that Trash listing will provide thread IDs too. Do not use a box item ID.",
		Example: `  hey restore 12345
  hey restore 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more thread IDs, not box item IDs. Use topic_id from hey search --in trash today; once hey thread list --in trash is available, that Trash listing is another source. A trashed thread has no box item ID. Only restores threads currently in Trash.",
		},
		Args: usageMinOneArg(),
		RunE: restoreCommand.run,
	}

	return restoreCommand
}

func (c *restoreCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := sdk.Topics().Restore(cmd.Context(), id); err != nil {
			return apierr.FromSDK(err)
		}
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s restored from Trash", len(ids), threadNoun(len(ids))), nil)
}
