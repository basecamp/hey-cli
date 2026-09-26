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
		Annotations: map[string]string{
			"agent_notes": "JSON answers note (HEY's plain text, formatting dropped), note_html (as HEY serves it) and note_markdown (the note as Markdown, which hey contact note set writes back unchanged).",
		},
		Example: `  hey contact note show 12345
  hey contact note show 12345 --json
  hey contact note show 12345 --jq '.data.note_markdown'`,
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
	if writer.EffectiveFormat() == output.FormatHTML {
		return writeNoteHTML(cmd.OutOrStdout(), note.NoteHtml)
	}
	if writer.IsStyled() {
		fmt.Fprintln(cmd.OutOrStdout(), renderedNote(note.Note, note.NoteHtml))
		return nil
	}
	return writeOK(newContactNoteResult(*note),
		output.WithSummary(fmt.Sprintf("Private note for contact %d", contactID)),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "edit", Command: fmt.Sprintf("hey contact note set %d", contactID), Description: "Edit the private note"}),
	)
}
