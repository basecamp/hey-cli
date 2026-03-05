package tui

import (
	"fmt"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

type postingItem struct {
	posting models.Posting
}

func (i postingItem) Title() string {
	title := i.posting.Summary
	if title == "" && i.posting.Topic != nil {
		title = i.posting.Topic.Name
	}
	if title == "" {
		title = i.posting.Creator.Name
	}
	if !i.posting.Seen {
		return "● " + title
	}
	return "  " + title
}
func (i postingItem) Description() string {
	date := ""
	if len(i.posting.CreatedAt) >= 10 {
		date = i.posting.CreatedAt[:10]
	}
	return fmt.Sprintf("  %s · %s", i.posting.Creator.Name, date)
}
func (i postingItem) FilterValue() string {
	if i.posting.Summary != "" {
		return i.posting.Summary
	}
	if i.posting.Topic != nil {
		return i.posting.Topic.Name
	}
	return i.posting.Creator.Name
}

type boxModel struct {
	list list.Model
}

func newBoxModel() boxModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowStatusBar(false)
	return boxModel{list: l}
}

func (m *boxModel) setItems(box models.Box, postings []models.Posting) tea.Cmd {
	m.list.Title = box.Name
	items := make([]list.Item, len(postings))
	for i, p := range postings {
		items[i] = postingItem{posting: p}
	}
	return m.list.SetItems(items)
}

func (m *boxModel) setSize(w, h int) {
	m.list.SetSize(w, h)
}

func (m boxModel) update(msg tea.Msg) (boxModel, tea.Cmd) {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m boxModel) view() string {
	return m.list.View()
}

func (m boxModel) selectedPosting() *models.Posting {
	item := m.list.SelectedItem()
	if item == nil {
		return nil
	}
	pi, ok := item.(postingItem)
	if !ok {
		return nil
	}
	return &pi.posting
}
