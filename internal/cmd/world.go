package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type worldCommand struct {
	cmd *cobra.Command
}

func newWorldCommand() *worldCommand {
	worldCommand := &worldCommand{}
	worldCommand.cmd = &cobra.Command{
		Use:   "world",
		Short: "Manage HEY World posts",
	}
	worldCommand.cmd.AddCommand(newWorldDeleteCommand().cmd)
	return worldCommand
}

type worldDeleteCommand struct {
	cmd     *cobra.Command
	confirm bool
}

func newWorldDeleteCommand() *worldDeleteCommand {
	deleteCommand := &worldDeleteCommand{}
	deleteCommand.cmd = &cobra.Command{
		Use:     "delete <token>",
		Short:   "Delete a published HEY World post",
		Long:    "Delete a published HEY World post by its token. This does not move an email to Trash and cannot be undone through the CLI.",
		Example: `  hey world delete abc123 --confirm`,
		Annotations: map[string]string{
			"agent_notes": "Destructive action for published HEY World content. Requires the post token and an explicit --confirm flag. Never substitute a box posting ID for the token.",
		},
		RunE: deleteCommand.run,
		Args: usageExactOneArg(),
	}
	deleteCommand.cmd.Flags().BoolVar(&deleteCommand.confirm, "confirm", false, "Confirm permanent removal of the published post")
	return deleteCommand
}

func (c *worldDeleteCommand) run(cmd *cobra.Command, args []string) error {
	token := strings.TrimSpace(args[0])
	if token == "" {
		return output.ErrUsage("HEY World post token cannot be empty")
	}
	if !c.confirm {
		return output.ErrUsageHint(
			"deleting a HEY World post requires --confirm",
			"This removes published content. Re-run with `--confirm` only if that is your intent.",
		)
	}
	if err := requireAuth(); err != nil {
		return err
	}

	if err := sdk.World().Delete(cmd.Context(), token); err != nil {
		return convertSDKError(err)
	}

	summary := fmt.Sprintf("HEY World post %s deleted", token)
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), summary+".")
		return nil
	}
	return writeOK(nil, output.WithSummary(summary))
}
