package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type trashCommand struct {
	cmd *cobra.Command
}

func newTrashCommand() *trashCommand {
	trashCommand := &trashCommand{}
	trashCommand.cmd = &cobra.Command{
		Use:   "trash <id>...",
		Short: "Move email threads to Trash",
		Long:  "Move one or more email threads to Trash. For a shared thread, HEY removes your access instead of deleting it for everyone.",
		Example: `  hey trash 12345
  hey trash 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box output. Shared threads lose your access rather than being deleted for everyone.",
		},
		RunE: trashCommand.run,
		Args: usageMinOneArg(),
	}

	return trashCommand
}

func (c *trashCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().MoveToTrash(cmd.Context(), ids...); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s moved to Trash", len(ids), threadNoun(len(ids))), nil)
}
