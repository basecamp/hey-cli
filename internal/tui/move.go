package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/models"
)

type movePicker struct {
	postingID    int64
	summary      string
	destinations []models.Box
	cursor       int
}

func newMovePicker(posting models.Posting, boxes []models.Box, currentBoxID int64) *movePicker {
	destinations := make([]models.Box, 0, len(boxes))
	for _, box := range boxes {
		if box.ID == currentBoxID || strings.EqualFold(box.Kind, hey.BoxKindBubbleUp) || strings.EqualFold(box.Name, "Bubble Up") {
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

func (p *movePicker) selected() *models.Box {
	if p.cursor < 0 || p.cursor >= len(p.destinations) {
		return nil
	}
	return &p.destinations[p.cursor]
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
