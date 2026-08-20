package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type mailSearchForm struct {
	input  textinput.Model
	status string
	styles styles
}

func newMailSearchForm(query string, styles styles) *mailSearchForm {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Search threads and messages…"
	input.SetValue(query)
	return &mailSearchForm{input: input, styles: styles}
}

func (f *mailSearchForm) init() tea.Cmd {
	return f.input.Focus()
}

func (f *mailSearchForm) resize(width, _ int) {
	f.input.SetWidth(max(width-12, 10))
}

func (f *mailSearchForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, string, bool) {
	if msg.Key().Code == tea.KeyEnter {
		query := strings.TrimSpace(f.input.Value())
		if query == "" {
			f.status = "Enter words to search for"
			return nil, "", false
		}
		return nil, query, true
	}
	return f.update(msg), "", false
}

func (f *mailSearchForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *mailSearchForm) helpBindings() []helpBinding {
	return []helpBinding{
		{"enter", "search"},
		{"esc", "cancel"},
	}
}

func (f *mailSearchForm) view() string {
	var b strings.Builder
	b.WriteString(f.styles.title.Render("Search email"))
	b.WriteString("\n\n")
	b.WriteString(styleMuted.Render("Search: "))
	b.WriteString(f.input.View())
	if f.status != "" {
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorError).Render(f.status))
	}
	return b.String()
}
