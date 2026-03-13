package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/client"
	"github.com/basecamp/hey-cli/internal/models"
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

type errMsg struct{ err error } //nolint:errname // bubbletea convention: Msg types end in Msg, not Error

func (e errMsg) Error() string { return e.err.Error() }

type model struct {
	state  viewState
	width  int
	height int
	sdk    *hey.Client
	legacy *client.Client // kept for gap operations (topic entries, journal fallback, relative URL fetches)
	ctx    context.Context
	cancel context.CancelFunc
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

	loading bool
	err     error
	lastKey string // debug: last key event received
}

func newModel(sdk *hey.Client, legacy *client.Client) model {
	s := newStyles()
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel is stored in model and called on ctrl+c
	return model{
		state:     viewBoxes,
		sdk:       sdk,
		legacy:    legacy,
		ctx:       ctx,
		cancel:    cancel,
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
			m.cancel()
			return m, tea.Quit
		}
		if msg.Key().Code == tea.KeyTab {
			switch m.state { //nolint:exhaustive // only tab-navigable views need handling
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
		if len(msg.images) == 0 {
			m.detail.setContent(msg.title, msg.body)
			m.state = viewJournalDetail
			return m, nil
		}
		var body strings.Builder
		body.Grow(len(msg.body) + len(msg.images)*128)
		body.WriteString(msg.body)
		var uploadCmds []tea.Cmd
		for i, imgData := range msg.images {
			imageID := i + 1
			cols, rows := imageDimensions(imgData, m.width-4)
			body.WriteString("\n\n" + renderImagePlaceholder(imageID, cols, rows))
			seq := kittyUploadAndPlace(imgData, imageID, cols, rows)
			uploadCmds = append(uploadCmds, tea.Raw(seq))
		}
		m.detail.setContent(msg.title, body.String())
		m.state = viewJournalDetail
		return m, tea.Batch(uploadCmds...)

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

// --- SDK type to models type converters ---

func sdkBoxToModel(b generated.Box) models.Box {
	return models.Box{
		ID:   b.Id,
		Kind: b.Kind,
		Name: b.Name,
	}
}

func sdkPostingToModel(p generated.Posting) models.Posting {
	return models.Posting{
		ID:        p.Id,
		CreatedAt: formatTimestamp(p.CreatedAt),
		UpdatedAt: formatTimestamp(p.UpdatedAt),
		Kind:      p.Kind,
		Seen:      p.Seen,
		Bundled:   p.Bundled,
		Muted:     p.Muted,
		Summary:   p.Summary,
		EntryKind: p.EntryKind,
		AppURL:    p.AppUrl,
		Creator: models.Contact{
			ID:           p.Creator.Id,
			Name:         p.Creator.Name,
			EmailAddress: p.Creator.EmailAddress,
		},
	}
}

func sdkCalendarToModel(c generated.Calendar) models.Calendar {
	return models.Calendar{
		ID:       c.Id,
		Name:     c.Name,
		Kind:     c.Kind,
		Owned:    c.Owned,
		Personal: c.Personal,
		External: c.External,
	}
}

func sdkRecordingToModel(r generated.Recording) models.Recording {
	return models.Recording{
		ID:               r.Id,
		Title:            r.Title,
		AllDay:           r.AllDay,
		Recurring:        r.Recurring,
		StartsAt:         formatTimestamp(r.StartsAt),
		EndsAt:           formatTimestamp(r.EndsAt),
		StartsAtTimeZone: r.StartsAtTimeZone,
		EndsAtTimeZone:   r.EndsAtTimeZone,
		CreatedAt:        formatTimestamp(r.CreatedAt),
		UpdatedAt:        formatTimestamp(r.UpdatedAt),
		Type:             r.Type,
		Content:          r.Content,
		RemindersLabel:   r.RemindersLabel,
	}
}

func formatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format("2006-01-02T15:04:05Z")
}

// --- Async data fetching commands (using SDK) ---

func (m model) fetchBoxes() tea.Cmd {
	return func() tea.Msg {
		ctx := m.ctx
		result, err := m.sdk.Boxes().List(ctx)
		if err != nil {
			return errMsg{err}
		}
		var sdkBoxes []generated.Box
		if result != nil {
			sdkBoxes = *result
		}
		boxes := make([]models.Box, len(sdkBoxes))
		for i, b := range sdkBoxes {
			boxes[i] = sdkBoxToModel(b)
		}
		return boxesLoadedMsg(boxes)
	}
}

func (m model) fetchBox(boxID int64) tea.Cmd {
	return func() tea.Msg {
		ctx := m.ctx
		resp, err := m.sdk.Boxes().Get(ctx, boxID, nil)
		if err != nil {
			return errMsg{err}
		}

		box := models.Box{
			ID:   resp.Id,
			Kind: resp.Kind,
			Name: resp.Name,
		}

		postings := make([]models.Posting, 0, len(resp.Postings))
		for _, p := range resp.Postings {
			postings = append(postings, sdkPostingToModel(p))
		}

		return boxLoadedMsg{box: box, postings: postings}
	}
}

// fetchTopic uses the legacy client — SDK entries lack body content (Gap 1).
func (m model) fetchTopic(topicID int64, title string) tea.Cmd {
	return func() tea.Msg {
		if m.legacy == nil {
			return errMsg{fmt.Errorf("topic view requires legacy client (SDK entries lack body content)")}
		}
		entries, err := m.legacy.GetTopicEntries(topicID)
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
					data, _ = m.legacy.Get(imgURL)
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
		ctx := m.ctx
		payload, err := m.sdk.Calendars().List(ctx)
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

func (m model) fetchCalendar(cal models.Calendar) tea.Cmd {
	return func() tea.Msg {
		ctx := m.ctx
		now := time.Now()
		startsOn := now.Format("2006-01-02")
		endsOn := now.AddDate(0, 0, 30).Format("2006-01-02")

		resp, err := m.sdk.Calendars().GetRecordings(ctx, cal.ID, &generated.GetCalendarRecordingsParams{
			StartsOn: startsOn,
			EndsOn:   endsOn,
		})
		if err != nil {
			return errMsg{err}
		}

		// Convert SDK CalendarRecordingsResponse to models.RecordingsResponse
		recordings := make(models.RecordingsResponse)
		if resp == nil {
			resp = &generated.CalendarRecordingsResponse{}
		}
		for recType, recs := range *resp {
			modelRecs := make([]models.Recording, len(recs))
			for i, r := range recs {
				modelRecs[i] = sdkRecordingToModel(r)
			}
			recordings[recType] = modelRecs
		}

		return calendarLoadedMsg{calendar: cal, recordings: recordings}
	}
}

func (m model) fetchJournal() tea.Cmd {
	return func() tea.Msg {
		ctx := m.ctx
		payload, err := m.sdk.Calendars().List(ctx)
		if err != nil {
			return errMsg{err}
		}
		if payload == nil {
			return errMsg{fmt.Errorf("no calendars returned")}
		}

		var personalID int64
		for _, cw := range payload.Calendars {
			if cw.Calendar.Personal {
				personalID = cw.Calendar.Id
				break
			}
		}
		if personalID == 0 {
			return errMsg{fmt.Errorf("no personal calendar found")}
		}

		now := time.Now()
		startsOn := now.AddDate(-1, 0, 0).Format("2006-01-02")
		endsOn := now.Format("2006-01-02")
		resp, err := m.sdk.Calendars().GetRecordings(ctx, personalID, &generated.GetCalendarRecordingsParams{
			StartsOn: startsOn,
			EndsOn:   endsOn,
		})
		if err != nil {
			return errMsg{err}
		}

		var entries []models.Recording
		if resp == nil {
			resp = &generated.CalendarRecordingsResponse{}
		}
		for _, recs := range *resp {
			for _, r := range recs {
				if r.Type == "Calendar::JournalEntry" {
					entries = append(entries, sdkRecordingToModel(r))
				}
			}
		}
		return journalLoadedMsg(entries)
	}
}

func (m model) fetchJournalEntry(rec models.Recording) tea.Cmd {
	return func() tea.Msg {
		date := rec.StartsAt[:10]

		ctx := m.ctx
		entry, err := m.sdk.Journal().Get(ctx, date)
		if err != nil || entry == nil || entry.Content == "" {
			// Fall back to legacy HTML scrape, matching CLI journal read behavior
			if m.legacy != nil {
				legacyEntry, legacyErr := m.legacy.GetJournalEntry(date)
				if legacyErr == nil && legacyEntry.Body != "" {
					return journalDetailMsg{title: date, body: htmlToText(legacyEntry.Body)}
				}
			}
			body := strings.TrimSpace(rec.Content)
			if body == "" {
				body = "(empty)"
			}
			return journalDetailMsg{title: date, body: body}
		}

		body := htmlToText(entry.Content)

		// Download image data for Kitty unicode placeholder rendering
		var images [][]byte
		for _, imgURL := range extractImageURLs(entry.Content) {
			var data []byte
			if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
				data = fetchImageData(imgURL)
			} else {
				// Use SDK's Get for relative URLs
				sdkResp, getErr := m.sdk.Get(ctx, imgURL)
				if getErr == nil && sdkResp != nil {
					data = sdkResp.Data
				}
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

func fetchImageData(imgURL string) []byte {
	req, err := http.NewRequestWithContext(context.Background(), "GET", imgURL, nil)
	if err != nil {
		return nil
	}
	resp, err := imageHTTPClient.Do(req)
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
func Run(sdk *hey.Client, legacy *client.Client) error {
	p := tea.NewProgram(newModel(sdk, legacy))
	_, err := p.Run()
	return err
}
