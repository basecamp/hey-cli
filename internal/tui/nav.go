package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

// section identifies the top-level navigation area.
type section int

const (
	sectionMail section = iota
	sectionCalendar
	sectionJournal
)

// focusRow identifies which navigation row has keyboard focus.
type focusRow int

const (
	rowSection focusRow = iota // row 1: Mail / Calendar / Journal
	rowSubnav                  // row 2: boxes / calendars / dates
	rowContent                 // content area
)

// navItem is a single item in a navigation row.
type navItem struct {
	icon  string // squared unicode char (e.g. 🄼) or empty
	label string
}

// --- Row 1: sections (static) ---

var sectionItems = []navItem{
	{"🄼", "Mail"},
	{"🄲", "Calendar"},
	{"🄹", "Journal"},
}

// sectionForShortcut returns the section for a Shift+letter shortcut, or -1.
func sectionForShortcut(key string) section {
	switch key {
	case "M":
		return sectionMail
	case "C":
		return sectionCalendar
	case "J":
		return sectionJournal
	}
	return -1
}

// --- Row 2: boxes (ordered with shortcuts) ---

type boxSpec struct {
	name string
	icon string
	key  string // shift+letter shortcut
}

var knownBoxes = []boxSpec{
	{"Imbox", "🄸", "I"},
	{"Bubble up", "🄱", "B"},
	{"Paper Trail", "🄿", "P"},
	{"The Feed", "🄵", "F"},
	{"Set Aside", "🄰", "A"},
	{"Reply Later", "🄻", "R"},
}

// orderBoxes sorts boxes by the preferred order. Known boxes appear first
// in their predefined order; unknown boxes are appended at the end.
func orderBoxes(boxes []models.Box) []models.Box {
	ordered := make([]models.Box, 0, len(boxes))
	used := make(map[int64]bool)

	// Add known boxes in preferred order
	for _, spec := range knownBoxes {
		for _, b := range boxes {
			if strings.EqualFold(b.Name, spec.name) && !used[b.ID] {
				ordered = append(ordered, b)
				used[b.ID] = true
				break
			}
		}
	}
	// Append any remaining boxes
	for _, b := range boxes {
		if !used[b.ID] {
			ordered = append(ordered, b)
		}
	}
	return ordered
}

// boxNavItems builds nav items for the box row, applying icons to known boxes.
func boxNavItems(boxes []models.Box) []navItem {
	items := make([]navItem, len(boxes))
	for i, b := range boxes {
		icon := ""
		for _, spec := range knownBoxes {
			if strings.EqualFold(b.Name, spec.name) {
				icon = spec.icon
				break
			}
		}
		items[i] = navItem{icon: icon, label: b.Name}
	}
	return items
}

// boxForShortcut returns the index of the box matching a Shift+letter shortcut, or -1.
func boxForShortcut(key string, boxes []models.Box) int {
	for _, spec := range knownBoxes {
		if spec.key == key {
			for i, b := range boxes {
				if strings.EqualFold(b.Name, spec.name) {
					return i
				}
			}
		}
	}
	return -1
}

// calendarNavItems builds nav items for the calendar row.
func calendarNavItems(calendars []models.Calendar) []navItem {
	items := make([]navItem, len(calendars))
	for i, c := range calendars {
		items[i] = navItem{label: c.Name}
	}
	return items
}

// journalNavItems builds nav items for the journal date row.
func journalNavItems(dates []string) []navItem {
	items := make([]navItem, len(dates))
	for i, d := range dates {
		items[i] = navItem{label: d}
	}
	return items
}

// --- Rendering ---

// renderRule draws a horizontal rule with a centered label:
//
//	——————————————————— label ———————————————————
func renderRule(width int, label string) string {
	if label == "" {
		return lipgloss.NewStyle().Foreground(colorMuted).Render(strings.Repeat("─", width))
	}
	padded := " " + label + " "
	padLen := lipgloss.Width(padded)
	ruleLen := max(width-padLen, 0)
	left := ruleLen / 2
	right := ruleLen - left
	line := strings.Repeat("─", left) + padded + strings.Repeat("─", right)
	return lipgloss.NewStyle().Foreground(colorMuted).Render(line)
}

// renderNavRow draws a row of nav items with the selected one bolded.
// If centered is true, the row is horizontally centered within width.
// When items overflow the available width, the row scrolls horizontally
// to keep the selected item visible and shows ‹/› indicators.
func renderNavRow(items []navItem, selected int, focused bool, width int, centered bool) string {
	const sep = "  "
	sepW := lipgloss.Width(sep)

	// Pre-render each item and measure its display width.
	type rendered struct {
		str string
		w   int
	}
	all := make([]rendered, len(items))
	totalW := 0
	for i, item := range items {
		text := item.label
		if item.icon != "" {
			text = item.icon + " " + text
		}

		var s string
		if i == selected {
			style := lipgloss.NewStyle().Bold(true)
			if focused {
				style = style.Foreground(colorPrimary)
			}
			s = style.Render(text)
		} else {
			s = lipgloss.NewStyle().Foreground(colorMuted).Render(text)
		}
		w := lipgloss.Width(s)
		all[i] = rendered{s, w}
		totalW += w
	}
	totalW += sepW * max(len(items)-1, 0) // separators

	// If everything fits, no scrolling needed.
	if totalW <= width {
		parts := make([]string, len(all))
		for i, r := range all {
			parts[i] = r.str
		}
		row := strings.Join(parts, sep)
		if centered {
			rowWidth := lipgloss.Width(row)
			pad := max((width-rowWidth)/2, 0)
			return strings.Repeat(" ", pad) + row
		}
		return row
	}

	// Scrolling: find the largest window of items around `selected` that fits.
	leftArrow := lipgloss.NewStyle().Foreground(colorMuted).Render("‹ ")
	rightArrow := lipgloss.NewStyle().Foreground(colorMuted).Render(" ›")
	arrowW := lipgloss.Width(leftArrow) // both arrows have the same width

	// Start with the selected item and expand outward.
	lo, hi := selected, selected
	usedW := all[selected].w

	for {
		expandedLeft, expandedRight := false, false

		// Try expanding left.
		if lo > 0 {
			need := sepW + all[lo-1].w
			reserveR := 0
			if hi < len(items)-1 {
				reserveR = arrowW
			}
			reserveL := 0
			if lo-1 > 0 {
				reserveL = arrowW
			}
			if usedW+need+reserveL+reserveR <= width {
				lo--
				usedW += need
				expandedLeft = true
			}
		}

		// Try expanding right.
		if hi < len(items)-1 {
			need := sepW + all[hi+1].w
			reserveL := 0
			if lo > 0 {
				reserveL = arrowW
			}
			reserveR := 0
			if hi+1 < len(items)-1 {
				reserveR = arrowW
			}
			if usedW+need+reserveL+reserveR <= width {
				hi++
				usedW += need
				expandedRight = true
			}
		}

		if !expandedLeft && !expandedRight {
			break
		}
	}

	// Build the visible row.
	var b strings.Builder
	if lo > 0 {
		b.WriteString(leftArrow)
	}
	for i := lo; i <= hi; i++ {
		if i > lo {
			b.WriteString(sep)
		}
		b.WriteString(all[i].str)
	}
	if hi < len(items)-1 {
		b.WriteString(rightArrow)
	}

	row := b.String()
	if centered {
		rowWidth := lipgloss.Width(row)
		pad := max((width-rowWidth)/2, 0)
		return strings.Repeat(" ", pad) + row
	}
	return row
}

// renderHeader renders the full 3-row navigation header.
func renderHeader(m *model) string {
	var b strings.Builder

	// Row 1: section rule + items
	sectionLabel := "HEY"
	b.WriteString(renderRule(m.width, sectionLabel))
	b.WriteString("\n")
	b.WriteString(renderNavRow(sectionItems, int(m.section), m.focus == rowSection, m.width, true))
	b.WriteString("\n")

	// Row 2: sub-nav rule + items (delegated to active section view)
	row2Items, row2Selected, row2Label, centered := m.activeView.SubnavItems()

	b.WriteString(renderRule(m.width, row2Label))
	b.WriteString("\n")
	if len(row2Items) > 0 {
		b.WriteString(renderNavRow(row2Items, row2Selected, m.focus == rowSubnav, m.width, centered))
		b.WriteString("\n")
	}

	// Separator
	b.WriteString(renderRule(m.width, ""))

	return b.String()
}
