package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type spamCommand struct {
	cmd  *cobra.Command
	kind string
}

func newSpamCommand() *spamCommand {
	spamCommand := &spamCommand{}
	spamCommand.cmd = &cobra.Command{
		Use:   "spam <id>...",
		Short: "Mark email threads as spam",
		Long:  "Mark one or more email threads as spam. HEY moves the threads to Spam and trains its filters.",
		Example: `  hey spam 12345 --kind topic
  hey spam 12345 67890 --kind topic`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box output. Pass --kind exactly as returned by hey box --json. HEY World posts are rejected before any email action is requested. Marks each thread as spam and removes it from the current box.",
		},
		RunE: spamCommand.run,
		Args: usageMinOneArg(),
	}
	spamCommand.cmd.Flags().StringVar(&spamCommand.kind, "kind", "", "Item kind from hey box --json")

	return spamCommand
}

func (c *spamCommand) run(cmd *cobra.Command, args []string) error {
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

	if err := sdk.Postings().MarkSpam(cmd.Context(), ids...); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("%d %s marked as spam", len(ids), threadNoun(len(ids)))
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}

	return writeOK(nil, output.WithSummary(summary))
}
