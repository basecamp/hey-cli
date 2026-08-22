package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type eventFormMode int

const (
	eventFormCreate eventFormMode = iota
	eventFormEdit
)

const (
	eventFieldCalendar = iota
	eventFieldTitle
	eventFieldStartsDate
	eventFieldStartsTime
	eventFieldEndsDate
	eventFieldEndsTime
	eventFieldAllDay
	eventFieldNotify
	eventFieldCount
)

// eventReminder is one of the notice periods the web form offers under "Notify me". HEY
// accepts any duration, so these are a shortlist rather than a constraint.
type eventReminder struct {
	label string
	after time.Duration
}

var eventReminders = []eventReminder{
	{"no reminder", 0},
	{"5 minutes before", 5 * time.Minute},
	{"15 minutes before", 15 * time.Minute},
	{"30 minutes before", 30 * time.Minute},
	{"1 hour before", time.Hour},
	{"1 day before", 24 * time.Hour},
}

// eventForm creates and edits an event, over the day or week it was opened from. It carries
// the fields the web form leads with — which calendar, a name, when it starts and ends,
// whether it is all day, and when to be told — and not the ones behind its secondary row:
// Link, Location, Invites, Notes, Repeat, Countdown and Circle event have no SDK operation
// yet, and inventing a field the server cannot be told about would be worse than leaving it
// to the web app.
type eventForm struct {
	mode    eventFormMode
	eventID int64

	// calendars is what an event can be filed on. HEY takes a calendar_id on create and
	// not on update, so an edit shows which calendar it is on and cannot move it.
	calendars []Calendar
	calendar  int

	title      textinput.Model
	startsDate textinput.Model
	startsTime textinput.Model
	endsDate   textinput.Model
	endsTime   textinput.Model
	allDay     bool
	notify     int

	focus   int
	status  string
	isError bool
	saving  bool
	width   int
	styles  styles
}

// newEventForm opens on the day the reader is looking at, at the next whole hour, for an
// hour — the same guess the web form makes rather than an empty pair of fields.
func newEventForm(mode eventFormMode, event Recording, on time.Time, calendars []Calendar, styles styles) *eventForm {
	form := &eventForm{
		mode:      mode,
		eventID:   event.ID,
		calendars: calendars,
		styles:    styles,
		notify:    indexOfReminder(0),
	}

	form.title = eventInput("Design review", 0)
	form.startsDate = eventInput("2026-08-22", 10)
	form.startsTime = eventInput("09:00", 5)
	form.endsDate = eventInput("2026-08-22", 10)
	form.endsTime = eventInput("10:00", 5)

	if mode == eventFormEdit {
		form.title.SetValue(event.Title)
		form.allDay = event.AllDay
		form.calendar = indexOfCalendarColor(calendars, event.CalendarColor)
	}

	// An edit shows the event's own times; a new event is offered the next whole hour for
	// an hour. An event missing either time falls back to the same guess rather than to a
	// blank field.
	starts := nextWholeHour(on)
	if mode == eventFormEdit && !event.Starts().IsZero() {
		starts = event.Starts()
	}
	ends := starts.Add(time.Hour)
	if mode == eventFormEdit && !event.Ends().IsZero() {
		ends = event.Ends()
	}

	form.startsDate.SetValue(starts.Format("2006-01-02"))
	form.startsTime.SetValue(starts.Format("15:04"))
	form.endsDate.SetValue(ends.Format("2006-01-02"))
	form.endsTime.SetValue(ends.Format("15:04"))
	return form
}

func eventInput(placeholder string, width int) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	if width > 0 {
		input.SetWidth(width)
	}
	return input
}

// nextWholeHour is where a new event starts when the reader has not said: the next hour on
// the clock, so a form opened at 09:41 offers 10:00 rather than 09:41.
func nextWholeHour(at time.Time) time.Time {
	rounded := at.Truncate(time.Hour)
	if rounded.Before(at) {
		rounded = rounded.Add(time.Hour)
	}
	return rounded
}

func indexOfReminder(after time.Duration) int {
	for i, reminder := range eventReminders {
		if reminder.after == after {
			return i
		}
	}
	return 0
}

// indexOfCalendarColor finds the calendar an event is on by its color, which is all a
// recording carries of it. It falls back to the first rather than refusing the edit: an
// event on a calendar with no color — the reader's own — still has a name to change.
func indexOfCalendarColor(calendars []Calendar, calendarColor string) int {
	if calendarColor == "" {
		return 0
	}
	for i, calendar := range calendars {
		if calendar.Color == calendarColor {
			return i
		}
	}
	return 0
}

func (f *eventForm) init() tea.Cmd { return f.focusCurrent() }

// eventFormWidth is what the form asks for, for the reason the habit form gives: a frame
// hugs its widest line, and left alone this one would draw a box the width of the terminal
// around a handful of short fields.
const eventFormWidth = 52

func (f *eventForm) resize(width, _ int) {
	f.width = min(modalContentWidth(width), eventFormWidth)
	f.title.SetWidth(max(f.width-12, 10))
}

// focusCurrent puts the cursor in whichever field takes typing, and takes it out of the
// pickers — a blinking cursor on a field the arrows control would be a lie.
func (f *eventForm) focusCurrent() tea.Cmd {
	inputs := map[int]*textinput.Model{
		eventFieldTitle:      &f.title,
		eventFieldStartsDate: &f.startsDate,
		eventFieldStartsTime: &f.startsTime,
		eventFieldEndsDate:   &f.endsDate,
		eventFieldEndsTime:   &f.endsTime,
	}
	var focused tea.Cmd
	for field, input := range inputs {
		if field == f.focus {
			focused = input.Focus()
		} else {
			input.Blur()
		}
	}
	return focused
}

// skips is whether a field is not on this form at the moment. An all-day event has no times,
// so tab steps over them rather than landing on two fields that say nothing.
func (f *eventForm) skips(field int) bool {
	return f.allDay && (field == eventFieldStartsTime || field == eventFieldEndsTime)
}

func (f *eventForm) step(delta int) tea.Cmd {
	for range eventFieldCount {
		f.focus = (f.focus + delta + eventFieldCount) % eventFieldCount
		if !f.skips(f.focus) {
			break
		}
	}
	return f.focusCurrent()
}

// eventFormValues is what the form is asking the server to record.
type eventFormValues struct {
	CalendarID int64
	Title      string
	StartsAt   string
	EndsAt     string
	AllDay     bool
	StartTime  string
	EndTime    string
	TimeZone   string
	Reminders  []time.Duration
}

func (f *eventForm) values() eventFormValues {
	values := eventFormValues{
		Title:     strings.TrimSpace(f.title.Value()),
		StartsAt:  strings.TrimSpace(f.startsDate.Value()),
		EndsAt:    strings.TrimSpace(f.endsDate.Value()),
		AllDay:    f.allDay,
		StartTime: strings.TrimSpace(f.startsTime.Value()),
		EndTime:   strings.TrimSpace(f.endsTime.Value()),
		TimeZone:  time.Local.String(),
	}
	if f.calendar >= 0 && f.calendar < len(f.calendars) {
		values.CalendarID = f.calendars[f.calendar].ID
	}
	if after := eventReminders[f.notify].after; after > 0 {
		values.Reminders = []time.Duration{after}
	}
	return values
}

// validate says the first thing wrong with the form, and nothing when it is ready. The
// server would refuse most of this too, but it can only say so after a round trip.
func (f *eventForm) validate() string {
	values := f.values()

	if values.Title == "" {
		return "Name is required"
	}
	starts, err := time.ParseInLocation("2006-01-02", values.StartsAt, time.Local)
	if err != nil {
		return "Start date must be YYYY-MM-DD"
	}
	ends, err := time.ParseInLocation("2006-01-02", values.EndsAt, time.Local)
	if err != nil {
		return "End date must be YYYY-MM-DD"
	}

	if f.allDay {
		if ends.Before(starts) {
			return "The end is before the start"
		}
		return ""
	}

	startAt, err := time.Parse("15:04", values.StartTime)
	if err != nil {
		return "Start time must be HH:MM"
	}
	endAt, err := time.Parse("15:04", values.EndTime)
	if err != nil {
		return "End time must be HH:MM"
	}
	if ends.Add(clockOffset(endAt)).Before(starts.Add(clockOffset(startAt))) {
		return "The end is before the start"
	}
	return ""
}

func clockOffset(at time.Time) time.Duration {
	return time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute
}

func (f *eventForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.saving {
		return nil, false
	}
	switch {
	case msg.Key().Code == tea.KeyTab && msg.Key().Mod == tea.ModShift:
		return f.step(-1), false
	case msg.Key().Code == tea.KeyTab || msg.Key().Code == tea.KeyEnter:
		return f.step(1), false
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

	switch f.focus {
	case eventFieldCalendar:
		if len(f.calendars) > 0 {
			f.calendar = wrapIndex(f.calendar, len(f.calendars), msg)
		}
	case eventFieldNotify:
		f.notify = wrapIndex(f.notify, len(eventReminders), msg)
	case eventFieldAllDay:
		if s := msg.String(); s == " " || s == "space" {
			f.allDay = !f.allDay
		}
	default:
		return f.update(msg), false
	}
	return nil, false
}

func (f *eventForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch f.focus {
	case eventFieldTitle:
		f.title, cmd = f.title.Update(msg)
	case eventFieldStartsDate:
		f.startsDate, cmd = f.startsDate.Update(msg)
	case eventFieldStartsTime:
		f.startsTime, cmd = f.startsTime.Update(msg)
	case eventFieldEndsDate:
		f.endsDate, cmd = f.endsDate.Update(msg)
	case eventFieldEndsTime:
		f.endsTime, cmd = f.endsTime.Update(msg)
	}
	return cmd
}

func (f *eventForm) helpBindings() []helpBinding {
	bindings := []helpBinding{{"tab", "next field"}}
	switch f.focus {
	case eventFieldCalendar, eventFieldNotify:
		bindings = append(bindings, helpBinding{"←→", "choose"})
	case eventFieldAllDay:
		bindings = append(bindings, helpBinding{"space", "toggle"})
	}
	return append(bindings, helpBinding{"ctrl+s", "save"}, helpBinding{"esc", "cancel"})
}

func (f *eventForm) formTitle() string {
	if f.mode == eventFormEdit {
		return "Edit event"
	}
	return "New event"
}

func (f *eventForm) view() string {
	var b strings.Builder

	f.writeField(&b, "Calendar", eventFieldCalendar, f.calendarField())
	f.writeField(&b, "Name", eventFieldTitle, f.title.View())
	f.writeField(&b, "Starts", eventFieldStartsDate, f.startsDate.View())
	if !f.allDay {
		f.writeField(&b, "at", eventFieldStartsTime, f.startsTime.View())
	}
	f.writeField(&b, "Ends", eventFieldEndsDate, f.endsDate.View())
	if !f.allDay {
		f.writeField(&b, "at", eventFieldEndsTime, f.endsTime.View())
	}
	f.writeField(&b, "All day", eventFieldAllDay, checkbox(f.allDay))
	f.writeField(&b, "Notify", eventFieldNotify, f.notifyField())

	if f.status != "" {
		statusStyle := styleMuted
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n" + statusStyle.Render(f.status))
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeField marks the focused field's label, which is how a picker with no cursor of its
// own says the arrow keys belong to it.
func (f *eventForm) writeField(b *strings.Builder, label string, field int, value string) {
	labelStyle := styleMuted
	if f.focus == field {
		labelStyle = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	}
	fmt.Fprintf(b, "%s %s\n", labelStyle.Render(fmt.Sprintf("%-9s", label)), value)
}

func checkbox(on bool) string {
	if on {
		return "◉ yes"
	}
	return "○ no"
}

// calendarField names the calendar an event is filed on, in its own color, with the arrows
// stepping through the others. An edit says which one it is on and stops there, since HEY
// takes no calendar on update.
func (f *eventForm) calendarField() string {
	if len(f.calendars) == 0 {
		return styleMuted.Render("no calendar to file this on")
	}
	calendar := f.calendars[min(f.calendar, len(f.calendars)-1)]

	name := calendar.Name
	if name == "" {
		name = "Personal"
	}
	rendered := calendarMarkerStyle(calendar.Color).Render("●") + " " + name
	if f.mode == eventFormEdit {
		return rendered + " " + styleMuted.Render("(cannot be moved here)")
	}
	return rendered
}

func (f *eventForm) notifyField() string {
	return eventReminders[min(f.notify, len(eventReminders)-1)].label
}
