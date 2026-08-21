// Package terminal makes text that came from somewhere else safe to print.
//
// Everything HEY serves — a sender's name, a subject, a filename, a label — was
// written by somebody else, and a terminal acts on the escape sequences and
// control characters it is handed. Stripping the sequence outright is what makes
// the string inert. Defacing its ESC byte only hides the trigger: the payload
// stays behind as visible debris of somebody else's choosing, and whatever lays
// the output out then measures that debris as text.
//
// The bidirectional controls go the same way. They do not move the cursor, but
// they move what the reader sees: a right-to-left override turns "invoice‮fdp.exe"
// into a PDF on screen and an executable on disk, and an isolate can swap the
// order of a sender's name and address. Nothing HEY shows in a single line needs
// them — an RTL name still reads right-to-left without an explicit override.
package terminal

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

var lineBreaks = strings.NewReplacer("\n", " ", "\t", " ")

// Sanitize removes escape sequences, control characters and bidirectional controls
// from text on its way to a terminal. Newlines and tabs survive, because text is not
// necessarily one line: a message body, a jq result and a Markdown cell all carry
// them on purpose.
func Sanitize(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		case isBidiControl(r):
			return -1
		default:
			return r
		}
	}, ansi.Strip(value))
}

// isBidiControl reports the Unicode Bidi_Control property: the Arabic letter mark,
// the left-to-right and right-to-left marks, the embeddings, overrides and their
// pop, and the isolates and theirs.
func isBidiControl(r rune) bool {
	switch {
	case r == 0x061c, r == 0x200e, r == 0x200f:
		return true
	case r >= 0x202a && r <= 0x202e:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	default:
		return false
	}
}

// SanitizeLine is Sanitize for somewhere only one line fits — a table cell, a
// confirmation — where a newline or a tab would move what comes after it rather
// than merely reading oddly. Both become a space, so the words stay apart.
func SanitizeLine(value string) string {
	return lineBreaks.Replace(Sanitize(value))
}
