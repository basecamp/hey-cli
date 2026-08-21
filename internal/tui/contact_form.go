package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type contactFormMode int

const (
	contactFormAdd contactFormMode = iota
	contactFormEdit
)

type contactForm struct {
	mode      contactFormMode
	contactID int64
	inputs    []textinput.Model
	focus     int
	status    string
	isError   bool
	saving    bool
	styles    styles
}

const (
	contactFieldName = iota
	contactFieldEmail
	contactFieldAliases
)

func newContactForm(mode contactFormMode, contact Contact, styles styles) *contactForm {
	form := &contactForm{mode: mode, contactID: contact.ID, styles: styles}
	placeholders := []string{"Jane Doe", "jane@example.com", "jane.doe@example.org, jane@example.net"}
	for _, placeholder := range placeholders {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholder
		form.inputs = append(form.inputs, input)
	}
	if mode == contactFormEdit {
		form.inputs[contactFieldName].SetValue(contact.Name)
		form.inputs[contactFieldEmail].SetValue(contact.EmailAddress)
		aliases := make([]string, 0, len(contact.Aliases))
		for _, alias := range contact.Aliases {
			aliases = append(aliases, alias.EmailAddress)
		}
		form.inputs[contactFieldAliases].SetValue(strings.Join(aliases, ", "))
	}
	return form
}

func (f *contactForm) init() tea.Cmd { return f.focusCurrent() }

func (f *contactForm) focusCurrent() tea.Cmd {
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	return f.inputs[f.focus].Focus()
}

func (f *contactForm) resize(width, _ int) {
	for i := range f.inputs {
		f.inputs[i].SetWidth(max(width-12, 10))
	}
}

func (f *contactForm) values() (name, email string, aliases []string) {
	name = strings.TrimSpace(f.inputs[contactFieldName].Value())
	email = strings.TrimSpace(f.inputs[contactFieldEmail].Value())
	aliases = make([]string, 0)
	for _, value := range strings.Split(f.inputs[contactFieldAliases].Value(), ",") {
		if alias := strings.TrimSpace(value); alias != "" {
			aliases = append(aliases, alias)
		}
	}
	return
}

func (f *contactForm) validate() string {
	name, email, aliases := f.values()
	if name == "" {
		return "Name is required"
	}
	if email == "" {
		return "Email is required"
	}
	seen := map[string]bool{strings.ToLower(email): true}
	for _, alias := range aliases {
		key := strings.ToLower(alias)
		if seen[key] {
			return "Email addresses must be unique"
		}
		seen[key] = true
	}
	return ""
}

func (f *contactForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.saving {
		return nil, false
	}
	switch {
	case msg.Key().Code == tea.KeyTab && msg.Key().Mod == tea.ModShift:
		f.focus = (f.focus + len(f.inputs) - 1) % len(f.inputs)
		return f.focusCurrent(), false
	case msg.Key().Code == tea.KeyTab || msg.Key().Code == tea.KeyEnter:
		f.focus = (f.focus + 1) % len(f.inputs)
		return f.focusCurrent(), false
	case msg.String() == "ctrl+s":
		if problem := f.validate(); problem != "" {
			f.status = problem
			f.isError = true
			return nil, false
		}
		f.saving = true
		f.status = "Saving…"
		f.isError = false
		return nil, true
	}
	return f.update(msg), false
}

func (f *contactForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	return cmd
}

func (f *contactForm) helpBindings() []helpBinding {
	return []helpBinding{{"tab", "next field"}, {"ctrl+s", "save"}, {"esc", "cancel"}}
}

func (f *contactForm) view() string {
	title := "Add contact"
	if f.mode == contactFormEdit {
		title = "Edit contact"
	}
	labels := []string{"Name", "Email", "Aliases"}
	labelStyle := styleMuted
	var b strings.Builder
	b.WriteString(f.styles.title.Render(title))
	b.WriteString("\n\n")
	for i := range f.inputs {
		fmt.Fprintf(&b, "%s %s\n", labelStyle.Render(fmt.Sprintf("%8s:", labels[i])), f.inputs[i].View())
	}
	b.WriteString(labelStyle.Render("Aliases are comma-separated and replace the complete list."))
	if f.status != "" {
		style := labelStyle
		if f.isError {
			style = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n\n" + style.Render(f.status))
	}
	return b.String()
}

type contactNoteForm struct {
	contactID int64
	input     textarea.Model
	status    string
	isError   bool
	saving    bool
	styles    styles
}

func newContactNoteForm(contactID int64, note string, styles styles) *contactNoteForm {
	input := textarea.New()
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.Placeholder = "Add a private note…"
	input.SetValue(note)
	return &contactNoteForm{contactID: contactID, input: input, styles: styles}
}

func (f *contactNoteForm) init() tea.Cmd { return f.input.Focus() }

func (f *contactNoteForm) resize(width, height int) {
	f.input.SetWidth(max(width-4, 10))
	f.input.SetHeight(max(height-5, 3))
}

func (f *contactNoteForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.saving {
		return nil, false
	}
	if msg.String() == "ctrl+s" {
		if strings.TrimSpace(f.input.Value()) == "" {
			f.status = "Note is empty; cancel and use x to delete it"
			f.isError = true
			return nil, false
		}
		f.saving = true
		f.status = "Saving…"
		f.isError = false
		return nil, true
	}
	return f.update(msg), false
}

func (f *contactNoteForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *contactNoteForm) helpBindings() []helpBinding {
	return []helpBinding{{"ctrl+s", "save note"}, {"esc", "cancel"}}
}

func (f *contactNoteForm) view() string {
	var b strings.Builder
	b.WriteString(f.styles.title.Render("Private contact note"))
	b.WriteString("\n\n")
	b.WriteString(f.input.View())
	if f.status != "" {
		style := styleMuted
		if f.isError {
			style = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n" + style.Render(f.status))
	}
	return b.String()
}
