package tui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"hey-cli/internal/models"
)

type calendarItem struct {
	calendar models.Calendar
}

func (i calendarItem) Title() string       { return i.calendar.Name }
func (i calendarItem) Description() string { return i.calendar.Kind }
func (i calendarItem) FilterValue() string { return i.calendar.Name }

type calendarsModel struct {
	list list.Model
}

func newCalendarsModel() calendarsModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Calendars  (Tab → Mail)"
	l.SetShowStatusBar(false)
	return calendarsModel{list: l}
}

func (m *calendarsModel) setItems(calendars []models.Calendar) tea.Cmd {
	items := make([]list.Item, len(calendars))
	for i, c := range calendars {
		items[i] = calendarItem{calendar: c}
	}
	return m.list.SetItems(items)
}

func (m *calendarsModel) setSize(w, h int) {
	m.list.SetSize(w, h)
}

func (m calendarsModel) update(msg tea.Msg) (calendarsModel, tea.Cmd) {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m calendarsModel) view() string {
	return m.list.View()
}

func (m calendarsModel) selectedCalendar() *models.Calendar {
	item := m.list.SelectedItem()
	if item == nil {
		return nil
	}
	ci := item.(calendarItem)
	return &ci.calendar
}
