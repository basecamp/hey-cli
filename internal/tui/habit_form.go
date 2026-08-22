package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	habitvalues "github.com/basecamp/hey-cli/internal/habit"
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
	habitFieldCount
)

var habitDayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// habitForm creates and edits a habit. Only the name is typed: an icon is one of
// HEY's forty-seven, a color one of its eight and a day one of seven, so those are
// chosen with the arrow keys rather than spelled correctly from a list of accepted
// values printed underneath.
type habitForm struct {
	mode      habitFormMode
	habitID   int64
	name      textinput.Model
	icon      int
	color     int
	days      [7]bool
	dayCursor int
	focus     int
	status    string
	isError   bool
	saving    bool
	width     int
	styles    styles
}

func newHabitForm(mode habitFormMode, recording Recording, styles styles) *habitForm {
	name := textinput.New()
	name.Prompt = ""
	name.Placeholder = "Morning strength training"

	form := &habitForm{mode: mode, habitID: recording.ID, name: name, styles: styles}
	if mode == habitFormCreate {
		form.icon = indexOfIcon(habitvalues.DefaultIcon)
		form.color = indexOfColor(habitvalues.DefaultColor)
		for _, day := range habitvalues.EveryDay {
			form.days[day] = true
		}
	} else {
		form.name.SetValue(recording.Title)
		form.icon = indexOfIcon(recording.Icon)
		form.color = indexOfColor(recording.Color)
		for _, day := range recording.Days {
			if day >= 0 && day < int32(len(form.days)) {
				form.days[day] = true
			}
		}
	}
	return form
}

// indexOfIcon and indexOfColor fall back to the first value rather than refusing a
// habit whose icon this build has not heard of: HEY can add one at any time, and
// editing such a habit's name should not require knowing its icon.
func indexOfIcon(name string) int {
	for i, icon := range habitvalues.Icons {
		if icon.Name == name {
			return i
		}
	}
	return 0
}

func indexOfColor(name string) int {
	for i, color := range habitvalues.Colors {
		if color == name {
			return i
		}
	}
	return 0
}

func (f *habitForm) init() tea.Cmd { return f.focusCurrent() }

// focusCurrent puts the cursor in the name only while the name is the focused field:
// the pickers take the arrow keys, so a blinking cursor elsewhere would be a lie.
func (f *habitForm) focusCurrent() tea.Cmd {
	if f.focus == habitFieldName {
		return f.name.Focus()
	}
	f.name.Blur()
	return nil
}

// habitFormWidth is what the form asks for. A frame hugs its widest line, and the
// widest line here is the name input, so left to fill the screen the form draws a box
// as wide as the terminal around four short fields.
const habitFormWidth = 46

// resize sizes the name input for the frame the form is drawn in: the frame's own
// chrome and the label column stand to the left of every field.
func (f *habitForm) resize(width, _ int) {
	f.width = min(modalContentWidth(width), habitFormWidth)
	f.name.SetWidth(max(f.width-10, 10))
}

func (f *habitForm) values() (name, icon, color string, days []int32) {
	name = strings.TrimSpace(f.name.Value())
	icon = habitvalues.Icons[f.icon].Name
	color = habitvalues.Colors[f.color]
	for day, on := range f.days {
		if on {
			days = append(days, int32(day))
		}
	}
	return name, icon, color, days
}

func (f *habitForm) validate() string {
	name, _, _, days := f.values()
	if name == "" {
		return "Name is required"
	}
	if len(days) == 0 {
		return "Pick at least one day"
	}
	return ""
}

func (f *habitForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.saving {
		return nil, false
	}
	switch {
	case msg.Key().Code == tea.KeyTab && msg.Key().Mod == tea.ModShift:
		f.focus = (f.focus + habitFieldCount - 1) % habitFieldCount
		return f.focusCurrent(), false
	case msg.Key().Code == tea.KeyTab || msg.Key().Code == tea.KeyEnter:
		f.focus = (f.focus + 1) % habitFieldCount
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
	if f.focus == habitFieldName {
		return f.update(msg), false
	}
	f.choose(msg)
	return nil, false
}

// choose moves within the focused picker. Days are toggled rather than stepped through,
// since a habit is on any set of the seven.
func (f *habitForm) choose(msg tea.KeyPressMsg) {
	switch f.focus {
	case habitFieldIcon:
		f.icon = wrapIndex(f.icon, len(habitvalues.Icons), msg)
	case habitFieldColor:
		f.color = wrapIndex(f.color, len(habitvalues.Colors), msg)
	case habitFieldDays:
		switch msg.String() {
		case " ", "space":
			f.days[f.dayCursor] = !f.days[f.dayCursor]
		default:
			f.dayCursor = wrapIndex(f.dayCursor, len(f.days), msg)
		}
	}
}

// wrapIndex steps a picker's choice by one and comes round the other side, so the
// forty-seventh icon is one key left of the first.
func wrapIndex(index, count int, msg tea.KeyPressMsg) int {
	switch msg.Key().Code {
	case tea.KeyLeft, tea.KeyUp:
		return (index + count - 1) % count
	case tea.KeyRight, tea.KeyDown:
		return (index + 1) % count
	}
	return index
}

func (f *habitForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.name, cmd = f.name.Update(msg)
	return cmd
}

func (f *habitForm) helpBindings() []helpBinding {
	bindings := []helpBinding{{"tab", "next field"}}
	switch f.focus {
	case habitFieldIcon, habitFieldColor:
		bindings = append(bindings, helpBinding{"←→", "choose"})
	case habitFieldDays:
		bindings = append(bindings, helpBinding{"←→", "day"}, helpBinding{"space", "toggle"})
	}
	return append(bindings, helpBinding{"ctrl+s", "save"}, helpBinding{"esc", "cancel"})
}

func (f *habitForm) title() string {
	if f.mode == habitFormEdit {
		return "Edit habit"
	}
	return "Create habit"
}

// view is the form's body: the frame it stands in supplies the title and the border.
func (f *habitForm) view() string {
	var b strings.Builder
	f.writeField(&b, "Name", habitFieldName, f.name.View())
	f.writeField(&b, "Icon", habitFieldIcon, f.iconField())
	f.writeField(&b, "Color", habitFieldColor, f.colorField())
	f.writeField(&b, "Days", habitFieldDays, f.daysField())

	if f.status != "" {
		statusStyle := styleMuted
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n" + statusStyle.Render(f.status))
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeField marks the focused field's label, which is how a picker with no cursor of
// its own says that the arrow keys belong to it.
func (f *habitForm) writeField(b *strings.Builder, label string, field int, value string) {
	labelStyle := styleMuted
	if f.focus == field {
		labelStyle = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	}
	fmt.Fprintf(b, "%s %s\n", labelStyle.Render(fmt.Sprintf("%6s:", label)), value)
}

// iconField shows the chosen icon with its neighbors on either side, so stepping
// through forty-seven of them is a walk rather than a guess. The chosen one is bracketed
// rather than colored: a color emoji font paints in its own colors and ignores the
// foreground it is handed, so highlighting one by tinting it may do nothing at all.
func (f *habitForm) iconField() string {
	icon := habitvalues.Icons[f.icon]
	before := habitvalues.Icons[(f.icon+len(habitvalues.Icons)-1)%len(habitvalues.Icons)]
	after := habitvalues.Icons[(f.icon+1)%len(habitvalues.Icons)]
	bracket := lipgloss.NewStyle().Foreground(colorActive).Bold(true)

	return fmt.Sprintf("%s %s%s%s %s  %s",
		styleMuted.Render(before.Emoji),
		bracket.Render("‹"), icon.Emoji, bracket.Render("›"),
		styleMuted.Render(after.Emoji),
		lipgloss.NewStyle().Foreground(colorBright).Render(icon.Name))
}

// colorField draws all eight, since a color is chosen by seeing it.
func (f *habitForm) colorField() string {
	swatches := make([]string, 0, len(habitvalues.Colors))
	for i, name := range habitvalues.Colors {
		marker := "●"
		if i == f.color {
			marker = "◉"
		}
		swatches = append(swatches, habitMarkerStyle(name).Render(marker))
	}
	return strings.Join(swatches, " ") + "  " +
		lipgloss.NewStyle().Foreground(colorBright).Render(habitvalues.Colors[f.color])
}

// daysField draws the week with the chosen days filled in, the way HEY's own day
// toggles read.
func (f *habitForm) daysField() string {
	names := make([]string, 0, len(habitDayNames))
	for day, label := range habitDayNames {
		style := styleMuted
		if f.days[day] {
			style = lipgloss.NewStyle().Foreground(colorBright).Bold(true)
		}
		if f.focus == habitFieldDays && day == f.dayCursor {
			style = style.Underline(true)
		}
		names = append(names, style.Render(label))
	}
	return strings.Join(names, " ")
}
