package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

// collectionPicker is a modal for choosing a collection (folder) to open.
// Collections stay out of the box tab row; a single "Collections" tab opens
// this picker instead.
type collectionPicker struct {
	boxIndexes []int // indexes into mailView.boxes, one per collection
	names      []string
	cursor     int
}

func newCollectionPicker(boxes []models.Box, currentIndex int) *collectionPicker {
	p := &collectionPicker{}
	for i, b := range boxes {
		if b.Kind != mailSourceKindFolder {
			continue
		}
		if i == currentIndex {
			p.cursor = len(p.boxIndexes)
		}
		p.boxIndexes = append(p.boxIndexes, i)
		p.names = append(p.names, terminalSafeFolderText(b.Name))
	}
	return p
}

func (p *collectionPicker) selectedBoxIndex() int {
	if p.cursor < 0 || p.cursor >= len(p.boxIndexes) {
		return -1
	}
	return p.boxIndexes[p.cursor]
}

func (p *collectionPicker) update(msg tea.KeyPressMsg) {
	switch msg.Key().Code {
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.KeyDown:
		if p.cursor < len(p.boxIndexes)-1 {
			p.cursor++
		}
	}
}

// overlay composites the picker as a centered modal over the base content
// using a lipgloss layer compositor.
func (p *collectionPicker) overlay(base string, width, height int) string {
	modal := p.view(height)
	x := max((width-lipgloss.Width(modal))/2, 0)
	y := max((height-lipgloss.Height(modal))/2, 0)
	compositor := lipgloss.NewCompositor(
		lipgloss.NewLayer(base).Z(0),
		lipgloss.NewLayer(modal).X(x).Y(y).Z(1),
	)
	canvas := lipgloss.NewCanvas(width, height)
	compositor.Draw(canvas, canvas.Bounds())
	return canvas.Render()
}

func (p *collectionPicker) view(height int) string {
	title := lipgloss.NewStyle().Foreground(colorChrome).Bold(true).Render("Collections")
	selected := lipgloss.NewStyle().Foreground(colorActive).Bold(true)

	// Scroll the list when it cannot fit: border, padding and title take 6 lines.
	maxRows := max(height-6, 1)
	start := 0
	if p.cursor >= maxRows {
		start = p.cursor - maxRows + 1
	}
	rows := make([]string, 0, maxRows)
	for i := start; i < min(start+maxRows, len(p.names)); i++ {
		if i == p.cursor {
			rows = append(rows, selected.Render("› "+p.names[i]))
		} else {
			rows = append(rows, "  "+p.names[i])
		}
	}

	body := title + "\n\n" + strings.Join(rows, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorChrome).
		Padding(0, 2).
		Render(body)
}

func (p *collectionPicker) helpBindings() []helpBinding {
	return []helpBinding{
		{"↑/↓", "choose"},
		{"enter", "open"},
		{"esc", "cancel"},
	}
}
