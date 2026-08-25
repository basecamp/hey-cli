package tui

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// ANSI colors — adapt to the user's terminal theme instead of hardcoded hex.
//
// Omarchy (and terminal themes generally) define the 16 ANSI slots from the
// theme palette, and re-theme running terminals over OSC 4 on a theme switch.
// Keep these as named ANSI colors: hex values would freeze one theme's look.
// Omarchy's mapping (default/themed/*.tpl): White=foreground,
// BrightWhite=bright_foreground, BrightBlack=muted, Black=background,
// and the color names map to their theme keys (BrightYellow=bright_yellow…).
//
// applyTheme lays a Theme's accent overlay on top of the themed slots. They are
// mutated only from the Bubble Tea event loop (startup and ThemeChangedMsg),
// which is also the only place that renders.
var (
	colorPrimary  color.Color = lipgloss.BrightBlue  // titles, selected items, sender names
	colorMuted    color.Color = lipgloss.BrightBlack // decorative filler only — see styleMuted
	colorBright   color.Color = lipgloss.BrightWhite // emphasized text
	colorAlert                = lipgloss.Red         // attention: Omarchy themes signal alerts with red
	colorPositive             = lipgloss.Green       // good outcomes: a sender screened in
	colorNegative             = lipgloss.Red         // bad outcomes: a sender screened out
	colorLink     color.Color = lipgloss.BrightCyan  // hyperlinks in email bodies (markdown style "14"); subjects match it
	colorError    color.Color = lipgloss.Red         // errors

	// Interface chrome (rules, tabs, hotkeys) follows eza's convention for
	// dates and directories: regular Blue for secondary chrome, bold Blue
	// for the emphasized element.
	colorChrome = lipgloss.Blue

	// The selected tab uses eza's file-owner yellow. Tabs are always bold;
	// color alone marks the selection.
	colorActive = lipgloss.Yellow

	colorSelection color.Color                  // cursor row background; nil means none
	colorOnAccent  color.Color = lipgloss.Black // pill text on an accent-filled background

	// colorPaper is the terminal's own background and colorHues are the colors it renders
	// the ANSI slots as, both taken from the theme when it says. They are what anything
	// drawn *on* a hue needs: an ANSI slot's nominal value says nothing about what a
	// reader sees, since a theme retints the running terminal over OSC 4. ANSI blue is
	// #000080 nominally and a light periwinkle in a dark Omarchy theme.
	colorPaper color.Color = lipgloss.Black
	colorHues  map[string]color.Color
)

// styleMuted dims the theme's default foreground with the SGR faint
// attribute (what eza uses for backup files) instead of coloring it
// BrightBlack: many themes make BrightBlack nearly invisible, while a
// dimmed foreground stays legible everywhere. Use this for all secondary
// text, borders and separators.
var styleMuted = lipgloss.NewStyle().Faint(true)

// applyTheme makes theme the active palette. Styles built before the call keep the
// old colors — rebuild them with newStyles.
func applyTheme(theme Theme) {
	colorPrimary = theme.Accent
	colorMuted = theme.Muted
	colorBright = theme.Bright
	colorLink = lipgloss.BrightCyan
	colorError = theme.Error
	colorSelection = nil
	if theme.Selection != nil && (theme.Trusted || contrastRatio(theme.Accent, theme.Selection) >= minSelectionContrast) {
		colorSelection = theme.Selection
	}
	_, coverColorless = theme.Accent.(lipgloss.NoColor)
	// The pill keeps its classic black text on the ANSI accent, but a themed
	// accent can be dark enough that black is unreadable on it — lupine's blue
	// carries black at only 3.7:1 — so pick whichever of black or white reads
	// better there.
	colorOnAccent = lipgloss.Black
	if nc, ok := theme.Accent.(lipgloss.NoColor); ok {
		colorLink = nc
		colorOnAccent = nc
	} else if theme.Accent != color.Color(lipgloss.BrightBlue) &&
		contrastRatio(lipgloss.BrightWhite, theme.Accent) > contrastRatio(lipgloss.Black, theme.Accent) {
		colorOnAccent = lipgloss.BrightWhite
	}
	colorHues = theme.Hues
	colorPaper = theme.Background
	if colorPaper == nil {
		colorPaper = lipgloss.Black
		if !theme.Dark {
			colorPaper = lipgloss.BrightWhite
		}
	}
	if !theme.Dark && theme.Bright == lipgloss.BrightWhite {
		// Bright white is the background on a light terminal; ANSI black is its text.
		colorBright = lipgloss.Black
	}
}

// selectionStyle returns a style that paints the selection background when the
// theme has one, and the style unchanged otherwise. Every segment of a cursor row
// goes through it so the row reads as one highlighted line.
func selectionStyle(s lipgloss.Style) lipgloss.Style {
	if colorSelection == nil {
		return s
	}
	return s.Background(colorSelection)
}

// applyTheme keeps colorSelection only when the accent still reads on it: the
// cursor row is accent-colored text, and on a third of Omarchy's themes the
// accent-on-selection pair falls under 3:1 while 4.5:1 is where bold text
// stays crisp. Below that the row keeps the accent and drops the tint.
const minSelectionContrast = 4.5

// cursorStyles returns the styles for the cursor row of a list: the marker (the
// │ bar) and the text. Both are the accent, bold — the accent is what makes the
// row read as selected — over the selection background when the theme has a
// usable one.
func cursorStyles() (marker, text lipgloss.Style) {
	marker = selectionStyle(lipgloss.NewStyle().Foreground(colorPrimary).Bold(true))
	return marker, marker
}

// contrastRatio is WCAG relative-luminance contrast, 1 (identical) to 21.
func contrastRatio(a, b color.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	channel := func(v uint32) float64 {
		s := float64(v) / 0xffff
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

type styles struct {
	app       lipgloss.Style
	title     lipgloss.Style // bold primary for inline titles
	pill      lipgloss.Style // filled button, for a call to action above a list
	entryFrom lipgloss.Style
	entryDate lipgloss.Style
	entryBody lipgloss.Style
	separator lipgloss.Style
	helpKey   lipgloss.Style
	helpDesc  lipgloss.Style
	helpSep   lipgloss.Style
}

func newStyles() styles {
	return styles{
		app:       lipgloss.NewStyle().Padding(1, 2),
		title:     lipgloss.NewStyle().Foreground(colorPrimary).Bold(true),
		pill:      lipgloss.NewStyle().Foreground(colorOnAccent).Background(colorPrimary).Bold(true).Padding(0, 1),
		entryFrom: lipgloss.NewStyle().Foreground(colorPrimary).Bold(true),
		entryDate: styleMuted,
		entryBody: lipgloss.NewStyle(),
		separator: lipgloss.NewStyle().Foreground(colorChrome),
		helpKey:   lipgloss.NewStyle().Foreground(colorChrome).Bold(true),
		helpDesc:  lipgloss.NewStyle().Foreground(colorChrome),
		helpSep:   lipgloss.NewStyle().Foreground(colorChrome),
	}
}

// --- Error display ---

const errorViewHint = "esc to dismiss · ctrl+c ctrl+c to quit"

// errorView renders a styled error message inside a bordered box. It is drawn over the
// section it interrupted, so every line is padded to one width: an overlay's blank
// cells are what keep the content beneath from bleeding through.
func errorView(errMsg string, width int) string {
	border := lipgloss.NewStyle().Foreground(colorError)
	errStyle := lipgloss.NewStyle().Foreground(colorError).Bold(true)
	hint := styleMuted

	maxInner := min(width-4, 60)
	if maxInner <= 0 {
		return errStyle.Render("Error: " + errMsg)
	}

	lines := wrapText(errMsg, maxInner)
	innerWidth := 6
	for _, l := range lines {
		if len(l) > innerWidth {
			innerWidth = len(l)
		}
	}

	hintText := "  " + errorViewHint
	blockWidth := max(innerWidth+4, lipgloss.Width(hintText))
	padTo := func(rendered string) string {
		return rendered + strings.Repeat(" ", max(blockWidth-lipgloss.Width(rendered), 0))
	}

	var b strings.Builder
	b.WriteString(padTo(border.Render("╭─ Error "+strings.Repeat("─", innerWidth-6)+"╮")) + "\n")
	for _, l := range lines {
		pad := strings.Repeat(" ", innerWidth-len(l))
		b.WriteString(padTo(border.Render("│")+" "+errStyle.Render(l)+pad+" "+border.Render("│")) + "\n")
	}
	b.WriteString(padTo(border.Render("╰"+strings.Repeat("─", innerWidth+2)+"╯")) + "\n")
	b.WriteString(strings.Repeat(" ", blockWidth) + "\n")
	b.WriteString(padTo(hint.Render(hintText)))
	return b.String()
}

// wrapText wraps a string to fit within maxWidth characters.
func wrapText(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{s}
	}

	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > maxWidth {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	lines = append(lines, line)
	return lines
}
