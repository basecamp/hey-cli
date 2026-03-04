package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"hey-cli/internal/client"
	"hey-cli/internal/models"
)

type viewState int

const (
	viewBoxes viewState = iota
	viewBox
	viewTopic
	viewCalendars
	viewCalendar
	viewRecordingDetail
	viewJournal
	viewJournalDetail
)

// Async messages
type boxesLoadedMsg []models.Box

type boxLoadedMsg struct {
	box      models.Box
	postings []models.Posting
}

type topicLoadedMsg struct {
	title   string
	entries []models.Entry
	images  [][]byte
}

type calendarsLoadedMsg []models.Calendar
type journalLoadedMsg []models.Recording

type calendarLoadedMsg struct {
	calendar   models.Calendar
	recordings models.RecordingsResponse
}

type journalDetailMsg struct {
	title  string
	body   string
	images [][]byte // raw image data
}

type recordingDetailMsg struct {
	title string
	body  string
}

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

type model struct {
	state  viewState
	width  int
	height int
	client *client.Client
	styles styles

	boxes boxesModel
	box   boxModel
	topic topicModel

	calendars       calendarsModel
	calendar        calendarModel
	calendarsLoaded bool

	journal       journalModel
	journalLoaded bool

	detail detailModel

	loading  bool
	err      error
	lastKey  string // debug: last key event received
}

func newModel(c *client.Client) model {
	s := newStyles()
	return model{
		state:     viewBoxes,
		client:    c,
		styles:    s,
		boxes:     newBoxesModel(),
		box:       newBoxModel(),
		topic:     newTopicModel(s),
		calendars: newCalendarsModel(),
		calendar:  newCalendarModel(),
		journal:   newJournalModel(),
		detail:    newDetailModel(s),
	}
}

func (m model) Init() tea.Cmd {
	return m.fetchBoxes()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.boxes.setSize(msg.Width, msg.Height)
		m.box.setSize(msg.Width, msg.Height)
		m.topic.setSize(msg.Width, msg.Height)
		m.calendars.setSize(msg.Width, msg.Height)
		m.calendar.setSize(msg.Width, msg.Height)
		m.journal.setSize(msg.Width, msg.Height)
		m.detail.setSize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyPressMsg:
		m.lastKey = fmt.Sprintf("key=%q code=0x%x mod=%d", msg.String(), msg.Key().Code, msg.Key().Mod)
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if msg.Key().Code == tea.KeyTab {
			switch m.state {
			case viewBoxes:
				m.state = viewCalendars
				if !m.calendarsLoaded {
					m.loading = true
					return m, m.fetchCalendars()
				}
				return m, nil
			case viewCalendars:
				m.state = viewJournal
				if !m.journalLoaded {
					m.loading = true
					return m, m.fetchJournal()
				}
				return m, nil
			case viewJournal:
				m.state = viewBoxes
				return m, nil
			}
		}

	case boxesLoadedMsg:
		m.loading = false
		cmd := m.boxes.setItems([]models.Box(msg))
		return m, cmd

	case boxLoadedMsg:
		m.loading = false
		cmd := m.box.setItems(msg.box, msg.postings)
		m.state = viewBox
		return m, cmd

	case topicLoadedMsg:
		m.loading = false
		m.topic.setEntries(msg.title, msg.entries)
		m.state = viewTopic
		var uploadCmds []tea.Cmd
		for i, imgData := range msg.images {
			imageID := i + 1
			cols, rows := imageDimensions(imgData, m.width-4)
			m.topic.appendContent("\n\n" + renderImagePlaceholder(imageID, cols, rows))
			seq := kittyUploadAndPlace(imgData, imageID, cols, rows)
			uploadCmds = append(uploadCmds, tea.Raw(seq))
		}
		if len(uploadCmds) > 0 {
			return m, tea.Batch(uploadCmds...)
		}
		return m, nil

	case calendarsLoadedMsg:
		m.loading = false
		m.calendarsLoaded = true
		cmd := m.calendars.setItems([]models.Calendar(msg))
		return m, cmd

	case journalLoadedMsg:
		m.loading = false
		m.journalLoaded = true
		cmd := m.journal.setItems([]models.Recording(msg))
		return m, cmd

	case calendarLoadedMsg:
		m.loading = false
		cmd := m.calendar.setItems(msg.calendar, msg.recordings)
		m.state = viewCalendar
		return m, cmd

	case journalDetailMsg:
		m.loading = false
		body := msg.body
		var uploadCmds []tea.Cmd
		for i, imgData := range msg.images {
			imageID := i + 1
			cols, rows := imageDimensions(imgData, m.width-4)
			body += "\n\n" + renderImagePlaceholder(imageID, cols, rows)
			seq := kittyUploadAndPlace(imgData, imageID, cols, rows)
			uploadCmds = append(uploadCmds, tea.Raw(seq))
		}
		m.detail.setContent(msg.title, body)
		m.state = viewJournalDetail
		if len(uploadCmds) > 0 {
			return m, tea.Batch(uploadCmds...)
		}
		return m, nil

	case recordingDetailMsg:
		m.loading = false
		m.detail.setContent(msg.title, msg.body)
		m.state = viewRecordingDetail
		return m, nil

	case errMsg:
		m.loading = false
		m.err = msg.err
		return m, nil
	}

	var cmd tea.Cmd
	switch m.state {
	case viewBoxes:
		cmd = m.updateBoxes(msg)
	case viewBox:
		cmd = m.updateBox(msg)
	case viewTopic:
		cmd = m.updateTopic(msg)
	case viewCalendars:
		cmd = m.updateCalendars(msg)
	case viewCalendar:
		cmd = m.updateCalendar(msg)
	case viewRecordingDetail:
		cmd = m.updateRecordingDetail(msg)
	case viewJournal:
		cmd = m.updateJournal(msg)
	case viewJournalDetail:
		cmd = m.updateJournalDetail(msg)
	}
	return m, cmd
}

func (m *model) updateBoxes(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		if msg.Key().Code == tea.KeyEnter && m.boxes.list.FilterState() != list.Filtering {
			box := m.boxes.selectedBox()
			if box != nil {
				m.loading = true
				return m.fetchBox(box.ID)
			}
		}
	}

	var cmd tea.Cmd
	m.boxes, cmd = m.boxes.update(msg)
	return cmd
}

func (m *model) updateBox(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.Key().Code {
		case tea.KeyEscape, tea.KeyBackspace:
			if m.box.list.FilterState() == list.Unfiltered {
				m.state = viewBoxes
				return nil
			}
		case tea.KeyEnter:
			if m.box.list.FilterState() != list.Filtering {
				posting := m.box.selectedPosting()
				if posting != nil {
					topicID := posting.ResolveTopicID()
					if topicID == 0 {
						topicID = posting.ID
					}
					m.loading = true
					return m.fetchTopic(topicID, posting.Summary)
				}
			}
		}
	}

	var cmd tea.Cmd
	m.box, cmd = m.box.update(msg)
	return cmd
}

func (m *model) updateTopic(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.Key().Code {
		case tea.KeyEscape, tea.KeyBackspace:
			m.state = viewBox
			return nil
		default:
			if msg.String() == "q" {
				m.state = viewBox
				return nil
			}
		}
	}

	var cmd tea.Cmd
	m.topic, cmd = m.topic.update(msg)
	return cmd
}

func (m *model) updateCalendars(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		if msg.Key().Code == tea.KeyEnter && m.calendars.list.FilterState() != list.Filtering {
			cal := m.calendars.selectedCalendar()
			if cal != nil {
				m.loading = true
				return m.fetchCalendar(*cal)
			}
		}
	}

	var cmd tea.Cmd
	m.calendars, cmd = m.calendars.update(msg)
	return cmd
}

func (m *model) updateCalendar(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.Key().Code {
		case tea.KeyEscape, tea.KeyBackspace:
			if m.calendar.list.FilterState() == list.Unfiltered {
				m.state = viewCalendars
				return nil
			}
		case tea.KeyEnter:
			if m.calendar.list.FilterState() != list.Filtering {
				rec := m.calendar.selectedRecording()
				if rec != nil {
					return m.showRecordingDetail(*rec)
				}
			}
		}
	}

	var cmd tea.Cmd
	m.calendar, cmd = m.calendar.update(msg)
	return cmd
}

func (m *model) updateRecordingDetail(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.Key().Code {
		case tea.KeyEscape, tea.KeyBackspace:
			m.state = viewCalendar
			return nil
		default:
			if msg.String() == "q" {
				m.state = viewCalendar
				return nil
			}
		}
	}

	var cmd tea.Cmd
	m.detail, cmd = m.detail.update(msg)
	return cmd
}

func (m *model) updateJournal(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		if msg.Key().Code == tea.KeyEnter && m.journal.list.FilterState() != list.Filtering {
			rec := m.journal.selectedRecording()
			if rec != nil && len(rec.StartsAt) >= 10 {
				m.loading = true
				return m.fetchJournalEntry(*rec)
			}
		}
	}

	var cmd tea.Cmd
	m.journal, cmd = m.journal.update(msg)
	return cmd
}

func (m *model) updateJournalDetail(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.Key().Code {
		case tea.KeyEscape, tea.KeyBackspace:
			m.state = viewJournal
			return nil
		default:
			if msg.String() == "q" {
				m.state = viewJournal
				return nil
			}
		}
	}

	var cmd tea.Cmd
	m.detail, cmd = m.detail.update(msg)
	return cmd
}

func (m model) View() tea.View {
	var content string

	if m.err != nil {
		content = fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	} else if m.loading {
		content = "Loading..."
	} else {
		switch m.state {
		case viewBoxes:
			content = m.boxes.view()
		case viewBox:
			content = m.box.view()
		case viewTopic:
			content = m.topic.view()
		case viewCalendars:
			content = m.calendars.view()
		case viewCalendar:
			content = m.calendar.view()
		case viewRecordingDetail:
			content = m.detail.view()
		case viewJournal:
			content = m.journal.view()
		case viewJournalDetail:
			content = m.detail.view()
		}
	}

	if m.lastKey != "" {
		content = content + "\n\n[DEBUG] last key: " + m.lastKey
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// Async data fetching commands

func (m model) fetchBoxes() tea.Cmd {
	return func() tea.Msg {
		boxes, err := m.client.ListBoxes()
		if err != nil {
			return errMsg{err}
		}
		return boxesLoadedMsg(boxes)
	}
}

func (m model) fetchBox(boxID int) tea.Cmd {
	return func() tea.Msg {
		resp, err := m.client.GetBox(boxID)
		if err != nil {
			return errMsg{err}
		}

		var postings []models.Posting
		for _, raw := range resp.Postings {
			var p models.Posting
			if err := json.Unmarshal(raw, &p); err != nil {
				continue
			}
			postings = append(postings, p)
		}

		return boxLoadedMsg{box: resp.Box, postings: postings}
	}
}

func (m model) fetchTopic(topicID int, title string) tea.Cmd {
	return func() tea.Msg {
		entries, err := m.client.GetTopicEntries(topicID)
		if err != nil {
			return errMsg{err}
		}

		// Download image data from all entry bodies
		var images [][]byte
		for _, e := range entries {
			for _, imgURL := range extractImageURLs(e.Body) {
				var data []byte
				if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
					data = fetchImageData(imgURL)
				} else {
					data, _ = m.client.Get(imgURL)
				}
				if len(data) > 0 {
					images = append(images, data)
				}
			}
		}

		return topicLoadedMsg{title: title, entries: entries, images: images}
	}
}

func (m model) fetchCalendars() tea.Cmd {
	return func() tea.Msg {
		calendars, err := m.client.ListCalendars()
		if err != nil {
			return errMsg{err}
		}
		return calendarsLoadedMsg(calendars)
	}
}

func (m model) fetchCalendar(cal models.Calendar) tea.Cmd {
	return func() tea.Msg {
		now := time.Now()
		startsOn := now.Format("2006-01-02")
		endsOn := now.AddDate(0, 0, 30).Format("2006-01-02")
		recordings, err := m.client.GetCalendarRecordings(cal.ID, startsOn, endsOn)
		if err != nil {
			return errMsg{err}
		}
		return calendarLoadedMsg{calendar: cal, recordings: recordings}
	}
}

func (m model) fetchJournal() tea.Cmd {
	return func() tea.Msg {
		calendars, err := m.client.ListCalendars()
		if err != nil {
			return errMsg{err}
		}

		var personalID int
		for _, c := range calendars {
			if c.Personal {
				personalID = c.ID
				break
			}
		}
		if personalID == 0 {
			return errMsg{fmt.Errorf("no personal calendar found")}
		}

		now := time.Now()
		startsOn := now.AddDate(-1, 0, 0).Format("2006-01-02")
		endsOn := now.Format("2006-01-02")
		recordings, err := m.client.GetCalendarRecordings(personalID, startsOn, endsOn)
		if err != nil {
			return errMsg{err}
		}

		var entries []models.Recording
		for _, recs := range recordings {
			for _, r := range recs {
				if r.Type == "Calendar::JournalEntry" {
					entries = append(entries, r)
				}
			}
		}
		return journalLoadedMsg(entries)
	}
}

func (m model) fetchJournalEntry(rec models.Recording) tea.Cmd {
	return func() tea.Msg {
		date := rec.StartsAt[:10]

		entry, err := m.client.GetJournalEntry(date)
		if err != nil || entry.Body == "" {
			body := strings.TrimSpace(rec.Content)
			if body == "" {
				body = "(empty)"
			}
			return journalDetailMsg{title: date, body: body}
		}

		body := htmlToText(entry.Body)

		// Download image data for Kitty unicode placeholder rendering
		var images [][]byte
		for _, imgURL := range extractImageURLs(entry.Body) {
			var data []byte
			if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
				data = fetchImageData(imgURL)
			} else {
				data, _ = m.client.Get(imgURL)
			}
			if len(data) > 0 {
				images = append(images, data)
			}
		}

		return journalDetailMsg{title: date, body: body, images: images}
	}
}

func (m model) showRecordingDetail(rec models.Recording) tea.Cmd {
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

var imageHTTPClient = &http.Client{Timeout: 10 * time.Second}

func fetchImageData(url string) []byte {
	resp, err := imageHTTPClient.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	return data
}

// Run starts the TUI program.
func Run(c *client.Client) error {
	p := tea.NewProgram(newModel(c))
	_, err := p.Run()
	return err
}
