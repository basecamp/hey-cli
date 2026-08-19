package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type contactNoteDeleteCommand struct {
	cmd *cobra.Command
}

func newContactNoteDeleteCommand() *contactNoteDeleteCommand {
	deleteCommand := &contactNoteDeleteCommand{}
	deleteCommand.cmd = &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a private contact note",
		Long:    "Permanently delete the private note from a contact. The contact remains unchanged.",
		Example: `  hey contacts note delete 12345`,
		RunE:    deleteCommand.run,
		Args:    usageExactOneArg(),
	}
	return deleteCommand
}

func (c *contactNoteDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	if err := sdk.Contacts().DeleteNote(cmd.Context(), contactID); err != nil {
		return convertSDKError(err)
	}
	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Private note for contact %d deleted.\n", contactID)
		return nil
	}
	return writeOK(map[string]int64{"id": contactID},
		output.WithSummary("Private contact note deleted"),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "write", Command: fmt.Sprintf("hey contacts note set %d", contactID), Description: "Add a private note"}),
	)
}
