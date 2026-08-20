package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

// formatDisplayDate converts an ISO timestamp to "Nov 24, 2025" format.
func formatDisplayDate(ts string) string {
	if len(ts) < 10 {
		return ts
	}
	t, err := time.Parse("2006-01-02", ts[:10])
	if err != nil {
		// Try full ISO format
		t, err = time.Parse("2006-01-02T15:04:05Z", ts)
		if err != nil {
			return ts[:10]
		}
	}
	return t.Format("Jan 2, 2006")
}

// contentList renders a scrollable list of postings with a cursor.
type contentList struct {
	postings      []models.Posting
	cursor        int
	scrollOff     int
	width         int
	height        int // visible rows (each posting takes 2 lines)
	hideSeenState bool
	selected      map[int64]struct{}
}

func (c *contentList) setPostings(postings []models.Posting) {
	if !c.hideSeenState {
		postings = partitionSeen(postings)
	}
	c.postings = postings
	c.cursor = 0
	c.scrollOff = 0
	c.clearSelected()
}

// partitionSeen orders unseen postings before seen ones, keeping the
// relative order inside each group. This forms the "New for You" and
// "Previously Seen" sections, as in the HEY web app.
func partitionSeen(postings []models.Posting) []models.Posting {
	ordered := make([]models.Posting, 0, len(postings))
	for _, p := range postings {
		if !p.Seen {
			ordered = append(ordered, p)
		}
	}
	for _, p := range postings {
		if p.Seen {
			ordered = append(ordered, p)
		}
	}
	return ordered
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
	c.postings = partitionSeen(c.postings)
	for i := range c.postings {
		if c.postings[i].ID == id {
			c.cursor = i
			break
		}
	}
	c.ensureVisible()
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
	if c.cursor < len(c.postings)-1 {
		c.cursor++
		c.ensureVisible()
	}
}

// headerCount reports how many section headers the list shows.
func (c *contentList) headerCount() int {
	if c.hideSeenState || len(c.postings) == 0 {
		return 0
	}
	n := 0
	if !c.postings[0].Seen {
		n++ // "New for You"
	}
	if c.postings[len(c.postings)-1].Seen {
		n++ // "Previously Seen"
	}
	return n
}

// visibleItems reports how many postings fit: 2 lines per posting, minus
// one line per section header.
func (c *contentList) visibleItems() int {
	return max((c.height-c.headerCount())/2, 1)
}

func (c *contentList) ensureVisible() {
	visibleItems := c.visibleItems()
	if c.cursor < c.scrollOff {
		c.scrollOff = c.cursor
	}
	if c.cursor >= c.scrollOff+visibleItems {
		c.scrollOff = c.cursor - visibleItems + 1
	}
}

func (c *contentList) selectedPosting() *models.Posting {
	if c.cursor < 0 || c.cursor >= len(c.postings) {
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

func (c *contentList) selectedIDs() []int64 {
	ids := make([]int64, 0, len(c.selected))
	for _, posting := range c.postings {
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
	end := min(c.scrollOff+c.visibleItems(), len(c.postings))

	cursorBar := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	unseenDot := lipgloss.NewStyle().Foreground(colorAlert).Bold(true)
	selectedMark := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	// Every row uses the same styles: bold bright subject; date, sender and
	// excerpt in the faint secondary style. Read state shows as the section a
	// row sits in ("New for You" / "Previously Seen") plus the alert dot; the
	// cursor row renders bold on top of the base styles.
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

		if !c.hideSeenState {
			if i == 0 && !p.Seen {
				fmt.Fprintln(&b, sectionHeader("New for You", c.width))
			}
			if p.Seen && (i == 0 || !c.postings[i-1].Seen) {
				fmt.Fprintln(&b, sectionHeader("Previously Seen", c.width))
			}
		}

		emphasize := func(base lipgloss.Style) lipgloss.Style {
			if isCursor {
				base = base.Bold(true)
			}
			return base
		}

		// Line 1: [│] [●] Subject (count)                Nov 24, 2025
		// The cursor shows only as the bar on the left; the row keeps its
		// seen/unseen colors.
		var line1 strings.Builder
		if isCursor {
			line1.WriteString(cursorBar.Render("│") + " ")
		} else {
			line1.WriteString("  ")
		}
		if _, isSelected := c.selected[p.ID]; isSelected {
			line1.WriteString(selectedMark.Render("✓") + " ")
		} else if !p.Seen && !c.hideSeenState {
			line1.WriteString(unseenDot.Render("●") + " ")
		} else {
			line1.WriteString("  ")
		}

		// Subject: Posting.Name is the thread title, Summary is the last message excerpt
		subject := p.Name
		if subject == "" && p.Topic != nil {
			subject = p.Topic.Name
		}
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
		line1.WriteString(strings.Repeat(" ", gap))
		line1.WriteString(emphasize(dateBase).Render(date))

		// Line 2: [│]   extension@ Creator Name — excerpt...
		var line2 strings.Builder
		if isCursor {
			line2.WriteString(cursorBar.Render("│") + "   ")
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
	}

	return b.String()
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
