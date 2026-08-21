package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/mail"
)

// formatDisplayDate renders a timestamp as "Nov 24, 2025" in the reader's own zone, which
// is the only zone the day is right in: a thread that arrived at 23:30 UTC belongs to the
// next day east of it.
func formatDisplayDate(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Local().Format("Jan 2, 2006")
}

// listPaging is where a list that grows as the reader scrolls keeps its place in what the
// server holds: the cursor for the page after the deepest one read, empty once there is
// nothing left to read; what the top page held when it was last read, which is exactly how
// much of the list a live re-read replaces; how many pages deep the list has grown; and
// whether the next page is already on its way, so the bottom is only asked for once.
type listPaging struct {
	nextPage string
	headIDs  map[int64]struct{}
	pages    int
	loading  bool
}

func (p *listPaging) reset() {
	*p = listPaging{}
}

// read starts the list over at its top page.
func (p *listPaging) read(headIDs map[int64]struct{}, nextPage string) {
	p.headIDs = headIDs
	p.pages = 1
	p.nextPage = nextPage
	p.loading = false
}

// grew records a page arriving below the list. An empty page is the end of it whatever
// cursor came with it: paging on from there would ask the same question forever.
func (p *listPaging) grew(rowsRead int, nextPage string) {
	p.pages++
	p.loading = false
	if rowsRead == 0 {
		p.nextPage = ""
	} else {
		p.nextPage = nextPage
	}
}

// refreshed records the top page having been read again. The cursor for what comes next
// belongs to the deepest page, so a re-read of the top only moves it while the top page is
// the whole list — below that the reader has already walked past it.
func (p *listPaging) refreshed(headIDs map[int64]struct{}, nextPage string) {
	p.headIDs = headIDs
	if p.pages <= 1 {
		p.pages = 1
		p.nextPage = nextPage
	}
}

func (p *listPaging) hasMore() bool {
	return p.nextPage != ""
}

// loadMoreThreshold is how close to the bottom of a list the cursor comes before the next
// page is read. A page arrives while there is still something left to scroll through,
// rather than after the cursor has already stopped against the end.
const loadMoreThreshold = 5

// noSelection is the cursor of a list with nothing the reader can reach: every row is
// under the cover, so there is nothing to open, act on or select.
const noSelection = -1

// contentList renders a scrollable list of postings with a cursor.
type contentList struct {
	postings      []mail.Posting
	cursor        int
	scrollOff     int
	width         int
	height        int // visible rows (each posting takes 2 lines)
	hideSeenState bool
	selected      map[int64]struct{}

	cover       coverPreset // art that hides Previously Seen, coverNone for none
	coverPeeked bool        // the reader lifted the cover to get at what is under it
	coverArt    coverRenderer
}

// setCover puts art over Previously Seen. Setting it closes the cover, so a box
// arrives covered rather than however the last one was left, and whatever the
// cursor and the selection were on goes out from under the art with it.
func (c *contentList) setCover(preset coverPreset) {
	c.cover = preset
	c.coverPeeked = false
	c.settleCover()
}

// toggleCoverPeek lifts the cover off Previously Seen, or puts it back.
func (c *contentList) toggleCoverPeek() {
	c.coverPeeked = !c.coverPeeked
	c.settleCover()
}

// settleCover keeps the cursor and the selection out from under the cover. A
// re-read, or a thread the reader just opened turning seen, otherwise slides
// them under it, leaving a cursor on a row nobody can see and a bulk action
// aimed at threads the reader thinks are put away.
func (c *contentList) settleCover() {
	c.dropCoveredSelection()
	c.clampCursor()
}

// coveredFrom is the index of the first Previously Seen posting while the cover
// is down, or -1 when nothing is hidden. Everything from there on is under the
// art: out of reach, and not rendered.
func (c *contentList) coveredFrom() int {
	if c.cover == coverNone || c.coverPeeked || c.hideSeenState {
		return -1
	}
	for i := range c.postings {
		if sectionOf(c.postings[i]) == sectionPreviouslySeen {
			return i
		}
	}
	return -1
}

// itemCount is how many postings the reader can move through and act on.
func (c *contentList) itemCount() int {
	if from := c.coveredFrom(); from >= 0 {
		return from
	}
	return len(c.postings)
}

func (c *contentList) dropCoveredSelection() {
	count := c.itemCount()
	for id := range c.selected {
		for i := count; i < len(c.postings); i++ {
			if c.postings[i].ID == id {
				delete(c.selected, id)
				break
			}
		}
	}
}

// clampCursor keeps the cursor on a row the reader can reach. With everything under the
// cover there is no such row, and the cursor says so rather than resting on the first
// hidden posting.
func (c *contentList) clampCursor() {
	if c.itemCount() == 0 {
		c.cursor = noSelection
		c.scrollOff = 0
		return
	}
	last := c.itemCount() - 1
	c.cursor = min(max(c.cursor, 0), last)
	c.scrollOff = min(c.scrollOff, last)
	c.ensureVisible()
}

func (c *contentList) setPostings(postings []mail.Posting) {
	if !c.hideSeenState {
		postings = partitionSections(postings)
	}
	c.postings = postings
	c.cursor = 0
	c.scrollOff = 0
	c.clearSelected()
	c.clampCursor()
}

// growPostings adds the page after the one at the bottom of the list. A posting the list
// already shows is dropped rather than added again: a thread that sank in the ordering
// between two reads would otherwise arrive on two pages.
func (c *contentList) growPostings(more []mail.Posting) {
	grown := c.postings
	shown := postingIDs(c.postings)
	for _, posting := range more {
		if _, alreadyShown := shown[posting.ID]; !alreadyShown {
			grown = append(grown, posting)
		}
	}
	c.keepPlaceIn(grown)
}

// refreshHead replaces the top page of the list with a newly read one and leaves the pages
// the reader scrolled down to as they were read. headIDs is what the top page held the
// last time it was read, so a thread that has since left it goes with it rather than
// sinking into the list below.
func (c *contentList) refreshHead(head []mail.Posting, headIDs map[int64]struct{}) {
	fresh := postingIDs(head)
	refreshed := append([]mail.Posting(nil), head...)
	for _, posting := range c.postings {
		_, wasInHead := headIDs[posting.ID]
		_, isInHead := fresh[posting.ID]
		if !wasInHead && !isInHead {
			refreshed = append(refreshed, posting)
		}
	}
	c.keepPlaceIn(refreshed)
}

func postingIDs(postings []mail.Posting) map[int64]struct{} {
	ids := make(map[int64]struct{}, len(postings))
	for _, posting := range postings {
		ids[posting.ID] = struct{}{}
	}
	return ids
}

// keepPlaceIn puts the list on a newly assembled set of postings while the user is looking
// at it, so the reader keeps their place: the cursor stays on the posting it was on, the
// window stays where it was scrolled to, and a multi-selection keeps every row that is
// still there. A posting that left the box takes the cursor or its selection with it.
func (c *contentList) keepPlaceIn(postings []mail.Posting) {
	if !c.hideSeenState {
		postings = partitionSections(postings)
	}
	var cursorID int64
	if posting := c.selectedPosting(); posting != nil {
		cursorID = posting.ID
	}

	c.postings = postings
	c.cursor = 0
	for i := range c.postings {
		if c.postings[i].ID == cursorID {
			c.cursor = i
			break
		}
	}
	c.keepSelected()
	c.scrollOff = min(c.scrollOff, max(len(c.postings)-1, 0))
	c.settleCover()
}

// keepSelected drops the postings that are no longer in the list from the selection.
func (c *contentList) keepSelected() {
	if len(c.selected) == 0 {
		return
	}
	remaining := make(map[int64]struct{}, len(c.selected))
	for _, posting := range c.postings {
		if _, wasSelected := c.selected[posting.ID]; wasSelected {
			remaining[posting.ID] = struct{}{}
		}
	}
	c.selected = remaining
}

// postingSection is the group a posting belongs to in the Imbox. On the server
// a posting's read state is one of unseen, bubbled up or seen, and the web app
// stacks the sections in that order.
type postingSection int

const (
	sectionBubbledUp postingSection = iota
	sectionNewForYou
	sectionPreviouslySeen
)

var postingSections = []postingSection{sectionBubbledUp, sectionNewForYou, sectionPreviouslySeen}

func sectionOf(p mail.Posting) postingSection {
	switch {
	case p.BubbledUp:
		return sectionBubbledUp
	case !p.Seen:
		return sectionNewForYou
	default:
		return sectionPreviouslySeen
	}
}

func (s postingSection) label() string {
	switch s {
	case sectionBubbledUp:
		return "Bubbled Up"
	case sectionNewForYou:
		return "New for You"
	default:
		return "Previously Seen"
	}
}

// partitionSections groups postings by section, keeping the relative order
// inside each group.
func partitionSections(postings []mail.Posting) []mail.Posting {
	ordered := make([]mail.Posting, 0, len(postings))
	for _, section := range postingSections {
		for _, p := range postings {
			if sectionOf(p) == section {
				ordered = append(ordered, p)
			}
		}
	}
	return ordered
}

// markSeen moves a posting into "Previously Seen", clearing the bubbled up
// state the way Postings::SeenController does.
func (c *contentList) markSeen(index int) {
	c.postings[index].Seen = true
	c.postings[index].BubbledUp = false
	c.resort()
}

// resort re-partitions the list after a posting changes its seen state and
// keeps the cursor on the same posting.
func (c *contentList) resort() {
	if c.hideSeenState {
		return
	}
	var id int64
	if p := c.selectedPosting(); p != nil {
		id = p.ID
	}
	c.postings = partitionSections(c.postings)
	for i := range c.postings {
		if c.postings[i].ID == id {
			c.cursor = i
			break
		}
	}
	c.settleCover()
}

func (c *contentList) setSize(w, h int) {
	c.width = w
	c.height = h
}

func (c *contentList) moveUp() {
	if c.cursor > 0 {
		c.cursor--
		c.ensureVisible()
	}
}

func (c *contentList) moveDown() {
	if c.cursor < c.itemCount()-1 {
		c.cursor++
		c.ensureVisible()
	}
}

// listHeight is the rows the postings get. A cover holds back its divider and
// the art's floor at the bottom of the list, so the cover is always on screen
// rather than something you could scroll past.
func (c *contentList) listHeight() int {
	if c.coveredFrom() < 0 {
		return c.height
	}
	return max(c.height-1-coverMinRows, 2)
}

// visibleItemsFrom reports how many postings fit from start, including only
// the section headers that the resulting window renders.
func (c *contentList) visibleItemsFrom(start int) int {
	rows := 0
	count := 0
	height := c.listHeight()
	for i := start; i < c.itemCount(); i++ {
		postingRows := 2
		if c.sectionLabelAt(i) != "" {
			postingRows++
		}
		if rows+postingRows > height {
			break
		}
		rows += postingRows
		count++
	}
	return max(count, 1)
}

func (c *contentList) sectionLabelAt(index int) string {
	if c.hideSeenState || index < 0 || index >= len(c.postings) {
		return ""
	}
	section := sectionOf(c.postings[index])
	if index == 0 || sectionOf(c.postings[index-1]) != section {
		return section.label()
	}
	return ""
}

// hasRowsBelow reports whether the list carries on past the bottom of the window. A list
// that does not is a list the reader can see the end of, which is a reason to read the page
// below it without waiting to be asked.
//
// Rows under the cover count. They are not rows the reader can scroll into, but a covered
// Imbox that has run out of unseen threads has nothing worth reading on for: the box is
// ordered by seen first (haystack's `render_box` sorts `[ :seen, observed_at: :desc ]`), so
// every posting after the first seen one is seen too. Counting only what the reader can
// reach makes such a list read the box to its end a page at a time, each page landing
// entirely under the art and asking for the next.
func (c *contentList) hasRowsBelow() bool {
	return c.scrollOff+c.visibleItemsFrom(c.scrollOff) < len(c.postings)
}

func (c *contentList) ensureVisible() {
	if c.cursor < c.scrollOff {
		c.scrollOff = c.cursor
		return
	}
	if c.cursor < c.scrollOff+c.visibleItemsFrom(c.scrollOff) {
		return
	}

	// Fill the window backwards from the cursor without moving above the
	// previous offset. Capacity can grow after a section header scrolls away.
	start := c.cursor
	for start > c.scrollOff {
		candidate := start - 1
		if c.cursor >= candidate+c.visibleItemsFrom(candidate) {
			break
		}
		start = candidate
	}
	c.scrollOff = start
}

// selectedPosting is the posting under the cursor, or nil when the cursor is on nothing the
// reader can reach. What is under the cover is not on screen, so it is not what a key press
// means either.
func (c *contentList) selectedPosting() *mail.Posting {
	if c.cursor < 0 || c.cursor >= c.itemCount() {
		return nil
	}
	return &c.postings[c.cursor]
}

func (c *contentList) toggleSelected() bool {
	posting := c.selectedPosting()
	if posting == nil {
		return false
	}
	if c.selected == nil {
		c.selected = make(map[int64]struct{})
	}
	if _, exists := c.selected[posting.ID]; exists {
		delete(c.selected, posting.ID)
		return false
	}
	c.selected[posting.ID] = struct{}{}
	return true
}

// selectedIDs is what a bulk action aims at: the selected postings the reader can reach.
// A thread that went under the cover between the selection and the action goes with it.
func (c *contentList) selectedIDs() []int64 {
	ids := make([]int64, 0, len(c.selected))
	for _, posting := range c.postings[:c.itemCount()] {
		if _, exists := c.selected[posting.ID]; exists {
			ids = append(ids, posting.ID)
		}
	}
	return ids
}

func (c *contentList) clearSelected() {
	c.selected = nil
}

func (c *contentList) view() string {
	if len(c.postings) == 0 {
		return styleMuted.Render("  (empty)")
	}

	var b strings.Builder
	rendered := 0
	end := min(c.scrollOff+c.visibleItemsFrom(c.scrollOff), c.itemCount())

	cursorMarker, _ := cursorStyles()
	selectedGap := selectionStyle(lipgloss.NewStyle())
	unseenDot := lipgloss.NewStyle().Foreground(colorAlert).Bold(true)
	selectedMark := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	// Every row uses the same styles: bold bright subject; date, sender and
	// excerpt in the faint secondary style. Read state shows as the section a
	// row sits in ("Bubbled Up" / "New for You" / "Previously Seen") plus the
	// alert dot; the cursor row renders bold on top of the base styles.
	subjectBase := lipgloss.NewStyle().Foreground(colorBright).Bold(true)
	dateBase := styleMuted
	senderBase := styleMuted
	excerptBase := styleMuted

	// The date gets its own right-hand column, as in the HEY web app. Both
	// lines of a row stop before the column, so the right edge stays clean.
	dateCol := 0
	for i := c.scrollOff; i < end; i++ {
		dateCol = max(dateCol, lipgloss.Width(formatDisplayDate(c.postings[i].CreatedAt)))
	}
	prefixWidth := 4 // "│ ● " or "    "
	textWidth := max(c.width-prefixWidth-2-dateCol, 10)

	for i := c.scrollOff; i < end; i++ {
		p := c.postings[i]
		isCursor := i == c.cursor

		if label := c.sectionLabelAt(i); label != "" {
			if c.cover != coverNone && sectionOf(p) == sectionPreviouslySeen {
				fmt.Fprintln(&b, coverHeader(label, "v to cover", c.width))
			} else {
				fmt.Fprintln(&b, sectionHeader(label, c.width))
			}
			rendered++
		}

		// The cursor row renders bold on top of the base styles and, when the
		// theme has a usable selection, paints every segment — gaps included —
		// with the selection background so it reads as one highlighted line.
		emphasize := func(base lipgloss.Style) lipgloss.Style {
			if isCursor {
				base = selectionStyle(base.Bold(true))
			}
			return base
		}
		gapStyle, dot, mark := lipgloss.NewStyle(), unseenDot, selectedMark
		if isCursor {
			gapStyle, dot, mark = selectedGap, selectionStyle(unseenDot), selectionStyle(selectedMark)
		}

		// Line 1: [│] [●] Subject (count)                Nov 24, 2025
		// The cursor shows only as the bar on the left; the row keeps its
		// seen/unseen colors.
		var line1 strings.Builder
		if isCursor {
			line1.WriteString(cursorMarker.Render("│") + gapStyle.Render(" "))
		} else {
			line1.WriteString("  ")
		}
		if _, isSelected := c.selected[p.ID]; isSelected {
			line1.WriteString(mark.Render("✓") + gapStyle.Render(" "))
		} else if !p.Seen && !c.hideSeenState {
			line1.WriteString(dot.Render("●") + gapStyle.Render(" "))
		} else {
			line1.WriteString(gapStyle.Render("  "))
		}

		// Subject: Posting.Name is the thread title, Summary is the last message excerpt
		subject := p.Name
		if subject == "" {
			subject = p.Summary
		}
		if subject == "" {
			subject = p.Creator.Name
		}
		if p.Muted {
			subject = "[Ignored] " + subject
		}
		if p.VisibleEntryCount > 1 {
			subject += fmt.Sprintf(" (%d)", p.VisibleEntryCount)
		}

		date := formatDisplayDate(p.CreatedAt)
		subject = truncateToWidth(subject, textWidth)
		// Pad through the gap and right-align the date within its column.
		gap := max(textWidth-lipgloss.Width(subject)+2+dateCol-lipgloss.Width(date), 1)

		line1.WriteString(emphasize(subjectBase).Render(subject))
		line1.WriteString(gapStyle.Render(strings.Repeat(" ", gap)))
		line1.WriteString(emphasize(dateBase).Render(date))

		// Line 2: [│]   extension@ Creator Name — excerpt...
		var line2 strings.Builder
		if isCursor {
			line2.WriteString(cursorMarker.Render("│") + gapStyle.Render("   "))
		} else {
			line2.WriteString("    ")
		}

		name := p.Creator.Name
		if p.AlternativeSenderName != "" {
			name = p.AlternativeSenderName
		}
		if name == "" {
			name = p.Creator.EmailAddress
		}

		// Build: [extension@] Creator Name — Summary excerpt
		sender := name
		if len(p.Extenzions) > 0 {
			sender = p.Extenzions[0].Name + "@ " + name
		}

		// Summary is the last message excerpt — always show it
		var excerpt string
		if p.Summary != "" && p.Summary != p.Name {
			excerpt = " — " + p.Summary
		}

		if lipgloss.Width(sender) > textWidth {
			sender = truncateToWidth(sender, textWidth)
			excerpt = ""
		} else {
			excerpt = truncateToWidth(excerpt, textWidth-lipgloss.Width(sender))
		}

		line2.WriteString(emphasize(senderBase).Render(sender))
		line2.WriteString(emphasize(excerptBase).Render(excerpt))

		fmt.Fprintln(&b, line1.String())
		fmt.Fprintln(&b, line2.String())
		rendered += 2
	}

	if from := c.coveredFrom(); from >= 0 {
		b.WriteString(c.coverView(len(c.postings)-from, rendered))
	}
	return b.String()
}

// coverView is the lid over Previously Seen: its divider, saying how much is
// under there and how to look, and then the art filling the list to the bottom.
// The threads themselves are not rendered at all — that is the whole point of a
// cover, and it is why the art can have every row the postings did not use.
func (c *contentList) coverView(hidden, rowsUsed int) string {
	hint := fmt.Sprintf("%d hidden · v to peek", hidden)
	header := coverHeader(sectionPreviouslySeen.label(), hint, c.width)

	rows := c.height - rowsUsed - 1
	if rows < coverMinRows {
		return header
	}
	return header + "\n" + c.coverArt.view(c.cover, c.width, rows)
}

// sectionHeader renders a list section label with a rule filling the rest
// of the width: "New for You ──────────".
func sectionHeader(label string, width int) string {
	s := lipgloss.NewStyle().Foreground(colorChrome).Bold(true).Render(label)
	if fill := width - lipgloss.Width(label) - 3; fill > 0 {
		s += " " + lipgloss.NewStyle().Foreground(colorChrome).Render(strings.Repeat("─", fill))
	}
	return s
}

// coverHeader is a section label with a hint on its right, where the HEY web app
// puts the cover's buttons: "Previously Seen ──── 34 hidden · v to peek".
func coverHeader(label, hint string, width int) string {
	rule := lipgloss.NewStyle().Foreground(colorChrome)
	fill := width - lipgloss.Width(label) - lipgloss.Width(hint) - 4
	if fill < 1 {
		return sectionHeader(label, width)
	}
	return rule.Bold(true).Render(label) + " " +
		rule.Render(strings.Repeat("─", fill)) + " " +
		styleMuted.Render(hint)
}

// truncateToWidth trims s so its rendered width fits in w cells, appending
// "..." when anything was cut. Returns "" when w cannot hold the ellipsis.
func truncateToWidth(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 3 {
		return ""
	}
	runes := []rune(s)
	for lipgloss.Width(string(runes)) > w-3 && len(runes) > 0 {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "..."
}
