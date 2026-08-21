package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/mail"
)

type movePicker struct {
	plainModal

	postingID    int64
	summary      string
	destinations []mail.Source
	cursor       int
}

// newMovePicker offers the boxes a thread can be moved to: not where it already is, not
// somewhere the reader organizes mail themselves, and not Bubble Up, which HEY schedules
// rather than files into.
func newMovePicker(posting mail.Posting, boxes []mail.Source, currentSource mail.Source) *movePicker {
	destinations := make([]mail.Source, 0, len(boxes))
	for _, box := range boxes {
		isCurrentSource := box.ID == currentSource.ID && box.Kind == currentSource.Kind
		if isOrganizedMailSource(box.Kind) || isCurrentSource || box.BoxKind == hey.BoxKindBubbleUp {
			continue
		}
		destinations = append(destinations, box)
	}
	return &movePicker{
		postingID:    posting.ID,
		summary:      posting.Summary,
		destinations: destinations,
	}
}

func (p *movePicker) selected() *mail.Source {
	if p.cursor < 0 || p.cursor >= len(p.destinations) {
		return nil
	}
	return &p.destinations[p.cursor]
}

func (p *movePicker) handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.Key().Code {
	case tea.KeyEscape:
		return nil, false
	case tea.KeyEnter:
		if destination := p.selected(); destination != nil {
			return view.movePostingToBox(p.postingID, *destination), false
		}
		return nil, false
	}
	p.update(msg)
	return nil, true
}

func (p *movePicker) draw(view *mailView) string {
	return p.view(view.vc.styles, view.vc.width)
}

func (p *movePicker) update(msg tea.KeyPressMsg) {
	switch msg.Key().Code {
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.KeyDown:
		if p.cursor < len(p.destinations)-1 {
			p.cursor++
		}
	}
}

func (p *movePicker) view(styles styles, width int) string {
	contentWidth := max(width-4, 1)
	var b strings.Builder
	b.WriteString(styles.title.Render("Move thread"))
	if p.summary != "" {
		fmt.Fprintf(&b, "\n%s", truncateStr(p.summary, contentWidth))
	}
	b.WriteString("\n\n")
	for i, box := range p.destinations {
		prefix := "  "
		name := box.Name
		if i == p.cursor {
			prefix = "› "
			name = styles.title.Render(name)
		}
		fmt.Fprintf(&b, "%s%s\n", prefix, name)
	}
	b.WriteString("\n")
	b.WriteString(strings.Join(wrapText("Moving to a box other than Imbox marks the thread as seen.", contentWidth), "\n"))
	return b.String()
}

func (p *movePicker) helpBindings() []helpBinding {
	return []helpBinding{
		{"↑↓", "select"},
		{"enter", "move"},
		{"esc", "cancel"},
	}
}
