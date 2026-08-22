package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// calendarPicker chooses which calendar to read, opened over the day with C. It is a
// menu rather than a row of tabs: a reader can be on any number of calendars, shared
// ones come and go, and the row above the grid is the span — Day, Week, Year — which is
// always those three.
type calendarPicker struct {
	names  []string
	cursor int
}

func newCalendarPicker(calendars []Calendar, current int) *calendarPicker {
	picker := &calendarPicker{cursor: max(current, 0)}
	for _, calendar := range calendars {
		picker.names = append(picker.names, terminal.SanitizeLine(calendar.Name))
	}
	return picker
}

func (p *calendarPicker) moveCursor(msg tea.KeyPressMsg) {
	p.cursor = stepListCursor(p.cursor, len(p.names), msg)
}

// selected answers the index into the view's calendars, and -1 when there is nothing
// under the cursor.
func (p *calendarPicker) selected() int {
	if p.cursor < 0 || p.cursor >= len(p.names) {
		return -1
	}
	return p.cursor
}

func (p *calendarPicker) draw(base string, width, height int) string {
	return overlayModal(base, framedList("Calendars", p.names, p.cursor, width, height), width, height)
}

func (p *calendarPicker) helpBindings() []helpBinding {
	return []helpBinding{{"↑↓", "choose"}, {"enter", "open"}, {"esc", "close"}}
}
