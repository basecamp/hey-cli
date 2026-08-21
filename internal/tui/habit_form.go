package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	habitvalues "github.com/basecamp/hey-cli/internal/habit"
	"github.com/basecamp/hey-cli/internal/models"
)

type habitFormMode int

const (
	habitFormCreate habitFormMode = iota
	habitFormEdit
)

const (
	habitFieldName = iota
	habitFieldIcon
	habitFieldColor
	habitFieldDays
)

type habitForm struct {
	mode    habitFormMode
	habitID int64
	inputs  []textinput.Model
	focus   int
	status  string
	isError bool
	saving  bool
	width   int
	styles  styles
}

func newHabitForm(mode habitFormMode, recording models.Recording, styles styles) *habitForm {
	form := &habitForm{mode: mode, habitID: recording.ID, styles: styles}
	placeholders := []string{"Morning strength training", habitvalues.DefaultIcon, habitvalues.DefaultColor, "monday,wednesday,friday"}
	for _, placeholder := range placeholders {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholder
		form.inputs = append(form.inputs, input)
	}
	if mode == habitFormCreate {
		form.inputs[habitFieldIcon].SetValue(habitvalues.DefaultIcon)
		form.inputs[habitFieldColor].SetValue(habitvalues.DefaultColor)
		form.inputs[habitFieldDays].SetValue(habitvalues.FormatDays(habitvalues.EveryDay))
	} else {
		form.inputs[habitFieldName].SetValue(recording.Title)
		form.inputs[habitFieldIcon].SetValue(recording.Icon)
		form.inputs[habitFieldColor].SetValue(recording.Color)
		form.inputs[habitFieldDays].SetValue(habitvalues.FormatDays(recording.Days))
	}
	return form
}

func (f *habitForm) init() tea.Cmd { return f.focusCurrent() }

func (f *habitForm) focusCurrent() tea.Cmd {
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	return f.inputs[f.focus].Focus()
}

func (f *habitForm) resize(width, _ int) {
	f.width = width
	for i := range f.inputs {
		f.inputs[i].SetWidth(max(width-12, 10))
	}
}

func (f *habitForm) values() (name, icon, color string, days []int32, err error) {
	name = strings.TrimSpace(f.inputs[habitFieldName].Value())
	icon = strings.TrimSpace(f.inputs[habitFieldIcon].Value())
	color = strings.TrimSpace(f.inputs[habitFieldColor].Value())
	days, err = habitvalues.ParseDays(f.inputs[habitFieldDays].Value())
	return
}

func (f *habitForm) validate() string {
	name, icon, color, _, err := f.values()
	if name == "" {
		return "Name is required"
	}
	if icon == "" {
		return "Icon is required"
	}
	if problem := habitvalues.ValidateIcon(icon); problem != nil {
		return problem.Error()
	}
	if color == "" {
		return "Color is required"
	}
	if problem := habitvalues.ValidateColor(color); problem != nil {
		return problem.Error()
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func (f *habitForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
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

func (f *habitForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	return cmd
}

func (f *habitForm) helpBindings() []helpBinding {
	return []helpBinding{{"tab", "next field"}, {"ctrl+s", "save"}, {"esc", "cancel"}}
}

func (f *habitForm) view() string {
	title := "Create habit"
	if f.mode == habitFormEdit {
		title = "Edit habit"
	}
	labels := []string{"Name", "Icon", "Color", "Days"}
	var b strings.Builder
	b.WriteString(f.styles.title.Render(title))
	b.WriteString("\n\n")
	for i := range f.inputs {
		fmt.Fprintf(&b, "%s %s\n", styleMuted.Render(fmt.Sprintf("%8s:", labels[i])), f.inputs[i].View())
	}
	guidance := []string{
		"Icons: " + habitvalues.IconValues,
		"Colors: " + habitvalues.ColorValues,
		"Days accept weekday names or 0 (Sunday) through 6 (Saturday).",
	}
	for _, text := range guidance {
		for _, line := range wrapText(text, max(f.width, 20)) {
			b.WriteString(styleMuted.Render(line) + "\n")
		}
	}
	if f.status != "" {
		statusStyle := styleMuted
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n\n" + statusStyle.Render(f.status))
	}
	return b.String()
}
