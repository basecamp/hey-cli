package tui

import (
	"fmt"
	"strings"
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

// --- Calendar section view ---

type calendarView struct {
	vc *viewContext

	calendars []models.Calendar
	calIndex  int

	recordingL    recordingList
	topicViewport viewport.Model
	topicContent  string
	inThread      bool
	loading       bool
}

func newCalendarView(vc *viewContext) *calendarView {
	return &calendarView{
		vc:            vc,
		topicViewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
}

func (v *calendarView) Init() tea.Cmd {
	if len(v.calendars) == 0 {
		v.loading = true
		return v.fetchCalendars()
	}
	if v.calIndex < len(v.calendars) {
		v.loading = true
		return v.fetchRecordings(v.calendars[v.calIndex].ID)
	}
	return nil
}

func (v *calendarView) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
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
		v.recordingL.setRecordings(msg.recordings)
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
	return v.recordingL.view()
}

func (v *calendarView) HelpBindings() []helpBinding { return nil }

func (v *calendarView) SubnavItems() ([]navItem, int, string, bool) {
	label := "Calendar"
	if v.calIndex >= 0 && v.calIndex < len(v.calendars) {
		label = v.calendars[v.calIndex].Name
	}
	return calendarNavItems(v.calendars), v.calIndex, label, false
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

	switch msg.Key().Code {
	case tea.KeyUp:
		v.recordingL.moveUp()
	case tea.KeyDown:
		v.recordingL.moveDown()
	case tea.KeyEnter:
		r := v.recordingL.selectedRecording()
		if r != nil {
			return v.showRecordingDetail(*r)
		}
	}
	return nil
}

func (v *calendarView) InThread() bool  { return v.inThread }
func (v *calendarView) ExitThread()     { v.inThread = false }
func (v *calendarView) Loading() bool   { return v.loading }

func (v *calendarView) Resize(width, height int) {
	v.recordingL.setSize(width, height)
	v.topicViewport.SetWidth(width)
	v.topicViewport.SetHeight(height)
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
	}
}

// --- Fetch commands ---

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
	return func() tea.Msg {
		now := time.Now()
		resp, err := v.vc.sdk.Calendars().GetRecordings(v.vc.ctx, calID, &generated.GetCalendarRecordingsParams{
			StartsOn: now.Format("2006-01-02"),
			EndsOn:   now.AddDate(0, 0, 30).Format("2006-01-02"),
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

func (v *calendarView) showRecordingDetail(rec models.Recording) tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder

		if rec.Title != "" {
			fmt.Fprintf(&b, "%s\n\n", rec.Title)
		}
		if rec.AllDay {
			fmt.Fprintf(&b, "All day\n")
		} else {
			if len(rec.StartsAt) >= 16 {
				fmt.Fprintf(&b, "Starts: %s\n", rec.StartsAt[:16])
			}
			if len(rec.EndsAt) >= 16 {
				fmt.Fprintf(&b, "Ends:   %s\n", rec.EndsAt[:16])
			}
		}
		if rec.StartsAtTimeZone != "" {
			fmt.Fprintf(&b, "Timezone: %s\n", rec.StartsAtTimeZone)
		}
		if rec.Recurring {
			fmt.Fprintf(&b, "Recurring: yes\n")
		}
		if rec.RemindersLabel != "" {
			fmt.Fprintf(&b, "Reminders: %s\n", rec.RemindersLabel)
		}
		if rec.Content != "" {
			fmt.Fprintf(&b, "\n%s\n", rec.Content)
		}

		title := rec.Title
		if title == "" && len(rec.StartsAt) >= 10 {
			title = rec.StartsAt[:10]
		}
		return recordingDetailMsg{title: title, body: b.String()}
	}
}
