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
	helpKey  string
	notice   string
}

func newHelpBar(s styles) helpBar {
	return helpBar{styles: s, helpKey: "?"}
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

func (h *helpBar) setNotice(notice string) {
	h.notice = notice
}

// height returns the number of lines the help bar occupies.
func (h helpBar) height() int {
	v := h.view()
	if v == "" {
		return 0
	}
	return strings.Count(v, "\n") + 1
}

// compactBindings keeps the everyday actions in the footer. Every binding still
// lives in bindings for the full help dialog, including the ones that do not fit.
func (h helpBar) compactBindings() []helpBinding {
	var result []helpBinding
	add := func(binding helpBinding) {
		if binding.key == "?" || binding.key == "f1" ||
			binding.desc == "quit" || binding.desc == "next row" || binding.desc == "prev row" {
			return
		}
		for _, existing := range result {
			if existing.key == binding.key {
				return
			}
		}
		result = append(result, binding)
	}
	for _, binding := range h.bindings {
		if binding.desc == "" {
			add(binding)
		}
	}
	for _, key := range []string{"ctrl+c", "↑↓", "enter", "tab", "esc/q", "esc", "/", "c", "r", "f", "v", "t", "space/x", "ctrl+s", "ctrl+a"} {
		for _, binding := range h.bindings {
			if binding.key == key {
				add(binding)
			}
		}
	}
	for _, binding := range h.bindings {
		add(binding)
	}
	return result
}

func (h helpBar) renderBinding(binding helpBinding) string {
	if binding.desc == "" {
		return binding.key
	}
	return h.styles.helpKey.Render(binding.key) + " " + h.styles.helpDesc.Render(binding.desc)
}

func (h helpBar) view() string {
	if h.width <= 0 {
		return ""
	}
	separator := h.styles.helpSep.Render(" • ")
	hint := ""
	if h.helpKey != "" {
		hint = h.renderBinding(helpBinding{h.helpKey, "help"})
	}
	if h.notice != "" {
		width := h.width
		if hint != "" {
			width -= lipgloss.Width(hint) + lipgloss.Width(separator)
			if width <= 0 {
				return ansi.Truncate(hint, h.width, "…")
			}
		}
		notice := lipgloss.NewStyle().Foreground(colorError).Render(strings.ReplaceAll(h.notice, "\n", " "))
		notice = ansi.Truncate(notice, width, "…")
		if hint != "" {
			return notice + separator + hint
		}
		return notice
	}
	var parts []string
	used := 0
	reserve := lipgloss.Width(hint)
	for _, binding := range h.compactBindings() {
		rendered := h.renderBinding(binding)
		extra := lipgloss.Width(rendered)
		if len(parts) > 0 {
			extra += lipgloss.Width(separator)
		}
		trailing := 0
		if hint != "" {
			trailing = lipgloss.Width(separator)
		}
		if used+extra+trailing+reserve > h.width {
			break
		}
		parts = append(parts, rendered)
		used += extra
	}
	if hint != "" {
		parts = append(parts, hint)
	}
	return ansi.Truncate(strings.Join(parts, separator), h.width, "…")
}

// dialogLines lays out the complete context help, independent of the footer's
// limited space. Input forms use F1 so a question mark remains text there.
func (h helpBar) dialogLines(width int) []string {
	var lines []string
	if h.notice != "" {
		lines = append(lines, strings.Split(ansi.Wrap(h.notice, width, ""), "\n")...)
		lines = append(lines, "")
	}
	groups := []struct {
		name     string
		bindings []helpBinding
	}{
		{name: "Actions"}, {name: "Filing"}, {name: "Navigation"}, {name: "App"},
	}
	for _, binding := range h.bindings {
		group := 0
		switch binding.key {
		case "?", "f1", "ctrl+a", "ctrl+c", "ctrl+c ctrl+c":
			group = 3
		case "↑↓", "←→", "tab", "shift+tab", "esc", "esc/q", "q", "j/k", "tab/shift+tab", "pgup/pgdown", "M", "o", "C", "J", "1", "2", "3", "4", "5", "6", "9", "L", "K":
			group = 2
		}
		switch binding.desc {
		case "move", "labels", "retry labels", "collections", "retry collections", "imbox", "reply later", "set aside", "feed", "paper trail", "trash", "spam", "ignore", "stop ignoring":
			group = 1
		}
		if binding.key == "?" {
			continue
		} // F1 is available in every context.
		duplicate := false
		for _, existing := range groups[group].bindings {
			if existing == binding {
				duplicate = true
				break
			}
		}
		if !duplicate {
			groups[group].bindings = append(groups[group].bindings, binding)
		}
	}
	renderGroup := func(index, columnWidth int) []string {
		group := groups[index]
		if len(group.bindings) == 0 {
			return nil
		}
		result := []string{h.styles.title.Render(group.name)}
		for _, binding := range group.bindings {
			key := binding.key
			if binding.desc != "" {
				key += strings.Repeat(" ", max(16-lipgloss.Width(key), 1))
			}
			rendered := h.styles.helpKey.Render(key) + h.styles.helpDesc.Render(binding.desc)
			result = append(result, strings.Split(ansi.Wrap(rendered, columnWidth, ""), "\n")...)
		}
		return result
	}
	column := func(indexes []int, columnWidth int) []string {
		var result []string
		for _, index := range indexes {
			group := renderGroup(index, columnWidth)
			if len(group) == 0 {
				continue
			}
			if len(result) > 0 {
				result = append(result, "")
			}
			result = append(result, group...)
		}
		return result
	}
	if width >= 72 {
		// Keep related sections together in two shorter columns on desktop-sized
		// terminals. Small terminals retain a single scrollable column.
		columnWidth := (width - 4) / 2
		left := column([]int{0, 2}, columnWidth)
		right := column([]int{1, 3}, columnWidth)
		for i := 0; i < max(len(left), len(right)); i++ {
			a, b := "", ""
			if i < len(left) {
				a = left[i]
			}
			if i < len(right) {
				b = right[i]
			}
			lines = append(lines, a+strings.Repeat(" ", columnWidth-lipgloss.Width(a)+4)+b)
		}
		return lines
	}
	return append(lines, column([]int{0, 1, 2, 3}, width)...)
}
