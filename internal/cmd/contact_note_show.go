package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type contactNoteShowCommand struct {
	cmd *cobra.Command
}

func newContactNoteShowCommand() *contactNoteShowCommand {
	showCommand := &contactNoteShowCommand{}
	showCommand.cmd = &cobra.Command{
		Use:   "show <id>",
		Short: "Read a private contact note",
		Example: `  hey contacts note show 12345
  hey contacts note show 12345 --json`,
		RunE: showCommand.run,
		Args: usageExactOneArg(),
	}
	return showCommand
}

func (c *contactNoteShowCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	note, err := sdk.Contacts().Note(cmd.Context(), contactID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if note == nil {
		return apierr.ErrNotFound("contact note", args[0])
	}
	if writer.IsStyled() {
		if note.Note == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "(empty)")
		} else if htmlOutput {
			fmt.Fprintln(cmd.OutOrStdout(), note.NoteHtml)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), note.Note)
		}
		return nil
	}
	return writeOK(note,
		output.WithSummary(fmt.Sprintf("Private note for contact %d", contactID)),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "edit", Command: fmt.Sprintf("hey contacts note set %d", contactID), Description: "Edit the private note"}),
	)
}
