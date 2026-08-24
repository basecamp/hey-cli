package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type spamCommand struct {
	cmd *cobra.Command
}

func newSpamCommand() *spamCommand {
	spamCommand := &spamCommand{}
	spamCommand.cmd = &cobra.Command{
		Use:   "spam <box-item-id>...",
		Short: "Mark email threads as spam",
		Long:  "Mark one or more email threads as spam. HEY moves the threads to Spam and trains its filters.",
		Example: `  hey spam 12345
  hey spam 12345 67890`,
		Annotations: map[string]string{
			"agent_notes": "Accepts one or more box item IDs from hey box view output. Marks each thread as spam and removes it from the current box.",
		},
		RunE: spamCommand.run,
		Args: usageMinOneArg(),
	}

	return spamCommand
}

func (c *spamCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ids, err := parseIntArgs(args)
	if err != nil {
		return err
	}

	if err := sdk.Postings().MarkSpam(cmd.Context(), ids...); err != nil {
		return apierr.FromSDK(err)
	}

	return writeMutation(cmd, fmt.Sprintf("%d %s marked as spam", len(ids), threadNoun(len(ids))), nil)
}
