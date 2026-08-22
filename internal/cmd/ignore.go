package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type ignoreCommand struct {
	cmd *cobra.Command
}

func newIgnoreCommand() *ignoreCommand {
	ignoreCommand := &ignoreCommand{}
	ignoreCommand.cmd = &cobra.Command{
		Use:   "ignore <id>...",
		Short: "Ignore email threads",
		Long:  "Ignore one or more email threads so new replies do not bring them back to your attention.",
		Example: `  hey ignore 12345
  hey ignore 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box view output. Ignored threads remain in their box and can be restored with hey stop-ignoring.",
		},
		RunE: ignoreCommand.run,
		Args: usageMinOneArg(),
	}

	return ignoreCommand
}

func (c *ignoreCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().Mute(cmd.Context(), ids...); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s ignored", len(ids), threadNoun(len(ids))), nil)
}
