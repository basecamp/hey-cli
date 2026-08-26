package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type journalFormAction int

const (
	journalFormNone journalFormAction = iota
	journalFormSave
	journalFormRemove
)

type journalForm struct {
	date           string
	initial        string
	input          textarea.Model
	status         string
	isError        bool
	saving         bool
	confirmRemove  bool
	confirmDiscard bool
	styles         styles
}

func newJournalForm(date, content string, styles styles) *journalForm {
	input := newTextArea()
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.Placeholder = "Write about your day…"
	input.SetValue(content)
	return &journalForm{date: date, initial: content, input: input, styles: styles}
}

func (f *journalForm) init() tea.Cmd { return f.input.Focus() }

func (f *journalForm) resize(width, height int) {
	f.input.SetWidth(max(width-4, 10))
	f.input.SetHeight(max(height-8, 3))
}

func (f *journalForm) content() string {
	return strings.TrimSpace(f.input.Value())
}

func (f *journalForm) dirty() bool {
	return f.input.Value() != f.initial
}

func (f *journalForm) canClose() bool {
	if !f.dirty() || f.confirmDiscard {
		return true
	}
	f.confirmDiscard = true
	f.confirmRemove = false
	f.status = "Press esc again to discard your changes"
	f.isError = false
	return false
}

func (f *journalForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, journalFormAction) {
	if f.saving {
		return nil, journalFormNone
	}

	switch msg.String() {
	case "ctrl+s":
		f.confirmDiscard = false
		f.confirmRemove = false
		if f.content() == "" {
			if strings.TrimSpace(f.initial) == "" {
				f.status = "Write something before saving"
			} else {
				f.status = "The entry is empty. Press ctrl+d twice to remove it"
			}
			f.isError = true
			return nil, journalFormNone
		}
		f.saving = true
		f.status = "Saving…"
		f.isError = false
		return nil, journalFormSave
	case "ctrl+d":
		f.confirmDiscard = false
		if strings.TrimSpace(f.initial) == "" {
			f.status = "There is no saved entry to remove"
			f.isError = true
			return nil, journalFormNone
		}
		if !f.confirmRemove {
			f.confirmRemove = true
			f.status = "Press ctrl+d again to permanently remove this entry"
			f.isError = false
			return nil, journalFormNone
		}
		f.saving = true
		f.status = "Removing…"
		f.isError = false
		return nil, journalFormRemove
	}

	f.confirmDiscard = false
	f.confirmRemove = false
	return f.update(msg), journalFormNone
}

func (f *journalForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *journalForm) helpBindings() []helpBinding {
	bindings := []helpBinding{{"ctrl+s", "save"}, {"esc", "cancel"}}
	if strings.TrimSpace(f.initial) != "" {
		label := "remove"
		if f.confirmRemove {
			label = "confirm remove"
		}
		bindings = append(bindings, helpBinding{"ctrl+d", label})
	}
	return bindings
}

func (f *journalForm) view() string {
	var b strings.Builder
	b.WriteString(f.styles.title.Render("Journal · " + f.date))
	b.WriteString("\n\n")
	b.WriteString(f.input.View())
	b.WriteString("\n" + styleMuted.Render("Rich formatting appears as HTML. Removal uses ctrl+d and requires confirmation."))
	if f.status != "" {
		statusStyle := styleMuted
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n" + statusStyle.Render(f.status))
	}
	return b.String()
}
