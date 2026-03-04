package tui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"hey-cli/internal/models"
)

type boxItem struct {
	box models.Box
}

func (i boxItem) Title() string       { return i.box.Name }
func (i boxItem) Description() string { return i.box.Kind }
func (i boxItem) FilterValue() string { return i.box.Name }

type boxesModel struct {
	list list.Model
}

func newBoxesModel() boxesModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Mailboxes  (Tab → Calendars)"
	l.SetShowStatusBar(false)
	return boxesModel{list: l}
}

func (m *boxesModel) setItems(boxes []models.Box) tea.Cmd {
	items := make([]list.Item, len(boxes))
	for i, b := range boxes {
		items[i] = boxItem{box: b}
	}
	return m.list.SetItems(items)
}

func (m *boxesModel) setSize(w, h int) {
	m.list.SetSize(w, h)
}

func (m boxesModel) update(msg tea.Msg) (boxesModel, tea.Cmd) {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m boxesModel) view() string {
	return m.list.View()
}

func (m boxesModel) selectedBox() *models.Box {
	item := m.list.SelectedItem()
	if item == nil {
		return nil
	}
	bi := item.(boxItem)
	return &bi.box
}
