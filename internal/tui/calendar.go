package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

type recordingItem struct {
	recording models.Recording
	recType   string
}

func (i recordingItem) Title() string {
	prefix := "[All day]"
	if !i.recording.AllDay && len(i.recording.StartsAt) >= 16 {
		prefix = fmt.Sprintf("[%s]", i.recording.StartsAt[11:16])
	}
	return prefix + " " + i.recording.Title
}

func (i recordingItem) Description() string {
	var parts []string
	parts = append(parts, i.recType)
	if len(i.recording.StartsAt) >= 10 {
		parts = append(parts, i.recording.StartsAt[:10])
	}
	if i.recording.Recurring {
		parts = append(parts, "recurring")
	}
	return strings.Join(parts, " · ")
}

func (i recordingItem) FilterValue() string { return i.recording.Title }

type calendarModel struct {
	list list.Model
}

func newCalendarModel() calendarModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowStatusBar(false)
	return calendarModel{list: l}
}

func (m *calendarModel) setItems(cal models.Calendar, resp models.RecordingsResponse) tea.Cmd {
	m.list.Title = cal.Name

	// Sort type keys for stable ordering
	keys := slices.Sorted(maps.Keys(resp))

	var items []list.Item
	for _, k := range keys {
		for _, r := range resp[k] {
			items = append(items, recordingItem{recording: r, recType: k})
		}
	}
	return m.list.SetItems(items)
}

func (m *calendarModel) setSize(w, h int) {
	m.list.SetSize(w, h)
}

func (m calendarModel) update(msg tea.Msg) (calendarModel, tea.Cmd) {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m calendarModel) view() string {
	return m.list.View()
}

func (m calendarModel) selectedRecording() *models.Recording {
	item := m.list.SelectedItem()
	if item == nil {
		return nil
	}
	ri, ok := item.(recordingItem)
	if !ok {
		return nil
	}
	return &ri.recording
}
