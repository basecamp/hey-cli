package tui

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

type journalItem struct {
	recording models.Recording
}

func (i journalItem) Title() string {
	if len(i.recording.StartsAt) >= 10 {
		return i.recording.StartsAt[:10]
	}
	return i.recording.Title
}

func (i journalItem) Description() string {
	content := strings.TrimSpace(i.recording.Content)
	if content == "" {
		return "(empty)"
	}
	return content
}

func (i journalItem) FilterValue() string { return i.recording.Content }

type journalModel struct {
	list list.Model
}

func newJournalModel() journalModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Journal  (Tab → Mail)"
	l.SetShowStatusBar(false)
	return journalModel{list: l}
}

func (m *journalModel) setItems(recordings []models.Recording) tea.Cmd {
	var entries []models.Recording
	for _, r := range recordings {
		if r.Type == "Calendar::JournalEntry" {
			entries = append(entries, r)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StartsAt > entries[j].StartsAt
	})

	items := make([]list.Item, len(entries))
	for i, r := range entries {
		items[i] = journalItem{recording: r}
	}
	return m.list.SetItems(items)
}

func (m *journalModel) setSize(w, h int) {
	m.list.SetSize(w, h)
}

func (m journalModel) update(msg tea.Msg) (journalModel, tea.Cmd) {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m journalModel) view() string {
	return m.list.View()
}

func (m journalModel) selectedRecording() *models.Recording {
	item := m.list.SelectedItem()
	if item == nil {
		return nil
	}
	ji, ok := item.(journalItem)
	if !ok {
		return nil
	}
	return &ji.recording
}
