package tui

import (
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

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
		v.rebuildView()
		return nil, true

	case recordingDetailMsg:
		v.loading = false
		v.inThread = true
		v.topicContent = msg.body
		v.topicViewport.SetContent(v.topicContent)
		v.topicViewport.GotoTop()
		return nil, true
	}

	if v.inThread {
		var cmd tea.Cmd
		v.topicViewport, cmd = v.topicViewport.Update(msg)
		return cmd, cmd != nil
	}

	return nil, false
}

func (v *calendarView) View() string {
	if v.inThread {
		return v.topicViewport.View()
	}
	return v.contentVP.View()
}

func (v *calendarView) HelpBindings() []helpBinding {
	return []helpBinding{
		{"v", v.viewMode.next().String() + " view"},
	}
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
	if v.inThread {
		var cmd tea.Cmd
		v.topicViewport, cmd = v.topicViewport.Update(msg)
		return cmd
	}

	switch msg.String() {
	case "v":
		v.viewMode = v.viewMode.next()
		if v.calIndex >= 0 && v.calIndex < len(v.calendars) {
			v.loading = true
			return v.fetchRecordings(v.calendars[v.calIndex].ID)
		}
		v.rebuildView()
		return nil
	}

	// Delegate scrolling to the content viewport
	var cmd tea.Cmd
	v.contentVP, cmd = v.contentVP.Update(msg)
	return cmd
}

func (v *calendarView) InThread() bool { return v.inThread }
func (v *calendarView) ExitThread()    { v.inThread = false }
func (v *calendarView) Loading() bool  { return v.loading }

func (v *calendarView) Resize(width, height int) {
	v.contentVP.SetWidth(width)
	v.contentVP.SetHeight(height)
	v.topicViewport.SetWidth(width)
	v.topicViewport.SetHeight(height)
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
		return identityLoadedMsg{firstWeekDay: time.Weekday(identity.FirstWeekDay)}
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
		resp, err := v.vc.sdk.Calendars().GetRecordings(v.vc.ctx, calID, &generated.GetCalendarRecordingsParams{
			StartsOn: start.Format("2006-01-02"),
			EndsOn:   end.Format("2006-01-02"),
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
