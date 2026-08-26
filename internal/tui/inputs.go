package tui

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
)

// themeDark is which side of the palette the active theme is on, kept by
// applyTheme for the text fields built below.
var themeDark = true

// newTextArea and newTextInput are how every text field in the TUI is built.
// bubbles' New() hardcodes its dark palette — the focused cursor line on ANSI
// slot 0 and blurred text on slot 7 — which on a stock light terminal is a
// black band over light-grey text (hey-cli#331). Omarchy remaps those two
// slots to the theme's own paper and ink, which is why the defect never shows
// there. The theme knows which side it is on, so the fields are told rather
// than left to guess. Do not call textarea.New or textinput.New directly.
func newTextArea() textarea.Model {
	field := textarea.New()
	field.SetStyles(textarea.DefaultStyles(themeDark))
	return field
}

func newTextInput() textinput.Model {
	field := textinput.New()
	field.SetStyles(textinput.DefaultStyles(themeDark))
	return field
}
