package tui

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// --- Calendar view types ---

// Calendar is one of the reader's calendars, as the picker and the habit form need it.
// Personal is the one HEY files a habit or a todo on when no calendar is named, and it
// has no name of its own — HEY leaves the field empty and the web app labels the row
// from the identity instead.
type Calendar struct {
	ID   int64
	Name string
	// OwnerEmail is the account the calendar belongs to, which is what tells two calendars of
	// the same name apart — a reader with a work account and a personal one has two "Maybe"s
	// and no way to know which is which from the name. It is what the web app shows under a
	// calendar in its own list.
	OwnerEmail string
	Color      string
	Personal   bool
	// External is a calendar HEY subscribes to rather than owns — haystack's `internal`
	// scope is `where.missing(:subscription)`, and this is the other side of it.
	External bool
}

// listed is whether the picker offers this calendar at all. The personal calendar is
// never offered: it holds the reader's own habits and todos, it is on in every client,
// and with no name of its own it would be a blank row that cannot be switched off.
func (c Calendar) listed() bool {
	return !c.Personal
}

// fileable is whether an event can be put on this calendar. It is the set haystack accepts
// — `accessible_calendars.internal`, which `calendar_from_params` looks the submitted
// calendar_id up in — and it is narrower than the calendars a period is drawn from.
//
// The personal calendar is the one that catches you out. `Identity#calendars` is
// `accessible_calendars.ids.including(personal_calendar.id)`, so it is in every list HEY
// serves, but `accessible_calendars` itself does not contain it — the id has to be added by
// hand. Offer it as somewhere to file an event and the create answers 404, because the
// lookup behind it finds nothing. The web app's own calendar select offers
// `accessible_calendars.internal` for exactly this reason.
func (c Calendar) fileable() bool {
	return !c.Personal && !c.External
}

// CalendarYear is a year as HEY draws one: a grid of days, and the events that span more
// than one of them. It is deliberately not a year's worth of recordings — see
// renderYearView.
type CalendarYear struct {
	// PaddingDays is how many cells sit before January 1st, so the grid lines up under
	// the reader's first weekday.
	PaddingDays   int
	Days          []YearDay
	SpannedEvents []Recording
}

// YearDay is one cell of that grid. Backgrounded is whether the day carries a background
// image, which the web app paints behind the cell.
type YearDay struct {
	Date         time.Time
	Backgrounded bool
}

// Recording is anything HEY keeps on a calendar — an event, a todo, a habit, a time
// track — told apart by Type.
//
// Its times are time.Time and stay that way. They used to be strings rendered in UTC and
// parsed back, which is how the whole calendar came to be drawn in UTC: an event at 14:00Z
// sat on the 14:00 column wherever the reader was. Read Starts and Ends rather than these
// fields — those answer in the zone a reader thinks in.
type Recording struct {
	ID       int64
	ParentID int64
	Title    string
	AllDay   bool
	StartsAt time.Time
	EndsAt   time.Time
	Type     string
	// StartsAtZone and EndsAtZone are the IANA names of the zones the event was set in, and
	// they are empty for most events — HEY serves them only for one saved with a zone of its
	// own. They do not move the event: it is at one instant whatever zone it was set in, and
	// the calendar draws it where the reader's clock puts it. What they are for is the form,
	// which shows a zoned event on the clock it was written on.
	StartsAtZone string
	EndsAtZone   string
	CompletedAt  time.Time
	Label        string
	Icon         string
	// Color is a habit's own color. An event has none — what it wears is its
	// calendar's, which is CalendarColor, and the two are different fields in HEY too.
	Color string
	// Notes, Location, Link, Attendees and AttachedEntryID are what an event carries besides
	// when it is. They are read as well as written because HEY's update clears the lot of them
	// on any write that leaves them out — so an edit form that did not know them would wipe an
	// event's notes and location every time somebody changed its name.
	//
	// Notes arrive as plain text however they were written: HEY serves the description through
	// to_plain_text, so formatting cannot survive a round trip through any client.
	Notes           string
	Location        string
	Link            string
	Attendees       []string
	AttachedEntryID int64
	// Highlighted is what HEY calls the web app's "Circle event".
	Highlighted bool
	// Recurring and RepeatKind are how an event repeats. The kind is served but neither the
	// date it runs until nor the number of times, which is why an edit form can keep a
	// schedule or replace it but cannot show what it is bounded by.
	Recurring  bool
	RepeatKind string
	// ParentTitle is the title of the recording this one hangs off, which is the only place
	// some recordings have one: a countdown carries a label — "10 days before" — and no title
	// of its own, because what it is counting down to is the event above it.
	ParentTitle string
	// OccurrenceID names one instance of a repeating event — "153688907_2026-08-21", the
	// series and the day — and is what HEY serves instead of an id for an occurrence it has
	// not written down yet. Such a recording arrives with ID 0, which is why the arrows hold
	// on to key() rather than to the id: selecting by the id alone meant a repeating event
	// could never be picked out at all.
	OccurrenceID string
	// CalendarID is which calendar this is filed on, and CalendarColor how a reader tells
	// whose event they are looking at. HEY leaves the color empty for the personal calendar
	// and two calendars can wear the same one, so the id is what the edit form matches on and
	// the color only what it falls back to.
	CalendarID    int64
	CalendarColor string
	Days          []int32
}

// Starts and Ends are when a recording begins and ends, in the zone the reader is in.
//
// An all-day event is the exception, and it is not one HEY leaves to guesswork: its
// timestamp is a calendar date, which haystack serves as UTC midnight on purpose —
// `_recording.jbuilder` wraps it in `Time.use_zone("UTC")` so no offset creeps in. Convert
// that and a birthday moves to the day before for every reader west of UTC.
func (r Recording) Starts() time.Time { return localizedEventTime(r.StartsAt, r.AllDay) }
func (r Recording) Ends() time.Time   { return localizedEventTime(r.EndsAt, r.AllDay) }

// Done is whether a habit or a todo has been completed.
func (r Recording) Done() bool { return !r.CompletedAt.IsZero() }

// key is what the arrows hold on to, and what says two recordings are the same one. It is the
// id where there is one and the occurrence id otherwise — see OccurrenceID — and empty for a
// recording HEY has given neither, which is then not selectable because there would be nothing
// to act on.
func (r Recording) key() string {
	switch {
	case r.ID != 0:
		return strconv.FormatInt(r.ID, 10)
	case r.OccurrenceID != "":
		return r.OccurrenceID
	}
	return ""
}

func localizedEventTime(at time.Time, allDay bool) time.Time {
	if at.IsZero() || allDay {
		return at
	}
	return at.Local()
}

// --- Calendar messages ---

type calendarRequestKind int

const (
	calendarRequestNone calendarRequestKind = iota
	calendarRequestCalendars
	calendarRequestRecordings
	calendarRequestToggle
	calendarRequestMutation
	calendarRequestCategories
)

type calendarsLoadedMsg struct {
	requestResult
	calendars []Calendar
	selected  map[int64]bool
}

// calendarToggledMsg carries the selection HEY was left holding. The toggle answers it,
// so the picker never has to guess what the next period read will cover.
type calendarToggledMsg struct {
	requestResult
	selected map[int64]bool
	name     string
	on       bool
}

type recordingsLoadedMsg struct {
	requestResult
	recordings []Recording
}

// yearLoadedMsg is the year's own answer. It rides the same lane as the recordings, since
// it is the same read from the reader's side — the span they are looking at.
type yearLoadedMsg struct {
	requestResult
	year CalendarYear
}

// identityLoadedMsg stays off the request lane: the first day of the week is read
// once, alongside the calendars rather than instead of them, so putting it on the
// lane would cancel the read it was batched with.
type identityLoadedMsg struct {
	firstWeekDay time.Weekday
}

type timeTrackCategoriesLoadedMsg struct {
	requestResult
	categories []generated.TimeTrackCategory
}

type timeTrackCategorySavedMsg struct {
	requestResult
	summary string
}

// calendarMutationMsg is the answer to a write on the calendar — a habit, a to-do —
// carrying what to say about it either way.
type calendarMutationMsg struct {
	requestResult
	action  string // what happened, once it has
	failure string // and what did not, when it did not
}

// calendarTickMsg moves the highlight over the selected event along. It carries nothing: the
// phase is the view's, so a tick that arrives after the selection has gone simply stops.
type calendarTickMsg struct{}

// calendarTickInterval is how often the highlight moves. Slow enough that a whole span being
// re-rendered behind it costs nothing anyone can feel, fast enough to read as motion rather
// than as a blink.
const calendarTickInterval = 140 * time.Millisecond

func calendarTick() tea.Cmd {
	return tea.Tick(calendarTickInterval, func(time.Time) tea.Msg { return calendarTickMsg{} })
}

// animate keeps the highlight moving for as long as there is something selected to draw it on,
// and lets the loop stop as soon as there is not. Nothing restarts it but a key, which is the
// only thing that can select an event in the first place.
func (v *calendarView) animate() tea.Cmd {
	if v.selectedEvent == "" || v.animating {
		return nil
	}
	v.animating = true
	return calendarTick()
}

// --- Calendar section view ---

type calendarView struct {
	vc *viewContext

	calendars []Calendar

	// selected is the calendars HEY is drawing the period from, by id. It is the
	// server's answer rather than this view's choice: a period read is scoped to the
	// identity's selection whatever this holds, so the picker writes the toggle and
	// takes the selection back from it instead of keeping its own.
	selected map[int64]bool

	viewMode     calendarViewMode
	firstWeekDay time.Weekday

	// now is the clock the calendar anchors on. It is read on every fetch and
	// every render, so a TUI left open overnight moves to the new day instead of
	// fetching around the day it started on while the grid highlights today.
	now func() time.Time

	// anchor is the day the reader has moved to, and zero for today. Following the
	// clock is the zero value on purpose: t puts the view back by clearing this, and
	// a view left on today then keeps up with the clock overnight.
	anchor time.Time

	// Recordings split by type, for the day and the week
	events []Recording
	todos  []Recording
	habits []Recording

	// habitCompletions is which habit was done on which day, which the week needs and a
	// habit's own CompletedAt cannot carry: over a week a habit has one per day it was
	// done on, and folding them keeps only the last.
	habitCompletions []Recording

	// countdowns are the days counting down to an event, which HEY serves as recordings of
	// their own on every day between the notice period and the event.
	countdowns []Recording

	// selectedEvent is the event the arrows have walked to, by Recording.key. It is a key
	// rather than an index because the list under it is read again on every step, every write
	// and every live change, and an index would point at whatever moved into that slot.
	selectedEvent string

	// lastTimedEvent is the grid event the reader was on before they crossed down into the
	// all-day band, so ↑ brings them back to it rather than to the top of the day.
	lastTimedEvent string

	// selectFromEdge remembers which way the reader stepped off the end of a span, so the
	// events of the one they land on can be walked into from the far side. The step is a
	// read, so the answer arrives after the key: -1 means take the last event of what comes
	// back, +1 the first, 0 that nothing is waiting on it.
	selectFromEdge int

	// inYearCell is whether the reader has stepped into the day the year's cursor is on. A
	// year is a grid before it is a list of events, so the arrows move between cells until
	// this is set and walk one cell's events afterwards.
	inYearCell bool

	// selectPhase is how far the highlight over the selected event has travelled. It is
	// advanced by a tick rather than by anything the reader does, and it stops when there is
	// nothing selected to draw it on. See calendarTickMsg.
	selectPhase int

	// animating is whether a tick is already in flight, so a key pressed mid-frame does not
	// start a second loop running alongside the first.
	animating bool

	// drawnSinceTick is set by View and cleared by every tick. A tick that finds it clear
	// knows nothing has drawn the calendar since the last one — the reader is in another
	// section — and stops the loop rather than animating a screen nobody is looking at. There
	// is no hook for a section being left, and this needs none: only the active view is drawn.
	drawnSinceTick bool

	// year is what the year span draws, and it is a different answer than the
	// recordings above rather than a summary of them.
	year CalendarYear

	// Scrollable content viewport for the calendar views
	contentVP viewport.Model

	// drawn is whether a day has ever reached the screen, which is what the spinner
	// waits for. See Loading.
	drawn bool

	// editing is the recording the open event form was opened on. A save needs it because what
	// it is addressing is not always an event: one day of a repeating event has no id of its
	// own and is written through the occurrence route instead.
	editing Recording

	timeTrackCategories *timeTrackCategoryManager
	habitPicker         *habitPicker
	habitForm           *habitForm
	todoPicker          *todoPicker
	calendarPicker      *calendarPicker
	eventForm           *eventForm

	// confirmDelete is the event whose x has been pressed once, since removing something
	// off a calendar asks twice — as deleting a habit does.
	confirmDelete string

	requests requestLane[calendarRequestKind]
}

func newCalendarView(vc *viewContext) *calendarView {
	return &calendarView{
		vc:           vc,
		now:          time.Now,
		firstWeekDay: time.Monday,
		contentVP:    viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
}

func (v *calendarView) Init() tea.Cmd {
	cmds := []tea.Cmd{v.fetchIdentity()}
	if len(v.calendars) == 0 {
		cmds = append(cmds, v.requestCalendars())
	} else {
		cmds = append(cmds, v.requestRecordings())
	}
	return tea.Batch(cmds...)
}

func (v *calendarView) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case calendarTickMsg:
		v.animating = false
		if v.selectedEvent == "" || !v.drawnSinceTick {
			return nil, true
		}
		v.drawnSinceTick = false
		v.selectPhase++
		v.rebuildKeepingScroll()
		return v.animate(), true

	case identityLoadedMsg:
		v.firstWeekDay = msg.firstWeekDay
		v.rebuildView()
		return nil, true

	case calendarsLoadedMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		v.calendars = msg.calendars
		v.selected = msg.selected
		if v.calendarPicker != nil {
			v.calendarPicker.setCalendars(v.listedCalendars(), v.selected)
		}
		if len(v.calendars) > 0 {
			return v.requestRecordings(), true
		}
		return nil, true

	case calendarToggledMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			return notifyError("Could not switch "+msg.name, msg.err), true
		}
		v.selected = msg.selected
		if v.calendarPicker != nil {
			v.calendarPicker.setCalendars(v.listedCalendars(), v.selected)
		}
		return tea.Batch(notify(toggleNotice(msg.name, msg.on)), v.requestRecordings()), true

	case recordingsLoadedMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		v.events, v.todos, v.habits, v.habitCompletions, v.countdowns = splitRecordings(msg.recordings)
		if v.habitPicker != nil {
			v.habitPicker.setHabits(v.manageableHabits())
		}
		if v.todoPicker != nil {
			v.todoPicker.setTodos(v.todos)
		}
		was := v.selectedEvent
		v.settleSelection()
		v.rebuildView()
		if v.selectedEvent != was {
			return v.animate(), true
		}
		return nil, true

	case yearLoadedMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		v.year = msg.year
		v.rebuildView()
		return nil, true

	case calendarMutationMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			// A form that is open says so itself; everything else says it in a toast,
			// which is over the picker the write came from.
			if v.habitForm != nil {
				v.habitForm.saving = false
				v.habitForm.status = errorNotice(msg.failure, msg.err)
				v.habitForm.isError = true
				return nil, true
			}
			if v.eventForm != nil {
				v.eventForm.saving = false
				v.eventForm.status = errorNotice(msg.failure, msg.err)
				v.eventForm.isError = true
				return nil, true
			}
			return notifyError(msg.failure, msg.err), true
		}
		v.habitForm = nil
		v.eventForm = nil
		if len(v.calendars) > 0 {
			return tea.Batch(notify(msg.action), v.requestRecordings()), true
		}
		return notify(msg.action), true

	case timeTrackCategoriesLoadedMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if v.timeTrackCategories == nil {
			return nil, true
		}
		if msg.err != nil {
			v.timeTrackCategories.status = errorNotice("Could not load categories", msg.err)
			return nil, true
		}
		v.timeTrackCategories.setCategories(msg.categories)
		return nil, true

	case timeTrackCategorySavedMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if v.timeTrackCategories == nil {
			return nil, true
		}
		if msg.err != nil {
			v.timeTrackCategories.status = errorNotice("Could not save the category", msg.err)
			return nil, true
		}
		v.timeTrackCategories.status = msg.summary
		return v.requestTimeTrackCategories(), true
	}

	if v.habitForm != nil {
		return v.habitForm.update(msg), true
	}
	if v.eventForm != nil {
		return v.eventForm.update(msg), true
	}

	return nil, false
}

func (v *calendarView) View() string {
	v.drawnSinceTick = true

	if v.timeTrackCategories != nil {
		return v.timeTrackCategories.view(v.vc.styles, v.vc.width, v.vc.height)
	}
	view := v.contentVP.View()
	if footer := v.todosFooter(); footer != "" {
		view += "\n" + footer
	}
	// The form stands over the picker it was opened from, and the picker over the
	// calendar, so a habit is edited without the day it belongs to leaving the screen.
	if v.habitPicker != nil {
		view = v.habitPicker.draw(view, v.vc.width, v.vc.height)
	}
	if v.todoPicker != nil {
		view = v.todoPicker.draw(view, v.vc.width, v.vc.height)
	}
	if v.calendarPicker != nil {
		view = v.calendarPicker.draw(view, v.vc.width, v.vc.height)
	}
	if v.habitForm != nil {
		frame := modalFrame(v.habitForm.title(), v.habitForm.view(), v.vc.width)
		view = overlayModal(view, frame, v.vc.width, v.vc.height)
	}
	// The event form stands over the calendar itself: an event is picked out on the grid
	// with the arrows rather than from a list, so there is no picker underneath it.
	if v.eventForm != nil {
		frame := modalFrame(v.eventForm.formTitle(), v.eventForm.view(), v.vc.width)
		view = overlayModal(view, frame, v.vc.width, v.vc.height)
	}
	return view
}

// todosFooter is the week's to-dos, standing at the bottom of the screen under the grid
// rather than scrolling away inside it. A to-do is not due at an hour, so it does not
// belong in a grid that is precise about time — and a day with a long event pushes
// everything below it out of the viewport.
func (v *calendarView) todosFooter() string {
	if v.viewMode == viewYear {
		return ""
	}
	header := hintedSectionHeader(todosSectionLabel, "s to manage", v.vc.width)
	if len(v.todos) == 0 {
		// The line stays on an empty week, because it is where a to-do is added from.
		return header + "\n" + styleMuted.Render("Nothing to do")
	}
	return header + "\n" + renderTodosRibbon(v.todos, v.vc.width)
}

func (v *calendarView) todosFooterHeight() int {
	if footer := v.todosFooter(); footer != "" {
		return lipgloss.Height(footer)
	}
	return 0
}

func (v *calendarView) HelpBindings() []helpBinding {
	if v.timeTrackCategories != nil {
		return v.timeTrackCategories.helpBindings()
	}
	if v.habitForm != nil {
		return v.habitForm.helpBindings()
	}
	if v.eventForm != nil {
		return v.eventForm.helpBindings()
	}
	if v.habitPicker != nil {
		return v.habitPicker.helpBindings()
	}
	if v.todoPicker != nil {
		return v.todoPicker.helpBindings()
	}
	if v.calendarPicker != nil {
		return v.calendarPicker.helpBindings()
	}
	// Every span says which keys move it on the line that names it, where they belong to
	// the date they act on, so the help bar carries none of that.
	var bindings []helpBinding

	// What the arrows do is the one thing that differs between the spans, so each says its own
	// — and the year says it twice, since the arrows change hands when the reader steps into a
	// cell. a stands wherever an event can be filed; e and x wait for one to be picked out.
	switch v.viewMode {
	case viewDay:
		bindings = append(bindings, helpBinding{"←→", "event"})
		if _, allDay := v.selectableGroups(); len(allDay) > 0 {
			bindings = append(bindings, helpBinding{"↑↓", "all day"})
		}
	case viewWeek:
		bindings = append(bindings, helpBinding{"←→", "day"}, helpBinding{"↑↓", "event"})
	case viewYear:
		if v.inYearCell {
			bindings = append(bindings, helpBinding{"↑↓", "event"}, helpBinding{"esc", "leave the day"})
		} else {
			bindings = append(bindings, helpBinding{"←→↑↓", "day"}, helpBinding{"enter", "open the day"})
		}
	}

	bindings = append(bindings, helpBinding{"a", "new event"})
	if event, ok := v.selectedRecording(); ok {
		label := "delete"
		if v.confirmDelete == event.key() {
			label = "press x again to delete"
		}
		bindings = append(bindings, helpBinding{"e", "edit"}, helpBinding{"x", label})
	}

	// The spans are not in here: the row above the grid shows each one's number in the
	// tab itself, the way the box row does. Which calendar is being read is only in the
	// menu, so the key that opens it has to be said.
	if len(v.listedCalendars()) > 0 {
		bindings = append(bindings, helpBinding{"g", "calendars"})
	}
	bindings = append(bindings, helpBinding{"c", "time categories"})
	if v.showsHabits() {
		bindings = append(bindings, helpBinding{"b", "habits"})
	}
	return bindings
}

// showsHabits reports whether the day has habits to manage: the hint on the section
// header and the h that opens the picker are the same offer.
func (v *calendarView) showsHabits() bool {
	return len(v.manageableHabits()) > 0 || v.viewingPersonalCalendar()
}

// SubnavItems is the span the calendar is read over — Day, Week, Year — and the rule
// above the row names the one that is on, as the box row's rule names the open box.
func (v *calendarView) SubnavItems() ([]navItem, int, string, bool) {
	return calendarNavItems(), int(v.viewMode), v.viewMode.String(), true
}

func (v *calendarView) SubnavLeft() tea.Cmd {
	return v.setViewMode(v.viewMode - 1)
}

func (v *calendarView) SubnavRight() tea.Cmd {
	return v.setViewMode(v.viewMode + 1)
}

// setViewMode reads the range the new span covers, and does nothing at either end: the
// row stops rather than wrapping, as the box tabs do.
func (v *calendarView) setViewMode(mode calendarViewMode) tea.Cmd {
	if mode < viewDay || mode > viewYear || mode == v.viewMode {
		return nil
	}
	v.viewMode = mode
	// Each span gives the arrows something different, so what they were pointing at in the one
	// before does not carry over.
	v.inYearCell = false
	v.selectedEvent = ""
	return v.reread()
}

// HandleContentKey answers what the key did, and starts the highlight moving when the key is
// what picked the event out. Several keys can do that — an arrow, a step onto another day,
// entering a year cell — so it is noticed here, by the selection having changed, rather than
// remembered at each of them.
func (v *calendarView) HandleContentKey(msg tea.KeyPressMsg) tea.Cmd {
	was := v.selectedEvent
	cmd := v.handleContentKey(msg)
	if v.selectedEvent != was {
		return tea.Batch(cmd, v.animate())
	}
	return cmd
}

func (v *calendarView) handleContentKey(msg tea.KeyPressMsg) tea.Cmd {
	if v.timeTrackCategories != nil {
		return v.handleTimeTrackCategoryKey(msg)
	}
	if v.habitForm != nil {
		if msg.Key().Code == tea.KeyEscape && !v.habitForm.saving {
			v.habitForm = nil
			return nil
		}
		cmd, submit := v.habitForm.handleKey(msg)
		if submit {
			return v.saveHabit()
		}
		return cmd
	}
	if v.eventForm != nil {
		// The form's time zone list wants esc for itself — closing the list is what esc means
		// while it is open, and closing the whole form under it would throw away everything
		// typed so far.
		if msg.Key().Code == tea.KeyEscape && !v.eventForm.saving && !v.eventForm.capturesKeys() {
			v.eventForm = nil
			return nil
		}
		cmd, submit := v.eventForm.handleKey(msg)
		if submit {
			return v.saveEvent()
		}
		return cmd
	}
	if v.requests.kind == calendarRequestMutation {
		return nil
	}

	if v.habitPicker != nil {
		return v.handleHabitPickerKey(msg)
	}

	if v.todoPicker != nil {
		return v.handleTodoPickerKey(msg)
	}

	if v.calendarPicker != nil {
		return v.handleCalendarPickerKey(msg)
	}

	// x asks twice, so any other key is the reader changing their mind.
	if msg.String() != "x" {
		v.confirmDelete = ""
	}

	switch msg.String() {
	// a, e and x on an event, as they are on a habit in its own modal.
	case "a":
		return v.startEventForm(eventFormCreate, Recording{})
	case "e":
		if event, ok := v.selectedRecording(); ok {
			return v.startEventForm(eventFormEdit, event)
		}
		return nil
	case "x":
		return v.removeSelectedEvent()
	// b for habits, as in HEY's own calendar.
	case "b":
		v.habitPicker = newHabitPicker(v.manageableHabits(), v.viewMode != viewYear)
		return nil
	// s for the to-dos, which HEY files under "Sometime this week".
	case "s":
		v.todoPicker = newTodoPicker(v.todos)
		v.todoPicker.resize(v.vc.width)
		return nil
	// g for the calendars, since shift+C is the jump to this section and never reaches
	// here — the model reads the section shortcuts before a view sees a key.
	case "g":
		if listed := v.listedCalendars(); len(listed) > 0 {
			v.calendarPicker = newCalendarPicker(listed, v.selected)
		}
		return nil
	case "c":
		v.timeTrackCategories = newTimeTrackCategoryManager()
		return v.requestTimeTrackCategories()
	// The span is picked by number, as a box is in the mail list, and the row above the
	// grid shows which one is on.
	case "1":
		return v.setViewMode(viewDay)
	case "2":
		return v.setViewMode(viewWeek)
	case "3":
		return v.setViewMode(viewYear)
	case "p":
		return v.step(-1)
	case "n":
		return v.step(1)
	case "t":
		return v.today()
	}

	if cmd, handled := v.handleArrowKey(msg); handled {
		return cmd
	}

	// Delegate scrolling to the content viewport
	var cmd tea.Cmd
	v.contentVP, cmd = v.contentVP.Update(msg)
	return cmd
}

// handleArrowKey is where the three spans stop being the same screen with a different date on
// it. Each one gives the arrows the thing it is actually made of:
//
// The day is one day, so ← and → walk its events and carry on into the day either side.
//
// The week is seven days, so ← and → walk the days and ↑ and ↓ walk the selected day's events.
// The cursor is the anchor, which is what gives b, s and a the day the reader is pointing at
// rather than the day the week happens to start on.
//
// The year is a grid, and a grid wants moving through before anything in it can be worked on:
// the arrows move between cells, enter steps into one, and only then do ↑ and ↓ belong to that
// day's events. esc steps back out. Without the two stages ↑ and ↓ would have to be both a
// week's worth of movement and an event's, and a year of cells has no way to show which.
func (v *calendarView) handleArrowKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	key := msg.String()

	switch v.viewMode {
	case viewDay:
		switch key {
		case "left":
			return v.moveAlongTheDay(-1), true
		case "right":
			return v.moveAlongTheDay(1), true
		case "up":
			return v.crossTheDay(-1), true
		case "down":
			return v.crossTheDay(1), true
		}
	case viewWeek:
		switch key {
		case "left":
			return v.moveCursorDay(-1), true
		case "right":
			return v.moveCursorDay(1), true
		case "up":
			return v.moveSelection(-1), true
		case "down":
			return v.moveSelection(1), true
		}
	case viewYear:
		switch key {
		case "left":
			return v.moveCursorDay(-1), true
		case "right":
			return v.moveCursorDay(1), true
		case "up":
			if v.inYearCell {
				return v.moveSelection(-1), true
			}
			return v.moveCursorDay(-7), true
		case "down":
			if v.inYearCell {
				return v.moveSelection(1), true
			}
			return v.moveCursorDay(7), true
		case "enter":
			v.enterYearCell()
			return nil, true
		}
	}
	return nil, false
}

// moveAlongTheDay is ← and → on the day view: along the grid, and off either end into the day
// before or after. The all-day band is not on the grid — it is a bar under it, belonging to no
// hour — so it is not walked sideways; crossTheDay is how the reader gets to it.
func (v *calendarView) moveAlongTheDay(delta int) tea.Cmd {
	timed, allDay := v.selectableGroups()

	// On an all-day event, ← and → step the day: there is nothing to their left or right on the
	// band, and the day either side is what the reader is asking for.
	if indexOfEvent(allDay, v.selectedEvent) >= 0 {
		return v.stepSpanFromEdge(delta)
	}
	return v.walk(timed, delta, func() tea.Cmd { return v.stepSpanFromEdge(delta) })
}

// crossTheDay is ↑ and ↓ on the day view, which cross between the grid and the all-day band
// under it: ↓ off the grid lands on the band, ↑ off the top of the band comes back to the grid,
// and within the band the two walk it. Nothing crosses back down onto the grid from above it,
// because the grid is where the reader already was.
func (v *calendarView) crossTheDay(delta int) tea.Cmd {
	timed, allDay := v.selectableGroups()

	if at := indexOfEvent(allDay, v.selectedEvent); at >= 0 {
		if delta < 0 && at == 0 {
			return v.selectEvent(v.lastTimedOr(timed))
		}
		return v.walk(allDay, delta, nil)
	}

	// On the grid, or on nothing yet: ↓ is the way into the band.
	if delta > 0 && len(allDay) > 0 {
		return v.selectEvent(allDay[0].key())
	}
	return nil
}

// lastTimedOr is the grid event ↑ off the band comes back to: the one the reader left, so
// crossing down and back up again is where they were rather than at the start of the day.
func (v *calendarView) lastTimedOr(timed []Recording) string {
	if len(timed) == 0 {
		return ""
	}
	if indexOfEvent(timed, v.lastTimedEvent) >= 0 {
		return v.lastTimedEvent
	}
	return timed[0].key()
}

func (v *calendarView) selectEvent(key string) tea.Cmd {
	if key == "" {
		return nil
	}
	v.selectedEvent = key
	if timed, _ := v.selectableGroups(); indexOfEvent(timed, key) >= 0 {
		v.lastTimedEvent = key
	}
	v.rebuildKeepingScroll()
	return nil
}

func indexOfEvent(events []Recording, key string) int {
	if key == "" {
		return -1
	}
	for i, event := range events {
		if event.key() == key {
			return i
		}
	}
	return -1
}

// enterYearCell hands the cell's own events to ↑ and ↓, starting on the first so the reader
// can see that the arrows have changed hands.
func (v *calendarView) enterYearCell() {
	v.inYearCell = true
	if events := v.selectableEvents(); len(events) > 0 && v.selectedEvent == "" {
		v.selectedEvent = events[0].key()
	}
	v.rebuildView()
}

func (v *calendarView) leaveYearCell() {
	v.inYearCell = false
	v.selectedEvent = ""
	v.rebuildView()
}

// CancelPendingDetail is how esc reaches a year cell. The model reads esc before a view sees a
// key, and only offers it on through here — so stepping out of a cell is the same seam a mail
// thread's read is cancelled through, rather than a key the calendar handles itself.
func (v *calendarView) CancelPendingDetail() bool {
	if !v.inYearCell {
		return false
	}
	v.leaveYearCell()
	return true
}

// handleHabitPickerKey gives the open picker every key: managing a habit is what the
// modal is for, so a is a new habit here rather than whatever a means outside it.
// handleCalendarPickerKey gives the open picker every key. The picker stays open across a
// toggle: switching calendars on and off is a few decisions at once, not one, and the
// period behind it is read again after each so the reader sees what they just changed.
func (v *calendarView) handleCalendarPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	picker := v.calendarPicker

	switch msg.String() {
	case "esc", "q":
		v.calendarPicker = nil
		return nil
	case "enter", " ", "space":
		calendar, ok := picker.highlighted()
		if !ok || v.togglePending() {
			return nil
		}
		return v.toggleCalendar(calendar)
	}

	picker.moveCursor(msg)
	return nil
}

// handleTodoPickerKey gives the open picker every key. While it is naming a new to-do
// every key is the input's, so a is a letter there rather than another to-do.
func (v *calendarView) handleTodoPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	picker := v.todoPicker

	if picker.editing() {
		switch msg.Key().Code {
		case tea.KeyEscape:
			picker.stopEditing()
			return nil
		case tea.KeyEnter:
			if picker.mode == todoRenaming {
				todo, title, ok := picker.renamed()
				picker.stopEditing()
				if !ok {
					return nil
				}
				return v.renameTodo(todo, title)
			}
			title, ok := picker.title()
			if !ok {
				return nil
			}
			picker.stopEditing()
			return v.addTodo(title)
		default:
			return picker.update(msg)
		}
	}

	switch msg.String() {
	case "esc", "q":
		v.todoPicker = nil
		return nil
	case "a":
		return picker.startAdding()
	case "e":
		return picker.startRenaming()
	case "enter":
		if todo := picker.selected(); todo != nil {
			return v.toggleTodo(*todo)
		}
		return nil
	case "x":
		todo := picker.selected()
		if todo == nil {
			return nil
		}
		if picker.confirmed != todo.ID {
			picker.confirmed = todo.ID
			picker.status = "Press x again to delete " + terminal.SanitizeLine(todo.Title)
			return nil
		}
		return v.deleteTodo(*todo)
	}

	picker.moveCursor(msg)
	picker.status = ""
	return nil
}

func (v *calendarView) handleHabitPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	picker := v.habitPicker

	switch msg.String() {
	case "esc", "q":
		v.habitPicker = nil
		return nil
	case "a":
		if !v.viewingPersonalCalendar() {
			picker.status = "Habits can only be created from the personal calendar"
			return nil
		}
		return v.startHabitForm(habitFormCreate, Recording{})
	case "enter":
		if !picker.completable {
			picker.status = "Keeping a habit is done from the day or the week"
			return nil
		}
		if habit := picker.selected(); habit != nil {
			return v.toggleHabitCompletion(*habit)
		}
		return nil
	case "e":
		if habit := picker.selected(); habit != nil {
			return v.startHabitForm(habitFormEdit, *habit)
		}
		return nil
	case "x":
		habit := picker.selected()
		if habit == nil {
			return nil
		}
		if picker.confirmed != habit.ID {
			picker.confirmed = habit.ID
			picker.status = fmt.Sprintf("Press x again to permanently delete %s and its history", terminal.SanitizeLine(habit.Title))
			return nil
		}
		return v.deleteHabit(*habit)
	}

	picker.moveCursor(msg)
	picker.status = ""
	return nil
}

func (v *calendarView) handleTimeTrackCategoryKey(msg tea.KeyPressMsg) tea.Cmd {
	manager := v.timeTrackCategories
	if v.requests.loading {
		return nil
	}
	if manager.mode != timeTrackCategoryBrowse {
		switch msg.Key().Code {
		case tea.KeyEscape:
			manager.cancelEdit()
			return nil
		case tea.KeyEnter:
			title, ok := manager.title()
			if !ok {
				return nil
			}
			mode := manager.mode
			selected := manager.selected()
			manager.cancelEdit()
			if mode == timeTrackCategoryCreate {
				return v.createTimeTrackCategory(title)
			}
			if selected != nil {
				// The input was filled with the title sanitized for the screen, so an
				// unedited rename is no rename — saving it would rewrite the title.
				if title == selected.Title || title == terminal.SanitizeLine(selected.Title) {
					return nil
				}
				return v.renameTimeTrackCategory(selected.Id, title)
			}
			manager.status = "Choose a category to rename"
			return nil
		default:
			return manager.update(msg)
		}
	}

	switch msg.String() {
	case "esc", "q":
		v.timeTrackCategories = nil
		return nil
	case "n":
		return manager.startCreate()
	case "enter", "r":
		return manager.startRename()
	case "x":
		selected := manager.selected()
		if selected == nil {
			manager.status = "Choose a category to delete"
			return nil
		}
		if !manager.confirmingDelete {
			manager.confirmingDelete = true
			manager.status = ""
			return nil
		}
		manager.confirmingDelete = false
		return v.deleteTimeTrackCategory(selected.Id, selected.Title)
	default:
		manager.move(msg.Key())
		return nil
	}
}

// The calendar has no thread to be in: a recording is read where it sits in the grid.
func (v *calendarView) InThread() bool { return false }
func (v *calendarView) ExitThread()    {}

// Loading is what puts the spinner over the content, and only the first read claims it.
// Once a day has been drawn, every later read — a step to the day either side, a habit
// ticked off, another calendar — keeps what is on screen until its answer lands, the way
// the mail list keeps its list while it reads the page below. A modal is the reader's
// focus, so a read behind one never claims the spinner either.
func (v *calendarView) Loading() bool {
	return v.requests.loading && !v.CapturingInput() && !v.drawn
}
func (v *calendarView) CapturingInput() bool {
	return v.timeTrackCategories != nil || v.habitForm != nil || v.eventForm != nil ||
		v.habitPicker != nil || v.todoPicker != nil || v.calendarPicker != nil
}

func (v *calendarView) AccountSwitchBlocked() bool {
	return v.requests.kind == calendarRequestMutation
}

// Restyle re-renders the day/week/year grid, which caches styled output in its
// viewport. The recording detail is plain text and needs nothing.
func (v *calendarView) Restyle() {
	v.rebuildKeepingScroll()
}

// rebuildKeepingScroll redraws without moving the grid, for the changes that are only about how
// something looks: a theme swap, a highlight moving on, the cursor stepping to the next day of
// a week that is all on screen anyway. rebuildView puts the span back at the top.
func (v *calendarView) rebuildKeepingScroll() {
	offset := v.contentVP.YOffset()
	v.rebuildView()
	v.contentVP.SetYOffset(offset)
}

func (v *calendarView) Resize(width, height int) {
	if v.habitForm != nil {
		v.habitForm.resize(width, height)
	}
	if v.eventForm != nil {
		v.eventForm.resize(width, height)
	}
	if v.todoPicker != nil {
		v.todoPicker.resize(width)
	}
	v.rebuildView()
}

// rebuildView re-renders the current view mode content into the viewport.
func (v *calendarView) rebuildView() {
	w := v.vc.width
	h := v.vc.height
	if w == 0 || h == 0 {
		return
	}

	anchor := v.day()
	dayLabels := dayLabelsFromRecordings(v.events, v.todos, v.habits)

	// The grid scrolls; the week's to-dos do not, so the viewport gives up the rows
	// they stand on. It is sized here rather than in Resize because the to-dos arrive
	// with the recordings, long after the screen has its size — and the day fills
	// whatever is left, so it has to be sized before it is drawn.
	v.contentVP.SetWidth(w)
	v.contentVP.SetHeight(max(h-2-v.todosFooterHeight(), 1))

	var content string
	cursorTop, cursorBottom := -1, -1
	switch v.viewMode {
	case viewDay:
		content = renderDayView(v.events, v.habits, v.countdowns, anchor, v.stepHint(), w, v.contentVP.Height(), v.selection())
	case viewWeek:
		content = renderWeekView(v.events, v.habits, v.habitCompletions, anchor, v.firstWeekDay, w, v.contentVP.Height(), v.stepHint(), dayLabels, v.selection())
	case viewYear:
		// The year's events are HEY's spanned_events — the all-day and multi-day ones —
		// because that is all a year read carries. eventsByDate spreads a multi-day event
		// over the days it covers, so the grid fills the same way it always did.
		content, cursorTop, cursorBottom = renderYearView(v.year.SpannedEvents, anchor, v.firstWeekDay, w, h, v.stepHint(), v.selection(), v.inYearCell)
	}

	v.contentVP.SetContent(content)
	v.drawn = true

	if v.viewMode == viewYear {
		v.revealRows(cursorTop, cursorBottom)
	} else {
		v.contentVP.GotoTop()
	}
}

// revealRows keeps the year's cursor on screen. It used to be scrolled to by counting weeks,
// which is not where a week is: a row is as tall as its busiest day, so the arrows walked the
// cursor off the bottom with the last weeks of the year still below it.
//
// A cursor already on screen is left where it is — re-centring on every keystroke would drag the
// grid about under the reader. One that has just gone off the edge is followed by the smallest
// scroll that brings it back, and one a whole screen away, which is where a fresh read or a jump
// to another year lands, is centred.
func (v *calendarView) revealRows(top, bottom int) {
	height := v.contentVP.Height()
	if top < 0 || height <= 0 {
		v.contentVP.GotoTop()
		return
	}

	offset := v.contentVP.YOffset()
	switch {
	case top >= offset && bottom < offset+height:
		return
	case bottom < offset-height || top > offset+2*height:
		offset = top - height/2
	case top < offset:
		offset = top
	default:
		offset = bottom - height + 1
	}
	v.contentVP.SetYOffset(max(offset, 0))
}

// day is the day the view is on: today, until the reader steps off it.
func (v *calendarView) day() time.Time {
	if v.anchor.IsZero() {
		return v.now()
	}
	return v.anchor
}

// onToday is whether the view is following the clock rather than pinned to a date, which
// is what decides whether t has anything to do.
func (v *calendarView) onToday() bool {
	return v.anchor.IsZero()
}

// showingToday is whether today is on screen, which is not the same question: stepping
// away and back pins the anchor to today's own date rather than clearing it, and a reader
// looking at today does not need to be told how to get to it.
func (v *calendarView) showingToday() bool {
	return v.onToday() || sameDay(v.day(), v.now())
}

// stepHint is the keys that move the view, said on the line that names the day rather
// than in the help bar: they belong to the date they act on. t is only mentioned once it
// would take the reader somewhere.
func (v *calendarView) stepHint() string {
	hint := "p/n " + v.viewMode.unit()
	if !v.showingToday() {
		hint += " · t today"
	}
	return hint
}

// step moves the view by its own unit — a day, a week or a year — since p and n mean
// "the one before this" and "the one after" whatever the view is showing.
func (v *calendarView) step(delta int) tea.Cmd {
	switch v.viewMode {
	case viewWeek:
		v.anchor = v.day().AddDate(0, 0, 7*delta)
	case viewYear:
		v.anchor = v.day().AddDate(delta, 0, 0)
	default:
		v.anchor = v.day().AddDate(0, 0, delta)
	}
	return v.reread()
}

// today puts the view back on the clock rather than on the date it happens to be today,
// so it keeps up with a TUI left open overnight.
func (v *calendarView) today() tea.Cmd {
	if v.onToday() {
		return nil
	}
	v.anchor = time.Time{}
	return v.reread()
}

// reread reads the period the view now covers, or redraws what is already here when the
// calendars have not been read yet.
func (v *calendarView) reread() tea.Cmd {
	if len(v.calendars) > 0 {
		return v.requestRecordings()
	}
	v.rebuildView()
	return nil
}

// --- Walking the events with the arrows ---

func (v *calendarView) selection() selection {
	return selection{
		eventKey: v.selectedEvent,
		day:      v.cursorDay(),
		phase:    v.selectPhase,
	}
}

// cursorDay is the day the arrows have walked to, for the views that draw more than one. The
// day view has nothing to mark — the whole screen is the day it is on — and the year marks its
// cell whether or not the reader has stepped into it, since that is what the arrows are moving.
func (v *calendarView) cursorDay() time.Time {
	if v.viewMode == viewDay {
		return time.Time{}
	}
	return v.day()
}

// moveCursorDay walks the cursor to another day, and reads the period again only when it has
// left the one on screen. The cursor is the anchor, which is what gives b, s and a their day
// without anything having to be told: they all file on v.day() already.
func (v *calendarView) moveCursorDay(days int) tea.Cmd {
	from := v.day()
	v.anchor = from.AddDate(0, 0, days)

	if v.sameSpan(from, v.anchor) {
		v.settleSelection()
		// The week keeps where the reader had scrolled to — the days are all on screen
		// already, so moving between them is not a reason to move the grid. The year does
		// not: its cursor is the anchor, and rebuilding is what scrolls the grid to follow it.
		if v.viewMode == viewYear {
			v.rebuildView()
		} else {
			v.rebuildKeepingScroll()
		}
		return nil
	}
	return v.reread()
}

// sameSpan is whether two days are drawn by the same screen, which is what decides whether
// moving the cursor between them needs another read.
func (v *calendarView) sameSpan(a, b time.Time) bool {
	switch v.viewMode {
	case viewWeek:
		return sameDay(weekStartDate(a, v.firstWeekDay), weekStartDate(b, v.firstWeekDay))
	case viewYear:
		return a.Year() == b.Year()
	case viewDay:
		return sameDay(a, b)
	}
	return false
}

// selectableEvents is the events ↑↓ walk, which are the ones on the day the cursor is on —
// the day in view, the week's selected column, or the year cell that has been stepped into.
// They come back in the order they are drawn: the timed ones by the clock, then the all-day
// ones under them, so walking them never jumps somewhere the eye has to hunt for.
//
// A year cell nobody has stepped into has nothing to walk. That is the point of stepping in:
// until then the arrows belong to the grid.
func (v *calendarView) selectableEvents() []Recording {
	timed, allDay := v.selectableGroups()
	return append(timed, allDay...)
}

// selectableGroups is the same events kept apart, because the day view's arrows treat them as
// two things: the grid is walked with ← and →, and the all-day band under it is somewhere ↑ and
// ↓ go. An all-day event has no hour, so stepping onto it sideways off a 17:00 meeting never
// read as going anywhere.
func (v *calendarView) selectableGroups() (timed, allDay []Recording) {
	var onDay []Recording
	switch v.viewMode {
	case viewDay, viewWeek:
		onDay = eventsByDate(v.events)[dateKey(v.day())]
	case viewYear:
		if !v.inYearCell {
			return nil, nil
		}
		// The year draws HEY's spanned events rather than the recordings the day and the week
		// are built from, so its cells are walked out of the same list they were drawn from.
		onDay = eventsByDate(v.year.SpannedEvents)[dateKey(v.day())]
	}

	timed, allDay = []Recording{}, []Recording{}
	for _, event := range onDay {
		if event.AllDay {
			allDay = append(allDay, event)
		} else {
			timed = append(timed, event)
		}
	}
	sort.SliceStable(timed, func(i, j int) bool { return timed[i].Starts().Before(timed[j].Starts()) })
	sort.SliceStable(allDay, func(i, j int) bool { return allDay[i].Starts().Before(allDay[j].Starts()) })
	return timed, allDay
}

// settleSelection is what a fresh read leaves the selection on. Stepping off the end of a
// span lands on the far end of the next one, so holding an arrow walks through the calendar
// rather than stopping at every screen. Otherwise a selection that is no longer there — the
// event was deleted, or the reader moved to a day it is not on — is simply let go of.
func (v *calendarView) settleSelection() {
	events := v.selectableEvents()

	if v.selectFromEdge != 0 && len(events) > 0 {
		if v.selectFromEdge < 0 {
			v.selectedEvent = events[len(events)-1].key()
		} else {
			v.selectedEvent = events[0].key()
		}
		v.selectFromEdge = 0
		return
	}
	v.selectFromEdge = 0

	for _, event := range events {
		if event.key() == v.selectedEvent {
			return
		}
	}
	v.selectedEvent = ""
}

// selectedRecording is the event under the arrows, and false when the selection has gone —
// a write, a live change or a step to another day can all take it away.
func (v *calendarView) selectedRecording() (Recording, bool) {
	for _, event := range v.selectableEvents() {
		if event.key() == v.selectedEvent {
			return event, true
		}
	}
	return Recording{}, false
}

// moveSelection walks the cursor day's events by one and stops at either end, which is what ↑
// and ↓ do in the week and the year: there the days belong to ← and →, and running off an event
// into another day would take the cursor somewhere the reader did not point it.
func (v *calendarView) moveSelection(delta int) tea.Cmd {
	return v.walk(v.selectableEvents(), delta, nil)
}

// walk moves the selection through one list of events by a step, and hands the ends to past.
func (v *calendarView) walk(events []Recording, delta int, past func() tea.Cmd) tea.Cmd {
	stepPast := func() tea.Cmd {
		if past == nil {
			return nil
		}
		return past()
	}

	if len(events) == 0 {
		return stepPast()
	}

	// Nothing selected yet: → takes the first, ← the last, so the first press picks up the
	// end the reader is moving away from.
	at := indexOfEvent(events, v.selectedEvent)
	if at < 0 {
		if delta > 0 {
			return v.selectEvent(events[0].key())
		}
		return v.selectEvent(events[len(events)-1].key())
	}

	next := at + delta
	if next < 0 || next >= len(events) {
		return stepPast()
	}
	return v.selectEvent(events[next].key())
}

// stepSpanFromEdge is what ← and → do on the day view's first and last event: the day before
// or after, walked into from the end the reader was heading towards.
func (v *calendarView) stepSpanFromEdge(delta int) tea.Cmd {
	v.selectFromEdge = delta
	return v.step(delta)
}

// listedCalendars is what the picker offers — everything but the personal one, which is
// always on and has no name to show.
func (v *calendarView) listedCalendars() []Calendar {
	listed := make([]Calendar, 0, len(v.calendars))
	for _, calendar := range v.calendars {
		if calendar.listed() {
			listed = append(listed, calendar)
		}
	}
	return listed
}

func (v *calendarView) togglePending() bool {
	return v.requests.loading && v.requests.kind == calendarRequestToggle
}

// viewingPersonalCalendar is whether the reader's own calendar is among the ones being
// drawn, which is what decides whether habits are theirs to manage. It is always among
// them: HEY keeps it in the selection and offers no way to switch it off.
func (v *calendarView) viewingPersonalCalendar() bool {
	for _, calendar := range v.calendars {
		if calendar.Personal {
			return true
		}
	}
	return false
}

// manageableHabits is the habits b offers, each marked done or not for the day the cursor is
// on. Which day that is matters over a span: splitRecordings folds a habit's completions into
// one CompletedAt, and a habit kept on three days of a week keeps only the last, so over a week
// or a year the picker would say a habit was done when the day the reader is pointing at is
// blank — and toggling it would then clear a different day's.
func (v *calendarView) manageableHabits() []Recording {
	doneOn := make(map[int64]time.Time, len(v.habitCompletions))
	for _, completion := range v.habitCompletions {
		if done := completion.Starts(); !done.IsZero() && sameDay(done, v.day()) {
			doneOn[completion.ParentID] = done
		}
	}

	seen := make(map[int64]bool)
	habits := make([]Recording, 0, len(v.habits))
	for _, habit := range v.habits {
		if habit.ID <= 0 || seen[habit.ID] {
			continue
		}
		seen[habit.ID] = true
		habit.CompletedAt = doneOn[habit.ID]
		habits = append(habits, habit)
	}
	return habits
}

func (v *calendarView) startHabitForm(mode habitFormMode, recording Recording) tea.Cmd {
	v.habitForm = newHabitForm(mode, recording, v.vc.styles)
	v.habitForm.resize(v.vc.width, v.vc.height)
	return v.habitForm.init()
}

// --- Writing events ---

// errNoCalendars is the one thing that stops the form opening: an event has to be filed
// somewhere, and there is nowhere to put it. See Calendar.fileable — the personal calendar
// and a subscription are not somewhere HEY lets an event be filed.
var errNoCalendars = errors.New("no calendar takes new events")

// fileableCalendars is where an event can go. It is not listedCalendars: that one is about
// which calendars a reader can switch off, and this one about which HEY will accept a write
// to. They differ on the personal calendar, which is switchable by nobody and writable by
// nobody either.
func (v *calendarView) fileableCalendars() []Calendar {
	fileable := make([]Calendar, 0, len(v.calendars))
	for _, calendar := range v.calendars {
		if calendar.fileable() {
			fileable = append(fileable, calendar)
		}
	}
	return fileable
}

// startEventForm opens the form over the day or week, on the day the reader is looking at.
// A new event needs somewhere to go, and the calendars a period is drawn from are the ones
// it can go on.
func (v *calendarView) startEventForm(mode eventFormMode, event Recording) tea.Cmd {
	fileable := v.fileableCalendars()
	if len(fileable) == 0 {
		return notifyError("Cannot add an event", errNoCalendars)
	}
	v.editing = event
	v.eventForm = newEventForm(mode, event, v.day(), fileable, v.lastCalendarID(), v.vc.styles)

	// An edit is handed what the event already carries, and this is load-bearing rather than a
	// courtesy: HEY clears the notes, location, link and attached email on any write that
	// leaves them out, so a form that did not know them would wipe all four every time somebody
	// renamed an event.
	if mode == eventFormEdit {
		v.eventForm.setDetails(eventDetails{
			Notes: event.Notes, Location: event.Location, Link: event.Link,
			Invites: event.Attendees, Circled: event.Highlighted,
			Repeats: event.Recurring, RepeatKind: event.RepeatKind,
			AttachedEntryID: event.AttachedEntryID,
		})
	}
	v.eventForm.resize(v.vc.width, v.vc.height)
	return v.eventForm.init()
}

// saveEvent writes what the form is holding, including which calendar it is on: an update
// takes a calendar the same way a create does, so stepping the form's picker moves the event.
func (v *calendarView) saveEvent() tea.Cmd {
	form := v.eventForm
	values := form.values()
	v.rememberCalendar(values.CalendarID)
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)

	return func() tea.Msg {
		var err error
		action := "Event created"
		if form.mode == eventFormCreate {
			_, err = v.vc.sdk.CalendarEvents().Create(ctx, hey.CreateCalendarEventParams{
				CalendarID:    values.CalendarID,
				Title:         values.Title,
				StartsAt:      values.StartsAt,
				EndsAt:        values.EndsAt,
				AllDay:        values.AllDay,
				StartTime:     values.StartTime,
				EndTime:       values.EndTime,
				StartTimeZone: values.StartTimeZone,
				EndTimeZone:   values.EndTimeZone,
				Reminders:     values.Reminders,
				Content: hey.EventContentParams{
					Notes: values.Notes, Location: values.Location,
					Link: values.Link, EntryID: values.AttachedEntryID,
				},
				Attendees:   values.Invites,
				Highlighted: &values.Circled,
				Countdown:   values.Countdown,
				Repeat:      values.Repeat,
			})
		} else {
			action = "Event updated"
			// The zones go on every update, empty ones included: HEY reads the pair out of
			// what was submitted or nils both, so leaving them out would quietly strip the
			// zones off an event that had them.
			changes := hey.UpdateCalendarEventParams{
				CalendarID:    &values.CalendarID,
				Title:         &values.Title,
				StartsAt:      &values.StartsAt,
				EndsAt:        &values.EndsAt,
				AllDay:        &values.AllDay,
				StartTime:     &values.StartTime,
				EndTime:       &values.EndTime,
				StartTimeZone: &values.StartTimeZone,
				EndTimeZone:   &values.EndTimeZone,
				Reminders:     values.Reminders,
				Content: hey.EventContentParams{
					Notes: values.Notes, Location: values.Location,
					Link: values.Link, EntryID: values.AttachedEntryID,
				},
				Attendees:   values.Invites,
				Highlighted: &values.Circled,
				Countdown:   values.Countdown,
				Repeat:      values.Repeat,
			}
			if occurrence, ok := v.editingOccurrence(); ok {
				action = "This day updated"
				_, err = v.vc.sdk.CalendarEvents().UpdateOccurrence(ctx, occurrence,
					hey.OccurrenceScopeThisEvent,
					hey.UpdateCalendarEventOccurrenceParams{UpdateCalendarEventParams: changes})
			} else {
				_, err = v.vc.sdk.CalendarEvents().Update(ctx, v.editing.ID, changes)
			}
		}
		return calendarMutationMsg{requestResult: newRequestResult(requestID, err), action: action, failure: "Save failed"}
	}
}

// lastCalendarID and rememberCalendar are where the calendar a new event opens on comes from and
// goes. It is a preference rather than data, so it is remembered as soon as the reader saves
// rather than after HEY answers, and a machine that cannot store it simply keeps offering the
// first calendar.
func (v *calendarView) lastCalendarID() int64 {
	if v.vc.loadLastCalendar == nil {
		return 0
	}
	return v.vc.loadLastCalendar()
}

func (v *calendarView) rememberCalendar(id int64) {
	if v.vc.saveLastCalendar == nil || id == 0 {
		return
	}
	_ = v.vc.saveLastCalendar(id)
}

func (v *calendarView) editingOccurrence() (hey.EventOccurrence, bool) {
	return occurrenceOf(v.editing)
}

// occurrenceOf is the day of a repeating event a recording stands for, and false for an ordinary
// event. HEY serves such a day with no id of its own, so it is addressed by the series and the
// date instead — and both the edit and the delete act on that day alone rather than the whole
// series, which is the narrower of the two things the reader might have meant.
func occurrenceOf(recording Recording) (hey.EventOccurrence, bool) {
	if recording.ID != 0 || recording.OccurrenceID == "" {
		return hey.EventOccurrence{}, false
	}
	occurrence, err := hey.ParseOccurrenceID(recording.OccurrenceID)
	return occurrence, err == nil
}

// removeSelectedEvent asks twice, as deleting a habit does: an event off a shared calendar
// is gone for everybody on it.
func (v *calendarView) removeSelectedEvent() tea.Cmd {
	event, ok := v.selectedRecording()
	if !ok {
		return nil
	}
	if v.confirmDelete != event.key() {
		v.confirmDelete = event.key()
		return nil
	}
	v.confirmDelete = ""

	// One day of a repeating event is taken off on its own rather than the series with it: HEY
	// keeps the rest by writing the day into the schedule's exceptions.
	occurrence, isOccurrence := occurrenceOf(event)
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)
	return func() tea.Msg {
		var err error
		action := "Event deleted"
		if isOccurrence {
			action = "This day deleted"
			err = v.vc.sdk.CalendarEvents().DeleteOccurrence(ctx, occurrence, hey.OccurrenceScopeThisEvent)
		} else {
			err = v.vc.sdk.CalendarEvents().Delete(ctx, event.ID)
		}
		return calendarMutationMsg{requestResult: newRequestResult(requestID, err), action: action, failure: "Could not delete the event"}
	}
}

func (v *calendarView) saveHabit() tea.Cmd {
	form := v.habitForm
	name, icon, color, days := form.values()
	params := hey.HabitParams{Name: name, Icon: icon, Color: color, Days: days}
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)
	return func() tea.Msg {
		var err error
		action := "Habit created"
		if form.mode == habitFormCreate {
			_, err = v.vc.sdk.Habits().Create(ctx, params)
		} else {
			action = "Habit updated"
			_, err = v.vc.sdk.Habits().Update(ctx, form.habitID, params)
		}
		return calendarMutationMsg{requestResult: newRequestResult(requestID, err), action: action, failure: "Save failed"}
	}
}

// toggleHabitCompletion does a habit for the day on screen, or undoes it. HEY records
// the doing as a recording of its own, on a day-scoped route, so which day is being
// looked at is part of the request rather than an argument to the habit.
func (v *calendarView) toggleHabitCompletion(habit Recording) tea.Cmd {
	day := v.day().Local().Format(time.DateOnly)
	done := habit.Done()
	// The day the cursor is on, said by name unless it is today — over a week or a year the
	// reader is often pointing at some other day, and "for today" would be a lie about which
	// day was just marked.
	when := "for today"
	if !v.showingToday() {
		when = "for " + v.day().Local().Format("Monday 2 January")
	}
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)
	return func() tea.Msg {
		var err error
		action := "Habit done " + when
		if done {
			action = "Habit cleared " + when
			_, err = v.vc.sdk.Habits().Uncomplete(ctx, day, habit.ID)
		} else {
			_, err = v.vc.sdk.Habits().Complete(ctx, day, habit.ID)
		}
		return calendarMutationMsg{requestResult: newRequestResult(requestID, err), action: action, failure: "Could not update the habit"}
	}
}

func (v *calendarView) deleteHabit(recording Recording) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)
	return func() tea.Msg {
		err := v.vc.sdk.Habits().Delete(ctx, recording.ID)
		return calendarMutationMsg{requestResult: newRequestResult(requestID, err), action: "Habit deleted", failure: "Delete failed"}
	}
}

// addTodo files a to-do on the day on screen, which is the week the picker is showing.
// HEY takes a bare date so the day is the reader's rather than UTC's.
func (v *calendarView) addTodo(title string) tea.Cmd {
	day := v.day().Local().Format(time.DateOnly)
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)
	return func() tea.Msg {
		_, err := v.vc.sdk.CalendarTodos().Create(ctx, title, day)
		return calendarMutationMsg{requestResult: newRequestResult(requestID, err), action: "To-do added", failure: "Could not add the to-do"}
	}
}

// renameTodo changes a to-do's title and nothing else: TodoChanges leaves a zero field
// alone, so the day it is filed on stays where it was.
func (v *calendarView) renameTodo(todo Recording, title string) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)
	return func() tea.Msg {
		_, err := v.vc.sdk.CalendarTodos().Update(ctx, todo.ID, hey.TodoChanges{Title: title})
		return calendarMutationMsg{requestResult: newRequestResult(requestID, err), action: "To-do renamed", failure: "Could not rename the to-do"}
	}
}

// toggleTodo ticks a to-do off or puts it back. Unlike a habit, which is done on a
// given day, a to-do is done or it is not.
func (v *calendarView) toggleTodo(todo Recording) tea.Cmd {
	done := todo.Done()
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)
	return func() tea.Msg {
		var err error
		action := "To-do done"
		if done {
			action = "To-do cleared"
			_, err = v.vc.sdk.CalendarTodos().Uncomplete(ctx, todo.ID)
		} else {
			_, err = v.vc.sdk.CalendarTodos().Complete(ctx, todo.ID)
		}
		return calendarMutationMsg{requestResult: newRequestResult(requestID, err), action: action, failure: "Could not update the to-do"}
	}
}

func (v *calendarView) deleteTodo(todo Recording) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)
	return func() tea.Msg {
		err := v.vc.sdk.CalendarTodos().Delete(ctx, todo.ID)
		return calendarMutationMsg{requestResult: newRequestResult(requestID, err), action: "To-do deleted", failure: "Could not delete the to-do"}
	}
}

// --- SDK type converters ---

// sdkCalendarToModel and sdkRecordingToModel keep the text as HEY served it: a habit's
// title goes back through the edit form, so sanitizing it here would rewrite it on an
// unrelated save. Every view sanitizes what it shows instead.
func sdkCalendarToModel(c generated.Calendar) Calendar {
	return Calendar{
		ID: c.Id, Name: c.Name, OwnerEmail: c.OwnerEmailAddress,
		Color: c.Color, Personal: c.Personal, External: c.External,
	}
}

func sdkRecordingToModel(r generated.Recording) Recording {
	parentTitle := ""
	if r.Parent != nil {
		parentTitle = r.Parent.Title
	}
	// Left nil rather than empty for an event nobody is invited to. An empty list is a request
	// to clear the roster, which HEY answers by making the caller the organizer and sending
	// cancellations — not what reading an event with no guests should turn into.
	var attendees []string
	for _, attendance := range r.Attendances {
		if attendance.EmailAddress != "" {
			attendees = append(attendees, attendance.EmailAddress)
		}
	}
	return Recording{
		ParentTitle: parentTitle,
		Notes:       r.Description, Location: r.Location, Link: r.Url,
		Attendees: attendees, AttachedEntryID: r.AttachedEntry.Id,
		Highlighted: r.Highlighted,
		Recurring:   r.Recurring, RepeatKind: r.RecurrenceSchedule.Kind,
		ID: r.Id, ParentID: r.ParentId, Title: r.Title, AllDay: r.AllDay, Type: r.Type,
		StartsAt: r.StartsAt, EndsAt: r.EndsAt,
		StartsAtZone: r.StartsAtTimeZone, EndsAtZone: r.EndsAtTimeZone,
		OccurrenceID: r.OccurrenceId,
		CompletedAt:  r.CompletedAt, Label: r.Label,
		Icon: r.Icon, Color: r.Color,
		CalendarID: r.Calendar.Id, CalendarColor: r.Calendar.Color,
		Days: append([]int32(nil), r.Days...),
	}
}

// --- Fetch commands ---

func (v *calendarView) fetchIdentity() tea.Cmd {
	return func() tea.Msg {
		if v.vc.sdk == nil || v.vc.ctx == nil {
			return identityLoadedMsg{firstWeekDay: time.Monday}
		}
		identity, err := v.vc.sdk.Identity().GetIdentity(v.vc.ctx)
		if err != nil || identity == nil {
			return identityLoadedMsg{firstWeekDay: time.Monday}
		}
		wd := identity.FirstWeekDay
		if wd < 0 || wd > 6 {
			wd = 1 // default to Monday
		}
		return identityLoadedMsg{firstWeekDay: time.Weekday(wd)}
	}
}

func (v *calendarView) requestCalendars() tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestCalendars)
	return func() tea.Msg {
		payload, err := v.vc.sdk.Calendars().List(ctx)
		if err != nil {
			return calendarsLoadedMsg{requestResult: newRequestResult(requestID, err)}
		}
		if payload == nil {
			return calendarsLoadedMsg{requestResult: newRequestResult(requestID, nil)}
		}
		calendars := make([]Calendar, 0, len(payload.Calendars))
		for _, cw := range payload.Calendars {
			calendars = append(calendars, sdkCalendarToModel(cw.Calendar))
		}
		return calendarsLoadedMsg{
			requestResult: newRequestResult(requestID, nil),
			calendars:     calendars,
			selected:      selectionSet(payload.SelectedCalendarIds),
		}
	}
}

// toggleCalendar switches one calendar and takes the selection back from the answer.
func (v *calendarView) toggleCalendar(calendar Calendar) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestToggle)
	on := !v.selected[calendar.ID]
	name := terminal.SanitizeLine(calendar.Name)
	return func() tea.Msg {
		ids, err := v.vc.sdk.Calendars().Toggle(ctx, calendar.ID)
		return calendarToggledMsg{
			requestResult: newRequestResult(requestID, err),
			selected:      selectionSet(ids),
			name:          name,
			on:            on,
		}
	}
}

func selectionSet(ids []int64) map[int64]bool {
	selected := make(map[int64]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	return selected
}

func toggleNotice(name string, on bool) string {
	if on {
		return name + " shown"
	}
	return name + " hidden"
}

// requestRecordings reads the period the view is on, scoped by HEY to the calendars the
// identity has switched on. The period is fixed when the read starts, which is why the
// answer has to be discarded when the span or the day has moved on since.
//
// A period is the read to use rather than a calendar's recordings over the same dates: a
// recurring event is one row on a calendar and HEY expands it into occurrences per
// period, so a weekly meeting drawn from a calendar's recordings appears once, on the day
// it was created.
func (v *calendarView) requestRecordings() tea.Cmd {
	date := v.day().Format("2006-01-02")
	mode := v.viewMode
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestRecordings)
	return func() tea.Msg {
		periods := v.vc.sdk.CalendarPeriods()

		// The year is its own answer, and one read: HEY serves a year as the grid it is
		// drawn as rather than as the recordings inside it.
		if mode == viewYear {
			year, err := periods.Year(ctx, date)
			if err != nil {
				return yearLoadedMsg{requestResult: newRequestResult(requestID, err)}
			}
			return yearLoadedMsg{requestResult: newRequestResult(requestID, nil), year: sdkYearToModel(year)}
		}

		var period *generated.CalendarPeriod
		var err error
		if mode == viewWeek {
			period, err = periods.Week(ctx, date)
		} else {
			period, err = periods.Day(ctx, date)
		}
		if err != nil {
			return recordingsLoadedMsg{requestResult: newRequestResult(requestID, err)}
		}
		return recordingsLoadedMsg{requestResult: newRequestResult(requestID, nil), recordings: recordingsIn(period)}
	}
}

func sdkYearToModel(year *generated.CalendarYear) CalendarYear {
	if year == nil {
		return CalendarYear{}
	}
	model := CalendarYear{PaddingDays: int(year.PaddingDaysCount)}
	for _, day := range year.Days {
		model.Days = append(model.Days, YearDay{Date: day.StartsAt, Backgrounded: day.Backgrounded})
	}
	for _, event := range year.SpannedEvents {
		model.SpannedEvents = append(model.SpannedEvents, sdkRecordingToModel(event))
	}
	return model
}

func recordingsIn(period *generated.CalendarPeriod) []Recording {
	if period == nil {
		return nil
	}
	var all []Recording
	for _, recs := range period.Recordings {
		for _, r := range recs {
			all = append(all, sdkRecordingToModel(r))
		}
	}
	return all
}

func (v *calendarView) requestTimeTrackCategories() tea.Cmd {
	if v.vc.sdk == nil || v.vc.ctx == nil {
		v.timeTrackCategories.status = "Could not load categories: time track categories are unavailable"
		return nil
	}
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestCategories)
	return func() tea.Msg {
		categories, err := v.vc.sdk.TimeTracks().Categories(ctx)
		return timeTrackCategoriesLoadedMsg{requestResult: newRequestResult(requestID, err), categories: categories}
	}
}

func (v *calendarView) createTimeTrackCategory(title string) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestCategories)
	return func() tea.Msg {
		err := v.vc.sdk.TimeTracks().CreateCategory(ctx, title)
		return timeTrackCategorySavedMsg{requestResult: newRequestResult(requestID, err), summary: fmt.Sprintf("Created %q", title)}
	}
}

func (v *calendarView) renameTimeTrackCategory(categoryID int64, title string) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestCategories)
	return func() tea.Msg {
		err := v.vc.sdk.TimeTracks().UpdateCategory(ctx, categoryID, title)
		return timeTrackCategorySavedMsg{requestResult: newRequestResult(requestID, err), summary: fmt.Sprintf("Renamed category to %q", title)}
	}
}

func (v *calendarView) deleteTimeTrackCategory(categoryID int64, title string) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestCategories)
	return func() tea.Msg {
		err := v.vc.sdk.TimeTracks().DeleteCategory(ctx, categoryID)
		return timeTrackCategorySavedMsg{requestResult: newRequestResult(requestID, err), summary: fmt.Sprintf("Deleted %q", title)}
	}
}
