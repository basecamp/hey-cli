package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type journalForm struct {
	date    string
	input   textarea.Model
	status  string
	isError bool
	saving  bool
	styles  styles
}

func newJournalForm(date, content string, styles styles) *journalForm {
	input := textarea.New()
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.Placeholder = "Write about your day…"
	input.SetValue(content)
	return &journalForm{date: date, input: input, styles: styles}
}

func (f *journalForm) init() tea.Cmd { return f.input.Focus() }

func (f *journalForm) resize(width, height int) {
	f.input.SetWidth(max(width-4, 10))
	f.input.SetHeight(max(height-7, 3))
}

func (f *journalForm) content() string {
	return strings.TrimSpace(f.input.Value())
}

func (f *journalForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.saving {
		return nil, false
	}
	if msg.String() == "ctrl+s" {
		f.saving = true
		f.status = "Saving…"
		f.isError = false
		return nil, true
	}
	return f.update(msg), false
}

func (f *journalForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *journalForm) helpBindings() []helpBinding {
	return []helpBinding{{"ctrl+s", "save"}, {"esc", "cancel"}}
}

func (f *journalForm) view() string {
	var b strings.Builder
	b.WriteString(f.styles.title.Render("Journal · " + f.date))
	b.WriteString("\n\n")
	b.WriteString(f.input.View())
	b.WriteString("\n" + styleMuted.Render("Rich formatting appears as HTML. Saving an empty entry removes it."))
	if f.status != "" {
		statusStyle := styleMuted
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n" + statusStyle.Render(f.status))
	}
	return b.String()
}
