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
// NoteMarkdownLossless says whether it can: a note holding an attachment, or anything
// else Markdown cannot carry, has to be changed as HTML instead.
type contactNoteResult struct {
	generated.ContactNote
	NoteMarkdown         htmlutil.Markdown `json:"note_markdown"`
	NoteMarkdownLossless bool              `json:"note_markdown_lossless"`
}

func newContactNoteCommand() *contactNoteCommand {
	noteCommand := &contactNoteCommand{}
	noteCommand.cmd = &cobra.Command{
		Use:   "note",
		Short: "Manage private contact notes",
		Annotations: map[string]string{
			"agent_notes": "Private notes support show, set, and delete. Show answers note_markdown, the form set takes, and note_markdown_lossless; when that is false, change note_html and set it with --note-html instead. Set replaces the whole note and accepts --note, positional content, stdin, or $EDITOR.",
		},
	}
	noteCommand.cmd.AddCommand(newContactNoteShowCommand().cmd)
	noteCommand.cmd.AddCommand(newContactNoteSetCommand().cmd)
	noteCommand.cmd.AddCommand(newContactNoteDeleteCommand().cmd)
	return noteCommand
}

func newContactNoteResult(note generated.ContactNote) contactNoteResult {
	return contactNoteResult{
		ContactNote:          note,
		NoteMarkdown:         contactNoteMarkdown(note.Note, note.NoteHtml),
		NoteMarkdownLossless: contactNoteLossless(note.Note, note.NoteHtml),
	}
}

// contactNoteMarkdown is a contact note as Markdown, converted from its HTML so bold,
// lists, links and headings survive.
func contactNoteMarkdown(note, noteHTML string) htmlutil.Markdown {
	return htmlutil.ToMarkdown(contactNoteSource(note, noteHTML))
}

// contactNoteLossless reports whether contactNoteMarkdown holds everything in the note,
// judged against the same HTML it was converted from.
func contactNoteLossless(note, noteHTML string) bool {
	return htmlutil.MarkdownIsLossless(contactNoteSource(note, noteHTML))
}

// contactNoteSource is the HTML a note is converted from: HEY's, or the plain text as
// HTML when HEY served none.
func contactNoteSource(note, noteHTML string) string {
	if noteHTML != "" {
		return noteHTML
	}
	return strings.ReplaceAll(stdhtml.EscapeString(note), "\n", "<br>")
}
