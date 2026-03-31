package tui

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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

// --- Loading wave ---

// spinnerTick returns a command that ticks every 50ms for the loading animation.
func spinnerTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// loadingView renders an animated braille ripple inside a bordered box.
func loadingView(width int, phase float64) string {
	border := lipgloss.NewStyle().Foreground(colorMuted)
	wave := lipgloss.NewStyle().Foreground(colorPrimary)

	barWidth := min(width-4, 30)
	if barWidth <= 0 {
		return "Loading..."
	}

	bar := string(generateWave(barWidth, phase))
	inner := " " + bar + " "
	innerWidth := lipgloss.Width(inner)

	top := border.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	mid := border.Render("│") + wave.Render(inner) + border.Render("│")
	bot := border.Render("╰" + strings.Repeat("─", innerWidth) + "╯")

	label := lipgloss.NewStyle().Foreground(colorMuted).Render("  Loading...")

	return top + "\n" + mid + "\n" + bot + "\n" + label
}

// generateWave creates a ripple pattern in braille — concentric rings
// expanding outward from the center, like a droplet hitting water.
func generateWave(width int, phase float64) []rune {
	cx := float64(width)
	cy := 1.5

	pattern := make([]rune, width)
	for i := range pattern {
		var bits byte
		for row := range 4 {
			for col := range 2 {
				x := float64(2*i+col) - cx
				y := (float64(row) - cy) * 2.5
				dist := math.Sqrt(x*x + y*y)
				v := math.Sin(dist*0.7 - phase)
				if v > 0.1 {
					bits |= dotBit[row][col]
				}
			}
		}
		pattern[i] = rune(0x2800 + int(bits)) //nolint:gosec // G115: bits is a byte (0-255), always valid braille range
	}
	return pattern
}

// dotBit maps (row, col) to the braille bit for that dot position.
var dotBit = [4][2]byte{
	{1 << 0, 1 << 3}, // row 0
	{1 << 1, 1 << 4}, // row 1
	{1 << 2, 1 << 5}, // row 2
	{1 << 6, 1 << 7}, // row 3
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
