package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type stopIgnoringCommand struct {
	cmd *cobra.Command
}

func newStopIgnoringCommand() *stopIgnoringCommand {
	stopIgnoringCommand := &stopIgnoringCommand{}
	stopIgnoringCommand.cmd = &cobra.Command{
		Use:   "stop-ignoring <id>...",
		Short: "Stop ignoring email threads",
		Long:  "Stop ignoring one or more email threads so new replies can bring them back to your attention.",
		Example: `  hey stop-ignoring 12345
  hey stop-ignoring 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box view output. Reverses hey ignore for each thread.",
		},
		RunE: stopIgnoringCommand.run,
		Args: usageMinOneArg(),
	}

	return stopIgnoringCommand
}

func (c *stopIgnoringCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().Unmute(cmd.Context(), ids...); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("Stopped ignoring %d %s", len(ids), threadNoun(len(ids))), nil)
}
