package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// coverPicker chooses the art over the Imbox's Previously Seen. The choice lasts
// as long as the session: HEY serves a box's cover to the web app but not over
// JSON, so there is nothing to read it back from and nowhere it belongs on disk.
type coverPicker struct {
	plainModal

	choices []coverPreset
	cursor  int
	art     coverRenderer
}

// coverPreviewRows is how much of the highlighted cover the picker shows. Enough
// to tell the six apart, which is less than they need to look their best.
const coverPreviewRows = 9

func newCoverPicker(current coverPreset) *coverPicker {
	picker := &coverPicker{
		choices: []coverPreset{
			coverNone, coverBlobs, coverGrid, coverPeace, coverTerrazzo, coverTopo, coverWaves,
		},
	}
	for i, choice := range picker.choices {
		if choice == current {
			picker.cursor = i
			break
		}
	}
	return picker
}

func (p *coverPicker) selected() coverPreset {
	if p.cursor < 0 || p.cursor >= len(p.choices) {
		return coverNone
	}
	return p.choices[p.cursor]
}

func (p *coverPicker) handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.Key().Code {
	case tea.KeyEscape:
		return nil, false
	case tea.KeyEnter:
		view.applyCover(p.selected())
		return nil, false
	}
	p.update(msg)
	return nil, true
}

func (p *coverPicker) draw(view *mailView) string {
	return p.view(view.vc.styles, view.vc.width)
}

func (p *coverPicker) update(msg tea.KeyPressMsg) {
	switch msg.Key().Code {
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.KeyDown:
		if p.cursor < len(p.choices)-1 {
			p.cursor++
		}
	}
}

// view lists the presets and draws the highlighted one underneath, because a
// cover is picked by looking at it rather than by reading six words.
func (p *coverPicker) view(styles styles, width int) string {
	contentWidth := max(width-4, 1)

	var b strings.Builder
	b.WriteString(styles.title.Render("Cover the Imbox"))
	b.WriteString("\n\n")

	for i, choice := range p.choices {
		prefix, label := "  ", coverLabel(choice)
		if i == p.cursor {
			prefix, label = "› ", styles.title.Render(label)
		}
		fmt.Fprintf(&b, "%s%s\n", prefix, label)
	}

	b.WriteString("\n")
	if preview := p.art.view(p.selected(), contentWidth, coverPreviewRows); preview != "" {
		b.WriteString(preview + "\n\n")
	}
	b.WriteString(strings.Join(wrapText(
		"A cover hides the threads you have already read. Press v to look under it.",
		contentWidth), "\n"))
	return b.String()
}

func coverLabel(preset coverPreset) string {
	if preset == coverNone {
		return "No cover"
	}
	return strings.ToUpper(string(preset)[:1]) + string(preset)[1:]
}

func (p *coverPicker) helpBindings() []helpBinding {
	return []helpBinding{
		{"↑↓", "select"},
		{"enter", "cover"},
		{"esc", "cancel"},
	}
}
