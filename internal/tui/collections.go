package tui

import (
	tea "charm.land/bubbletea/v2"

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
	filter      string
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
	choices := p.choices()
	if p.cursor < 0 || p.cursor >= len(choices) {
		return nil
	}
	return &choices[p.cursor]
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
	return overlayModal(view.contentView(), p.view(view.vc.styles, view.vc.width), view.vc.width, view.vc.height)
}

func (p *collectionMembershipPicker) choices() []mail.Collection {
	var choices []mail.Collection
	for _, collection := range p.collections {
		if pickerNameMatches(collection.Name, p.filter) {
			choices = append(choices, collection)
		}
	}
	return choices
}

func (p *collectionMembershipPicker) update(msg tea.KeyPressMsg) {
	if editPickerFilter(&p.filter, msg) {
		p.cursor = 0
		return
	}
	p.cursor = stepListCursor(p.cursor, len(p.choices()), msg)
}

func (p *collectionMembershipPicker) resize(_, height int) { p.height = height }

func (p *collectionMembershipPicker) view(_ styles, width int) string {
	choices := p.choices()
	names := make([]string, len(choices))
	for i, collection := range choices {
		mark := "[ ] "
		if p.postingHasCollection(collection.ID) {
			mark = "[x] "
		}
		names[i] = mark + terminal.SanitizeLine(collection.Name)
	}
	return actionPickerFrame("Thread collections", pickerThreadSubject(p.posting), p.filter, "Type to filter · Backspace to edit · Ctrl+U to clear", names, p.cursor, width, p.height)
}

func (p *collectionMembershipPicker) helpBindings() []helpBinding {
	return []helpBinding{{"type", "filter"}, {"↑↓", "select"}, {"enter", "toggle"}, {"esc", "cancel"}}
}
