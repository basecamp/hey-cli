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

// Calendar is one of the reader's calendars, as the subnav and the habit form need it.
// Personal is the one HEY files a habit or a todo on when no calendar is named.
type Calendar struct {
	ID       int64
	Name     string
	Personal bool
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
	Color       string
	Days        []int32
}

// --- Calendar messages ---

type calendarRequestKind int

const (
	calendarRequestNone calendarRequestKind = iota
	calendarRequestCalendars
	calendarRequestRecordings
	calendarRequestMutation
	calendarRequestCategories
)

type calendarsLoadedMsg struct {
	requestResult
	calendars []Calendar
}

type recordingsLoadedMsg struct {
	requestResult
	recordings []Recording
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
	calIndex  int

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

	// Recordings split by type
	events []Recording
	todos  []Recording
	habits []Recording

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
	} else if v.calIndex < len(v.calendars) {
		cmds = append(cmds, v.requestRecordings(v.calendars[v.calIndex].ID))
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
		if len(v.calendars) > 0 {
			v.calIndex = 0
			return v.requestRecordings(v.calendars[0].ID), true
		}
		return nil, true

	case recordingsLoadedMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		v.events, v.todos, v.habits = splitRecordings(msg.recordings)
		if v.habitPicker != nil {
			v.habitPicker.setHabits(v.manageableHabits())
		}
		if v.todoPicker != nil {
			v.todoPicker.setTodos(v.todos)
		}
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
		if v.calIndex >= 0 && v.calIndex < len(v.calendars) {
			return tea.Batch(notify(msg.action), v.requestRecordings(v.calendars[v.calIndex].ID)), true
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
	if len(v.calendars) > 1 {
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
		if len(v.calendars) > 1 {
			v.calendarPicker = newCalendarPicker(v.calendars, v.calIndex)
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
// handleCalendarPickerKey gives the open picker every key. Choosing the calendar that is
// already open just closes it, rather than reading the same range again.
func (v *calendarView) handleCalendarPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	picker := v.calendarPicker

	switch msg.String() {
	case "esc", "q":
		v.calendarPicker = nil
		return nil
	case "enter":
		index := picker.selected()
		v.calendarPicker = nil
		if index < 0 || index == v.calIndex {
			return nil
		}
		v.calIndex = index
		return v.reread()
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
		content = renderWeekView(v.events, v.habits, anchor, v.firstWeekDay, w, h, dayLabels)
	case viewYear:
		content = renderYearView(v.events, anchor, v.firstWeekDay, w, h, dayLabels)
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

// reread reads the range the view now covers, or redraws what is already here when
// there is no calendar to read from.
func (v *calendarView) reread() tea.Cmd {
	if v.calIndex >= 0 && v.calIndex < len(v.calendars) {
		return v.requestRecordings(v.calendars[v.calIndex].ID)
	}
	v.rebuildView()
	return nil
}

func (v *calendarView) viewingPersonalCalendar() bool {
	return v.calIndex >= 0 && v.calIndex < len(v.calendars) && v.calendars[v.calIndex].Personal
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
	return Calendar{ID: c.Id, Name: c.Name, Personal: c.Personal}
}

func sdkRecordingToModel(r generated.Recording) Recording {
	return Recording{
		ID: r.Id, ParentID: r.ParentId, Title: r.Title, AllDay: r.AllDay, Type: r.Type,
		StartsAt: formatTimestamp(r.StartsAt), EndsAt: formatTimestamp(r.EndsAt),
		CompletedAt: formatTimestamp(r.CompletedAt), Label: r.Label,
		Icon: r.Icon, Color: r.Color, Days: append([]int32(nil), r.Days...),
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
		return calendarsLoadedMsg{requestResult: newRequestResult(requestID, nil), calendars: calendars}
	}
}

// requestRecordings reads the range the current view mode covers around today. The
// range is fixed when the read starts, which is why the answer has to be discarded
// when the mode or the calendar has moved on since.
func (v *calendarView) requestRecordings(calID int64) tea.Cmd {
	start, end := dateRangeForMode(v.viewMode, v.day(), v.firstWeekDay)
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestRecordings)
	return func() tea.Msg {
		startsOn, endsOn := start.Format("2006-01-02"), end.Format("2006-01-02")
		resp, err := v.vc.sdk.Calendars().GetRecordings(ctx, calID, &generated.GetCalendarRecordingsParams{
			StartsOn: &startsOn,
			EndsOn:   &endsOn,
		})
		if err != nil {
			return recordingsLoadedMsg{requestResult: newRequestResult(requestID, err)}
		}
		var all []Recording
		if resp != nil {
			for _, recs := range *resp {
				for _, r := range recs {
					all = append(all, sdkRecordingToModel(r))
				}
			}
		}
		return recordingsLoadedMsg{requestResult: newRequestResult(requestID, nil), recordings: all}
	}
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
