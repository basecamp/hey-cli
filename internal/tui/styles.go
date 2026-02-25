package tui

import "charm.land/lipgloss/v2"

type styles struct {
	app       lipgloss.Style
	title     lipgloss.Style
	entryFrom lipgloss.Style
	entryDate lipgloss.Style
	entryBody lipgloss.Style
	separator lipgloss.Style
}

func newStyles() styles {
	return styles{
		app:       lipgloss.NewStyle().Padding(1, 2),
		title:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FAFAFA")).Background(lipgloss.Color("#7D56F4")).Padding(0, 1),
		entryFrom: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4")),
		entryDate: lipgloss.NewStyle().Foreground(lipgloss.Color("#999999")),
		entryBody: lipgloss.NewStyle(),
		separator: lipgloss.NewStyle().Foreground(lipgloss.Color("#555555")),
	}
}
