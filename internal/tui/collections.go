package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// collectionNavPicker chooses a collection to open from the Collections tab.
type collectionNavPicker struct {
	plainModal

	sourceIndexes []int
	names         []string
	cursor        int
}

func newCollectionNavPicker(sources []mail.Source, currentIndex int) *collectionNavPicker {
	picker := &collectionNavPicker{}
	for i, source := range sources {
		if source.Kind != mail.KindCollection {
			continue
		}
		if i == currentIndex {
			picker.cursor = len(picker.sourceIndexes)
		}
		picker.sourceIndexes = append(picker.sourceIndexes, i)
		picker.names = append(picker.names, terminal.SanitizeLine(source.Name))
	}
	return picker
}

func (p *collectionNavPicker) selectedSourceIndex() int {
	if p.cursor < 0 || p.cursor >= len(p.sourceIndexes) {
		return -1
	}
	return p.sourceIndexes[p.cursor]
}

func (p *collectionNavPicker) handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.Key().Code {
	case tea.KeyEscape:
		return nil, false
	case tea.KeyEnter:
		if index := p.selectedSourceIndex(); index >= 0 {
			return view.switchBox(index), false
		}
		return nil, false
	}
	p.update(msg)
	return nil, true
}

func (p *collectionNavPicker) draw(view *mailView) string {
	return p.overlay(view.listView(), view.vc.width, view.vc.height)
}

func (p *collectionNavPicker) update(msg tea.KeyPressMsg) {
	p.cursor = stepListCursor(p.cursor, len(p.sourceIndexes), msg)
}

func (p *collectionNavPicker) overlay(base string, width, height int) string {
	return overlayModal(base, framedList("Collections", p.names, p.cursor, width, height), width, height)
}

func (p *collectionNavPicker) helpBindings() []helpBinding {
	return []helpBinding{{"↑/↓", "choose"}, {"enter", "open"}, {"esc", "cancel"}}
}

// collectionMembershipPicker toggles the selected thread in an existing collection.
type collectionMembershipPicker struct {
	plainModal

	posting     mail.Posting
	collections []mail.Collection
	cursor      int
	offset      int
	height      int
}

func newCollectionMembershipPicker(posting mail.Posting, sources []mail.Source) *collectionMembershipPicker {
	collections := make([]mail.Collection, 0)
	for _, source := range sources {
		if source.Kind == mail.KindCollection {
			collections = append(collections, mail.Collection{ID: source.ID, Name: source.Name})
		}
	}
	return &collectionMembershipPicker{posting: posting, collections: collections}
}

func (p *collectionMembershipPicker) selected() *mail.Collection {
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

func (p *collectionMembershipPicker) handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.Key().Code {
	case tea.KeyEscape:
		return nil, false
	case tea.KeyEnter:
		collection := p.selected()
		if collection == nil {
			return nil, true
		}
		topicID := p.posting.TopicID
		if topicID == 0 {
			view.notice = "This item does not identify an email thread"
			return nil, false
		}
		if p.postingHasCollection(collection.ID) {
			return view.removePostingFromCollection(p.posting.ID, topicID, *collection), false
		}
		return view.addPostingToCollection(p.posting.ID, topicID, *collection), false
	}
	p.update(msg)
	return nil, true
}

func (p *collectionMembershipPicker) draw(view *mailView) string {
	return p.view(view.vc.styles, view.vc.width)
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

// resize takes the height alone: the rows are laid out against the width it is drawn
// with rather than a width it has to keep.
func (p *collectionMembershipPicker) resize(_, height int) {
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
		fmt.Fprintf(&body, "\n%s", truncateStr(terminal.SanitizeLine(p.posting.Summary), contentWidth))
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
		label := mark + " " + terminal.SanitizeLine(collection.Name)
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
