package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/models"
)

// --- Async messages ---

type boxesLoadedMsg []models.Box

type postingsLoadedMsg struct {
	postings []models.Posting
}

type topicLoadedMsg struct {
	title   string
	entries []models.Entry
	images  [][]byte
}

type calendarsLoadedMsg []models.Calendar

type recordingsLoadedMsg struct {
	recordings []models.Recording
}

type journalDetailMsg struct {
	title  string
	body   string
	images [][]byte
}

type recordingDetailMsg struct {
	title string
	body  string
}

type errMsg struct{ err error } //nolint:errname // bubbletea convention

func (e errMsg) Error() string { return e.err.Error() }

type ctrlCResetMsg struct{}
type spinnerTickMsg struct{}

// --- Model ---

type model struct {
	width  int
	height int
	sdk    *hey.Client
	ctx    context.Context
	cancel context.CancelFunc
	styles styles
	help   helpBar

	// Navigation state
	section  section
	focus    focusRow
	inThread bool

	// Row 2 data
	boxes        []models.Box
	boxIndex     int
	calendars    []models.Calendar
	calIndex     int
	journalDates []string
	dateIndex    int

	// Content
	postingList   contentList
	recordingL    recordingList
	topicViewport viewport.Model
	topicContent  string

	// Loading & error
	loading      bool
	spinnerPhase float64
	err          error
	ctrlCOnce    bool
}

func newModel(sdk *hey.Client) model {
	s := newStyles()
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel stored, called on ctrl+c
	return model{
		sdk:           sdk,
		ctx:           ctx,
		cancel:        cancel,
		styles:        s,
		help:          newHelpBar(s),
		section:       sectionMail,
		focus:         rowContent,
		loading:       true,
		topicViewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.fetchBoxes(), spinnerTick())
}

// --- Update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.setWidth(msg.Width)
		m.updateHelpBindings()
		m.resizeContent()
		return m, nil

	case spinnerTickMsg:
		if m.loading {
			m.spinnerPhase += 0.15
			return m, spinnerTick()
		}
		return m, nil

	case ctrlCResetMsg:
		m.ctrlCOnce = false
		m.updateHelpBindings()
		return m, nil

	// --- Data loaded messages ---
	case boxesLoadedMsg:
		m.boxes = orderBoxes([]models.Box(msg))
		m.loading = false
		if len(m.boxes) > 0 {
			m.boxIndex = 0
			m.loading = true
			return m, tea.Batch(m.fetchPostings(m.boxes[0].ID), spinnerTick())
		}
		return m, nil

	case postingsLoadedMsg:
		m.loading = false
		m.postingList.setPostings(msg.postings)
		return m, nil

	case topicLoadedMsg:
		m.loading = false
		m.inThread = true
		m.topicContent = m.renderEntries(msg.entries)
		m.topicViewport.SetContent(m.topicContent)
		m.topicViewport.GotoTop()
		m.updateHelpBindings()
		m.resizeContent()
		var uploadCmds []tea.Cmd
		for i, imgData := range msg.images {
			imageID := i + 1
			cols, rows := imageDimensions(imgData, m.width-4)
			m.topicContent += "\n\n" + renderImagePlaceholder(imageID, cols, rows)
			m.topicViewport.SetContent(m.topicContent)
			seq := kittyUploadAndPlace(imgData, imageID, cols, rows)
			uploadCmds = append(uploadCmds, tea.Raw(seq))
		}
		if len(uploadCmds) > 0 {
			return m, tea.Batch(uploadCmds...)
		}
		return m, nil

	case calendarsLoadedMsg:
		m.loading = false
		m.calendars = []models.Calendar(msg)
		if len(m.calendars) > 0 {
			m.calIndex = 0
			m.loading = true
			return m, tea.Batch(m.fetchRecordings(m.calendars[0].ID), spinnerTick())
		}
		return m, nil

	case recordingsLoadedMsg:
		m.loading = false
		m.recordingL.setRecordings(msg.recordings)
		return m, nil

	case journalDetailMsg:
		m.loading = false
		m.inThread = true
		body := msg.body
		m.topicContent = body
		m.topicViewport.SetContent(m.topicContent)
		m.topicViewport.GotoTop()
		m.updateHelpBindings()
		m.resizeContent()
		var uploadCmds []tea.Cmd
		for i, imgData := range msg.images {
			imageID := i + 1
			cols, rows := imageDimensions(imgData, m.width-4)
			m.topicContent += "\n\n" + renderImagePlaceholder(imageID, cols, rows)
			m.topicViewport.SetContent(m.topicContent)
			seq := kittyUploadAndPlace(imgData, imageID, cols, rows)
			uploadCmds = append(uploadCmds, tea.Raw(seq))
		}
		if len(uploadCmds) > 0 {
			return m, tea.Batch(uploadCmds...)
		}
		return m, nil

	case recordingDetailMsg:
		m.loading = false
		m.inThread = true
		m.topicContent = msg.body
		m.topicViewport.SetContent(m.topicContent)
		m.topicViewport.GotoTop()
		m.updateHelpBindings()
		m.resizeContent()
		return m, nil

	case postingActionDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		if msg.removes {
			// Remove the posting from the list (move/trash/ignore actions)
			if m.section == sectionMail && m.postingList.cursor < len(m.postingList.postings) {
				idx := m.postingList.cursor
				m.postingList.postings = append(m.postingList.postings[:idx], m.postingList.postings[idx+1:]...)
				if m.postingList.cursor >= len(m.postingList.postings) && m.postingList.cursor > 0 {
					m.postingList.cursor--
				}
			}
		} else if msg.action == "marked as seen" {
			// Update the posting in-place
			if m.section == sectionMail && m.postingList.cursor < len(m.postingList.postings) {
				m.postingList.postings[m.postingList.cursor].Seen = true
			}
		}
		return m, nil

	case errMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	// Pass through to viewport if in thread
	if m.inThread {
		var cmd tea.Cmd
		m.topicViewport, cmd = m.topicViewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

// --- Key handling ---

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Double Ctrl+C to quit
	if key == "ctrl+c" {
		if m.ctrlCOnce {
			m.cancel()
			return m, tea.Quit
		}
		m.ctrlCOnce = true
		m.updateHelpBindings()
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return ctrlCResetMsg{} })
	}
	if m.ctrlCOnce {
		m.ctrlCOnce = false
		m.updateHelpBindings()
	}

	// Esc/q: back out of thread, or no-op at top level
	if msg.Key().Code == tea.KeyEscape || key == "q" {
		if m.inThread {
			m.inThread = false
			m.updateHelpBindings()
			m.resizeContent()
			return m, nil
		}
		return m, nil
	}

	// Tab / Shift+Tab: cycle focus rows
	if msg.Key().Code == tea.KeyTab {
		if msg.Key().Mod == tea.ModShift {
			m.focus = (m.focus + 2) % 3 // reverse
		} else {
			m.focus = (m.focus + 1) % 3
		}
		m.updateHelpBindings()
		return m, nil
	}

	// Global shortcuts: Shift+letter for sections
	if sec := sectionForShortcut(key); sec >= 0 {
		return m.switchSection(sec)
	}

	// Box shortcuts (only when in Mail section)
	if m.section == sectionMail {
		if idx := boxForShortcut(key, m.boxes); idx >= 0 && idx != m.boxIndex {
			m.boxIndex = idx
			m.loading = true
			return m, tea.Batch(m.fetchPostings(m.boxes[idx].ID), spinnerTick())
		}
	}

	// Route by focus row
	switch m.focus {
	case rowSection:
		return m.handleSectionKeys(msg)
	case rowSubnav:
		return m.handleSubnavKeys(msg)
	case rowContent:
		return m.handleContentKeys(msg)
	}

	return m, nil
}

func (m model) switchSection(sec section) (tea.Model, tea.Cmd) {
	if sec == m.section {
		return m, nil
	}
	m.section = sec
	m.inThread = false
	m.updateHelpBindings()

	switch sec {
	case sectionMail:
		if len(m.boxes) == 0 {
			m.loading = true
			return m, tea.Batch(m.fetchBoxes(), spinnerTick())
		}
		// Re-fetch current box
		if m.boxIndex < len(m.boxes) {
			m.loading = true
			return m, tea.Batch(m.fetchPostings(m.boxes[m.boxIndex].ID), spinnerTick())
		}
	case sectionCalendar:
		if len(m.calendars) == 0 {
			m.loading = true
			return m, tea.Batch(m.fetchCalendars(), spinnerTick())
		}
		if m.calIndex < len(m.calendars) {
			m.loading = true
			return m, tea.Batch(m.fetchRecordings(m.calendars[m.calIndex].ID), spinnerTick())
		}
	case sectionJournal:
		m.journalDates = generateJournalDates(30)
		m.dateIndex = len(m.journalDates) - 1 // select today
		m.loading = true
		return m, tea.Batch(m.fetchJournalEntry(m.journalDates[m.dateIndex]), spinnerTick())
	}
	return m, nil
}

func (m model) handleSectionKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Key().Code {
	case tea.KeyLeft:
		if m.section > 0 {
			return m.switchSection(m.section - 1)
		}
	case tea.KeyRight:
		if m.section < sectionJournal {
			return m.switchSection(m.section + 1)
		}
	}
	return m, nil
}

func (m model) handleSubnavKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Key().Code {
	case tea.KeyLeft:
		return m.subnavLeft()
	case tea.KeyRight:
		return m.subnavRight()
	}
	return m, nil
}

func (m model) subnavLeft() (tea.Model, tea.Cmd) {
	switch m.section {
	case sectionMail:
		if m.boxIndex > 0 {
			m.boxIndex--
			m.loading = true
			return m, tea.Batch(m.fetchPostings(m.boxes[m.boxIndex].ID), spinnerTick())
		}
	case sectionCalendar:
		if m.calIndex > 0 {
			m.calIndex--
			m.loading = true
			return m, tea.Batch(m.fetchRecordings(m.calendars[m.calIndex].ID), spinnerTick())
		}
	case sectionJournal:
		if m.dateIndex > 0 {
			m.dateIndex--
			m.loading = true
			return m, tea.Batch(m.fetchJournalEntry(m.journalDates[m.dateIndex]), spinnerTick())
		}
	}
	return m, nil
}

func (m model) subnavRight() (tea.Model, tea.Cmd) {
	switch m.section {
	case sectionMail:
		if m.boxIndex < len(m.boxes)-1 {
			m.boxIndex++
			m.loading = true
			return m, tea.Batch(m.fetchPostings(m.boxes[m.boxIndex].ID), spinnerTick())
		}
	case sectionCalendar:
		if m.calIndex < len(m.calendars)-1 {
			m.calIndex++
			m.loading = true
			return m, tea.Batch(m.fetchRecordings(m.calendars[m.calIndex].ID), spinnerTick())
		}
	case sectionJournal:
		if m.dateIndex < len(m.journalDates)-1 {
			m.dateIndex++
			m.loading = true
			return m, tea.Batch(m.fetchJournalEntry(m.journalDates[m.dateIndex]), spinnerTick())
		}
	}
	return m, nil
}

func (m model) handleContentKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.inThread {
		var cmd tea.Cmd
		m.topicViewport, cmd = m.topicViewport.Update(msg)
		return m, cmd
	}

	switch msg.Key().Code {
	case tea.KeyUp:
		switch m.section {
		case sectionMail:
			m.postingList.moveUp()
		case sectionCalendar:
			m.recordingL.moveUp()
		case sectionJournal:
			var cmd tea.Cmd
			m.topicViewport, cmd = m.topicViewport.Update(msg)
			return m, cmd
		}
	case tea.KeyDown:
		switch m.section {
		case sectionMail:
			m.postingList.moveDown()
		case sectionCalendar:
			m.recordingL.moveDown()
		case sectionJournal:
			var cmd tea.Cmd
			m.topicViewport, cmd = m.topicViewport.Update(msg)
			return m, cmd
		}
	case tea.KeyEnter:
		return m.openSelected()
	default:
		// Posting action shortcuts (Mail section only)
		if m.section == sectionMail {
			return m.handlePostingAction(msg.String())
		}
	}
	return m, nil
}

func (m model) openSelected() (tea.Model, tea.Cmd) {
	switch m.section {
	case sectionMail:
		p := m.postingList.selectedPosting()
		if p != nil {
			topicID := p.ResolveTopicID()
			if topicID == 0 {
				topicID = p.ID
			}
			m.loading = true
			return m, tea.Batch(m.fetchTopic(topicID, p.Summary), spinnerTick())
		}
	case sectionCalendar:
		r := m.recordingL.selectedRecording()
		if r != nil {
			return m, m.showRecordingDetail(*r)
		}
	case sectionJournal:
		// Journal content shows directly, no nested enter
	}
	return m, nil
}

// --- Posting actions (Imbox shortcuts) ---

type postingActionDoneMsg struct {
	action  string
	removes bool // true if the posting should be removed from the list
	err     error
}

func (m model) handlePostingAction(key string) (tea.Model, tea.Cmd) {
	p := m.postingList.selectedPosting()
	if p == nil {
		return m, nil
	}

	switch key {
	case "l": // Reply Later — stays in list
		return m, m.doPostingAction("moved to Reply Later", false, func() error {
			return m.sdk.Postings().MoveToReplyLater(m.ctx, p.ID)
		})
	case "a": // Set Aside — removes from Imbox
		return m, m.doPostingAction("moved to Set Aside", true, func() error {
			return m.sdk.Postings().MoveToSetAside(m.ctx, p.ID)
		})
	case "e": // Mark seen — stays in list
		return m, m.doPostingAction("marked as seen", false, func() error {
			return m.sdk.Postings().MarkSeen(m.ctx, []int64{p.ID})
		})
	case "d": // Move to Feed — removes from current box
		return m, m.doPostingAction("moved to The Feed", true, func() error {
			return m.sdk.Postings().MoveToFeed(m.ctx, p.ID)
		})
	case "p": // Paper Trail — removes from current box
		return m, m.doPostingAction("moved to Paper Trail", true, func() error {
			return m.sdk.Postings().MoveToPaperTrail(m.ctx, p.ID)
		})
	case "t": // Trash — removes from current box
		return m, m.doPostingAction("moved to Trash", true, func() error {
			return m.sdk.Postings().MoveToTrash(m.ctx, p.ID)
		})
	case "-": // Ignore — removes from current box
		return m, m.doPostingAction("ignored", true, func() error {
			return m.sdk.Postings().Ignore(m.ctx, p.ID)
		})
	case "r": // Reply (open thread)
		topicID := p.ResolveTopicID()
		if topicID == 0 {
			topicID = p.ID
		}
		m.loading = true
		return m, tea.Batch(m.fetchTopic(topicID, p.Summary), spinnerTick())
	case "f": // Forward (open thread — full forward TBD)
		topicID := p.ResolveTopicID()
		if topicID == 0 {
			topicID = p.ID
		}
		m.loading = true
		return m, tea.Batch(m.fetchTopic(topicID, p.Summary), spinnerTick())
	}
	// b (label), n (collection), v (workflow) — not yet wired to API
	return m, nil
}

func (m model) doPostingAction(label string, removes bool, fn func() error) tea.Cmd {
	return func() tea.Msg {
		err := fn()
		return postingActionDoneMsg{action: label, removes: removes, err: err}
	}
}

// --- View ---

const headerHeight = 6 // rule + row1 + rule + row2 + rule + (gap absorbed)

func (m model) View() tea.View {
	var b strings.Builder

	// Header
	b.WriteString(renderHeader(&m))
	b.WriteString("\n")

	// Content
	if m.err != nil {
		b.WriteString(errorView(m.err.Error(), m.width))
	} else if m.loading {
		contentH := m.height - headerHeight - m.help.height() - 3
		if contentH < 1 {
			contentH = 1
		}
		b.WriteString(loadingView(m.width, contentH, m.spinnerPhase))
	} else if m.inThread {
		b.WriteString(m.topicViewport.View())
	} else {
		switch m.section {
		case sectionMail:
			b.WriteString(m.postingList.view())
		case sectionCalendar:
			b.WriteString(m.recordingL.view())
		case sectionJournal:
			b.WriteString(m.topicViewport.View())
		}
	}

	// Pad content to push separator + help to the bottom
	contentLines := strings.Count(b.String(), "\n")
	helpView := m.help.view()
	helpH := 0
	if helpView != "" {
		helpH = strings.Count(helpView, "\n") + 1
	}
	footerH := 1 + helpH // 1 for separator rule
	padLines := m.height - contentLines - footerH - 1
	for range max(padLines, 0) {
		b.WriteString("\n")
	}

	// Bottom separator + help (always at screen bottom)
	b.WriteString(renderRule(m.width, ""))
	if helpView != "" {
		b.WriteString("\n" + helpView)
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m *model) resizeContent() {
	helpH := m.help.height()
	contentH := m.height - headerHeight - helpH - 3 // 3 for bottom separator + gaps
	if contentH < 1 {
		contentH = 1
	}
	m.postingList.setSize(m.width, contentH)
	m.recordingL.setSize(m.width, contentH)
	m.topicViewport.SetWidth(m.width)
	m.topicViewport.SetHeight(contentH)
}

func (m *model) updateHelpBindings() {
	quitHint := helpBinding{"ctrl+c ctrl+c", "quit"}
	if m.ctrlCOnce {
		quitHint = helpBinding{"ctrl+c", "press again to quit"}
	}

	var bindings []helpBinding

	if m.inThread {
		bindings = []helpBinding{
			{"↑↓", "scroll"},
			{"esc/q", "back"},
			quitHint,
		}
	} else {
		switch m.focus {
		case rowSection:
			bindings = []helpBinding{
				{"←→", "section"},
				{"tab", "next row"},
				{"shift+M/C/J", "jump"},
				quitHint,
			}
		case rowSubnav:
			bindings = []helpBinding{
				{"←→", "switch"},
				{"tab", "next row"},
				{"shift+tab", "prev row"},
				quitHint,
			}
		case rowContent:
			bindings = []helpBinding{
				{"↑↓", "navigate"},
				{"enter", "open"},
				{"tab", "next row"},
				{"shift+tab", "prev row"},
				quitHint,
			}
			if m.section == sectionMail {
				bindings = append(bindings,
					helpBinding{"r", "reply"},
					helpBinding{"f", "forward"},
					helpBinding{"e", "seen"},
					helpBinding{"l", "reply later"},
					helpBinding{"a", "set aside"},
					helpBinding{"d", "feed"},
					helpBinding{"p", "paper trail"},
					helpBinding{"t", "trash"},
					helpBinding{"-", "ignore"},
				)
			}
		}
	}
	m.help.setBindings(bindings)
}

// --- Entry rendering (for topic/thread view) ---

func (m model) renderEntries(entries []models.Entry) string {
	var b strings.Builder
	sepWidth := max(m.width-4, 40)
	sep := m.styles.separator.Render(strings.Repeat("─", sepWidth))

	for i, e := range entries {
		if i > 0 {
			fmt.Fprintf(&b, "%s\n", sep)
		}

		from := e.Creator.Name
		if from == "" {
			from = e.Creator.EmailAddress
		}
		if e.AlternativeSenderName != "" {
			from = e.AlternativeSenderName
		}

		date := ""
		if len(e.CreatedAt) >= 16 {
			date = e.CreatedAt[:16]
		}

		fmt.Fprintf(&b, "%s  %s\n", m.styles.entryFrom.Render(from), m.styles.entryDate.Render(date))
		if e.Summary != "" {
			fmt.Fprintf(&b, "%s\n", e.Summary)
		}
		if e.Body != "" {
			fmt.Fprintf(&b, "\n%s\n", m.styles.entryBody.Render(htmlToText(e.Body)))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// --- Journal date generation ---

func generateJournalDates(n int) []string {
	dates := make([]string, n)
	today := time.Now()
	for i := range n {
		d := today.AddDate(0, 0, -(n - 1 - i))
		dates[i] = d.Format("2006-01-02")
	}
	return dates
}

// --- SDK type converters ---

func sdkBoxToModel(b generated.Box) models.Box {
	return models.Box{
		ID:   b.Id,
		Kind: b.Kind,
		Name: b.Name,
	}
}

func sdkPostingToModel(p generated.Posting) models.Posting {
	return models.Posting{
		ID:                    p.Id,
		CreatedAt:             formatTimestamp(p.CreatedAt),
		UpdatedAt:             formatTimestamp(p.UpdatedAt),
		Kind:                  p.Kind,
		Name:                  p.Name,
		Seen:                  p.Seen,
		Bundled:               p.Bundled,
		Muted:                 p.Muted,
		Summary:               p.Summary,
		EntryKind:             p.EntryKind,
		AppURL:                p.AppUrl,
		AlternativeSenderName: p.AlternativeSenderName,
		VisibleEntryCount:     p.VisibleEntryCount,
		Extenzions:            sdkExtenzionsToModel(p.Extenzions),
		Creator: models.Contact{
			ID:           p.Creator.Id,
			Name:         p.Creator.Name,
			EmailAddress: p.Creator.EmailAddress,
		},
	}
}

func sdkExtenzionsToModel(exts []generated.Extenzion) []models.Extenzion {
	if len(exts) == 0 {
		return nil
	}
	result := make([]models.Extenzion, len(exts))
	for i, e := range exts {
		result[i] = models.Extenzion{ID: e.Id, Name: e.Name}
	}
	return result
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

// --- Async fetch commands ---

func (m model) fetchBoxes() tea.Cmd {
	return func() tea.Msg {
		time.Sleep(12 * time.Second) // intentional delay for testing loading animation
		result, err := m.sdk.Boxes().List(m.ctx)
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

func (m model) fetchPostings(boxID int64) tea.Cmd {
	return func() tea.Msg {
		resp, err := m.sdk.Boxes().Get(m.ctx, boxID, nil)
		if err != nil {
			return errMsg{err}
		}
		postings := make([]models.Posting, 0, len(resp.Postings))
		for _, p := range resp.Postings {
			postings = append(postings, sdkPostingToModel(p))
		}
		return postingsLoadedMsg{postings: postings}
	}
}

func (m model) fetchTopic(topicID int64, title string) tea.Cmd {
	return func() tea.Msg {
		resp, err := m.sdk.GetHTML(m.ctx, fmt.Sprintf("/topics/%d/entries", topicID))
		if err != nil {
			return errMsg{err}
		}
		entries := htmlutil.ParseTopicEntriesHTML(string(resp.Data))

		var images [][]byte
		for _, e := range entries {
			for _, imgURL := range extractImageURLs(e.Body) {
				var data []byte
				if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
					data = fetchImageData(imgURL)
				} else {
					sdkResp, getErr := m.sdk.Get(m.ctx, imgURL)
					if getErr == nil && sdkResp != nil {
						data = sdkResp.Data
					}
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
		payload, err := m.sdk.Calendars().List(m.ctx)
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

func (m model) fetchRecordings(calID int64) tea.Cmd {
	return func() tea.Msg {
		now := time.Now()
		resp, err := m.sdk.Calendars().GetRecordings(m.ctx, calID, &generated.GetCalendarRecordingsParams{
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

func (m model) fetchJournalEntry(date string) tea.Cmd {
	return func() tea.Msg {
		content, err := m.sdk.Journal().GetContent(m.ctx, date)
		if err != nil || content == "" {
			return journalDetailMsg{title: date, body: "(empty)"}
		}

		body := htmlToText(content)

		var images [][]byte
		for _, imgURL := range extractImageURLs(content) {
			var data []byte
			if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
				data = fetchImageData(imgURL)
			} else {
				sdkResp, getErr := m.sdk.Get(m.ctx, imgURL)
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
func Run(sdk *hey.Client) error {
	p := tea.NewProgram(newModel(sdk))
	_, err := p.Run()
	return err
}
