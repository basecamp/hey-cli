package tui

import (
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
)

// The text fields take their palette from the theme's mode, the way the cover art
// is guarded by TestCoversDoNotDependOnTheThemeMode — except that here the two
// modes must differ: bubbles' dark default paints a light terminal with a black
// cursor line and grey text (hey-cli#331).
func TestTextFieldsFollowTheThemeMode(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultTheme()) })

	dark := defaultTheme()
	dark.Dark = true
	applyTheme(dark)
	darkArea, darkInput := newTextArea(), newTextInput()

	light := defaultTheme()
	light.Dark = false
	applyTheme(light)
	lightArea, lightInput := newTextArea(), newTextInput()

	if got, want := darkArea.Styles().Focused.CursorLine.GetBackground(), textarea.DefaultDarkStyles().Focused.CursorLine.GetBackground(); got != want {
		t.Errorf("dark textarea cursor line = %v, want bubbles' dark default %v", got, want)
	}
	if got, want := lightArea.Styles().Focused.CursorLine.GetBackground(), textarea.DefaultLightStyles().Focused.CursorLine.GetBackground(); got != want {
		t.Errorf("light textarea cursor line = %v, want bubbles' light default %v", got, want)
	}
	if darkArea.Styles().Focused.CursorLine.GetBackground() == lightArea.Styles().Focused.CursorLine.GetBackground() {
		t.Error("the textarea cursor line should change with the theme mode")
	}

	if got, want := lightInput.Styles().Blurred.Text.GetForeground(), textinput.DefaultLightStyles().Blurred.Text.GetForeground(); got != want {
		t.Errorf("light textinput blurred text = %v, want bubbles' light default %v", got, want)
	}
	if darkInput.Styles().Blurred.Text.GetForeground() == lightInput.Styles().Blurred.Text.GetForeground() {
		t.Error("the textinput blurred text should change with the theme mode")
	}
}
