package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	settingsFieldWeekStart = iota
	settingsFieldClock
	settingsFieldCount
)

// calendarSettingsForm edits the two calendar preferences HEY keeps on the identity: which
// day weeks start on, and whether times read on a 24-hour clock. Both live on the server —
// the web and mobile apps read the same answer — so a save is a write to HEY, not to a
// config file here.
type calendarSettingsForm struct {
	weekStart time.Weekday
	use24     bool

	// What arrived, to compare the form against: a form nobody changed makes no request.
	weekStartArrived time.Weekday
	use24Arrived     bool

	focus   int
	status  string
	isError bool
	saving  bool
	width   int
}

func newCalendarSettingsForm(weekStart time.Weekday, use24 bool) *calendarSettingsForm {
	return &calendarSettingsForm{
		weekStart:        weekStart,
		use24:            use24,
		weekStartArrived: weekStart,
		use24Arrived:     use24,
	}
}

// calendarSettingsFormWidth is what the form asks for, for the reason the habit form gives: a
// frame hugs its widest line, and left alone this one would draw a box the width of the
// terminal around two short fields.
const calendarSettingsFormWidth = 46

const settingsFieldLabelWidth = 16

func (f *calendarSettingsForm) resize(width, _ int) {
	f.width = min(modalContentWidth(width), calendarSettingsFormWidth)
}

func (f *calendarSettingsForm) step(delta int) {
	f.focus = (f.focus + delta + settingsFieldCount) % settingsFieldCount
}

// handleKey answers whether the key was the save.
func (f *calendarSettingsForm) handleKey(msg tea.KeyPressMsg) bool {
	if f.saving {
		return false
	}

	switch {
	case msg.Key().Code == tea.KeyTab && msg.Key().Mod == tea.ModShift:
		f.step(-1)
		return false
	case msg.Key().Code == tea.KeyTab, msg.Key().Code == tea.KeyEnter:
		f.step(1)
		return false
	case msg.String() == "ctrl+s":
		return f.submit()
	}

	switch f.focus {
	case settingsFieldWeekStart:
		f.chooseWeekStart(msg)
	case settingsFieldClock:
		if isSpace(msg) {
			f.use24 = !f.use24
		}
	}
	return false
}

// chooseWeekStart walks the seven days with the arrows, wrapping at either end.
func (f *calendarSettingsForm) chooseWeekStart(msg tea.KeyPressMsg) {
	switch msg.Key().Code {
	case tea.KeyUp, tea.KeyLeft:
		f.weekStart = (f.weekStart + 6) % 7
	case tea.KeyDown, tea.KeyRight:
		f.weekStart = (f.weekStart + 1) % 7
	}
}

// submit answers whether there is a write to make: a form nobody changed is told so
// instead of asking HEY to store what it already holds.
func (f *calendarSettingsForm) submit() bool {
	if !f.changed() {
		f.status = "Nothing changed"
		f.isError = false
		return false
	}
	f.saving = true
	f.status = "Saving…"
	f.isError = false
	return true
}

func (f *calendarSettingsForm) changed() bool {
	return f.weekStart != f.weekStartArrived || f.use24 != f.use24Arrived
}

func (f *calendarSettingsForm) helpBindings() []helpBinding {
	bindings := []helpBinding{{"tab", "next field"}}
	switch f.focus {
	case settingsFieldWeekStart:
		bindings = append(bindings, helpBinding{"↑↓", "a day"})
	case settingsFieldClock:
		bindings = append(bindings, helpBinding{"space", "toggle"})
	}
	return append(bindings, helpBinding{"ctrl+s", "save"}, helpBinding{"esc", "cancel"})
}

func (f *calendarSettingsForm) title() string { return "Calendar settings" }

func (f *calendarSettingsForm) view() string {
	var b strings.Builder
	f.writeRow(&b, "Start weeks on", f.weekStart.String(), settingsFieldWeekStart)
	f.writeRow(&b, "Use 24-hour time", checkbox(f.use24), settingsFieldClock)

	if f.status != "" {
		statusStyle := styleMuted
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n" + statusStyle.Render(truncateToWidth(f.status, max(f.width, 20))))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (f *calendarSettingsForm) writeRow(b *strings.Builder, label, value string, field int) {
	labelStyle := styleMuted
	if f.focus == field {
		labelStyle = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	}
	fmt.Fprintf(b, "%s %s\n", labelStyle.Render(fmt.Sprintf("%-*s", settingsFieldLabelWidth, label)), value)
}
