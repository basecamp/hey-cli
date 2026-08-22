package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// labelPicker is a modal for choosing a label (folder) to open.
// Labels stay out of the box tab row; a single "Labels" tab opens this picker.
type labelPicker struct {
	plainModal

	boxIndexes []int // indexes into mailView.boxes, one per label
	names      []string
	cursor     int
}

func newLabelPicker(boxes []mail.Source, currentIndex int) *labelPicker {
	p := &labelPicker{}
	for i, b := range boxes {
		if b.Kind != mail.KindFolder {
			continue
		}
		if i == currentIndex {
			p.cursor = len(p.boxIndexes)
		}
		p.boxIndexes = append(p.boxIndexes, i)
		p.names = append(p.names, terminal.SanitizeLine(b.Name))
	}
	return p
}

func (p *labelPicker) selectedBoxIndex() int {
	if p.cursor < 0 || p.cursor >= len(p.boxIndexes) {
		return -1
	}
	return p.boxIndexes[p.cursor]
}

func (p *labelPicker) handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.Key().Code {
	case tea.KeyEscape:
		return nil, false
	case tea.KeyEnter:
		if index := p.selectedBoxIndex(); index >= 0 {
			return view.switchBox(index), false
		}
		return nil, false
	}
	p.update(msg)
	return nil, true
}

func (p *labelPicker) draw(view *mailView) string {
	return p.overlay(view.listView(), view.vc.width, view.vc.height)
}

func (p *labelPicker) update(msg tea.KeyPressMsg) {
	p.cursor = stepListCursor(p.cursor, len(p.boxIndexes), msg)
}

func (p *labelPicker) overlay(base string, width, height int) string {
	return overlayModal(base, framedList("Labels", p.names, p.cursor, width, height), width, height)
}

func (p *labelPicker) helpBindings() []helpBinding {
	return []helpBinding{
		{"↑/↓", "choose"},
		{"enter", "open"},
		{"esc", "cancel"},
	}
}
