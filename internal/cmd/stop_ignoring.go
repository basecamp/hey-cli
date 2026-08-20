package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type stopIgnoringCommand struct {
	cmd  *cobra.Command
	kind string
}

func newStopIgnoringCommand() *stopIgnoringCommand {
	stopIgnoringCommand := &stopIgnoringCommand{}
	stopIgnoringCommand.cmd = &cobra.Command{
		Use:   "stop-ignoring <id>...",
		Short: "Stop ignoring email threads",
		Long:  "Stop ignoring one or more email threads so new replies can bring them back to your attention.",
		Example: `  hey stop-ignoring 12345 --kind topic
  hey stop-ignoring 12345 67890 --kind topic`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box output. Pass --kind exactly as returned by hey box --json. HEY World posts are rejected before any email action is requested. Reverses hey ignore for each thread.",
		},
		RunE: stopIgnoringCommand.run,
		Args: usageMinOneArg(),
	}
	stopIgnoringCommand.cmd.Flags().StringVar(&stopIgnoringCommand.kind, "kind", "", "Item kind from hey box --json")

	return stopIgnoringCommand
}

func (c *stopIgnoringCommand) run(cmd *cobra.Command, args []string) error {
	if err := validateEmailPostingKind(c.cmd.Name(), c.kind); err != nil {
		return err
	}
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().Unmute(cmd.Context(), ids...); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("Stopped ignoring %d %s", len(ids), threadNoun(len(ids)))
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
}
