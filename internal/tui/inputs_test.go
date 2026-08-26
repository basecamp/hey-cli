package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The text fields name no color of their own, so the theme's mode cannot change a
// byte of what they draw — the same guard TestCoversDoNotDependOnTheThemeMode gives
// the cover art. bubbles' default did depend on it: its dark palette paints a light
// terminal with a black cursor line and grey text (hey-cli#331).
func TestTextFieldsDoNotDependOnTheThemeMode(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })

	render := func(dark bool) (area, input string) {
		theme := defaultTheme()
		theme.Dark = dark
		applyTheme(theme)

		a := newTextArea()
		a.SetWidth(40)
		a.SetHeight(3)
		a.SetValue("Quarterly numbers for the board")
		a.Focus()
		i := newTextInput()
		i.SetWidth(40)
		i.SetValue("Jane Doe")
		i.Focus()
		return a.View(), i.View()
	}

	darkArea, darkInput := render(true)
	lightArea, lightInput := render(false)
	if darkArea != lightArea {
		t.Errorf("textarea renders differently on a light theme:\n%q\n%q", darkArea, lightArea)
	}
	if darkInput != lightInput {
		t.Errorf("textinput renders differently on a light theme:\n%q\n%q", darkInput, lightInput)
	}

	// The focused cursor line carries no background: that band is the defect.
	if bg, unset := textAreaStyles().Focused.CursorLine.GetBackground(), lipgloss.NewStyle().GetBackground(); bg != unset {
		t.Errorf("focused cursor line has a background %v, want none", bg)
	}
	if strings.Contains(darkArea, "\x1b[40m") || strings.Contains(darkArea, "48;5;0m") {
		t.Errorf("textarea paints a black cursor line: %q", darkArea)
	}
	if !strings.Contains(darkArea, "Quarterly numbers") || !strings.Contains(darkInput, "Jane Doe") {
		t.Errorf("fields dropped their text:\n%q\n%q", darkArea, darkInput)
	}
}
