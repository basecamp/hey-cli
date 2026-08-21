package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
func (p *labelPicker) overlay(base string, width, height int) string {
	modal := p.view(width, height)
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

func (p *labelPicker) view(width, height int) string {
	// Rounded borders and two cells of padding on each side use six columns.
	contentWidth := max(width-6, 1)
	title := lipgloss.NewStyle().Foreground(colorChrome).Bold(true).Render(truncateToWidth("Labels", contentWidth))
	selected := lipgloss.NewStyle().Foreground(colorActive).Bold(true)

	// Scroll the list when it cannot fit: border, padding and title take 6 lines.
	maxRows := max(height-6, 1)
	start := 0
	if p.cursor >= maxRows {
		start = p.cursor - maxRows + 1
	}
	rows := make([]string, 0, maxRows)
	for i := start; i < min(start+maxRows, len(p.names)); i++ {
		name := truncateToWidth(p.names[i], max(contentWidth-2, 1))
		if i == p.cursor {
			rows = append(rows, selected.Render("› "+name))
		} else {
			rows = append(rows, "  "+name)
		}
	}

	body := title + "\n\n" + strings.Join(rows, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorChrome).
		Padding(0, 2).
		Render(body)
}

func (p *labelPicker) helpBindings() []helpBinding {
	return []helpBinding{
		{"↑/↓", "choose"},
		{"enter", "open"},
		{"esc", "cancel"},
	}
}
