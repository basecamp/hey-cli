package cmd

import (
	stdhtml "html"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

type contactNoteCommand struct {
	cmd *cobra.Command
}

// contactNoteResult is a contact note as the CLI answers it: HEY's plain text and HTML
// as served, and the note as Markdown — the form `hey contact note set` takes — so a
// note can be read, changed and written back without losing its formatting.
type contactNoteResult struct {
	generated.ContactNote
	NoteMarkdown htmlutil.Markdown `json:"note_markdown"`
}

func newContactNoteCommand() *contactNoteCommand {
	noteCommand := &contactNoteCommand{}
	noteCommand.cmd = &cobra.Command{
		Use:   "note",
		Short: "Manage private contact notes",
		Annotations: map[string]string{
			"agent_notes": "Private notes support show, set, and delete. Show answers note_markdown, the form set takes. Set replaces the whole note and accepts --note, positional content, stdin, or $EDITOR.",
		},
	}
	noteCommand.cmd.AddCommand(newContactNoteShowCommand().cmd)
	noteCommand.cmd.AddCommand(newContactNoteSetCommand().cmd)
	noteCommand.cmd.AddCommand(newContactNoteDeleteCommand().cmd)
	return noteCommand
}

func newContactNoteResult(note generated.ContactNote) contactNoteResult {
	return contactNoteResult{ContactNote: note, NoteMarkdown: contactNoteMarkdown(note.Note, note.NoteHtml)}
}

// contactNoteMarkdown is a contact note as Markdown, converted from its HTML so bold,
// lists, links and headings survive; the plain text is the fallback when HEY served no
// HTML.
func contactNoteMarkdown(note, noteHTML string) htmlutil.Markdown {
	if noteHTML != "" {
		return htmlutil.ToMarkdown(noteHTML)
	}
	return htmlutil.ToMarkdown(strings.ReplaceAll(stdhtml.EscapeString(note), "\n", "<br>"))
}
