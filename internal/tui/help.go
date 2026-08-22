package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// helpBinding is a key-description pair for the help bar.
type helpBinding struct {
	key  string
	desc string
}

// modifiersLast moves the chorded keys to the end of the bar, keeping the order
// within each group. The single-key bindings are the ones a reader reaches for
// while working through mail, and scattering ctrl+ chords among them pushes those
// onto a second line and makes the whole bar read as a lookup table. Held
// together at the end they are a group you can skip past.
func modifiersLast(bindings []helpBinding) []helpBinding {
	plain := make([]helpBinding, 0, len(bindings))
	chorded := make([]helpBinding, 0, len(bindings))
	for _, binding := range bindings {
		if strings.Contains(binding.key, "+") {
			chorded = append(chorded, binding)
		} else {
			plain = append(plain, binding)
		}
	}
	return append(plain, chorded...)
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

func (h *helpBar) setStyles(s styles) {
	h.styles = s
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
		var rendered string
		if b.desc == "" {
			// A pre-styled segment the caller rendered itself.
			rendered = b.key
		} else {
			rendered = h.styles.helpKey.Render(b.key) + " " + h.styles.helpDesc.Render(b.desc)
		}
		items = append(items, item{str: rendered, width: lipgloss.Width(rendered)})
	}

	maxWidth := h.width
	var lines []string
	var line strings.Builder
	lineWidth := 0

	for _, it := range items {
		if maxWidth > 0 && it.width > maxWidth {
			if lineWidth > 0 {
				lines = append(lines, line.String())
				line.Reset()
				lineWidth = 0
			}
			wrapped := strings.Split(ansi.Wrap(it.str, maxWidth, ""), "\n")
			lines = append(lines, wrapped[:len(wrapped)-1]...)
			line.WriteString(wrapped[len(wrapped)-1])
			lineWidth = lipgloss.Width(wrapped[len(wrapped)-1])
			continue
		}
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
