package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

func TestEnterKeyString(t *testing.T) {
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	t.Logf("KeyEnter code: %d (0x%x)", tea.KeyEnter, tea.KeyEnter)
	t.Logf("msg.String() = %q", msg.String())
	t.Logf("msg.Key().Code = %d (0x%x)", msg.Key().Code, msg.Key().Code)
	t.Logf("msg.Key().Code == tea.KeyEnter: %v", msg.Key().Code == tea.KeyEnter)
}

func TestEnterOnBoxesTriggersFetch(t *testing.T) {
	m := modelWithBoxes()

	// Verify boxes are loaded and selected
	box := m.boxes.selectedBox()
	if box == nil {
		t.Fatal("selectedBox() is nil - no item selected in list")
	}
	t.Logf("selected box: %+v", box)
	t.Logf("filter state: %v", m.boxes.list.FilterState())

	// Press Enter
	enterMsg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	updated, cmd := m.Update(enterMsg)
	result := updated.(model)

	t.Logf("loading after enter: %v", result.loading)
	t.Logf("state after enter: %v", result.state)

	if !result.loading {
		t.Error("expected loading=true after Enter on boxes")
	}
	if cmd == nil {
		t.Error("expected a non-nil command (fetchBox)")
	}
}

func TestEnterOnBoxPostingTriggersFetch(t *testing.T) {
	m := modelWithBoxes()
	m.state = viewBox
	m.box.setItems(
		models.Box{ID: 1, Name: "Imbox"},
		testPostings(),
	)
	m.box.setSize(80, 24)

	posting := m.box.selectedPosting()
	if posting == nil {
		t.Fatal("selectedPosting() is nil")
	}
	t.Logf("selected posting: %+v", posting)
	t.Logf("posting.Topic: %+v", posting.Topic)

	enterMsg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	updated, cmd := m.Update(enterMsg)
	result := updated.(model)

	t.Logf("loading after enter: %v", result.loading)
	t.Logf("state after enter: %v", result.state)

	if !result.loading {
		t.Error("expected loading=true after Enter on posting")
	}
	if cmd == nil {
		t.Error("expected a non-nil command (fetchTopic)")
	}
}
