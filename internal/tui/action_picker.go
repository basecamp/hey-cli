package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// Keep short choices compact, but let a small terminal use all the space it has.
func actionPickerFrame(title, summary, filter, hint string, names []string, cursor, width, height int) string {
	width = min(width, 64)
	rows := modalContentRows(min(height, 18))
	contentWidth := modalContentWidth(width)
	var before, after []string
	if summary != "" && rows >= 5 {
		before = append(before, truncateToWidth(terminal.SanitizeLine(summary), contentWidth))
	}
	if filter != "" && rows >= 2 {
		before = append(before, truncateToWidth("Filter: "+terminal.SanitizeLine(filter), contentWidth))
	}
	if hint != "" && rows >= 5 {
		after = append(after, truncateToWidth(hint, contentWidth))
	}
	visible := min(8, max(rows-len(before)-len(after), 1))
	list := modalListRows(names, cursor, contentWidth, visible)
	if len(names) == 0 {
		list = []string{truncateToWidth("No matches", contentWidth)}
	}
	body := append(before, list...)
	body = append(body, after...)
	return modalFrame(title, strings.Join(body, "\n"), width)
}

func pickerNameMatches(name, filter string) bool {
	return strings.Contains(strings.ToLower(terminal.SanitizeLine(name)), strings.ToLower(filter))
}

// These lists only need an append-and-backspace filter; label creation keeps its
// existing text input with cursor movement and editing.
func editPickerFilter(filter *string, msg tea.KeyPressMsg) bool {
	previous := *filter
	switch {
	case msg.Key().Code == tea.KeyBackspace:
		if len(*filter) > 0 {
			_, size := utf8.DecodeLastRuneInString(*filter)
			*filter = (*filter)[:len(*filter)-size]
		}
	case msg.String() == "ctrl+u":
		*filter = ""
	case msg.Key().Text != "" && msg.Key().Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0:
		text := terminal.SanitizeLine(msg.Key().Text)
		runes := []rune(*filter + text)
		*filter = string(runes[:min(len(runes), 80)])
	}
	return previous != *filter
}
