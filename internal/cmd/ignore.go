package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type ignoreCommand struct {
	cmd  *cobra.Command
	kind string
}

func newIgnoreCommand() *ignoreCommand {
	ignoreCommand := &ignoreCommand{}
	ignoreCommand.cmd = &cobra.Command{
		Use:   "ignore <id>...",
		Short: "Ignore email threads",
		Long:  "Ignore one or more email threads so new replies do not bring them back to your attention.",
		Example: `  hey ignore 12345 --kind topic
  hey ignore 12345 67890 --kind topic`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box output. Pass --kind exactly as returned by hey box --json. HEY World posts are rejected before any email action is requested. Ignored threads remain in their box and can be restored with hey stop-ignoring.",
		},
		RunE: ignoreCommand.run,
		Args: usageMinOneArg(),
	}
	ignoreCommand.cmd.Flags().StringVar(&ignoreCommand.kind, "kind", "", "Item kind from hey box --json")

	return ignoreCommand
}

func (c *ignoreCommand) run(cmd *cobra.Command, args []string) error {
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

	if err := sdk.Postings().Mute(cmd.Context(), ids...); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("%d %s ignored", len(ids), threadNoun(len(ids)))
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
}
