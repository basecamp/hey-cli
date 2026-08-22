package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/terminal"
)

type snippetsLoadedMsg struct {
	form      *composeForm
	requestID uint64
	snippets  []generated.Snippet
	err       error
}

type snippetPicker struct {
	input textinput.Model

	snippets []generated.Snippet
	filtered []generated.Snippet
	cursor   int
	offset   int
	height   int
	loading  bool
	err      error

	returnFocus int
}

func newSnippetPicker(returnFocus int) *snippetPicker {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Filter snippets…"
	return &snippetPicker{input: input, cursor: -1, loading: true, returnFocus: returnFocus}
}

func (p *snippetPicker) focus() tea.Cmd {
	return p.input.Focus()
}

func (p *snippetPicker) loaded(snippets []generated.Snippet, err error) {
	p.loading = false
	p.err = err
	p.snippets = snippets
	p.filter()
}

func (p *snippetPicker) selected() *generated.Snippet {
	if p.loading || p.err != nil || p.cursor < 0 || p.cursor >= len(p.filtered) {
		return nil
	}
	return &p.filtered[p.cursor]
}

func (p *snippetPicker) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool, *generated.Snippet) {
	switch msg.Key().Code {
	case tea.KeyEscape:
		return nil, false, nil
	case tea.KeyEnter:
		return nil, true, p.selected()
	case tea.KeyUp:
		if p.cursor < 0 && len(p.filtered) > 0 {
			p.cursor = len(p.filtered) - 1
		} else if p.cursor > 0 {
			p.cursor--
		}
		p.ensureVisible()
		return nil, true, nil
	case tea.KeyDown:
		if p.cursor < len(p.filtered)-1 {
			p.cursor++
		}
		p.ensureVisible()
		return nil, true, nil
	}
	return p.update(msg), true, nil
}

func (p *snippetPicker) handleMsg(msg tea.Msg) tea.Cmd {
	return p.update(msg)
}

func (p *snippetPicker) update(msg tea.Msg) tea.Cmd {
	before := p.input.Value()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != before {
		p.filter()
	}
	return cmd
}

func (p *snippetPicker) filter() {
	query := strings.ToLower(strings.TrimSpace(p.input.Value()))
	p.filtered = p.filtered[:0]
	for _, snippet := range p.snippets {
		if query == "" || strings.Contains(strings.ToLower(snippet.Name), query) {
			p.filtered = append(p.filtered, snippet)
		}
	}
	p.cursor = -1
	p.offset = 0
	p.ensureVisible()
}

func (p *snippetPicker) resize(width, height int) {
	p.height = height
	p.input.SetWidth(max(width-4, 10))
	p.ensureVisible()
}

func (p *snippetPicker) visibleRows() int {
	return max(p.height-7, 1)
}

func (p *snippetPicker) ensureVisible() {
	if len(p.filtered) == 0 {
		p.cursor = -1
		p.offset = 0
		return
	}
	if p.cursor < 0 {
		p.offset = 0
		return
	}
	p.cursor = min(p.cursor, len(p.filtered)-1)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	rows := p.visibleRows()
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
}

func (p *snippetPicker) view(s styles, width int) string {
	contentWidth := max(width-4, 1)
	var b strings.Builder
	b.WriteString(s.title.Render("Insert snippet"))
	b.WriteString("\n\n")
	b.WriteString(p.input.View())
	b.WriteString("\n\n")

	switch {
	case p.loading:
		b.WriteString(styleMuted.Render("Loading snippets…"))
	case p.err != nil:
		b.WriteString(lipgloss.NewStyle().Foreground(colorError).Render(terminal.SanitizeLine(errorNotice("Could not load snippets", p.err))))
	case len(p.snippets) == 0:
		b.WriteString(styleMuted.Render("No snippets yet. Create one with `hey snippet create`."))
	case len(p.filtered) == 0:
		b.WriteString(styleMuted.Render("No matching snippets"))
	default:
		end := min(p.offset+p.visibleRows(), len(p.filtered))
		markerStyle, selectedStyle := cursorStyles()
		for i := p.offset; i < end; i++ {
			prefix := "  "
			name := terminal.SanitizeLine(p.filtered[i].Name)
			name = truncateStr(name, max(contentWidth-2, 1))
			if i == p.cursor {
				prefix = markerStyle.Render("› ")
				name = selectedStyle.Render(name)
			}
			fmt.Fprintf(&b, "%s%s\n", prefix, name)
		}
	}
	return b.String()
}

func (p *snippetPicker) helpBindings() []helpBinding {
	return []helpBinding{{"type", "filter"}, {"↑↓", "select"}, {"enter", "insert"}, {"esc", "back"}}
}
