package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// --- Screener messages ---

type screenerPendingLoadedMsg struct {
	requestID uint64
	page      int
	rows      []screenerRow
	count     int
	err       error
}

// screenerPendingRefreshedMsg is the queue read again after someone arrived in or left
// The Screener while it was open. Its own lane, so it can never be taken for a page the
// user asked for, and the rows land under the cursor rather than replacing it.
type screenerPendingRefreshedMsg struct {
	requestID uint64
	page      int
	rows      []screenerRow
	count     int
	err       error
}

type screenerScreenedLoadedMsg struct {
	requestID uint64
	page      int
	rows      []screenerRow
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
	detail   string // what they sent, or the address they write from
	trailing string // their address, or the decision and when it was made
}

type screenerPane struct {
	rows   []screenerRow
	cursor int
	scroll int
	page   int
	loaded bool
}

func (p *screenerPane) setRows(rows []screenerRow, page int) {
	p.rows = rows
	p.cursor = 0
	p.scroll = 0
	p.page = page
	p.loaded = true
}

// refreshRows replaces the rows with a newly read page while someone is working through
// it: the cursor stays on the sender it was on, and the window stays where it was. A
// sender who has been dealt with elsewhere takes the cursor with them.
func (p *screenerPane) refreshRows(rows []screenerRow, page int) {
	var cursorID int64
	if row := p.selected(); row != nil {
		cursorID = row.id
	}

	p.rows = rows
	p.page = page
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

func (p *screenerPane) remove(id int64) {
	for index, row := range p.rows {
		if row.id != id {
			continue
		}
		p.rows = append(p.rows[:index], p.rows[index+1:]...)
		if p.cursor >= len(p.rows) && p.cursor > 0 {
			p.cursor--
		}
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
	notice          string
	loading         bool
	requestID       uint64
	liveRequestID   uint64 // identifies the only live re-read allowed to update the queue
	mutations       int
}

func newScreenerView(vc *viewContext) *screenerView {
	return &screenerView{vc: vc}
}

func (v *screenerView) Init() tea.Cmd {
	v.notice = ""
	v.confirmingClear = false
	v.tab = screenerPendingTab
	return v.requestPending(1)
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
			v.notice = "Could not load The Screener: " + msg.err.Error()
			return nil, true
		}
		if len(msg.rows) == 0 && msg.page > v.pending.page {
			v.notice = "No more senders waiting"
			return nil, true
		}
		v.pendingCount = msg.count
		v.pending.setRows(msg.rows, msg.page)
		return nil, true

	case screenerPendingRefreshedMsg:
		if msg.requestID != v.liveRequestID || v.tab != screenerPendingTab {
			return nil, true
		}
		if msg.err != nil {
			v.notice = "Could not refresh The Screener: " + msg.err.Error()
			return nil, true
		}
		v.pendingCount = msg.count
		v.pending.refreshRows(msg.rows, msg.page)
		v.pending.ensureVisible(v.visibleRows())
		return nil, true

	case screenerScreenedLoadedMsg:
		if msg.requestID != v.requestID {
			return nil, true
		}
		v.loading = false
		if msg.err != nil {
			v.notice = "Could not load Screener History: " + msg.err.Error()
			return nil, true
		}
		if len(msg.rows) == 0 && msg.page > v.history.page {
			v.notice = "No more decisions"
			return nil, true
		}
		v.history.setRows(msg.rows, msg.page)
		return nil, true

	case screenerDecisionDoneMsg:
		if v.mutations > 0 {
			v.mutations--
		}
		if msg.err != nil {
			v.notice = "Could not screen " + msg.name + ": " + msg.err.Error()
			return nil, true
		}
		v.pending.remove(msg.clearanceID)
		v.pendingCount = max(v.pendingCount-1, 0)
		v.history.loaded = false
		v.notice = msg.name + " " + screenedVerb(msg.status)
		return nil, true

	case screenerClearedMsg:
		if v.mutations > 0 {
			v.mutations--
		}
		if msg.err != nil {
			v.notice = "Could not clear The Screener: " + msg.err.Error()
			return nil, true
		}
		v.pending.setRows(nil, 1)
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
	if pane.page > 1 {
		b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render(fmt.Sprintf("  Page %d", pane.page)))
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
			helpBinding{"y", "screen in"},
			helpBinding{"n", "screen out"},
			helpBinding{"tab", "screener history"},
		)
	} else {
		bindings = append(bindings, helpBinding{"tab", "the screener"})
	}
	bindings = append(bindings, helpBinding{"X", "clear all"})
	pane := v.pane()
	if len(pane.rows) > 0 {
		bindings = append(bindings, helpBinding{"]", "next page"})
	}
	if pane.page > 1 {
		bindings = append(bindings, helpBinding{"[", "previous page"})
	}
	return append(bindings, helpBinding{"esc/q", "back to mail"})
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
		return nil
	}

	switch key {
	case "q":
		return v.close()
	case "k":
		v.pane().moveUp(v.visibleRows())
	case "j":
		v.pane().moveDown(v.visibleRows())
	case "y", "i":
		return v.screen(hey.ClearanceApproved)
	case "n":
		return v.screen(hey.ClearanceDenied)
	case "X":
		v.confirmingClear = true
		v.notice = ""
	case "]":
		return v.requestPage(v.pane().page + 1)
	case "[":
		if v.pane().page > 1 {
			return v.requestPage(v.pane().page - 1)
		}
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
	return v.requestPage(1)
}

func (v *screenerView) requestPage(page int) tea.Cmd {
	if v.tab == screenerHistoryTab {
		return v.requestScreened(page)
	}
	return v.requestPending(page)
}

func (v *screenerView) requestPending(page int) tea.Cmd {
	v.requestID++
	v.loading = true
	return v.fetchPending(v.requestID, max(page, 1))
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
	return v.fetchPendingRefresh(v.liveRequestID, max(v.pending.page, 1)), false
}

func (v *screenerView) requestScreened(page int) tea.Cmd {
	v.requestID++
	v.loading = true
	return v.fetchScreened(v.requestID, max(page, 1))
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
	b.WriteString(v.vc.styles.title.Render("  Want to get emails from them?") + "\n")
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

func (v *screenerView) fetchPending(requestID uint64, page int) tea.Cmd {
	return func() tea.Msg {
		summary, err := v.vc.sdk.Clearances().Pending(v.vc.ctx, strconv.Itoa(page))
		if err != nil {
			return screenerPendingLoadedMsg{requestID: requestID, page: page, err: err}
		}
		message := screenerPendingLoadedMsg{requestID: requestID, page: page}
		if summary != nil {
			message.count = int(summary.PendingClearancesCount)
			for _, clearance := range summary.Clearances {
				message.rows = append(message.rows, pendingScreenerRow(clearance))
			}
		}
		return message
	}
}

// fetchPendingRefresh reads the same page fetchPending reads, in the live lane and
// without the spinner: nobody is waiting on it.
func (v *screenerView) fetchPendingRefresh(requestID uint64, page int) tea.Cmd {
	return func() tea.Msg {
		summary, err := v.vc.sdk.Clearances().Pending(v.vc.ctx, strconv.Itoa(page))
		if err != nil {
			return screenerPendingRefreshedMsg{requestID: requestID, page: page, err: err}
		}
		message := screenerPendingRefreshedMsg{requestID: requestID, page: page}
		if summary != nil {
			message.count = int(summary.PendingClearancesCount)
			for _, clearance := range summary.Clearances {
				message.rows = append(message.rows, pendingScreenerRow(clearance))
			}
		}
		return message
	}
}

func (v *screenerView) fetchScreened(requestID uint64, page int) tea.Cmd {
	return func() tea.Msg {
		clearances, err := v.vc.sdk.Clearances().Screened(v.vc.ctx, strconv.Itoa(page))
		if err != nil {
			return screenerScreenedLoadedMsg{requestID: requestID, page: page, err: err}
		}
		message := screenerScreenedLoadedMsg{requestID: requestID, page: page}
		for _, clearance := range clearances {
			message.rows = append(message.rows, screenedScreenerRow(clearance))
		}
		return message
	}
}

// --- Rendering ---

func pendingScreenerRow(clearance generated.Clearance) screenerRow {
	detail := clearance.MostRecentEntry.Subject
	if summary := clearance.MostRecentEntry.Summary; summary != "" {
		if detail == "" {
			detail = summary
		} else {
			detail += " – " + summary
		}
	}
	return screenerRow{
		id:       clearance.Id,
		name:     clearance.Petitioner.Name,
		email:    clearance.Petitioner.EmailAddress,
		detail:   detail,
		trailing: clearance.Petitioner.EmailAddress,
	}
}

func screenedScreenerRow(clearance generated.Clearance) screenerRow {
	trailing := screenedVerb(clearance.Status)
	if decided := formatDisplayDate(formatTimestamp(clearance.UpdatedAt)); decided != "" {
		trailing += " · " + decided
	}
	return screenerRow{
		id:       clearance.Id,
		name:     clearance.Petitioner.Name,
		email:    clearance.Petitioner.EmailAddress,
		detail:   clearance.Petitioner.EmailAddress,
		trailing: trailing,
	}
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

func renderScreenerRows(pane *screenerPane, visible, width int) string {
	// The cursor row goes through the same helpers as Mail and Contacts so a
	// theme's selection background tints every segment of it, gaps included.
	// Every text segment on it takes the accent, as in Mail: the selection is
	// gated for accent-on-selection contrast, not muted-on-selection.
	marker, selected := cursorStyles()
	selectedGap := selectionStyle(lipgloss.NewStyle())
	normal := lipgloss.NewStyle().Foreground(colorBright)
	muted := lipgloss.NewStyle().Foreground(colorMuted)

	var b strings.Builder
	end := min(pane.scroll+visible, len(pane.rows))
	for index := pane.scroll; index < end; index++ {
		row := pane.rows[index]
		isCursor := index == pane.cursor

		trailing := truncateStr(row.trailing, max(width/2, 10))
		name := truncateStr(screenerRowName(row), max(width-lipgloss.Width(trailing)-6, 10))
		gap := strings.Repeat(" ", max(width-4-lipgloss.Width(name)-lipgloss.Width(trailing), 1))
		detail := truncateStr(row.detail, max(width-6, 10))

		if isCursor {
			b.WriteString(marker.Render("│") + selectedGap.Render(" ") + selected.Render(name) + selectedGap.Render(gap))
			if trailing != "" {
				b.WriteString(selected.Render(trailing))
			}
			b.WriteString("\n" + marker.Render("│") + selectedGap.Render("   ") + selected.Render(detail) + "\n")
			continue
		}
		b.WriteString("  " + normal.Render(name) + gap)
		if trailing != "" {
			b.WriteString(muted.Render(trailing))
		}
		b.WriteString("\n    " + muted.Render(detail) + "\n")
	}
	return b.String()
}
