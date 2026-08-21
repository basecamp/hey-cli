package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

const mailSourceKindCollection = "collection"

// collectionNavPicker chooses a collection to open from the Collections tab.
type collectionNavPicker struct {
	sourceIndexes []int
	names         []string
	cursor        int
}

func newCollectionNavPicker(sources []models.Box, currentIndex int) *collectionNavPicker {
	picker := &collectionNavPicker{}
	for i, source := range sources {
		if source.Kind != mailSourceKindCollection {
			continue
		}
		if i == currentIndex {
			picker.cursor = len(picker.sourceIndexes)
		}
		picker.sourceIndexes = append(picker.sourceIndexes, i)
		picker.names = append(picker.names, terminalSafeCollectionText(source.Name))
	}
	return picker
}

func (p *collectionNavPicker) selectedSourceIndex() int {
	if p.cursor < 0 || p.cursor >= len(p.sourceIndexes) {
		return -1
	}
	return p.sourceIndexes[p.cursor]
}

func (p *collectionNavPicker) update(msg tea.KeyPressMsg) {
	switch msg.Key().Code {
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.KeyDown:
		if p.cursor < len(p.sourceIndexes)-1 {
			p.cursor++
		}
	}
}

func (p *collectionNavPicker) overlay(base string, width, height int) string {
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

func (p *collectionNavPicker) view(width, height int) string {
	contentWidth := max(width-6, 1)
	title := lipgloss.NewStyle().Foreground(colorChrome).Bold(true).Render(truncateToWidth("Collections", contentWidth))
	selected := lipgloss.NewStyle().Foreground(colorActive).Bold(true)

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

func (p *collectionNavPicker) helpBindings() []helpBinding {
	return []helpBinding{{"↑/↓", "choose"}, {"enter", "open"}, {"esc", "cancel"}}
}

// collectionMembershipPicker toggles the selected thread in an existing collection.
type collectionMembershipPicker struct {
	posting     models.Posting
	collections []models.Collection
	cursor      int
	offset      int
	height      int
}

func newCollectionMembershipPicker(posting models.Posting, sources []models.Box) *collectionMembershipPicker {
	collections := make([]models.Collection, 0)
	for _, source := range sources {
		if source.Kind == mailSourceKindCollection {
			collections = append(collections, models.Collection{ID: source.ID, Name: source.Name, AppURL: source.AppURL})
		}
	}
	return &collectionMembershipPicker{posting: posting, collections: collections}
}

func (p *collectionMembershipPicker) selected() *models.Collection {
	if p.cursor < 0 || p.cursor >= len(p.collections) {
		return nil
	}
	return &p.collections[p.cursor]
}

func (p *collectionMembershipPicker) postingHasCollection(collectionID int64) bool {
	for _, collection := range p.posting.Collections {
		if collection.ID == collectionID {
			return true
		}
	}
	return false
}

func (p *collectionMembershipPicker) update(msg tea.KeyPressMsg) {
	switch msg.Key().Code {
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.KeyDown:
		if p.cursor < len(p.collections)-1 {
			p.cursor++
		}
	}
	p.ensureVisible()
}

func (p *collectionMembershipPicker) resize(height int) {
	p.height = height
	p.ensureVisible()
}

func (p *collectionMembershipPicker) visibleRows() int {
	return max(p.height-5, 1)
}

func (p *collectionMembershipPicker) ensureVisible() {
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	rows := p.visibleRows()
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
}

func (p *collectionMembershipPicker) view(styles styles, width int) string {
	contentWidth := max(width-4, 1)
	var body strings.Builder
	body.WriteString(styles.title.Render("Thread collections"))
	if p.posting.Summary != "" {
		fmt.Fprintf(&body, "\n%s", truncateStr(p.posting.Summary, contentWidth))
	}
	body.WriteString("\n\n")
	end := min(p.offset+p.visibleRows(), len(p.collections))
	for i := p.offset; i < end; i++ {
		collection := p.collections[i]
		prefix := "  "
		if i == p.cursor {
			prefix = "› "
		}
		mark := "[ ]"
		if p.postingHasCollection(collection.ID) {
			mark = "[x]"
		}
		label := mark + " " + terminalSafeCollectionText(collection.Name)
		label = truncateStr(label, max(contentWidth-lipgloss.Width(prefix), 1))
		if i == p.cursor {
			label = styles.title.Render(label)
		}
		fmt.Fprintf(&body, "%s%s\n", prefix, label)
	}
	return body.String()
}

func (p *collectionMembershipPicker) helpBindings() []helpBinding {
	return []helpBinding{{"↑↓", "select"}, {"enter", "toggle"}, {"esc", "cancel"}}
}

func terminalSafeCollectionText(value string) string {
	return terminalSafeFolderText(value)
}
