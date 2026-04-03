package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ANSI colors — adapt to the user's terminal theme instead of hardcoded hex.
var (
	colorPrimary = lipgloss.BrightBlue  // titles, selected items, sender names
	colorMuted   = lipgloss.BrightBlack // borders, separators, secondary text
	colorBright  = lipgloss.BrightWhite // emphasized text
	colorError   = lipgloss.Red         // errors
)

type styles struct {
	app       lipgloss.Style
	title     lipgloss.Style // bold primary for inline titles
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
		entryFrom: lipgloss.NewStyle().Foreground(colorPrimary).Bold(true),
		entryDate: lipgloss.NewStyle().Foreground(colorMuted),
		entryBody: lipgloss.NewStyle(),
		separator: lipgloss.NewStyle().Foreground(colorMuted),
		helpKey:   lipgloss.NewStyle().Bold(true),
		helpDesc:  lipgloss.NewStyle().Foreground(colorMuted),
		helpSep:   lipgloss.NewStyle().Foreground(colorMuted),
	}
}

// --- Error display ---

// errorView renders a styled error message inside a bordered box.
func errorView(errMsg string, width int) string {
	border := lipgloss.NewStyle().Foreground(colorError)
	errStyle := lipgloss.NewStyle().Foreground(colorError).Bold(true)
	hint := lipgloss.NewStyle().Foreground(colorMuted)

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
