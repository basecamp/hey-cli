package tui

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// newTextArea and newTextInput are how every text field in the TUI is built.
//
// bubbles' New() hands a field a palette chosen for one background: the focused
// cursor line on ANSI slot 0, blurred text on slot 7, placeholders in the
// 256-color cube. On a stock light terminal that is a black band over grey
// text (hey-cli#331); Omarchy remaps slots 0 and 7 to the theme's own paper
// and ink, which is why the defect never shows there. Choosing the other
// palette for a light theme would fix the band and leave a field that has a
// mode at all — stale the moment the theme flips under it, since a widget keeps
// the Styles it was handed.
//
// So these styles name no color. Like the rest of styles.go they lean on what
// the terminal already decided: default foreground for text, the SGR faint
// attribute (styleMuted) for what is secondary, reverse video for a selection.
// A theme switch retints all of it over OSC 4 with nothing to re-apply, and
// flipping Theme.Dark changes no byte of the output — TestTextFieldsDoNotDependOnTheThemeMode
// holds that line the way TestCoversDoNotDependOnTheThemeMode does for the covers.
// Do not call textarea.New or textinput.New directly.
func newTextArea() textarea.Model {
	field := textarea.New()
	field.SetStyles(textAreaStyles())
	return field
}

func newTextInput() textinput.Model {
	field := textinput.New()
	field.SetStyles(textInputStyles())
	return field
}

func textAreaStyles() textarea.Styles {
	plain := lipgloss.NewStyle()
	focused := textarea.StyleState{
		Base:             plain,
		Text:             plain,
		LineNumber:       styleMuted,
		CursorLineNumber: plain,
		CursorLine:       plain, // no band: the cursor itself says where the line is
		EndOfBuffer:      styleMuted,
		Placeholder:      styleMuted,
		Prompt:           plain,
		Selection:        lipgloss.NewStyle().Reverse(true),
	}
	blurred := focused
	blurred.Text = styleMuted
	blurred.CursorLine = styleMuted
	blurred.CursorLineNumber = styleMuted
	return textarea.Styles{
		Focused: focused,
		Blurred: blurred,
		// No Color: the terminal's own cursor color, whatever the theme made it.
		Cursor: textarea.CursorStyle{Shape: textarea.DefaultDarkStyles().Cursor.Shape, Blink: true},
	}
}

func textInputStyles() textinput.Styles {
	plain := lipgloss.NewStyle()
	focused := textinput.StyleState{
		Text:        plain,
		Placeholder: styleMuted,
		Suggestion:  styleMuted,
		Prompt:      plain,
	}
	blurred := focused
	blurred.Text = styleMuted
	return textinput.Styles{
		Focused: focused,
		Blurred: blurred,
		Cursor:  textinput.CursorStyle{Shape: textinput.DefaultDarkStyles().Cursor.Shape, Blink: true},
	}
}
