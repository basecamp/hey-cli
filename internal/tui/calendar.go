package tui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/models"
)

// --- Calendar messages ---

type calendarsLoadedMsg []models.Calendar

type recordingsLoadedMsg struct {
	recordings []models.Recording
}

type recordingDetailMsg struct {
	title string
	body  string
}

type identityLoadedMsg struct {
	firstWeekDay time.Weekday
}

type timeTrackCategoriesLoadedMsg struct {
	categories []generated.TimeTrackCategory
	err        error
}

type timeTrackCategorySavedMsg struct {
	summary string
	err     error
}

type habitMutationMsg struct {
	action string
	err    error
}

// --- Calendar section view ---

type calendarView struct {
	vc *viewContext

	calendars []models.Calendar
	calIndex  int

	viewMode     calendarViewMode
	firstWeekDay time.Weekday
	anchorDate   time.Time

	// Recordings split by type
	events []models.Recording
	todos  []models.Recording
	habits []models.Recording

	// Scrollable content viewport for the calendar views
	contentVP viewport.Model

	// Detail view
	topicViewport viewport.Model
	topicContent  string
	inThread      bool
	loading       bool

	timeTrackCategories *timeTrackCategoryManager
	habitForm           *habitForm
	habitIndex          int
	habitMutating       bool
	confirmHabitDelete  bool
	notice              string
}

func newCalendarView(vc *viewContext) *calendarView {
	return &calendarView{
		vc:            vc,
		anchorDate:    time.Now(),
		firstWeekDay:  time.Monday,
		topicViewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		contentVP:     viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
}

func (v *calendarView) Init() tea.Cmd {
	cmds := []tea.Cmd{v.fetchIdentity()}
	if len(v.calendars) == 0 {
		v.loading = true
		cmds = append(cmds, v.fetchCalendars())
	} else if v.calIndex < len(v.calendars) {
		v.loading = true
		cmds = append(cmds, v.fetchRecordings(v.calendars[v.calIndex].ID))
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
		v.loading = false
		v.calendars = []models.Calendar(msg)
		if len(v.calendars) > 0 {
			v.calIndex = 0
			v.loading = true
			return v.fetchRecordings(v.calendars[0].ID), true
		}
		return nil, true

	case recordingsLoadedMsg:
		v.loading = false
		v.events, v.todos, v.habits = splitRecordings(msg.recordings)
		v.normalizeHabitSelection()
		v.rebuildView()
		return nil, true

	case habitMutationMsg:
		v.loading = false
		v.habitMutating = false
		if msg.err != nil {
			if v.habitForm != nil {
				v.habitForm.saving = false
				v.habitForm.status = "Save failed: " + msg.err.Error()
				v.habitForm.isError = true
			} else {
				v.notice = "Delete failed: " + msg.err.Error()
			}
			return nil, true
		}
		v.habitForm = nil
		v.confirmHabitDelete = false
		v.notice = msg.action
		if v.calIndex >= 0 && v.calIndex < len(v.calendars) {
			v.loading = true
			return v.fetchRecordings(v.calendars[v.calIndex].ID), true
		}
		return nil, true

	case recordingDetailMsg:
		v.loading = false
		v.inThread = true
		v.topicContent = msg.body
		v.topicViewport.SetContent(v.topicContent)
		v.topicViewport.GotoTop()
		return nil, true

	case timeTrackCategoriesLoadedMsg:
		v.loading = false
		if v.timeTrackCategories == nil {
			return nil, true
		}
		if msg.err != nil {
			v.timeTrackCategories.status = "Could not load categories: " + msg.err.Error()
			return nil, true
		}
		v.timeTrackCategories.setCategories(msg.categories)
		return nil, true

	case timeTrackCategorySavedMsg:
		v.loading = false
		if v.timeTrackCategories == nil {
			return nil, true
		}
		if msg.err != nil {
			v.timeTrackCategories.status = msg.err.Error()
			return nil, true
		}
		v.timeTrackCategories.status = msg.summary
		v.loading = true
		return v.fetchTimeTrackCategories(), true
	}

	if v.habitForm != nil {
		return v.habitForm.update(msg), true
	}
	if v.inThread {
		var cmd tea.Cmd
		v.topicViewport, cmd = v.topicViewport.Update(msg)
		return cmd, cmd != nil
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
	if v.inThread {
		return v.topicViewport.View()
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
	if v.inThread {
		return nil
	}
	bindings := []helpBinding{{"v", v.viewMode.next().String() + " view"}, {"c", "time categories"}, {"a", "create habit"}}
	if len(v.manageableHabits()) > 0 {
		bindings = append(bindings, helpBinding{"[/]", "select habit"}, helpBinding{"e", "edit habit"})
		deleteLabel := "delete habit"
		if v.confirmHabitDelete {
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
		v.loading = true
		return v.fetchRecordings(v.calendars[v.calIndex].ID)
	}
	return nil
}

func (v *calendarView) SubnavRight() tea.Cmd {
	if v.calIndex < len(v.calendars)-1 {
		v.calIndex++
		v.loading = true
		return v.fetchRecordings(v.calendars[v.calIndex].ID)
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
	if v.inThread {
		var cmd tea.Cmd
		v.topicViewport, cmd = v.topicViewport.Update(msg)
		return cmd
	}

	if msg.String() != "x" {
		v.confirmHabitDelete = false
	}
	v.notice = ""
	switch msg.String() {
	case "c":
		v.timeTrackCategories = newTimeTrackCategoryManager()
		v.loading = true
		return v.fetchTimeTrackCategories()
	case "v":
		v.viewMode = v.viewMode.next()
		if v.calIndex >= 0 && v.calIndex < len(v.calendars) {
			v.loading = true
			return v.fetchRecordings(v.calendars[v.calIndex].ID)
		}
		v.rebuildView()
		return nil
	case "a":
		return v.startHabitForm(habitFormCreate, models.Recording{})
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
			if !v.confirmHabitDelete {
				v.confirmHabitDelete = true
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
	if v.loading {
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
			v.loading = true
			if mode == timeTrackCategoryCreate {
				return v.createTimeTrackCategory(title)
			}
			if selected != nil {
				return v.renameTimeTrackCategory(selected.Id, title)
			}
			v.loading = false
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
		v.loading = true
		return v.deleteTimeTrackCategory(selected.Id, selected.Title)
	default:
		manager.move(msg.Key())
		return nil
	}
}

func (v *calendarView) InThread() bool { return v.inThread }
func (v *calendarView) ExitThread()    { v.inThread = false }
func (v *calendarView) Loading() bool  { return v.loading }
func (v *calendarView) CapturingInput() bool {
	return v.timeTrackCategories != nil || v.habitForm != nil
}

func (v *calendarView) AccountSwitchBlocked() bool { return v.habitMutating }

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
	v.topicViewport.SetWidth(width)
	v.topicViewport.SetHeight(height)
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

	dayLabels := dayLabelsFromEvents(v.events)

	var content string
	switch v.viewMode {
	case viewDay:
		content = renderDayView(v.events, v.todos, v.habits, v.anchorDate, w, h)
	case viewWeek:
		content = renderWeekView(v.events, v.todos, v.habits, v.anchorDate, v.firstWeekDay, w, h, dayLabels)
	case viewYear:
		content = renderYearView(v.events, v.anchorDate, v.firstWeekDay, w, h, dayLabels)
	}

	v.contentVP.SetContent(content)

	// For year view, scroll to the current week
	if v.viewMode == viewYear {
		today := time.Now()
		gridStart := weekStartDate(time.Date(v.anchorDate.Year(), 1, 1, 0, 0, 0, 0, v.anchorDate.Location()), v.firstWeekDay)
		weeksToToday := int(today.Sub(gridStart).Hours()/24) / 7
		// Center today's week in the viewport (+2 for header rows)
		offset := max(weeksToToday-h/2+2, 0)
		v.contentVP.SetYOffset(offset)
	} else {
		v.contentVP.GotoTop()
	}
}

func (v *calendarView) manageableHabits() []models.Recording {
	seen := make(map[int64]bool)
	habits := make([]models.Recording, 0, len(v.habits))
	for _, habit := range v.habits {
		if habit.ID <= 0 || seen[habit.ID] {
			continue
		}
		seen[habit.ID] = true
		habits = append(habits, habit)
	}
	return habits
}

func (v *calendarView) selectedHabit() *models.Recording {
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

func (v *calendarView) startHabitForm(mode habitFormMode, recording models.Recording) tea.Cmd {
	v.confirmHabitDelete = false
	v.habitForm = newHabitForm(mode, recording, v.vc.styles)
	v.habitForm.resize(v.vc.width, v.vc.height)
	return v.habitForm.init()
}

func (v *calendarView) saveHabit() tea.Cmd {
	form := v.habitForm
	name, icon, color, days, _ := form.values()
	params := hey.HabitParams{Name: name, Icon: icon, Color: color, Days: days}
	v.habitMutating = true
	v.loading = true
	return func() tea.Msg {
		var err error
		action := "Habit created"
		if form.mode == habitFormCreate {
			_, err = v.vc.sdk.Habits().Create(v.vc.ctx, params)
		} else {
			action = "Habit updated"
			_, err = v.vc.sdk.Habits().Update(v.vc.ctx, form.habitID, params)
		}
		return habitMutationMsg{action: action, err: err}
	}
}

func (v *calendarView) deleteHabit(recording models.Recording) tea.Cmd {
	v.habitMutating = true
	v.loading = true
	return func() tea.Msg {
		err := v.vc.sdk.Habits().Delete(v.vc.ctx, recording.ID)
		return habitMutationMsg{action: "Habit deleted", err: err}
	}
}

// --- SDK type converters ---

func sdkCalendarToModel(c generated.Calendar) models.Calendar {
	return models.Calendar{
		ID: c.Id, Name: c.Name, Kind: c.Kind,
		Owned: c.Owned, Personal: c.Personal, External: c.External,
	}
}

func sdkRecordingToModel(r generated.Recording) models.Recording {
	return models.Recording{
		ID: r.Id, Title: r.Title, AllDay: r.AllDay, Recurring: r.Recurring,
		StartsAt: formatTimestamp(r.StartsAt), EndsAt: formatTimestamp(r.EndsAt),
		StartsAtTimeZone: r.StartsAtTimeZone, EndsAtTimeZone: r.EndsAtTimeZone,
		CreatedAt: formatTimestamp(r.CreatedAt), UpdatedAt: formatTimestamp(r.UpdatedAt),
		Type: r.Type, Content: r.Content, RemindersLabel: r.RemindersLabel,
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

func (v *calendarView) fetchCalendars() tea.Cmd {
	return func() tea.Msg {
		payload, err := v.vc.sdk.Calendars().List(v.vc.ctx)
		if err != nil {
			return errMsg{err}
		}
		if payload == nil {
			return calendarsLoadedMsg(nil)
		}
		calendars := make([]models.Calendar, 0, len(payload.Calendars))
		for _, cw := range payload.Calendars {
			calendars = append(calendars, sdkCalendarToModel(cw.Calendar))
		}
		return calendarsLoadedMsg(calendars)
	}
}

func (v *calendarView) fetchRecordings(calID int64) tea.Cmd {
	start, end := dateRangeForMode(v.viewMode, v.anchorDate, v.firstWeekDay)
	return func() tea.Msg {
		startsOn, endsOn := start.Format("2006-01-02"), end.Format("2006-01-02")
		resp, err := v.vc.sdk.Calendars().GetRecordings(v.vc.ctx, calID, &generated.GetCalendarRecordingsParams{
			StartsOn: &startsOn,
			EndsOn:   &endsOn,
		})
		if err != nil {
			return errMsg{err}
		}
		var all []models.Recording
		if resp != nil {
			for _, recs := range *resp {
				for _, r := range recs {
					all = append(all, sdkRecordingToModel(r))
				}
			}
		}
		return recordingsLoadedMsg{recordings: all}
	}
}

func (v *calendarView) fetchTimeTrackCategories() tea.Cmd {
	return func() tea.Msg {
		if v.vc.sdk == nil || v.vc.ctx == nil {
			return timeTrackCategoriesLoadedMsg{err: fmt.Errorf("time track categories are unavailable")}
		}
		categories, err := v.vc.sdk.TimeTracks().Categories(v.vc.ctx)
		return timeTrackCategoriesLoadedMsg{categories: categories, err: err}
	}
}

func (v *calendarView) createTimeTrackCategory(title string) tea.Cmd {
	return func() tea.Msg {
		err := v.vc.sdk.TimeTracks().CreateCategory(v.vc.ctx, title)
		return timeTrackCategorySavedMsg{summary: fmt.Sprintf("Created %q", title), err: err}
	}
}

func (v *calendarView) renameTimeTrackCategory(categoryID int64, title string) tea.Cmd {
	return func() tea.Msg {
		err := v.vc.sdk.TimeTracks().UpdateCategory(v.vc.ctx, categoryID, title)
		return timeTrackCategorySavedMsg{summary: fmt.Sprintf("Renamed category to %q", title), err: err}
	}
}

func (v *calendarView) deleteTimeTrackCategory(categoryID int64, title string) tea.Cmd {
	return func() tea.Msg {
		err := v.vc.sdk.TimeTracks().DeleteCategory(v.vc.ctx, categoryID)
		return timeTrackCategorySavedMsg{summary: fmt.Sprintf("Deleted %q", title), err: err}
	}
}
