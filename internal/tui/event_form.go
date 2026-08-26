package tui

import (
	"fmt"
	stdhtml "html"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/terminal"
)

type eventFormMode int

const (
	eventFormCreate eventFormMode = iota
	eventFormEdit
)

// The fields down to eventFieldMore are the everyday ones and are always on screen; the rest
// are behind the More row. What is showing at any moment is fields().
const (
	eventFieldCalendar = iota
	eventFieldTitle
	eventFieldAllDay
	eventFieldStarts
	eventFieldEnds
	eventFieldNotify
	eventFieldMore
	eventFieldLocation
	eventFieldLink
	eventFieldInvites
	eventFieldNotes
	eventFieldRepeat
	eventFieldRepeatUntil
	eventFieldRepeatValue
	eventFieldCountdown
	eventFieldCountdownUnit
	eventFieldCircle
)

// eventReminder is one of the notice periods the web form offers under "Notify me". HEY
// accepts any duration, so these are the web form's own presets rather than a constraint.
// The labels are short because the whole set is on one line: a reader picking two reminders
// should see both without the modal growing a scroller.
type eventReminder struct {
	label string
	after time.Duration
}

var eventReminders = []eventReminder{
	{"5m", 5 * time.Minute},
	{"10m", 10 * time.Minute},
	{"15m", 15 * time.Minute},
	{"30m", 30 * time.Minute},
	{"1h", time.Hour},
	{"2h", 2 * time.Hour},
	{"1d", 24 * time.Hour},
}

// eventForm creates and edits an event, over the day or week it was opened from. It leads with
// what the web form leads with — which calendar, a name, when it starts and ends, whether it is
// all day, and when to be told — and keeps the rest behind a More row, the way the web form
// keeps them behind its secondary row: the everyday event is a name, a time and a calendar, and
// a modal that asks fourteen questions to record one is a wall.
//
// The reader opens the row with space. An edit whose event already has any of those fields
// opens with it open, because a form that hides what is there invites a save that throws it
// away — see setDetails, and the cautions HEY's own semantics make necessary.
type eventForm struct {
	mode    eventFormMode
	eventID int64

	// calendars is what an event can be filed on, and moved to: an update takes a calendar the
	// same way a create does.
	calendars []Calendar
	calendar  int

	title  textinput.Model
	allDay bool

	// starts and ends each ask their whole question — which day, what time, on whose clock —
	// so every decision about when the event is sits in one place rather than spread over
	// four fields and a toggle.
	starts *dateTimePicker
	ends   *dateTimePicker

	// chosenReminders runs alongside eventReminders, and notify is the one the arrows are on.
	chosenReminders []bool
	notify          int

	// revealed is the More row: everything below it is on screen only while it is true.
	revealed bool

	location textinput.Model
	link     textinput.Model
	invites  textinput.Model
	notes    textarea.Model

	repeat        *eventRepeat
	countdown     textinput.Model
	countdownUnit int
	circled       bool

	// invitesArrived is the guest list as the event had it, and what values() compares against:
	// HEY replaces a roster wholesale, so an untouched list has to be left out of the write
	// rather than sent back.
	invitesArrived string
	// notesArrived says the event turned up with notes, which is when saving costs their
	// formatting — HEY serves notes as plain text and takes them as rich text.
	notesArrived bool
	// attachedEntryID is the email attached to the event, carried through a save untouched. It
	// is not shown: there is nothing here to attach or detach one with, and leaving it out of
	// the write would detach it.
	attachedEntryID int64

	focus   int
	status  string
	isError bool
	saving  bool
	width   int
	styles  styles
}

// newEventForm opens on the day the reader is looking at, at the next whole hour, for an
// hour — the same guess the web form makes rather than an empty pair of fields.
//
// lastCalendarID is the calendar this reader filed on last. A new event opens on it while it
// is still one of the offered ones, because somebody who keeps a work calendar and a personal
// one files on the same one all week. An edit opens on the event's own calendar instead.
func newEventForm(mode eventFormMode, event Recording, on time.Time, calendars []Calendar, lastCalendarID int64, styles styles) *eventForm {
	form := &eventForm{
		mode:            mode,
		eventID:         event.ID,
		calendars:       calendars,
		styles:          styles,
		chosenReminders: make([]bool, len(eventReminders)),
	}

	form.title = eventInput("Design review", 0)
	form.location = eventInput("Conference room, or a city", 0)
	form.link = eventInput("meet.example.com/design-review", 0)
	form.invites = eventInput("amelia@example.com, kevin@example.org", 0)
	form.notes = eventNotesInput()
	form.repeat = newEventRepeat()
	form.countdown = eventInput("none", 4)

	if mode == eventFormEdit {
		form.title.SetValue(event.Title)
		form.allDay = event.AllDay
		form.calendar = indexOfCalendar(calendars, event)
	} else {
		form.calendar = indexOfCalendarID(calendars, lastCalendarID)
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

	// An event saved with zones of its own is shown on the clock it was written on — 09:00 in
	// Madrid stays 09:00 in Madrid, wherever it is being read. Everything else is the reader's
	// own clock, which the picker calls Local and sends as UTC.
	form.starts = newDateTimePicker(inZoneNamed(starts, event.StartsAtZone), form.allDay)
	form.ends = newDateTimePicker(inZoneNamed(ends, event.EndsAtZone), form.allDay)
	if !form.allDay {
		form.starts.setZoneName(event.StartsAtZone)
		form.ends.setZoneName(event.EndsAtZone)
	}
	return form
}

func eventInput(placeholder string, width int) textinput.Model {
	input := newTextInput()
	input.Prompt = ""
	input.Placeholder = placeholder
	if width > 0 {
		input.SetWidth(width)
	}
	return input
}

// eventNotesInput is the one field somebody writes a paragraph into, so it takes several lines
// and enter puts a new one in rather than moving on. Tab is how the reader leaves it.
func eventNotesInput() textarea.Model {
	input := newTextArea()
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.Placeholder = "Agenda, what to bring, anything"
	return input
}

var eventCountdownUnits = []struct {
	label string
	unit  hey.CountdownUnit
}{
	{"days", hey.CountdownUnitDays},
	{"weeks", hey.CountdownUnitWeeks},
	{"months", hey.CountdownUnitMonths},
}

// eventDetails is what an edit needs to know about the event beyond its name and times: the
// fields behind the More row. The form is handed them rather than defaulting to blank because
// HEY replaces an event's content wholesale on every write — an update that says nothing about
// the notes, the location, the link or the attached email clears all four.
type eventDetails struct {
	Notes    string
	Location string
	Link     string
	Invites  []string
	Circled  bool
	// Repeats and RepeatKind are the schedule the event already has. HEY serves the kind and
	// neither the date nor the count the series runs until, so the form offers to keep the
	// schedule rather than to re-send it.
	Repeats    bool
	RepeatKind string
	// AttachedEntryID is the email attached to the event, carried through a save untouched.
	AttachedEntryID int64
}

// setDetails fills the More row in from the event being edited and opens it whenever there is
// anything in there to see. A countdown is not among these: HEY keeps one as a child recording
// and serves it on nothing, so the form cannot show what an event has — which is why an edit
// says out loud that saving clears it.
func (f *eventForm) setDetails(details eventDetails) {
	f.notes.SetValue(details.Notes)
	f.notesArrived = strings.TrimSpace(details.Notes) != ""
	f.location.SetValue(details.Location)
	f.link.SetValue(details.Link)
	f.invitesArrived = strings.Join(details.Invites, ", ")
	f.invites.SetValue(f.invitesArrived)
	f.circled = details.Circled
	f.attachedEntryID = details.AttachedEntryID
	if details.Repeats {
		f.repeat.keepSchedule(details.RepeatKind)
	}
	f.revealed = f.revealed || details.anySet()
}

func (d eventDetails) anySet() bool {
	return strings.TrimSpace(d.Notes) != "" || strings.TrimSpace(d.Location) != "" ||
		strings.TrimSpace(d.Link) != "" || len(d.Invites) > 0 ||
		d.Circled || d.Repeats || d.AttachedEntryID != 0
}

// inZoneNamed moves an instant onto the clock of the zone named, and leaves it where it is
// when the name means nothing here — a zone HEY knows and this machine's database does not is
// no reason to show the reader an empty field.
func inZoneNamed(at time.Time, name string) time.Time {
	if zone, ok := loadEventZone(name); ok {
		return at.In(zone)
	}
	return at
}

func loadEventZone(name string) (*time.Location, bool) {
	if name == "" {
		return nil, false
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	return zone, true
}

// localZoneName is the IANA name of the reader's own zone, which is what HEY wants and what Go
// will not always tell you: time.Local.String() answers "Local" whenever Go read
// /etc/localtime rather than a name out of $TZ, so the name has to be recovered from where it
// read it. A machine that will not say gets no zone rather than a wrong one — the picker then
// stays on Local and the times go as UTC, which needs no name at all.
func localZoneName() string {
	if name := time.Local.String(); name != "Local" {
		return name
	}
	if name := strings.TrimPrefix(os.Getenv("TZ"), ":"); zoneMatchesLocal(name) {
		return name
	}
	link, err := os.Readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	if _, name, found := strings.Cut(link, "zoneinfo/"); found && zoneMatchesLocal(name) {
		return name
	}
	return ""
}

// zoneMatchesLocal is the check that keeps a stale name out. $TZ can hold a POSIX rule rather
// than a name, and /etc/localtime can have been replaced since the process started, so a name
// counts only if the clock it keeps is the one Go is already keeping.
func zoneMatchesLocal(name string) bool {
	zone, ok := loadEventZone(name)
	if !ok {
		return false
	}
	now := time.Now()
	_, named := now.In(zone).Zone()
	_, local := now.Local().Zone()
	return named == local
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

// indexOfCalendar finds the calendar an event is filed on. The id is the answer where the
// event carries one, and its color is the fallback for an event read from somewhere that does
// not — two calendars can wear the same color, so the color alone can land on the wrong one.
//
// It falls back to the first rather than refusing the edit: an event on a calendar this reader
// cannot file on still has a name to change.
func indexOfCalendar(calendars []Calendar, event Recording) int {
	for i, calendar := range calendars {
		if event.CalendarID != 0 && calendar.ID == event.CalendarID {
			return i
		}
	}
	if event.CalendarColor == "" {
		return 0
	}
	for i, calendar := range calendars {
		if calendar.Color == event.CalendarColor {
			return i
		}
	}
	return 0
}

// indexOfCalendarID answers the first calendar for an id that is not among them, which is
// what a remembered calendar the reader has since lost access to should fall back to.
func indexOfCalendarID(calendars []Calendar, id int64) int {
	if id == 0 {
		return 0
	}
	for i, calendar := range calendars {
		if calendar.ID == id {
			return i
		}
	}
	return 0
}

func (f *eventForm) init() tea.Cmd { return f.takeFocus(1) }

// eventFormWidth is what the form asks for, for the reason the habit form gives: a frame
// hugs its widest line, and left alone this one would draw a box the width of the terminal
// around a handful of short fields.
const eventFormWidth = 52

func (f *eventForm) resize(width, height int) {
	f.width = min(modalContentWidth(width), eventFormWidth)
	value := max(f.width-eventFieldLabelWidth-1, 10)
	f.title.SetWidth(value)
	f.location.SetWidth(value)
	f.link.SetWidth(value)
	f.invites.SetWidth(value)
	f.notes.SetWidth(value)

	// Everything but the notes takes some nineteen rows once the More row is open, so a short
	// screen gets fewer lines of them rather than a modal taller than the terminal.
	f.notes.SetHeight(min(3, max(modalContentRows(height)-19, 1)))
}

// fields is the tab order as the form stands: the everyday questions, the More row, and then
// whatever that row is showing. The repeat's end and the countdown's unit only appear once
// they have something to say, so tab never stops on a field that is not asking anything.
func (f *eventForm) fields() []int {
	fields := []int{
		eventFieldCalendar, eventFieldTitle, eventFieldAllDay,
		eventFieldStarts, eventFieldEnds, eventFieldNotify, eventFieldMore,
	}
	if !f.revealed {
		return fields
	}

	fields = append(fields,
		eventFieldLocation, eventFieldLink, eventFieldInvites, eventFieldNotes, eventFieldRepeat)
	if f.repeat.repeats() {
		fields = append(fields, eventFieldRepeatUntil)
		if f.repeat.needsValue() {
			fields = append(fields, eventFieldRepeatValue)
		}
	}
	fields = append(fields, eventFieldCountdown)
	if f.countdownValue() != "" {
		fields = append(fields, eventFieldCountdownUnit)
	}
	return append(fields, eventFieldCircle)
}

// input is the field that takes typing, or nil for one the arrows or space work.
func (f *eventForm) input(field int) *textinput.Model {
	switch field {
	case eventFieldTitle:
		return &f.title
	case eventFieldLocation:
		return &f.location
	case eventFieldLink:
		return &f.link
	case eventFieldInvites:
		return &f.invites
	case eventFieldRepeatValue:
		return f.repeat.valueInput()
	case eventFieldCountdown:
		return &f.countdown
	}
	return nil
}

func (f *eventForm) picker(field int) *dateTimePicker {
	switch field {
	case eventFieldStarts:
		return f.starts
	case eventFieldEnds:
		return f.ends
	}
	return nil
}

// step walks the form's fields, and a date-time picker's own fields while it is on one: the
// picker says when tab has walked off its end, and that is when the form takes the focus back.
func (f *eventForm) step(delta int) tea.Cmd {
	if picker := f.picker(f.focus); picker != nil {
		if cmd, inside := picker.step(delta); inside {
			return cmd
		}
	}
	fields := f.fields()
	at := max(slices.Index(fields, f.focus), 0)
	f.focus = fields[(at+delta+len(fields))%len(fields)]
	return f.takeFocus(delta)
}

// takeFocus puts the cursor in whichever field takes typing and keeps it out of the pickers —
// a blinking cursor on a field the arrows control would be a lie. A picker is entered at
// whichever end tab arrived from.
func (f *eventForm) takeFocus(delta int) tea.Cmd {
	f.title.Blur()
	f.location.Blur()
	f.link.Blur()
	f.invites.Blur()
	f.countdown.Blur()
	f.notes.Blur()
	f.repeat.blur()
	f.starts.blur()
	f.ends.blur()

	if picker := f.picker(f.focus); picker != nil {
		if delta < 0 {
			return picker.focusLast()
		}
		return picker.focusFirst()
	}
	if f.focus == eventFieldNotes {
		return f.notes.Focus()
	}
	if input := f.input(f.focus); input != nil {
		return input.Focus()
	}
	return nil
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
	// StartTimeZone and EndTimeZone are the zones the clock times above are written in, and
	// they are empty for a moment left on Local — that says UTC, which is how HEY reads a time
	// nobody named a zone for. Sending them empty on an update is not the same as leaving
	// them out: it clears the zones the event had, which is what moving back to Local means.
	StartTimeZone string
	EndTimeZone   string
	// Reminders is every notice period chosen, and HEY reads it as the whole set: an update
	// carrying none removes the reminders the event had. HEY also reads only the field
	// matching the event's all-day-ness, which is why the same durations mean something
	// different on an all-day event.
	Reminders []time.Duration
	// Notes is Trix HTML, because that is what HEY stores an event's notes as. What comes back
	// on a read is plain text, so this does not round-trip: an edit sends the notes as the
	// reader now sees them, and whatever formatting they had is replaced. The form says so.
	Notes    string
	Location string
	Link     string
	// AttachedEntryID is the email the event already had attached, sent back so the write does
	// not detach it. Nothing in the form attaches one.
	AttachedEntryID int64
	// Invites is the guest list, and nil means leave it alone — HEY replaces the roster with
	// whatever it is sent, so an untouched list must not be sent at all. An empty non-nil slice
	// is the reader clearing it, which removes every guest.
	Invites []string
	// Circled is HEY's highlighted flag, "Circle event" on its own form.
	Circled bool
	// Countdown is resend-or-lose-it like the content: the zero value deletes the countdown the
	// event had. HEY serves a countdown on nothing, so the form cannot show one and an edit
	// carries the warning instead.
	Countdown hey.CountdownParams
	// Repeat is nil whenever the reader did not name a frequency, which leaves the event's
	// recurrence exactly as it is.
	Repeat *hey.RepeatParams
}

// values is what goes on the wire.
//
// HEY reads a submitted clock time in the zone it was told to read it in, and for an API
// request that is UTC when it was told nothing — ApiRequest#set_utc_timezone sets it. So each
// moment has two honest ways to say when it is, and the form uses both:
//
// Left on Local, the time is converted to UTC here and no zone is named. Converting needs no
// name and is exact — 08:00 in Zagreb is one instant whatever anybody calls the zone — which
// matters because Go will not always give a name for the local zone at all.
//
// With a zone chosen, the time goes as the reader wrote it, next to the zone they wrote it in,
// and HEY stores the zone along with the event. That is what an event should keep when the
// reader travels, and it is the only way to say an event starts in one zone and ends in
// another.
//
// An all-day event is neither: it is sent as the date typed, unconverted, because it is a
// calendar date rather than a moment and shifting it would move a birthday.
func (f *eventForm) values() eventFormValues {
	values := eventFormValues{
		Title:           strings.TrimSpace(f.title.Value()),
		AllDay:          f.allDay,
		StartsAt:        f.starts.date(),
		EndsAt:          f.ends.date(),
		Reminders:       f.reminders(),
		Notes:           trixHTML(f.notes.Value()),
		Location:        strings.TrimSpace(f.location.Value()),
		Link:            eventLink(f.link.Value()),
		AttachedEntryID: f.attachedEntryID,
		Invites:         f.invitesValue(),
		Circled:         f.circled,
		Countdown:       f.countdownParams(),
		Repeat:          f.repeat.params(),
	}
	if f.calendar >= 0 && f.calendar < len(f.calendars) {
		values.CalendarID = f.calendars[f.calendar].ID
	}
	if f.allDay {
		return values
	}
	values.StartsAt, values.StartTime, values.StartTimeZone = wireMoment(f.starts)
	values.EndsAt, values.EndTime, values.EndTimeZone = wireMoment(f.ends)
	return values
}

// wireMoment is one moment as HEY should read it. A field the reader is still typing does not
// parse; validate refuses the save before that matters, and until then the strings as typed
// are the honest answer.
func wireMoment(p *dateTimePicker) (date, clock, zone string) {
	if name := p.zoneName(); name != "" {
		return p.date(), p.clock(), name
	}
	at, ok := p.moment()
	if !ok {
		return p.date(), p.clock(), ""
	}
	return at.UTC().Format("2006-01-02"), at.UTC().Format("15:04"), ""
}

// trixHTML is what the reader typed as the rich text HEY stores an event's notes as: escaped,
// with a line break where they pressed enter. It is the same shape the compose form sends an
// email body in, so a paragraph typed here reads as a paragraph on the web.
func trixHTML(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = stdhtml.EscapeString(strings.TrimRight(line, " \t"))
	}
	return "<div>" + strings.Join(lines, "<br>") + "</div>"
}

// eventLink is the link as HEY should keep it. Nobody types a scheme for a meeting link, and
// HEY validates the field as a URL, so a bare host is completed rather than refused.
func eventLink(typed string) string {
	typed = strings.TrimSpace(typed)
	if typed == "" || strings.Contains(typed, "://") || strings.HasPrefix(typed, "mailto:") {
		return typed
	}
	return "https://" + typed
}

// invitesValue is the guest list to send: nil while it reads as the event's own, which is what
// leaves the roster alone. Anything else replaces it wholesale — including nothing at all,
// which is how every guest is removed.
func (f *eventForm) invitesValue() []string {
	typed := strings.TrimSpace(f.invites.Value())
	if typed == f.invitesArrived {
		return nil
	}
	addresses := []string{}
	for _, address := range strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == ' ' }) {
		addresses = append(addresses, strings.TrimSpace(address))
	}
	return addresses
}

func (f *eventForm) countdownValue() string {
	return strings.TrimSpace(f.countdown.Value())
}

// countdownParams is the countdown to run up to the event, and the zero value — an empty
// field — is HEY's instruction to delete the one the event had.
func (f *eventForm) countdownParams() hey.CountdownParams {
	value, err := strconv.Atoi(f.countdownValue())
	if err != nil || value <= 0 {
		return hey.CountdownParams{}
	}
	return hey.CountdownParams{Value: value, Unit: eventCountdownUnits[f.countdownUnit].unit}
}

func (f *eventForm) reminders() []time.Duration {
	var chosen []time.Duration
	for i, on := range f.chosenReminders {
		if on {
			chosen = append(chosen, eventReminders[i].after)
		}
	}
	return chosen
}

// validate says the first thing wrong with what the reader filled in, and nothing when the
// form is ready. The server would refuse most of this too, but only after a round trip.
func (f *eventForm) validate() string {
	if strings.TrimSpace(f.title.Value()) == "" {
		return "Name is required"
	}
	if problem := f.starts.problem(); problem != "" {
		return "Starts — " + problem
	}
	if problem := f.ends.problem(); problem != "" {
		return "Ends — " + problem
	}

	// Eight in the morning in Auckland is the evening before in Madrid, so the two clocks on
	// their own do not say which comes first — the moments do.
	starts, startsOK := f.starts.moment()
	ends, endsOK := f.ends.moment()
	if startsOK && endsOK && ends.Before(starts) {
		return "The end is before the start"
	}
	return f.detailProblem()
}

// detailProblem is the first thing wrong behind the More row. It is asked separately so that a
// refusal can open the row it belongs to: a reader who hid a half-typed link should be shown
// the field the message is about.
func (f *eventForm) detailProblem() string {
	if problem := eventLinkProblem(f.link.Value()); problem != "" {
		return problem
	}
	for _, address := range f.invitesValue() {
		if !strings.Contains(address, "@") {
			return fmt.Sprintf("%q is not an email address", terminal.SanitizeLine(address))
		}
	}
	if value := f.countdownValue(); value != "" {
		count, err := strconv.Atoi(value)
		if err != nil || count < 1 || count > 30 {
			return "Countdown must be 1 to 30"
		}
	}
	return f.repeat.problem()
}

// eventLinkProblem refuses what HEY would refuse: the field is validated as a URL there, so a
// 422 after a round trip is the alternative to saying so here.
func eventLinkProblem(typed string) string {
	link := eventLink(typed)
	if link == "" {
		return ""
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.Host == "" || strings.ContainsAny(typed, " \t") {
		return "Link must be a web address"
	}
	return ""
}

// capturesKeys says a picker inside the form has taken the keys the form would otherwise
// read: its zone list wants tab, enter and esc, and esc reaches the form's own handler only
// because the view checks this first.
func (f *eventForm) capturesKeys() bool {
	picker := f.picker(f.focus)
	return picker != nil && picker.capturesKeys()
}

func (f *eventForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.saving {
		return nil, false
	}
	if f.capturesKeys() {
		return f.picker(f.focus).handleKey(msg), false
	}

	switch {
	case msg.Key().Code == tea.KeyTab && msg.Key().Mod == tea.ModShift:
		return f.step(-1), false
	case msg.Key().Code == tea.KeyTab:
		return f.step(1), false
	// Enter moves on everywhere but the notes, where it is a new line and tab is the way out.
	case msg.Key().Code == tea.KeyEnter && f.focus != eventFieldNotes:
		return f.step(1), false
	case msg.String() == "ctrl+s":
		if problem := f.validate(); problem != "" {
			f.status = problem
			f.isError = true
			if f.detailProblem() != "" {
				f.revealed = true
			}
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
		f.handleNotifyKey(msg)
	case eventFieldAllDay:
		if isSpace(msg) {
			f.setAllDay(!f.allDay)
		}
	case eventFieldMore:
		if isSpace(msg) {
			f.revealed = !f.revealed
		}
	case eventFieldCircle:
		if isSpace(msg) {
			f.circled = !f.circled
		}
	case eventFieldStarts, eventFieldEnds:
		return f.picker(f.focus).handleKey(msg), false
	case eventFieldNotes:
		return f.update(msg), false
	case eventFieldRepeat:
		f.repeat.stepFrequency(msg)
	case eventFieldRepeatUntil:
		f.repeat.stepUntil(msg)
	case eventFieldCountdownUnit:
		f.countdownUnit = wrapIndex(f.countdownUnit, len(eventCountdownUnits), msg)
	default:
		return f.update(msg), false
	}
	return nil, false
}

// update hands a message to the field the reader is typing in, whether that is a key or a
// cursor blink, so the caller does not have to know which of them takes text.
func (f *eventForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	if f.focus == eventFieldNotes {
		f.notes, cmd = f.notes.Update(msg)
		return cmd
	}
	if input := f.input(f.focus); input != nil {
		*input, cmd = input.Update(msg)
		return cmd
	}
	return nil
}

func isSpace(msg tea.KeyPressMsg) bool {
	s := msg.String()
	return s == " " || s == "space"
}

func (f *eventForm) setAllDay(allDay bool) {
	f.allDay = allDay
	f.starts.setAllDay(allDay)
	f.ends.setAllDay(allDay)
}

// handleNotifyKey walks the notice periods with the arrows and toggles the one it is on with
// space, the same key the all-day checkbox takes. Several can be on at once: HEY takes a set,
// and a reader who wants a day's warning and a nudge on the hour should not have to choose.
func (f *eventForm) handleNotifyKey(msg tea.KeyPressMsg) {
	if isSpace(msg) {
		f.chosenReminders[f.notify] = !f.chosenReminders[f.notify]
		return
	}
	f.notify = wrapIndex(f.notify, len(eventReminders), msg)
}

func (f *eventForm) helpBindings() []helpBinding {
	if picker := f.picker(f.focus); picker != nil && picker.capturesKeys() {
		return picker.helpBindings()
	}

	bindings := []helpBinding{{"tab", "next field"}}
	if picker := f.picker(f.focus); picker != nil {
		bindings = append(bindings, picker.helpBindings()...)
	}
	switch f.focus {
	case eventFieldCalendar, eventFieldRepeat, eventFieldRepeatUntil, eventFieldCountdownUnit:
		bindings = append(bindings, helpBinding{"←→", "choose"})
	case eventFieldAllDay, eventFieldCircle:
		bindings = append(bindings, helpBinding{"space", "toggle"})
	case eventFieldNotify:
		bindings = append(bindings, helpBinding{"←→", "move"}, helpBinding{"space", "toggle"})
	case eventFieldMore:
		bindings = append(bindings, helpBinding{"space", f.moreAction()})
	case eventFieldNotes:
		bindings = append(bindings, helpBinding{"enter", "new line"})
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

	f.writeRow(&b, "Calendar", f.calendarField(), eventFieldCalendar)
	f.writeRow(&b, "Name", f.title.View(), eventFieldTitle)
	f.writeRow(&b, "All day", checkbox(f.allDay), eventFieldAllDay)
	f.writeRow(&b, "Starts", f.starts.view(), eventFieldStarts)
	f.writeRow(&b, "Ends", f.ends.view(), eventFieldEnds)
	f.writeRow(&b, "Notify", f.notifyField(), eventFieldNotify)
	f.writeRow(&b, "More", f.moreField(), eventFieldMore)
	if f.revealed {
		f.writeRow(&b, "Location", f.location.View(), eventFieldLocation)
		f.writeRow(&b, "Link", f.link.View(), eventFieldLink)
		f.writeRow(&b, "Invites", f.invites.View(), eventFieldInvites)
		f.writeRow(&b, "Notes", indentAfterFirstLine(f.notes.View(), eventFieldValueColumn), eventFieldNotes)
		f.writeRow(&b, "Repeat", f.repeatField(),
			eventFieldRepeat, eventFieldRepeatUntil, eventFieldRepeatValue)
		f.writeRow(&b, "Countdown", f.countdownField(), eventFieldCountdown, eventFieldCountdownUnit)
		f.writeRow(&b, "Circle event", checkbox(f.circled), eventFieldCircle)
		if caution := f.caution(); caution != "" {
			wrapped := styleMuted.Width(f.width - eventFieldValueColumn).Render(caution)
			b.WriteString(strings.Repeat(" ", eventFieldValueColumn) +
				indentAfterFirstLine(wrapped, eventFieldValueColumn) + "\n")
		}
	}

	if f.status != "" {
		statusStyle := styleMuted
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString("\n" + statusStyle.Render(f.status))
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeRow marks the label of the focused field, which is how a picker with no cursor of its
// own says the arrow keys belong to it. A row can hold more than one field — the repeat is a
// frequency, an end and the date or count that end wants — and its label is marked for any of
// them.
func (f *eventForm) writeRow(b *strings.Builder, label, value string, fields ...int) {
	labelStyle := styleMuted
	if slices.Contains(fields, f.focus) {
		labelStyle = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	}
	fmt.Fprintf(b, "%s %s\n", labelStyle.Render(fmt.Sprintf("%-*s", eventFieldLabelWidth, label)), value)
}

// eventFieldLabelWidth is the label column, wide enough for "Circle event", and
// eventFieldValueIndent lines a second line up under the first: the label, the space after it,
// and the calendar's dot and its space.
const (
	eventFieldLabelWidth  = 12
	eventFieldValueColumn = eventFieldLabelWidth + 1
	eventFieldValueIndent = eventFieldValueColumn + 2
)

// indentAfterFirstLine puts the rest of a block under the first line, which is what a field
// several lines tall needs to sit in a labelled column.
func indentAfterFirstLine(text string, columns int) string {
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.Repeat(" ", columns) + lines[i]
	}
	return strings.Join(lines, "\n")
}

func checkbox(on bool) string {
	if on {
		return "◉ yes"
	}
	return "○ no"
}

// calendarField names the calendar an event is filed on, in its own color, with the arrows
// stepping through the others. An edit steps through them too: an update takes a calendar the
// same way a create does, which is how an event moves.
//
// The account is on a line of its own underneath, because a reader with a work account and a
// personal one has two calendars called Maybe and nothing else to tell them apart.
func (f *eventForm) calendarField() string {
	if len(f.calendars) == 0 {
		return styleMuted.Render("no calendar to file this on")
	}
	calendar := f.calendars[min(f.calendar, len(f.calendars)-1)]

	name := terminal.SanitizeLine(calendar.Name)
	if name == "" {
		name = "Personal"
	}
	line := calendarMarkerStyle(calendar.Color).Render("●") + " " + name
	if owner := terminal.SanitizeLine(calendar.OwnerEmail); owner != "" {
		line += "\n" + strings.Repeat(" ", eventFieldValueIndent) + styleMuted.Render(owner)
	}
	return line
}

// notifyField shows every notice period at once, filled in for the ones chosen, so the whole
// set reads off one line however many of them are on.
func (f *eventForm) notifyField() string {
	chips := make([]string, 0, len(eventReminders))
	for i, reminder := range eventReminders {
		chip := "○" + reminder.label
		if f.chosenReminders[i] {
			chip = "◉" + reminder.label
		}
		switch {
		case f.focus == eventFieldNotify && i == f.notify:
			chip = lipgloss.NewStyle().Foreground(colorActive).Bold(true).Render(chip)
		case !f.chosenReminders[i]:
			chip = styleMuted.Render(chip)
		}
		chips = append(chips, chip)
	}
	return strings.Join(chips, " ")
}

// moreField is the row that opens and closes everything below it. Closed, it names what is
// down there rather than saying "more": a reader looking for where the invites went should be
// able to read the answer off the form.
func (f *eventForm) moreField() string {
	if f.revealed {
		return "▾ hide"
	}
	return styleMuted.Render("▸ location, invites, notes, repeat…")
}

func (f *eventForm) moreAction() string {
	if f.revealed {
		return "hide"
	}
	return "show"
}

// repeatField is the whole recurrence on one line — how often, until when, and the date or the
// count that end wants — because the three read as one answer. Only the frequency is there when
// the event does not repeat: an event that happens once has no end to its repetition.
func (f *eventForm) repeatField() string {
	segments := []string{f.segment(eventFieldRepeat, f.repeat.frequencyLabel())}
	if f.repeat.repeats() {
		segments = append(segments, f.segment(eventFieldRepeatUntil, f.repeat.untilLabel()))
		if f.repeat.needsValue() {
			segments = append(segments, f.repeat.valueInput().View()+f.repeat.timesSuffix())
		}
	}
	return strings.Join(segments, styleMuted.Render(" · "))
}

// countdownField is how long HEY counts down for. The unit only appears once there is a number
// to give it, so an event with no countdown reads as one field rather than two.
func (f *eventForm) countdownField() string {
	if f.countdownValue() == "" {
		return f.countdown.View()
	}
	return f.countdown.View() + styleMuted.Render(" · ") +
		f.segment(eventFieldCountdownUnit, eventCountdownUnits[f.countdownUnit].label)
}

// segment marks the part of a shared row the keys belong to, the same way the date-time picker
// marks its own: the text inputs carry a cursor, so only the stepped choices need coloring.
func (f *eventForm) segment(field int, value string) string {
	if f.focus == field {
		return lipgloss.NewStyle().Foreground(colorActive).Bold(true).Render(value)
	}
	return styleMuted.Render(value)
}

// caution is what an edit cannot promise, said before the reader saves rather than discovered
// afterwards. Both come from HEY reading these fields as a replacement: it serves notes back as
// plain text, and it serves a countdown back not at all.
func (f *eventForm) caution() string {
	if f.mode != eventFormEdit {
		return ""
	}

	var cautions []string
	if f.notesArrived {
		cautions = append(cautions, "Notes save as typed, replacing any formatting they had.")
	}
	cautions = append(cautions, "Saving clears a countdown HEY does not show here.")
	return strings.Join(cautions, " ")
}
