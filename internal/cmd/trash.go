package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type trashCommand struct {
	cmd  *cobra.Command
	kind string
}

func newTrashCommand() *trashCommand {
	trashCommand := &trashCommand{}
	trashCommand.cmd = &cobra.Command{
		Use:   "trash <id>...",
		Short: "Move email threads to Trash",
		Long:  "Move one or more email threads to Trash. For a shared thread, HEY removes your access instead of deleting it for everyone.",
		Example: `  hey trash 12345 --kind topic
  hey trash 12345 67890 --kind topic`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs (id) from hey box or hey search output. For hey box, select kind=topic records. Pass --kind topic. Shared threads lose your access rather than being deleted for everyone.",
		},
		RunE: trashCommand.run,
		Args: emailPostingArgs(&trashCommand.kind, usageMinOneArg()),
	}
	trashCommand.cmd.Flags().StringVar(&trashCommand.kind, "kind", "", "Email thread kind; must be topic (required)")

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
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("%d %s moved to Trash", len(ids), threadNoun(len(ids)))
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
}
