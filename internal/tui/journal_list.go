package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/terminal"
)

type journalSummary struct {
	ID      int64
	Date    string
	Starts  time.Time
	Preview string
}

type journalList struct {
	entries   []journalSummary
	cursor    int
	scrollOff int
	width     int
	height    int
}

func (l *journalList) setEntries(entries []journalSummary) {
	l.entries = entries
	l.cursor = 0
	l.scrollOff = 0
}

func (l *journalList) growEntries(entries []journalSummary) {
	seen := make(map[int64]bool, len(l.entries))
	for _, entry := range l.entries {
		seen[entry.ID] = true
	}
	for _, entry := range entries {
		if !seen[entry.ID] {
			l.entries = append(l.entries, entry)
			seen[entry.ID] = true
		}
	}
}

func (l *journalList) setSize(width, height int) {
	l.width = width
	l.height = height
	l.ensureVisible()
}

func (l *journalList) moveUp() {
	if l.cursor > 0 {
		l.cursor--
		l.ensureVisible()
	}
}

func (l *journalList) moveDown() {
	if l.cursor < len(l.entries)-1 {
		l.cursor++
		l.ensureVisible()
	}
}

func (l *journalList) visibleCount() int {
	return max(l.height/2, 1)
}

func (l *journalList) hasRowsBelow() bool {
	return l.scrollOff+l.visibleCount() < len(l.entries)
}

func (l *journalList) ensureVisible() {
	visible := l.visibleCount()
	if l.cursor < l.scrollOff {
		l.scrollOff = l.cursor
	}
	if l.cursor >= l.scrollOff+visible {
		l.scrollOff = l.cursor - visible + 1
	}
}

func (l *journalList) selected() *journalSummary {
	if l.cursor < 0 || l.cursor >= len(l.entries) {
		return nil
	}
	return &l.entries[l.cursor]
}

func (l *journalList) selectDate(date string) {
	for index := range l.entries {
		if l.entries[index].Date == date {
			l.cursor = index
			l.ensureVisible()
			return
		}
	}
}

func (l *journalList) view() string {
	if len(l.entries) == 0 {
		return ""
	}
	end := min(l.scrollOff+l.visibleCount(), len(l.entries))
	cursorMarker, selected := cursorStyles()
	selectedGap := selectionStyle(lipgloss.NewStyle())
	normal := lipgloss.NewStyle().Foreground(colorBright)

	var b strings.Builder
	for index := l.scrollOff; index < end; index++ {
		entry := l.entries[index]
		active := index == l.cursor
		prefix := "  "
		if active {
			prefix = cursorMarker.Render("│") + selectedGap.Render(" ")
		}
		date := friendlyJournalDate(entry.Starts)
		preview := truncateStr(terminal.SanitizeLine(entry.Preview), max(l.width-6, 10))
		if preview == "" {
			preview = "(empty)"
		}
		if active {
			fmt.Fprintf(&b, "%s%s\n", prefix, selected.Render(date))
			fmt.Fprintf(&b, "%s%s%s\n", cursorMarker.Render("│"), selectedGap.Render("  "), selected.Render(preview))
		} else {
			fmt.Fprintf(&b, "%s%s\n", prefix, normal.Render(date))
			fmt.Fprintf(&b, "    %s\n", styleMuted.Render(preview))
		}
	}
	return b.String()
}

func friendlyJournalDate(starts time.Time) string {
	local := starts.Local()
	if local.IsZero() {
		return "Journal entry"
	}
	today := time.Now().Local()
	if sameJournalDay(local, today) {
		return "Today · " + local.Format("Monday, January 2")
	}
	return local.Format("Monday, January 2, 2006")
}

func sameJournalDay(left, right time.Time) bool {
	ly, lm, ld := left.Date()
	ry, rm, rd := right.Date()
	return ly == ry && lm == rm && ld == rd
}
