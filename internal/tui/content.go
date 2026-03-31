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
	postings  []models.Posting
	cursor    int
	scrollOff int
	width     int
	height    int // visible rows (each posting takes 2 lines)
}

func (c *contentList) setPostings(postings []models.Posting) {
	c.postings = postings
	c.cursor = 0
	c.scrollOff = 0
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

func (c *contentList) ensureVisible() {
	visibleItems := c.height / 2 // 2 lines per posting
	if visibleItems < 1 {
		visibleItems = 1
	}
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

func (c *contentList) view() string {
	if len(c.postings) == 0 {
		return lipgloss.NewStyle().Foreground(colorMuted).Render("  (empty)")
	}

	visibleItems := c.height / 2
	if visibleItems < 1 {
		visibleItems = 1
	}

	var b strings.Builder
	end := min(c.scrollOff+visibleItems, len(c.postings))

	selected := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	normal := lipgloss.NewStyle().Foreground(colorBright)
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	unseenDot := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	for i := c.scrollOff; i < end; i++ {
		p := c.postings[i]
		isCursor := i == c.cursor

		// Line 1: [│] [●] Subject (count)                Nov 24, 2025
		var line1 strings.Builder
		if isCursor {
			line1.WriteString(selected.Render("│") + " ")
		} else {
			line1.WriteString("  ")
		}
		if !p.Seen {
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
		if p.VisibleEntryCount > 1 {
			subject += fmt.Sprintf(" (%d)", p.VisibleEntryCount)
		}

		date := formatDisplayDate(p.CreatedAt)

		// Calculate available width for subject
		dateWidth := len(date)
		prefixWidth := 4                                         // "│ ● " or "    "
		subjectWidth := max(c.width-prefixWidth-dateWidth-2, 10) // 2 for gap
		if len(subject) > subjectWidth {
			subject = subject[:subjectWidth-3] + "..."
		}
		gap := max(c.width-prefixWidth-len(subject)-dateWidth, 1)

		// Subject always bright/white, date always muted (cursor adds bold+color)
		if isCursor {
			line1.WriteString(selected.Render(subject))
			line1.WriteString(strings.Repeat(" ", gap))
			line1.WriteString(selected.Render(date))
		} else {
			line1.WriteString(normal.Render(subject))
			line1.WriteString(strings.Repeat(" ", gap))
			line1.WriteString(muted.Render(date))
		}

		// Line 2: [│]   extension@ Creator Name — excerpt...
		var line2 strings.Builder
		if isCursor {
			line2.WriteString(selected.Render("│") + "   ")
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
		var desc string
		if len(p.Extenzions) > 0 {
			desc = p.Extenzions[0].Name + "@ " + name
		} else {
			desc = name
		}

		// Summary is the last message excerpt — always show it
		if p.Summary != "" && p.Summary != p.Name {
			desc += " — " + p.Summary
		}

		descWidth := max(c.width-4-2, 10) // 4 prefix + 2 margin
		if lipgloss.Width(desc) > descWidth {
			runes := []rune(desc)
			for lipgloss.Width(string(runes)) > descWidth-3 && len(runes) > 0 {
				runes = runes[:len(runes)-1]
			}
			desc = string(runes) + "..."
		}

		if isCursor {
			line2.WriteString(selected.Render(desc))
		} else {
			line2.WriteString(muted.Render(desc))
		}

		fmt.Fprintln(&b, line1.String())
		fmt.Fprintln(&b, line2.String())
	}

	return b.String()
}

// --- Calendar recordings content ---

type recordingList struct {
	recordings []models.Recording
	cursor     int
	scrollOff  int
	width      int
	height     int
}

func (c *recordingList) setRecordings(recordings []models.Recording) {
	c.recordings = recordings
	c.cursor = 0
	c.scrollOff = 0
}

func (c *recordingList) setSize(w, h int) {
	c.width = w
	c.height = h
}

func (c *recordingList) moveUp() {
	if c.cursor > 0 {
		c.cursor--
		c.ensureVisible()
	}
}

func (c *recordingList) moveDown() {
	if c.cursor < len(c.recordings)-1 {
		c.cursor++
		c.ensureVisible()
	}
}

func (c *recordingList) ensureVisible() {
	visibleItems := c.height / 2
	if visibleItems < 1 {
		visibleItems = 1
	}
	if c.cursor < c.scrollOff {
		c.scrollOff = c.cursor
	}
	if c.cursor >= c.scrollOff+visibleItems {
		c.scrollOff = c.cursor - visibleItems + 1
	}
}

func (c *recordingList) selectedRecording() *models.Recording {
	if c.cursor < 0 || c.cursor >= len(c.recordings) {
		return nil
	}
	return &c.recordings[c.cursor]
}

func (c *recordingList) view() string {
	if len(c.recordings) == 0 {
		return lipgloss.NewStyle().Foreground(colorMuted).Render("  (empty)")
	}

	visibleItems := c.height / 2
	if visibleItems < 1 {
		visibleItems = 1
	}

	selected := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	normal := lipgloss.NewStyle().Foreground(colorBright)
	muted := lipgloss.NewStyle().Foreground(colorMuted)

	var b strings.Builder
	end := min(c.scrollOff+visibleItems, len(c.recordings))

	for i := c.scrollOff; i < end; i++ {
		r := c.recordings[i]
		isCursor := i == c.cursor

		// Line 1: [time] Title                             Date
		var line1 strings.Builder
		line1.WriteString("  ")

		prefix := "[All day]"
		if !r.AllDay && len(r.StartsAt) >= 16 {
			prefix = fmt.Sprintf("[%s]", r.StartsAt[11:16])
		}

		title := prefix + " " + r.Title
		date := ""
		if len(r.StartsAt) >= 10 {
			date = r.StartsAt[:10]
		}

		subjectWidth := max(c.width-4-len(date)-2, 10)
		if len(title) > subjectWidth {
			title = title[:subjectWidth-3] + "..."
		}
		gap := max(c.width-2-len(title)-len(date), 1)

		if isCursor {
			line1.WriteString(selected.Render(title))
			line1.WriteString(strings.Repeat(" ", gap))
			line1.WriteString(selected.Render(date))
		} else {
			line1.WriteString(normal.Render(title))
			line1.WriteString(strings.Repeat(" ", gap))
			line1.WriteString(muted.Render(date))
		}

		// Line 2: type info
		var line2 strings.Builder
		line2.WriteString("    ")
		var parts []string
		parts = append(parts, r.Type)
		if r.Recurring {
			parts = append(parts, "recurring")
		}
		desc := strings.Join(parts, " · ")
		if isCursor {
			line2.WriteString(selected.Render(desc))
		} else {
			line2.WriteString(muted.Render(desc))
		}

		fmt.Fprintln(&b, line1.String())
		fmt.Fprintln(&b, line2.String())
	}

	return b.String()
}
