package tui

import (
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
var (
	colorPrimary = lipgloss.BrightBlue  // titles, selected items, sender names
	colorMuted   = lipgloss.BrightBlack // decorative filler only — see styleMuted
	colorBright  = lipgloss.BrightWhite // emphasized text
	colorAlert   = lipgloss.Red         // attention: Omarchy themes signal alerts with red
	colorError   = lipgloss.Red         // errors

	// Interface chrome (rules, tabs, hotkeys) follows eza's convention for
	// dates and directories: regular Blue for secondary chrome, bold Blue
	// for the emphasized element.
	colorChrome = lipgloss.Blue

	// The selected tab uses eza's file-owner yellow. Tabs are always bold;
	// color alone marks the selection.
	colorActive = lipgloss.Yellow
)

// styleMuted dims the theme's default foreground with the SGR faint
// attribute (what eza uses for backup files) instead of coloring it
// BrightBlack: many themes make BrightBlack nearly invisible, while a
// dimmed foreground stays legible everywhere. Use this for all secondary
// text, borders and separators.
var styleMuted = lipgloss.NewStyle().Faint(true)

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
		pill:      lipgloss.NewStyle().Foreground(lipgloss.Black).Background(colorPrimary).Bold(true).Padding(0, 1),
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

// errorView renders a styled error message inside a bordered box.
func errorView(errMsg string, width int) string {
	border := lipgloss.NewStyle().Foreground(colorError)
	errStyle := lipgloss.NewStyle().Foreground(colorError).Bold(true)
	hint := styleMuted

	maxInner := min(width-4, 60)
	if maxInner <= 0 {
		return errStyle.Render("Error: " + errMsg)
	}

	lines := wrapText(errMsg, maxInner)
	innerWidth := 0
	for _, l := range lines {
		if len(l) > innerWidth {
			innerWidth = len(l)
		}
	}

	top := border.Render("╭─ Error " + strings.Repeat("─", max(innerWidth-6, 0)) + "╮")
	bot := border.Render("╰" + strings.Repeat("─", innerWidth+2) + "╯")

	var mid strings.Builder
	for _, l := range lines {
		pad := strings.Repeat(" ", innerWidth-len(l))
		mid.WriteString(border.Render("│") + " " + errStyle.Render(l) + pad + " " + border.Render("│") + "\n")
	}

	hintLine := hint.Render("  Press ctrl+c ctrl+c to quit")

	return top + "\n" + mid.String() + bot + "\n\n" + hintLine
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
