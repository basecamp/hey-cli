package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// calendarPicker switches calendars on and off, opened over the day with g. It is a menu
// rather than a row of tabs: a reader can be on any number of calendars, shared ones come
// and go, and the row above the grid is the span — Day, Week, Year — which is always those
// three.
//
// It is a multi-select because HEY's calendar is: a period is drawn from every calendar
// the identity has switched on at once, not from one at a time. Which are on is the
// server's answer, so the picker holds what it was handed rather than a choice of its own,
// and takes a fresh selection back from every toggle.
type calendarPicker struct {
	calendars []Calendar
	selected  map[int64]bool
	cursor    int
}

func newCalendarPicker(calendars []Calendar, selected map[int64]bool) *calendarPicker {
	return &calendarPicker{calendars: calendars, selected: selected}
}

// setCalendars takes the selection HEY answered. The cursor stays where the reader left
// it — they are working down the list — and is pulled back only when the list shrank
// under it.
func (p *calendarPicker) setCalendars(calendars []Calendar, selected map[int64]bool) {
	p.calendars = calendars
	p.selected = selected
	p.cursor = min(p.cursor, max(len(calendars)-1, 0))
}

func (p *calendarPicker) moveCursor(msg tea.KeyPressMsg) {
	p.cursor = stepListCursor(p.cursor, len(p.calendars), msg)
}

// highlighted is the calendar under the cursor, and false when the list is empty.
func (p *calendarPicker) highlighted() (Calendar, bool) {
	if p.cursor < 0 || p.cursor >= len(p.calendars) {
		return Calendar{}, false
	}
	return p.calendars[p.cursor], true
}

// draw puts the picker over the calendar it was opened from. Its rows carry each
// calendar's own color, so it lays them out itself rather than handing plain names to
// framedList.
func (p *calendarPicker) draw(base string, width, height int) string {
	contentWidth := modalContentWidth(width)
	visible := modalContentRows(height)

	var rows []string
	start, end := modalListWindow(len(p.calendars), p.cursor, visible)
	for i := start; i < end; i++ {
		calendar := p.calendars[i]
		on := p.selected[calendar.ID]
		marker := calendarMarkerStyle(calendar.Color).Render(habitMarker(on))

		label := truncateToWidth(terminal.SanitizeLine(calendar.Name), max(contentWidth-4, 1))
		labelStyle := lipgloss.NewStyle().Foreground(colorBright)
		prefix := "  "
		// A calendar switched off is still a calendar, so it dims rather than
		// disappearing — the row is how it gets switched back on.
		if !on {
			labelStyle = styleMuted
		}
		if i == p.cursor {
			labelStyle = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
			prefix = "› "
		}
		rows = append(rows, prefix+marker+" "+labelStyle.Render(label))
	}

	body := strings.Join(rows, "\n")
	if len(p.calendars) == 0 {
		body = styleMuted.Render("No other calendars")
	}
	return overlayModal(base, modalFrame("Calendars", body, width), width, height)
}

// calendarMarkerStyle is the dot beside a calendar: its own color where HEY gave it one,
// and the reader's own ink where it did not.
func calendarMarkerStyle(calendarColor string) lipgloss.Style {
	if slot, ok := heyColors[calendarColor]; ok {
		return lipgloss.NewStyle().Foreground(slot).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(colorBright).Bold(true)
}

func (p *calendarPicker) helpBindings() []helpBinding {
	return []helpBinding{{"↑↓", "choose"}, {"space", "show or hide"}, {"esc", "close"}}
}
