package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// --- The track that is running ---

// OngoingTrack is the time track HEY has running, for everything that says so: the time
// tracking menu, and the day's now row.
//
// Category is there because HEY serves it, and it is empty for every track that is still
// going. That is HEY's shape rather than a gap here — the only write that files a track
// under a category is the same one that ends it — so the field fills in the day a running
// track can carry one.
type OngoingTrack struct {
	ID        int64
	Category  string
	StartedAt time.Time
}

// Elapsed is how long the track has been going, to the second. A clock that has gone
// backwards reads as nothing rather than as time owed.
func (t OngoingTrack) Elapsed(now time.Time) time.Duration {
	if t.StartedAt.IsZero() {
		return 0
	}
	return max(now.Sub(t.StartedAt).Truncate(time.Second), 0)
}

func ongoingTrackFrom(recording *generated.Recording) *OngoingTrack {
	if recording == nil || recording.Id == 0 {
		return nil
	}
	return &OngoingTrack{
		ID:        recording.Id,
		Category:  terminal.SanitizeLine(recording.Category),
		StartedAt: recording.StartsAt.Local(),
	}
}

// formatElapsed is a stretch of time as a stopwatch reads it, seconds and all — running or
// finished, on the badge, in the menu, down the log and in its total.
//
// Always hours, even for a track a few seconds old. A stopwatch that drops the hours until it
// has one is ambiguous at a glance — 0:45 is either three quarters of a minute or three quarters
// of an hour — and it changes width on the hour, which on the day's clock row would move a badge
// that has to fit the space the time leaves it.
func formatElapsed(d time.Duration) string {
	seconds := int(max(d, 0).Seconds())
	return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

// --- The time tracking menu ---

type timeTrackAction int

const (
	// timeTrackToggle is one row rather than two: starting and stopping are the same
	// place on the menu, and which one it offers is whether a track is running.
	timeTrackToggle timeTrackAction = iota
	timeTrackTracked
	timeTrackCategories
)

var timeTrackActions = []timeTrackAction{timeTrackToggle, timeTrackTracked, timeTrackCategories}

// timeTrackMenu is what c opens over the calendar: what is being tracked, the key that
// starts or stops it, and the way through to the tracked time and the categories.
type timeTrackMenu struct {
	cursor  int
	status  string
	isError bool
}

func newTimeTrackMenu() *timeTrackMenu {
	return &timeTrackMenu{}
}

func (m *timeTrackMenu) highlighted() timeTrackAction {
	return timeTrackActions[min(max(m.cursor, 0), len(timeTrackActions)-1)]
}

func (m *timeTrackMenu) moveCursor(msg tea.KeyPressMsg) {
	m.cursor = stepListCursor(m.cursor, len(timeTrackActions), msg)
}

func (m *timeTrackMenu) notice(text string) {
	m.status = text
	m.isError = false
}

func (m *timeTrackMenu) problem(text string) {
	m.status = text
	m.isError = true
}

func timeTrackActionLabel(action timeTrackAction, running bool) string {
	switch action {
	case timeTrackToggle:
		if running {
			return "Stop tracking"
		}
		return "Start tracking"
	case timeTrackTracked:
		return "Tracked time"
	case timeTrackCategories:
		return "Categories"
	}
	return ""
}

func (m *timeTrackMenu) draw(base string, track *OngoingTrack, known bool, now time.Time, width, height int, use24 bool) string {
	contentWidth := modalContentWidth(width)

	rows := []string{m.trackingLine(track, known, contentWidth, now, use24), ""}
	for i, action := range timeTrackActions {
		label := truncateToWidth(timeTrackActionLabel(action, track != nil), max(contentWidth-2, 1))
		if i == m.cursor {
			rows = append(rows, lipgloss.NewStyle().Foreground(colorActive).Bold(true).Render("› "+label))
		} else {
			rows = append(rows, "  "+label)
		}
	}
	if m.status != "" {
		style := styleMuted
		if m.isError {
			style = lipgloss.NewStyle().Foreground(colorError)
		}
		rows = append(rows, "", style.Render(truncateToWidth(terminal.SanitizeLine(m.status), contentWidth)))
	}

	return overlayModal(base, modalFrame("Time tracking", strings.Join(rows, "\n"), width), width, height)
}

// trackingLine is the reason to open the menu while a track is running: what it is filed
// under, when it started, and how long it has been going.
func (m *timeTrackMenu) trackingLine(track *OngoingTrack, known bool, contentWidth int, now time.Time, use24 bool) string {
	if track == nil {
		if !known {
			return styleMuted.Render("Reading what is being tracked…")
		}
		return styleMuted.Render("Nothing is being tracked")
	}

	what := "Tracking"
	if track.Category != "" {
		what += " " + track.Category
	}
	line := fmt.Sprintf("● %s since %s · %s", what, clockTime(track.StartedAt, use24), formatElapsed(track.Elapsed(now)))
	return lipgloss.NewStyle().Foreground(colorActive).Bold(true).
		Render(truncateToWidth(line, contentWidth))
}

func (m *timeTrackMenu) helpBindings(running bool) []helpBinding {
	return []helpBinding{
		{"↑↓", "choose"},
		{"enter", strings.ToLower(timeTrackActionLabel(m.highlighted(), running))},
		{"esc", "close"},
	}
}

// --- Tracked time ---

// trackedTime is one finished track as the screen lists it. Only finished ones exist here:
// HEY's tracked time is its completed tracks, and the one still running is on the menu with a
// stopwatch beside it.
//
// How long it took is computed rather than served: the index carries the two instants to the
// second, so a track a few seconds long reads as the time it took instead of as no time at all.
type trackedTime struct {
	ID       int64
	Category string
	Notes    string
	StartsAt time.Time
	EndsAt   time.Time
}

func (t trackedTime) Duration() time.Duration {
	return max(t.EndsAt.Sub(t.StartsAt), 0)
}

// trackedTimeFrom describes one completed track. Category is a title, plain, and empty for a
// track filed under nothing — the screen says that in its own voice rather than repeating a
// word back as though it were somebody's category.
//
// The text is kept as HEY served it, for the reason a contact's is: these fields go back
// through the edit form, and sanitizing them here would rewrite a note on an unrelated save.
// Every row sanitizes what it draws.
func trackedTimeFrom(recording generated.Recording) trackedTime {
	return trackedTime{
		ID:       recording.Id,
		Category: recording.Category,
		Notes:    recording.Notes,
		StartsAt: recording.StartsAt.Local(),
		EndsAt:   recording.EndsAt.Local(),
	}
}

// trackedTimeScreen is the tracked time itself: not a modal over the calendar but the
// screen, since a column of days and hours has nothing to gain from a border and every row
// to lose to one.
//
// It reads a page and grows downwards as the reader scrolls, like every other list here. The
// categories arrive with every page — HEY serves the whole list for its own filter — so the
// edit form can offer them without a read of its own.
type trackedTimeScreen struct {
	tracks     []trackedTime
	categories []generated.TimeTrackCategory
	cursor     int
	loaded     bool
	// complete is whether the last page has arrived, which is the difference between "this is
	// everything" and "this is what has been read so far".
	complete bool
	status   string
	// notice is a page that did not arrive, which the screen says out loud: a list that
	// quietly stopped growing looks like a list that ended.
	notice           string
	confirmingDelete bool

	contentVP viewport.Model
	width     int
	use24     bool
}

func newTrackedTimeScreen(use24 bool) *trackedTimeScreen {
	return &trackedTimeScreen{
		contentVP: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		use24:     use24,
	}
}

// setTracks is the first page, or the list read again from the top: the cursor goes back to
// the newest track and the window with it.
func (s *trackedTimeScreen) setTracks(tracks []trackedTime, categories []generated.TimeTrackCategory) {
	s.tracks = tracks
	s.setCategories(categories)
	s.cursor = 0
	s.loaded = true
	s.status = ""
	s.notice = ""
	s.confirmingDelete = false
	s.contentVP.SetContent(strings.Join(s.rows(), "\n"))
	s.contentVP.GotoTop()
}

// growTracks adds the page below, leaving the cursor and the scroll where the reader left them.
func (s *trackedTimeScreen) growTracks(tracks []trackedTime, categories []generated.TimeTrackCategory) {
	s.tracks = append(s.tracks, tracks...)
	s.setCategories(categories)
	s.rebuild()
}

// setCategories keeps the last non-empty list: a page of a calendar with no categories yet
// carries none, and dropping them would empty the edit form's choices mid-scroll.
func (s *trackedTimeScreen) setCategories(categories []generated.TimeTrackCategory) {
	if len(categories) > 0 {
		s.categories = categories
	}
}

func (s *trackedTimeScreen) selected() *trackedTime {
	if s.cursor < 0 || s.cursor >= len(s.tracks) {
		return nil
	}
	return &s.tracks[s.cursor]
}

func (s *trackedTimeScreen) moveCursor(delta int) {
	if len(s.tracks) == 0 {
		return
	}
	s.cursor = min(max(s.cursor+delta, 0), len(s.tracks)-1)
	s.rebuild()
}

// wantsMore is when the page below is worth reading: the cursor has come within
// loadMoreThreshold of the last row, or the reader can already see the end of the list —
// which is also true of a first page too short to fill the window.
func (s *trackedTimeScreen) wantsMore() bool {
	if !s.loaded {
		return false
	}
	return s.contentVP.AtBottom() || len(s.tracks)-s.cursor <= loadMoreThreshold
}

func (s *trackedTimeScreen) resize(width, height int) {
	s.width = width
	s.contentVP.SetWidth(width)
	s.contentVP.SetHeight(max(height-trackedTimeChromeRows, 1))
	s.rebuild()
}

// trackedTimeChromeRows is the header, the line naming the window and the blank line under
// them, which the rows scroll beneath.
const trackedTimeChromeRows = 3

func (s *trackedTimeScreen) rebuild() {
	s.contentVP.SetContent(strings.Join(s.rows(), "\n"))
	s.followCursor()
}

// followCursor scrolls only as far as it must to keep the highlighted row on screen. Every
// track is one row, so the cursor is its own line number.
func (s *trackedTimeScreen) followCursor() {
	height := max(s.contentVP.Height(), 1)
	switch {
	case s.cursor < s.contentVP.YOffset():
		s.contentVP.SetYOffset(s.cursor)
	case s.cursor >= s.contentVP.YOffset()+height:
		s.contentVP.SetYOffset(s.cursor - height + 1)
	}
}

func (s *trackedTimeScreen) rows() []string {
	switch {
	case s.status != "":
		return []string{lipgloss.NewStyle().Foreground(colorError).Render(truncateToWidth(s.status, max(s.width, 1)))}
	case !s.loaded:
		return []string{styleMuted.Render("Reading tracked time…")}
	case len(s.tracks) == 0:
		return []string{styleMuted.Render("Nothing tracked yet")}
	}

	rows := make([]string, 0, len(s.tracks))
	for i, track := range s.tracks {
		rows = append(rows, s.row(track, i == s.cursor))
	}
	return rows
}

// row is one track: the day it started, the hours it ran, how long that took, what it is filed
// under and whatever was written on it. The columns are colored to be read down; the row under
// the cursor is the accent throughout, because a row picked out is read across.
func (s *trackedTimeScreen) row(track trackedTime, selected bool) string {
	strong := lipgloss.NewStyle().Foreground(colorBright)
	soft := styleMuted
	accent := lipgloss.NewStyle().Foreground(colorActive)
	marker := "  "
	if selected {
		_, cursor := cursorStyles()
		strong, soft, accent = cursor, cursor, cursor
		marker = cursor.Render("›") + " "
	}

	category, categoryStyle := truncateToWidth(terminal.SanitizeLine(track.Category), 20), strong
	if category == "" {
		category, categoryStyle = "Uncategorized", soft
	}

	line := marker +
		strong.Render(fmt.Sprintf("%-16s", track.StartsAt.Format("Mon Jan 2 2006"))) + "  " +
		soft.Render(fmt.Sprintf("%s – %s", clockTime(track.StartsAt, s.use24), clockTime(track.EndsAt, s.use24))) + "  " +
		accent.Render(fmt.Sprintf("%-9s", formatElapsed(track.Duration()))) + " " +
		categoryStyle.Render(category)
	if notes := terminal.SanitizeLine(track.Notes); notes != "" {
		line += "  " + soft.Render(notes)
	}
	return truncateToWidth(line, max(s.width, 1))
}

func (s *trackedTimeScreen) view() string {
	header := hintedSectionHeader("Tracked time", "esc back", max(s.width, 1))
	return header + "\n" + s.summaryLine() + "\n\n" + s.contentVP.View()
}

// summaryLine is how much there is of it, and it only claims to be everything once the last
// page has arrived — a list still growing says what it has read so far. It counts rather than
// totalling: hours summed across a partial list would be a number that keeps changing.
func (s *trackedTimeScreen) summaryLine() string {
	line := "Reading tracked time"
	switch {
	case s.confirmingDelete:
		line = "Press x again to delete this track"
	case !s.loaded:
	case s.complete:
		line = fmt.Sprintf("Everything tracked · %d %s", len(s.tracks), trackNoun(len(s.tracks)))
	default:
		line = fmt.Sprintf("%d %s so far · more below", len(s.tracks), trackNoun(len(s.tracks)))
	}
	if s.notice != "" {
		line += " · " + terminal.SanitizeLine(s.notice)
	}

	style := styleMuted
	if s.notice != "" || s.confirmingDelete {
		style = lipgloss.NewStyle().Foreground(colorError)
	}
	return style.Render(truncateToWidth(line, max(s.width, 1)))
}

func trackNoun(count int) string {
	if count == 1 {
		return "track"
	}
	return "tracks"
}

func (s *trackedTimeScreen) helpBindings() []helpBinding {
	bindings := []helpBinding{{"↑↓", "choose"}}
	if s.selected() != nil {
		label := "delete"
		if s.confirmingDelete {
			label = "confirm delete"
		}
		bindings = append(bindings, helpBinding{"e", "edit"}, helpBinding{"x", label})
	}
	return append(bindings, helpBinding{"esc", "back"})
}

// --- Categories ---

type timeTrackCategoryEditMode int

const (
	timeTrackCategoryBrowse timeTrackCategoryEditMode = iota
	timeTrackCategoryCreate
	timeTrackCategoryRename
)

// timeTrackCategoryManager is the categories a finished track can be filed under. It stays
// a modal, over the menu it was opened from: naming and renaming a category is a detour
// from tracking time rather than a screen anyone works in.
type timeTrackCategoryManager struct {
	categories       []generated.TimeTrackCategory
	cursor           int
	mode             timeTrackCategoryEditMode
	input            textinput.Model
	status           string
	confirmingDelete bool
}

func newTimeTrackCategoryManager() *timeTrackCategoryManager {
	input := newTextInput()
	input.Prompt = ""
	input.Placeholder = "Category title…"
	return &timeTrackCategoryManager{input: input}
}

func (m *timeTrackCategoryManager) selected() *generated.TimeTrackCategory {
	if m.cursor < 0 || m.cursor >= len(m.categories) {
		return nil
	}
	return &m.categories[m.cursor]
}

func (m *timeTrackCategoryManager) setCategories(categories []generated.TimeTrackCategory) {
	selectedID := int64(0)
	if selected := m.selected(); selected != nil {
		selectedID = selected.Id
	}
	m.categories = categories
	m.cursor = min(m.cursor, max(len(categories)-1, 0))
	for i := range categories {
		if categories[i].Id == selectedID {
			m.cursor = i
			break
		}
	}
	m.confirmingDelete = false
}

func (m *timeTrackCategoryManager) startCreate() tea.Cmd {
	m.mode = timeTrackCategoryCreate
	m.confirmingDelete = false
	m.status = ""
	m.input.SetValue("")
	return m.input.Focus()
}

func (m *timeTrackCategoryManager) startRename() tea.Cmd {
	selected := m.selected()
	if selected == nil {
		m.status = "Choose a category to rename"
		return nil
	}
	m.mode = timeTrackCategoryRename
	m.confirmingDelete = false
	m.status = ""
	m.input.SetValue(terminal.SanitizeLine(selected.Title))
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m *timeTrackCategoryManager) cancelEdit() {
	m.mode = timeTrackCategoryBrowse
	m.status = ""
	m.input.Blur()
}

func (m *timeTrackCategoryManager) title() (string, bool) {
	title := strings.TrimSpace(m.input.Value())
	if title == "" {
		m.status = "Enter a category title"
		return "", false
	}
	return title, true
}

func (m *timeTrackCategoryManager) update(msg tea.Msg) tea.Cmd {
	if m.mode == timeTrackCategoryBrowse {
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *timeTrackCategoryManager) moveCursor(msg tea.KeyPressMsg) {
	m.cursor = stepListCursor(m.cursor, len(m.categories), msg)
	m.confirmingDelete = false
}

func (m *timeTrackCategoryManager) draw(base string, width, height int) string {
	contentWidth := modalContentWidth(width)

	if m.mode != timeTrackCategoryBrowse {
		title := "New category"
		if m.mode == timeTrackCategoryRename {
			title = "Rename category"
		}
		m.input.SetWidth(max(contentWidth-9, 10))
		body := styleMuted.Render("Title: ") + m.input.View()
		if m.status != "" {
			body += "\n\n" + lipgloss.NewStyle().Foreground(colorError).Render(truncateToWidth(m.status, contentWidth))
		}
		return overlayModal(base, modalFrame(title, body, width), width, height)
	}

	visible := modalContentRows(height)
	if m.status != "" || m.confirmingDelete {
		visible = max(visible-3, 1)
	}

	var rows []string
	start, end := modalListWindow(len(m.categories), m.cursor, visible)
	for i := start; i < end; i++ {
		label := truncateToWidth(terminal.SanitizeLine(m.categories[i].Title), max(contentWidth-2, 1))
		if i == m.cursor {
			rows = append(rows, lipgloss.NewStyle().Foreground(colorActive).Bold(true).Render("› "+label))
		} else {
			rows = append(rows, "  "+label)
		}
	}

	body := strings.Join(rows, "\n")
	if len(m.categories) == 0 {
		body = styleMuted.Render("No categories yet")
	}
	body += "\n\n" + styleMuted.Render(truncateToWidth("Categories file a track once it has stopped.", contentWidth))
	switch {
	case m.confirmingDelete:
		body += "\n" + lipgloss.NewStyle().Foreground(colorError).
			Render(truncateToWidth("Press x again to delete it. Its tracked time stays, uncategorized.", contentWidth))
	case m.status != "":
		body += "\n" + styleMuted.Render(truncateToWidth(terminal.SanitizeLine(m.status), contentWidth))
	}
	return overlayModal(base, modalFrame("Time track categories", body, width), width, height)
}

func (m *timeTrackCategoryManager) helpBindings() []helpBinding {
	if m.mode != timeTrackCategoryBrowse {
		return []helpBinding{{"enter", "save"}, {"esc", "cancel"}}
	}
	return []helpBinding{
		{"↑↓", "choose"},
		{"a", "new category"},
		{"e", "rename"},
		{"x", "delete"},
		{"esc", "back"},
	}
}
