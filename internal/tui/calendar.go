package tui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
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
	calendarRequestHabitMutation
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

type habitMutationMsg struct {
	requestResult
	action string
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

	// Recordings split by type
	events []Recording
	todos  []Recording
	habits []Recording

	// Scrollable content viewport for the calendar views
	contentVP viewport.Model

	timeTrackCategories    *timeTrackCategoryManager
	habitForm              *habitForm
	habitIndex             int
	confirmedHabitDeleteID int64
	notice                 string

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
	v.confirmedHabitDeleteID = 0
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
		v.confirmedHabitDeleteID = 0
		v.events, v.todos, v.habits = splitRecordings(msg.recordings)
		v.normalizeHabitSelection()
		v.rebuildView()
		return nil, true

	case habitMutationMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			if v.habitForm != nil {
				v.habitForm.saving = false
				v.habitForm.status = errorNotice("Save failed", msg.err)
				v.habitForm.isError = true
			} else {
				v.notice = errorNotice("Delete failed", msg.err)
			}
			return nil, true
		}
		v.habitForm = nil
		v.confirmedHabitDeleteID = 0
		v.notice = msg.action
		if v.calIndex >= 0 && v.calIndex < len(v.calendars) {
			return v.requestRecordings(v.calendars[v.calIndex].ID), true
		}
		return nil, true

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
	if v.habitForm != nil {
		return v.habitForm.view()
	}
	var heading string
	if v.notice != "" {
		heading = v.vc.styles.title.Render(v.notice) + "\n"
	}
	if habit := v.selectedHabit(); habit != nil {
		heading += styleMuted.Render(fmt.Sprintf("Selected habit %d/%d: %s (ID %d)", v.habitIndex+1, len(v.manageableHabits()), habit.Title, habit.ID)) + "\n"
	}
	return heading + v.contentVP.View()
}

func (v *calendarView) HelpBindings() []helpBinding {
	if v.timeTrackCategories != nil {
		return v.timeTrackCategories.helpBindings()
	}
	if v.habitForm != nil {
		return v.habitForm.helpBindings()
	}
	bindings := []helpBinding{{"v", v.viewMode.next().String() + " view"}, {"c", "time categories"}}
	if v.viewingPersonalCalendar() {
		bindings = append(bindings, helpBinding{"a", "create habit"})
	}
	if len(v.manageableHabits()) > 0 {
		bindings = append(bindings, helpBinding{"[/]", "select habit"}, helpBinding{"e", "edit habit"})
		deleteLabel := "delete habit"
		if v.habitDeleteConfirmed() {
			deleteLabel = "confirm delete"
		}
		bindings = append(bindings, helpBinding{"x", deleteLabel})
	}
	return bindings
}

func (v *calendarView) SubnavItems() ([]navItem, int, string, bool) {
	label := "Calendar"
	if v.calIndex >= 0 && v.calIndex < len(v.calendars) {
		label = v.calendars[v.calIndex].Name
	}
	label += " · " + v.viewMode.String()
	return calendarNavItems(v.calendars), v.calIndex, label, true
}

func (v *calendarView) SubnavLeft() tea.Cmd {
	if v.calIndex > 0 {
		v.calIndex--
		return v.requestRecordings(v.calendars[v.calIndex].ID)
	}
	return nil
}

func (v *calendarView) SubnavRight() tea.Cmd {
	if v.calIndex < len(v.calendars)-1 {
		v.calIndex++
		return v.requestRecordings(v.calendars[v.calIndex].ID)
	}
	return nil
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
	if v.requests.kind == calendarRequestHabitMutation {
		return nil
	}

	if msg.String() != "x" {
		v.confirmedHabitDeleteID = 0
	}
	v.notice = ""
	switch msg.String() {
	case "c":
		v.timeTrackCategories = newTimeTrackCategoryManager()
		return v.requestTimeTrackCategories()
	case "v":
		v.viewMode = v.viewMode.next()
		if v.calIndex >= 0 && v.calIndex < len(v.calendars) {
			return v.requestRecordings(v.calendars[v.calIndex].ID)
		}
		v.rebuildView()
		return nil
	case "a":
		if !v.viewingPersonalCalendar() {
			v.notice = "Habits can only be created from the personal calendar"
			return nil
		}
		return v.startHabitForm(habitFormCreate, Recording{})
	case "[":
		v.moveHabitSelection(-1)
		return nil
	case "]":
		v.moveHabitSelection(1)
		return nil
	case "e":
		if habit := v.selectedHabit(); habit != nil {
			return v.startHabitForm(habitFormEdit, *habit)
		}
	case "x":
		if habit := v.selectedHabit(); habit != nil {
			if v.confirmedHabitDeleteID != habit.ID {
				v.confirmedHabitDeleteID = habit.ID
				v.notice = fmt.Sprintf("Press x again to permanently delete %s and its history", habit.Title)
				return nil
			}
			return v.deleteHabit(*habit)
		}
	}

	// Delegate scrolling to the content viewport
	var cmd tea.Cmd
	v.contentVP, cmd = v.contentVP.Update(msg)
	return cmd
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
func (v *calendarView) Loading() bool  { return v.requests.loading }
func (v *calendarView) CapturingInput() bool {
	return v.timeTrackCategories != nil || v.habitForm != nil
}

func (v *calendarView) AccountSwitchBlocked() bool {
	return v.requests.kind == calendarRequestHabitMutation
}

// Restyle re-renders the day/week/year grid, which caches styled output in its
// viewport. The recording detail is plain text and needs nothing.
func (v *calendarView) Restyle() {
	offset := v.contentVP.YOffset()
	v.rebuildView()
	v.contentVP.SetYOffset(offset)
}

func (v *calendarView) Resize(width, height int) {
	v.contentVP.SetWidth(width)
	v.contentVP.SetHeight(max(height-2, 1))
	if v.habitForm != nil {
		v.habitForm.resize(width, height)
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

	anchor := v.now()
	dayLabels := dayLabelsFromRecordings(v.events, v.todos, v.habits)

	var content string
	switch v.viewMode {
	case viewDay:
		content = renderDayView(v.events, v.todos, v.habits, anchor, w, h)
	case viewWeek:
		content = renderWeekView(v.events, v.todos, v.habits, anchor, v.firstWeekDay, w, h, dayLabels)
	case viewYear:
		content = renderYearView(v.events, anchor, v.firstWeekDay, w, h, dayLabels)
	}

	v.contentVP.SetContent(content)

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

func (v *calendarView) selectedHabit() *Recording {
	habits := v.manageableHabits()
	if len(habits) == 0 {
		return nil
	}
	v.habitIndex = max(0, min(v.habitIndex, len(habits)-1))
	habit := habits[v.habitIndex]
	return &habit
}

func (v *calendarView) normalizeHabitSelection() {
	habits := v.manageableHabits()
	if len(habits) == 0 {
		v.habitIndex = 0
		return
	}
	v.habitIndex = max(0, min(v.habitIndex, len(habits)-1))
}

func (v *calendarView) moveHabitSelection(delta int) {
	habits := v.manageableHabits()
	if len(habits) == 0 {
		v.habitIndex = 0
		return
	}
	v.habitIndex = (v.habitIndex + delta + len(habits)) % len(habits)
}

func (v *calendarView) habitDeleteConfirmed() bool {
	habit := v.selectedHabit()
	return habit != nil && v.confirmedHabitDeleteID == habit.ID
}

func (v *calendarView) startHabitForm(mode habitFormMode, recording Recording) tea.Cmd {
	v.confirmedHabitDeleteID = 0
	v.habitForm = newHabitForm(mode, recording, v.vc.styles)
	v.habitForm.resize(v.vc.width, v.vc.height)
	return v.habitForm.init()
}

func (v *calendarView) saveHabit() tea.Cmd {
	form := v.habitForm
	name, icon, color, days, _ := form.values()
	params := hey.HabitParams{Name: name, Icon: icon, Color: color, Days: days}
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestHabitMutation)
	return func() tea.Msg {
		var err error
		action := "Habit created"
		if form.mode == habitFormCreate {
			_, err = v.vc.sdk.Habits().Create(ctx, params)
		} else {
			action = "Habit updated"
			_, err = v.vc.sdk.Habits().Update(ctx, form.habitID, params)
		}
		return habitMutationMsg{requestResult: newRequestResult(requestID, err), action: action}
	}
}

func (v *calendarView) deleteHabit(recording Recording) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, calendarRequestHabitMutation)
	return func() tea.Msg {
		err := v.vc.sdk.Habits().Delete(ctx, recording.ID)
		return habitMutationMsg{requestResult: newRequestResult(requestID, err), action: "Habit deleted"}
	}
}

// --- SDK type converters ---

func sdkCalendarToModel(c generated.Calendar) Calendar {
	return Calendar{ID: c.Id, Name: c.Name, Personal: c.Personal}
}

func sdkRecordingToModel(r generated.Recording) Recording {
	return Recording{
		ID: r.Id, Title: r.Title, AllDay: r.AllDay, Type: r.Type,
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
	start, end := dateRangeForMode(v.viewMode, v.now(), v.firstWeekDay)
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
