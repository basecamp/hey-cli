package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// helpBinding is a key-description pair for the help bar.
type helpBinding struct {
	key  string
	desc string
}

// helpBar renders a row of key bindings at the bottom of the screen.
type helpBar struct {
	width    int
	bindings []helpBinding
	styles   styles
}

func newHelpBar(s styles) helpBar {
	return helpBar{styles: s}
}

func (h *helpBar) setWidth(w int) {
	h.width = w
}

func (h *helpBar) setBindings(bindings []helpBinding) {
	h.bindings = bindings
}

// height returns the number of lines the help bar occupies.
func (h helpBar) height() int {
	v := h.view()
	if v == "" {
		return 0
	}
	return strings.Count(v, "\n") + 1
}

func (h helpBar) view() string {
	if len(h.bindings) == 0 {
		return ""
	}

	sep := h.styles.helpSep.Render(" • ")
	sepWidth := lipgloss.Width(sep)

	type item struct {
		str   string
		width int
	}

	var items []item
	for _, b := range h.bindings {
		rendered := h.styles.helpKey.Render(b.key) + " " + h.styles.helpDesc.Render(b.desc)
		items = append(items, item{str: rendered, width: lipgloss.Width(rendered)})
	}

	maxWidth := h.width
	var lines []string
	var line strings.Builder
	lineWidth := 0

	for _, it := range items {
		if lineWidth > 0 && maxWidth > 0 && lineWidth+sepWidth+it.width > maxWidth {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
		if lineWidth > 0 {
			line.WriteString(sep)
			lineWidth += sepWidth
		}
		line.WriteString(it.str)
		lineWidth += it.width
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}

	return strings.Join(lines, "\n")
}
