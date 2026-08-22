package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/terminal"
)

// --- Screener messages ---

type screenerPendingLoadedMsg struct {
	requestID uint64
	rows      []screenerRow
	count     int
	nextPage  string
	err       error
}

// screenerPendingRefreshedMsg is the top of the queue read again after someone arrived in
// or left The Screener while it was open. Its own lane, so it can never be taken for a
// page the user asked for, and the rows land under the cursor rather than replacing it.
type screenerPendingRefreshedMsg struct {
	requestID uint64
	rows      []screenerRow
	count     int
	nextPage  string
	err       error
}

type screenerScreenedLoadedMsg struct {
	requestID uint64
	rows      []screenerRow
	nextPage  string
	err       error
}

// screenerRowsAppendedMsg is the page below the one on screen in either pane, read because
// the reader scrolled towards the bottom of it. count is only answered for the queue.
type screenerRowsAppendedMsg struct {
	requestID uint64
	tab       screenerTab
	rows      []screenerRow
	count     int
	nextPage  string
	err       error
}

type screenerDecisionDoneMsg struct {
	clearanceID int64
	name        string
	status      string
	err         error
}

type screenerClearedMsg struct{ err error }

// screenerClosedMsg asks the app to put the mail view back on screen.
type screenerClosedMsg struct{}

// --- Screener panes ---

type screenerTab int

const (
	screenerPendingTab screenerTab = iota
	screenerHistoryTab
)

var screenerTabItems = []navItem{
	{label: "The Screener"},
	{label: "Screener History"},
}

// screenerRow is one sender in either pane: someone waiting to be screened, or
// someone already screened in or out.
type screenerRow struct {
	id       int64
	name     string
	email    string
	subject  string // subject of what they sent, or the address they write from
	summary  string // excerpt of what they sent
	trailing string // when they wrote, or the decision and when it was made
}

type screenerPane struct {
	rows   []screenerRow
	cursor int
	scroll int
	paging listPaging
	loaded bool
}

func (p *screenerPane) setRows(rows []screenerRow, nextPage string) {
	p.rows = rows
	p.cursor = 0
	p.scroll = 0
	p.loaded = true
	p.paging.read(screenerRowIDs(rows), nextPage)
}

// growRows adds the page below the one at the bottom of the pane, skipping anyone it
// already shows: a sender whose place in the ordering changed between two reads would
// otherwise turn up twice.
func (p *screenerPane) growRows(more []screenerRow, nextPage string) {
	grown := p.rows
	shown := screenerRowIDs(p.rows)
	for _, row := range more {
		if _, alreadyShown := shown[row.id]; !alreadyShown {
			grown = append(grown, row)
		}
	}
	p.keepPlaceIn(grown)
	p.paging.grew(len(more), nextPage)
}

// refreshHead replaces the top page of the pane with a newly read one and leaves the pages
// below it as they were read. A sender who has left the top page goes with it rather than
// sinking into the rows underneath.
func (p *screenerPane) refreshHead(head []screenerRow, nextPage string) {
	fresh := screenerRowIDs(head)
	refreshed := append([]screenerRow(nil), head...)
	for _, row := range p.rows {
		_, wasInHead := p.paging.headIDs[row.id]
		_, isInHead := fresh[row.id]
		if !wasInHead && !isInHead {
			refreshed = append(refreshed, row)
		}
	}
	p.keepPlaceIn(refreshed)
	p.paging.refreshed(fresh, nextPage)
}

func screenerRowIDs(rows []screenerRow) map[int64]struct{} {
	ids := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		ids[row.id] = struct{}{}
	}
	return ids
}

// keepPlaceIn puts the pane on a newly assembled set of rows while someone is working
// through it: the cursor stays on the sender it was on, and the window stays where it was.
// A sender who has been dealt with elsewhere takes the cursor with them.
func (p *screenerPane) keepPlaceIn(rows []screenerRow) {
	var cursorID int64
	if row := p.selected(); row != nil {
		cursorID = row.id
	}

	p.rows = rows
	p.loaded = true
	p.cursor = 0
	for index := range p.rows {
		if p.rows[index].id == cursorID {
			p.cursor = index
			break
		}
	}
	p.scroll = min(p.scroll, max(len(p.rows)-1, 0))
}

func (p *screenerPane) selected() *screenerRow {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return nil
	}
	return &p.rows[p.cursor]
}

// remove takes a sender the pane has finished with out of it. The window comes with them:
// screening off the bottom of a queue the reader has scrolled down would otherwise leave
// the window past the last row, drawing nothing at all where the rows were.
func (p *screenerPane) remove(id int64) {
	for index, row := range p.rows {
		if row.id != id {
			continue
		}
		p.rows = append(p.rows[:index], p.rows[index+1:]...)
		if p.cursor >= len(p.rows) && p.cursor > 0 {
			p.cursor--
		}
		p.scroll = min(p.scroll, p.cursor)
		return
	}
}

func (p *screenerPane) moveUp(visible int) {
	if p.cursor > 0 {
		p.cursor--
		p.ensureVisible(visible)
	}
}

func (p *screenerPane) moveDown(visible int) {
	if p.cursor < len(p.rows)-1 {
		p.cursor++
		p.ensureVisible(visible)
	}
}

func (p *screenerPane) ensureVisible(visible int) {
	if p.cursor < p.scroll {
		p.scroll = p.cursor
	}
	if p.cursor >= p.scroll+visible {
		p.scroll = p.cursor - visible + 1
	}
}

// --- Screener view ---

// screenerView is the full-screen Screener, opened with ctrl+s from the mail list.
// It captures every key while it is open, so it is a place you are in rather than a
// picker layered over the mail list.
type screenerView struct {
	vc *viewContext

	tab     screenerTab
	pending screenerPane
	history screenerPane

	pendingCount    int
	confirmingClear bool
	emphaticNo      bool // undocumented Shift+F toggle: "No" becomes "Fuck no!"
	notice          string
	loading         bool
	requestID       uint64
	liveRequestID   uint64 // identifies the only live re-read allowed to update the queue
	moreRequestID   uint64 // identifies the only page-below read allowed to grow a pane
	mutations       int
}

func newScreenerView(vc *viewContext) *screenerView {
	return &screenerView{vc: vc}
}

func (v *screenerView) Init() tea.Cmd {
	v.notice = ""
	v.confirmingClear = false
	v.tab = screenerPendingTab
	return v.requestPending()
}

// Restyle is a no-op: the screener keeps plain rows and styles them on every View.
func (v *screenerView) Restyle() {}

func (v *screenerView) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case screenerPendingLoadedMsg:
		if msg.requestID != v.requestID {
			return nil, true
		}
		v.loading = false
		if msg.err != nil {
			v.notice = errorNotice("Could not load The Screener", msg.err)
			return nil, true
		}
		v.pendingCount = msg.count
		v.pending.setRows(msg.rows, msg.nextPage)
		return v.loadMoreRows(), true

	case screenerPendingRefreshedMsg:
		if msg.requestID != v.liveRequestID || v.tab != screenerPendingTab {
			return nil, true
		}
		if msg.err != nil {
			v.notice = errorNotice("Could not refresh The Screener", msg.err)
			return nil, true
		}
		v.pendingCount = msg.count
		v.pending.refreshHead(msg.rows, msg.nextPage)
		v.pending.ensureVisible(v.visibleRows())
		return nil, true

	case screenerScreenedLoadedMsg:
		if msg.requestID != v.requestID {
			return nil, true
		}
		v.loading = false
		if msg.err != nil {
			v.notice = errorNotice("Could not load Screener History", msg.err)
			return nil, true
		}
		v.history.setRows(msg.rows, msg.nextPage)
		return v.loadMoreRows(), true

	case screenerRowsAppendedMsg:
		if msg.requestID != v.moreRequestID || msg.tab != v.tab {
			return nil, true
		}
		pane := v.pane()
		pane.paging.loading = false
		if msg.err != nil {
			v.notice = errorNotice("Could not load more senders", msg.err)
			return nil, true
		}
		if msg.tab == screenerPendingTab {
			v.pendingCount = msg.count
		}
		pane.growRows(msg.rows, msg.nextPage)
		return v.loadMoreRows(), true

	case screenerDecisionDoneMsg:
		if v.mutations > 0 {
			v.mutations--
		}
		if msg.err != nil {
			v.notice = errorNotice("Could not screen "+msg.name, msg.err)
			return nil, true
		}
		v.pending.remove(msg.clearanceID)
		v.pendingCount = max(v.pendingCount-1, 0)
		v.history.loaded = false
		v.notice = msg.name + " " + screenedVerb(msg.status)
		// A sender being dealt with can uncover the bottom of the queue, so the senders
		// behind them come up rather than leaving an empty pane with a count over it.
		return v.loadMoreRows(), true

	case screenerClearedMsg:
		if v.mutations > 0 {
			v.mutations--
		}
		if msg.err != nil {
			v.notice = errorNotice("Could not clear The Screener", msg.err)
			return nil, true
		}
		v.pending.setRows(nil, "")
		v.pendingCount = 0
		v.notice = "The Screener is clearing. Everyone waiting will be asked about again on their next email."
		return nil, true
	}
	return nil, false
}

func (v *screenerView) View() string {
	if v.confirmingClear {
		return v.clearConfirmationView()
	}

	var b strings.Builder
	if v.notice != "" {
		b.WriteString(v.vc.styles.title.Render(truncateStr(v.notice, max(v.vc.width, 1))) + "\n")
	}
	b.WriteString(v.explanation() + "\n")

	pane := v.pane()
	if len(pane.rows) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render("  " + v.emptyMessage()))
		return b.String()
	}
	b.WriteString(renderScreenerRows(pane, v.visibleRows(), v.vc.width))
	if pane.paging.loading {
		b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render("  Loading more…"))
	}
	return b.String()
}

// CapturingInput keeps every key inside the Screener while it is open.
func (v *screenerView) CapturingInput() bool { return true }

func (v *screenerView) AccountSwitchBlocked() bool { return v.mutations > 0 }

func (v *screenerView) HelpBindings() []helpBinding {
	if v.confirmingClear {
		return []helpBinding{
			{"y", "clear all unscreened email"},
			{"n/esc", "cancel"},
		}
	}
	bindings := []helpBinding{{"↑↓", "navigate"}}
	if v.tab == screenerPendingTab {
		bindings = append(bindings,
			helpBinding{key: v.screenQuestion()},
			helpBinding{"tab", "screener history"},
		)
	} else {
		bindings = append(bindings, helpBinding{"tab", "the screener"})
	}
	bindings = append(bindings, helpBinding{"X", "clear all"})
	return append(bindings, helpBinding{"esc/q", "back to mail"})
}

// screenQuestion renders the screening prompt for the help bar: the question
// in chrome, then Yes and No as keys with their hotkey letters underlined.
func (v *screenerView) screenQuestion() string {
	question := v.vc.styles.helpDesc.Render("Want to get emails from them?")
	yes := renderNavLabel("Yes", "Y", v.vc.styles.helpKey)
	noLabel := "No"
	if v.emphaticNo {
		noLabel = "Fuck no!"
	}
	no := renderNavLabel(noLabel, "N", v.vc.styles.helpKey)
	return question + " " + yes + " " + no
}

func (v *screenerView) SubnavItems() ([]navItem, int, string, bool) {
	label := "The Screener"
	if v.tab == screenerHistoryTab {
		label = "Screener History"
	} else if v.pendingCount > 0 {
		label = fmt.Sprintf("The Screener (%d)", v.pendingCount)
	}
	return screenerTabItems, int(v.tab), label, true
}

func (v *screenerView) SubnavLeft() tea.Cmd  { return v.switchTab(screenerPendingTab) }
func (v *screenerView) SubnavRight() tea.Cmd { return v.switchTab(screenerHistoryTab) }

func (v *screenerView) HandleContentKey(msg tea.KeyPressMsg) tea.Cmd {
	if v.confirmingClear {
		return v.handleClearConfirmationKey(msg)
	}

	key := msg.String()
	if key == "F" {
		v.emphaticNo = !v.emphaticNo
		return nil
	}
	switch msg.Key().Code {
	case tea.KeyEscape:
		return v.close()
	case tea.KeyTab:
		return v.switchTab(v.otherTab())
	case tea.KeyUp:
		v.pane().moveUp(v.visibleRows())
		return nil
	case tea.KeyDown:
		v.pane().moveDown(v.visibleRows())
		return v.loadMoreRows()
	}

	switch key {
	case "q":
		return v.close()
	case "k":
		v.pane().moveUp(v.visibleRows())
	case "j":
		v.pane().moveDown(v.visibleRows())
		return v.loadMoreRows()
	case "y", "i":
		return v.screen(hey.ClearanceApproved)
	case "n":
		return v.screen(hey.ClearanceDenied)
	case "X":
		v.confirmingClear = true
		v.notice = ""
	}
	return nil
}

// InThread reports the Screener as a detail view so the app treats it as a place
// you are inside of rather than a list row.
func (v *screenerView) InThread() bool { return true }

func (v *screenerView) ExitThread() {}

func (v *screenerView) Loading() bool { return v.loading }

func (v *screenerView) Resize(int, int) {}

func (v *screenerView) pane() *screenerPane {
	if v.tab == screenerHistoryTab {
		return &v.history
	}
	return &v.pending
}

func (v *screenerView) otherTab() screenerTab {
	if v.tab == screenerHistoryTab {
		return screenerPendingTab
	}
	return screenerHistoryTab
}

func (v *screenerView) switchTab(tab screenerTab) tea.Cmd {
	if tab == v.tab {
		return nil
	}
	v.tab = tab
	v.notice = ""
	if v.pane().loaded {
		return nil
	}
	if v.tab == screenerHistoryTab {
		return v.requestScreened()
	}
	return v.requestPending()
}

func (v *screenerView) requestPending() tea.Cmd {
	v.requestID++
	v.moreRequestID++
	v.loading = true
	v.pending.paging.reset()
	return v.fetchPending(v.requestID)
}

// loadMoreRows reads the page below the one the reader has scrolled to in the pane they are
// in, or the one below a pane whose end they can already see. One page is asked for at a
// time, in its own lane: the rows already on screen are what they are working through.
func (v *screenerView) loadMoreRows() tea.Cmd {
	pane := v.pane()
	if pane.paging.loading || !pane.paging.hasMore() {
		return nil
	}
	rowsBelow := len(pane.rows)-pane.scroll > v.visibleRows()
	if rowsBelow && len(pane.rows)-pane.cursor > loadMoreThreshold {
		return nil
	}

	pane.paging.loading = true
	v.moreRequestID++
	return v.fetchMoreRows(v.moreRequestID, v.tab, pane.paging.nextPage)
}

// refreshPending is what the doorbell asks of The Screener: read the queue again under
// the cursor. It waits while a decision is in flight or the clear-everything question is
// on screen — held says so, and the caller comes back to it. When the queue isn't what
// is on screen there is nothing to read now, only to mark for the next look at it.
func (v *screenerView) refreshPending() (cmd tea.Cmd, held bool) {
	if v.mutations > 0 || v.confirmingClear {
		return nil, true
	}
	if v.tab != screenerPendingTab {
		v.pending.loaded = false
		return nil, false
	}

	v.liveRequestID++
	return v.fetchPendingRefresh(v.liveRequestID), false
}

func (v *screenerView) requestScreened() tea.Cmd {
	v.requestID++
	v.moreRequestID++
	v.loading = true
	v.history.paging.reset()
	return v.fetchScreened(v.requestID)
}

func (v *screenerView) screen(status string) tea.Cmd {
	if v.tab != screenerPendingTab {
		return nil
	}
	row := v.pane().selected()
	if row == nil {
		return nil
	}
	clearanceID, name := row.id, screenerRowName(*row)
	v.mutations++
	v.notice = ""
	return func() tea.Msg {
		_, err := v.vc.sdk.Clearances().Screen(v.vc.ctx, clearanceID, status, hey.ScreenOptions{})
		return screenerDecisionDoneMsg{clearanceID: clearanceID, name: name, status: status, err: err}
	}
}

func (v *screenerView) handleClearConfirmationKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.Key().Code == tea.KeyEnter || msg.String() == "y" {
		v.confirmingClear = false
		v.mutations++
		return func() tea.Msg {
			return screenerClearedMsg{err: v.vc.sdk.Clearances().Punt(v.vc.ctx)}
		}
	}
	if msg.Key().Code == tea.KeyEscape || msg.String() == "n" || msg.String() == "q" {
		v.confirmingClear = false
	}
	return nil
}

func (v *screenerView) close() tea.Cmd {
	if v.mutations > 0 {
		return nil
	}
	return func() tea.Msg { return screenerClosedMsg{} }
}

func (v *screenerView) explanation() string {
	width := max(v.vc.width-2, 20)
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	if v.tab == screenerHistoryTab {
		return muted.Render("  All the contacts you've screened in or out.") + "\n"
	}
	lines := wrapText("The people below are trying to email you for the first time. You get to decide if you want to hear from them.", width)
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(muted.Render("  "+line) + "\n")
	}
	return b.String()
}

func (v *screenerView) emptyMessage() string {
	if v.tab == screenerHistoryTab {
		return "Nobody has been screened yet"
	}
	return "Nobody is waiting to be screened"
}

func (v *screenerView) visibleRows() int {
	headerLines := strings.Count(v.explanation(), "\n") + 1
	if v.notice != "" {
		headerLines++
	}
	return max((v.vc.height-headerLines)/2, 1)
}

func (v *screenerView) clearConfirmationView() string {
	width := max(v.vc.width-4, 20)
	muted := lipgloss.NewStyle().Foreground(colorMuted)

	var b strings.Builder
	b.WriteString(v.vc.styles.title.Render("Not sure what to do with these?") + "\n\n")
	for _, line := range wrapText("If you don't want to decide on these senders, you can clear them all.", width) {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	for _, line := range wrapText("All emails currently in the Screener will go to the trash. You'll be asked to screen each sender again if they email you in the future.", width) {
		b.WriteString(muted.Render(line) + "\n")
	}
	return b.String()
}

// --- Fetch commands ---

func (v *screenerView) fetchPending(requestID uint64) tea.Cmd {
	return func() tea.Msg {
		rows, count, nextPage, err := v.readPendingPage("")
		return screenerPendingLoadedMsg{requestID: requestID, rows: rows, count: count, nextPage: nextPage, err: err}
	}
}

// fetchPendingRefresh reads the top of the queue in the live lane and without the spinner:
// nobody is waiting on it.
func (v *screenerView) fetchPendingRefresh(requestID uint64) tea.Cmd {
	return func() tea.Msg {
		rows, count, nextPage, err := v.readPendingPage("")
		return screenerPendingRefreshedMsg{requestID: requestID, rows: rows, count: count, nextPage: nextPage, err: err}
	}
}

func (v *screenerView) fetchScreened(requestID uint64) tea.Cmd {
	return func() tea.Msg {
		rows, nextPage, err := v.readScreenedPage("")
		return screenerScreenedLoadedMsg{requestID: requestID, rows: rows, nextPage: nextPage, err: err}
	}
}

// fetchMoreRows reads the page below whichever pane the reader is in, in the growing lane
// and without the spinner.
func (v *screenerView) fetchMoreRows(requestID uint64, tab screenerTab, page string) tea.Cmd {
	return func() tea.Msg {
		message := screenerRowsAppendedMsg{requestID: requestID, tab: tab}
		if tab == screenerHistoryTab {
			message.rows, message.nextPage, message.err = v.readScreenedPage(page)
		} else {
			message.rows, message.count, message.nextPage, message.err = v.readPendingPage(page)
		}
		return message
	}
}

func (v *screenerView) readPendingPage(page string) ([]screenerRow, int, string, error) {
	result, err := v.vc.sdk.Clearances().PendingPage(v.vc.ctx, page)
	if err != nil || result == nil {
		return nil, 0, "", err
	}
	rows := make([]screenerRow, 0, len(result.Clearances))
	for _, clearance := range result.Clearances {
		rows = append(rows, pendingScreenerRow(clearance))
	}
	return rows, result.PendingCount, result.NextPage, nil
}

func (v *screenerView) readScreenedPage(page string) ([]screenerRow, string, error) {
	result, err := v.vc.sdk.Clearances().ScreenedPage(v.vc.ctx, page)
	if err != nil || result == nil {
		return nil, "", err
	}
	rows := make([]screenerRow, 0, len(result.Clearances))
	for _, clearance := range result.Clearances {
		rows = append(rows, screenedScreenerRow(clearance))
	}
	return rows, result.NextPage, nil
}

// --- Rendering ---

func pendingScreenerRow(clearance generated.Clearance) screenerRow {
	subject, summary := clearanceEntryParts(clearance)
	return screenerRow{
		id:       clearance.Id,
		name:     terminal.SanitizeLine(clearance.Petitioner.Name),
		email:    terminal.SanitizeLine(clearance.Petitioner.EmailAddress),
		subject:  subject,
		summary:  summary,
		trailing: formatDisplayDate(clearance.MostRecentEntry.CreatedAt),
	}
}

func screenedScreenerRow(clearance generated.Clearance) screenerRow {
	trailing := screenedVerb(clearance.Status)
	if decided := formatDisplayDate(clearance.UpdatedAt); decided != "" {
		trailing += " · " + decided
	}
	subject, summary := clearanceEntryParts(clearance)
	return screenerRow{
		id:       clearance.Id,
		name:     terminal.SanitizeLine(clearance.Petitioner.Name),
		email:    terminal.SanitizeLine(clearance.Petitioner.EmailAddress),
		subject:  subject,
		summary:  summary,
		trailing: trailing,
	}
}

// clearanceEntryParts returns the subject and excerpt of what the petitioner
// sent, falling back to their address when HEY serves no entry data.
func clearanceEntryParts(clearance generated.Clearance) (subject, summary string) {
	subject = clearance.MostRecentEntry.Subject
	summary = clearance.MostRecentEntry.Summary
	if subject == "" && summary == "" {
		subject = clearance.Petitioner.EmailAddress
	}
	return terminal.SanitizeLine(subject), terminal.SanitizeLine(summary)
}

func screenedVerb(status string) string {
	if status == hey.ClearanceDenied {
		return "screened out"
	}
	return "screened in"
}

func screenerRowName(row screenerRow) string {
	if row.name != "" {
		return row.name
	}
	if row.email != "" {
		return row.email
	}
	return fmt.Sprintf("clearance %d", row.id)
}

// screenerRowLabel is the first-line label: the name with the address in
// angle brackets, as an email From header writes it.
func screenerRowLabel(row screenerRow) string {
	if row.name != "" && row.email != "" {
		return row.name + " <" + row.email + ">"
	}
	return screenerRowName(row)
}

func renderScreenerRows(pane *screenerPane, visible, width int) string {
	// Rows mirror the mail lists: a bold bright first line ("Name <address>")
	// with a bright trailing date, then an indented second line whose subject
	// takes the hyperlink color and whose excerpt is faint. The cursor row
	// uses the accent foreground that is checked against the selection
	// background, with the bar and background spanning both lines.
	marker, selectedText := cursorStyles()
	selectedGap := selectionStyle(lipgloss.NewStyle())
	labelBase := lipgloss.NewStyle().Foreground(colorBright).Bold(true)
	trailingBase := lipgloss.NewStyle().Foreground(colorBright)
	subjectBase := lipgloss.NewStyle().Foreground(colorLink).Bold(true)
	summaryBase := styleMuted

	var b strings.Builder
	end := min(pane.scroll+visible, len(pane.rows))
	for index := pane.scroll; index < end; index++ {
		row := pane.rows[index]
		isCursor := index == pane.cursor

		emphasize := func(base lipgloss.Style) lipgloss.Style {
			if isCursor {
				return selectedText
			}
			return base
		}
		gapStyle := lipgloss.NewStyle()
		if isCursor {
			gapStyle = selectedGap
		}

		trailing := truncateStr(row.trailing, max(width/2, 10))
		label := truncateStr(screenerRowLabel(row), max(width-lipgloss.Width(trailing)-6, 10))
		gap := strings.Repeat(" ", max(width-4-lipgloss.Width(label)-lipgloss.Width(trailing), 1))

		// Line 2: Subject — excerpt, colored like a mail row's second line.
		subject := row.subject
		var summary string
		if row.summary != "" {
			summary = " — " + row.summary
		}
		detailWidth := max(width-6, 10)
		if lipgloss.Width(subject) > detailWidth {
			subject = truncateStr(subject, detailWidth)
			summary = ""
		} else {
			summary = truncateStr(summary, max(detailWidth-lipgloss.Width(subject), 0))
		}

		if isCursor {
			b.WriteString(marker.Render("│") + gapStyle.Render(" "))
		} else {
			b.WriteString("  ")
		}
		b.WriteString(emphasize(labelBase).Render(label) + gapStyle.Render(gap))
		if trailing != "" {
			b.WriteString(emphasize(trailingBase).Render(trailing))
		}
		b.WriteString("\n")
		if isCursor {
			b.WriteString(marker.Render("│") + gapStyle.Render("   "))
		} else {
			b.WriteString("    ")
		}
		b.WriteString(emphasize(subjectBase).Render(subject))
		b.WriteString(emphasize(summaryBase).Render(summary))
		if isCursor {
			// Pad to the first line's width so the selection background
			// covers the whole row.
			pad := width - 6 - lipgloss.Width(subject) - lipgloss.Width(summary)
			if pad > 0 {
				b.WriteString(gapStyle.Render(strings.Repeat(" ", pad)))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
