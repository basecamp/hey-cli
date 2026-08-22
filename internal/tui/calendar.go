package tui

import (
	"fmt"
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
	ID       int64
	Name     string
	Color    string
	Personal bool
}

// listed is whether the picker offers this calendar at all. The personal calendar is
// never offered: it holds the reader's own habits and todos, it is on in every client,
// and with no name of its own it would be a blank row that cannot be switched off.
func (c Calendar) listed() bool {
	return !c.Personal
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
// track — told apart by Type. Its times are strings because that is the shape the
// calendar views read them in; giving them time.Time is the next thing to do here.
type Recording struct {
	ID          int64
	ParentID    int64
	Title       string
	AllDay      bool
	StartsAt    string
	EndsAt      string
	Type        string
	CompletedAt string
	Label       string
	Icon        string
	// Color is a habit's own color. An event has none — what it wears is its
	// calendar's, which is CalendarColor, and the two are different fields in HEY too.
	Color string
	// CalendarColor is the color of the calendar this is filed on, which is how a reader
	// tells whose event they are looking at. HEY leaves it empty for the personal
	// calendar: `_calendar.jbuilder` serves the color `unless calendar.personal?`.
	CalendarColor string
	Days          []int32
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

	// year is what the year span draws, and it is a different answer than the
	// recordings above rather than a summary of them.
	year CalendarYear

	// Scrollable content viewport for the calendar views
	contentVP viewport.Model

	// drawn is whether a day has ever reached the screen, which is what the spinner
	// waits for. See Loading.
	drawn bool

	timeTrackCategories *timeTrackCategoryManager
	habitPicker         *habitPicker
	habitForm           *habitForm
	todoPicker          *todoPicker
	calendarPicker      *calendarPicker

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
		v.events, v.todos, v.habits, v.habitCompletions = splitRecordings(msg.recordings)
		if v.habitPicker != nil {
			v.habitPicker.setHabits(v.manageableHabits())
		}
		if v.todoPicker != nil {
			v.todoPicker.setTodos(v.todos)
		}
		v.rebuildView()
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
			return notifyError(msg.failure, msg.err), true
		}
		v.habitForm = nil
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

	return nil, false
}

func (v *calendarView) View() string {
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
	if v.habitPicker != nil {
		return v.habitPicker.helpBindings()
	}
	if v.todoPicker != nil {
		return v.todoPicker.helpBindings()
	}
	if v.calendarPicker != nil {
		return v.calendarPicker.helpBindings()
	}
	// The day says which keys move it on the line that names it. The week and the year
	// have no such line, so the help bar carries it for them.
	var bindings []helpBinding
	if v.viewMode != viewDay {
		bindings = append(bindings, helpBinding{"←→", v.viewMode.unit()})
		if !v.onToday() {
			bindings = append(bindings, helpBinding{"t", "today"})
		}
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
	return v.reread()
}

func (v *calendarView) HandleContentKey(msg tea.KeyPressMsg) tea.Cmd {
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

	switch msg.String() {
	// b for habits, as in HEY's own calendar.
	case "b":
		v.habitPicker = newHabitPicker(v.manageableHabits())
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
	case "left", "p":
		return v.step(-1)
	case "right", "n":
		return v.step(1)
	case "t":
		return v.today()
	}

	// Delegate scrolling to the content viewport
	var cmd tea.Cmd
	v.contentVP, cmd = v.contentVP.Update(msg)
	return cmd
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
	return v.timeTrackCategories != nil || v.habitForm != nil ||
		v.habitPicker != nil || v.todoPicker != nil || v.calendarPicker != nil
}

func (v *calendarView) AccountSwitchBlocked() bool {
	return v.requests.kind == calendarRequestMutation
}

// Restyle re-renders the day/week/year grid, which caches styled output in its
// viewport. The recording detail is plain text and needs nothing.
func (v *calendarView) Restyle() {
	offset := v.contentVP.YOffset()
	v.rebuildView()
	v.contentVP.SetYOffset(offset)
}

func (v *calendarView) Resize(width, height int) {
	if v.habitForm != nil {
		v.habitForm.resize(width, height)
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
	switch v.viewMode {
	case viewDay:
		content = renderDayView(v.events, v.habits, anchor, v.stepHint(), w, v.contentVP.Height())
	case viewWeek:
		content = renderWeekView(v.events, v.habits, v.habitCompletions, anchor, v.firstWeekDay, w, h, dayLabels)
	case viewYear:
		// The year's events are HEY's spanned_events — the all-day and multi-day ones —
		// because that is all a year read carries. eventsByDate spreads a multi-day event
		// over the days it covers, so the grid fills the same way it always did.
		content = renderYearView(v.year.SpannedEvents, anchor, v.firstWeekDay, w, h)
	}

	v.contentVP.SetContent(content)
	v.drawn = true

	// For year view, scroll to the current week
	if v.viewMode == viewYear {
		gridStart := weekStartDate(time.Date(anchor.Year(), 1, 1, 0, 0, 0, 0, anchor.Location()), v.firstWeekDay)
		weeksToToday := daysBetween(gridStart, anchor) / 7
		// Center today's week in the viewport (+2 for header rows)
		offset := max(weeksToToday-h/2+2, 0)
		v.contentVP.SetYOffset(offset)
	} else {
		v.contentVP.GotoTop()
	}
}

// day is the day the view is on: today, until the reader steps off it.
func (v *calendarView) day() time.Time {
	if v.anchor.IsZero() {
		return v.now()
	}
	return v.anchor
}

func (v *calendarView) onToday() bool {
	return v.anchor.IsZero()
}

// stepHint is the keys that move the view, said on the line that names the day rather
// than in the help bar: they belong to the date they act on. t is only mentioned once it
// would do something.
func (v *calendarView) stepHint() string {
	hint := "←→ " + v.viewMode.unit()
	if !v.onToday() {
		hint += " · t today"
	}
	return hint
}

// step moves the view by its own unit — a day, a week or a year — since ← and → mean
// "the one before this" whatever the view is showing.
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

func (v *calendarView) manageableHabits() []Recording {
	seen := make(map[int64]bool)
	habits := make([]Recording, 0, len(v.habits))
	for _, habit := range v.habits {
		if habit.ID <= 0 || seen[habit.ID] {
			continue
		}
		seen[habit.ID] = true
		habits = append(habits, habit)
	}
	return habits
}

func (v *calendarView) startHabitForm(mode habitFormMode, recording Recording) tea.Cmd {
	v.habitForm = newHabitForm(mode, recording, v.vc.styles)
	v.habitForm.resize(v.vc.width, v.vc.height)
	return v.habitForm.init()
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
	done := habit.CompletedAt != ""
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestMutation)
	return func() tea.Msg {
		var err error
		action := "Habit done for today"
		if done {
			action = "Habit cleared for today"
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
	done := todo.CompletedAt != ""
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
	return Calendar{ID: c.Id, Name: c.Name, Color: c.Color, Personal: c.Personal}
}

func sdkRecordingToModel(r generated.Recording) Recording {
	return Recording{
		ID: r.Id, ParentID: r.ParentId, Title: r.Title, AllDay: r.AllDay, Type: r.Type,
		StartsAt: formatTimestamp(r.StartsAt), EndsAt: formatTimestamp(r.EndsAt),
		CompletedAt: formatTimestamp(r.CompletedAt), Label: r.Label,
		Icon: r.Icon, Color: r.Color, CalendarColor: r.Calendar.Color,
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
