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
	sectionContacts
	sectionCalendar
	sectionJournal
)

// focusRow identifies which navigation row has keyboard focus.
type focusRow int

const (
	rowSection focusRow = iota // row 1: Mail / Contacts / Calendar / Journal
	rowSubnav                  // row 2: boxes / calendars / dates
	rowContent                 // content area
)

// navItem is a single item in a navigation row.
type navItem struct {
	shortcut string // Shift+letter shortcut, underlined inside the label
	label    string
}

// --- Row 1: sections (static) ---

var sectionItems = []navItem{
	{"M", "Mail"},
	{"O", "Contacts"},
	{"C", "Calendar"},
	{"J", "Journal"},
}

// sectionForShortcut returns the section for a Shift+letter shortcut, or -1.
func sectionForShortcut(key string) section {
	switch key {
	case "M":
		return sectionMail
	case "O":
		return sectionContacts
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
	key  string // number shortcut, matching the web app
}

var knownBoxes = []boxSpec{
	{"Imbox", "1"},
	{"The Feed", "2"},
	{"Paper Trail", "3"},
	{"Reply Later", "4"},
	{"Set Aside", "5"},
	{"Bubble up", "6"},
}

// orderBoxes sorts boxes by the preferred order. Known boxes appear first
// in their predefined order; unknown boxes are appended at the end.
func orderBoxes(boxes []models.Box) []models.Box {
	ordered := make([]models.Box, 0, len(boxes))
	type sourceKey struct {
		id   int64
		kind string
	}
	used := make(map[sourceKey]bool)

	// Add known boxes in preferred order
	for _, spec := range knownBoxes {
		for _, b := range boxes {
			key := sourceKey{id: b.ID, kind: b.Kind}
			if b.Kind != mailSourceKindFolder && strings.EqualFold(b.Name, spec.name) && !used[key] {
				ordered = append(ordered, b)
				used[key] = true
				break
			}
		}
	}
	// Append any remaining boxes
	for _, b := range boxes {
		key := sourceKey{id: b.ID, kind: b.Kind}
		if !used[key] {
			ordered = append(ordered, b)
		}
	}
	return ordered
}

// boxNavItems builds nav items for the box row, attaching shortcuts to known boxes.
func boxNavItems(boxes []models.Box) []navItem {
	items := make([]navItem, len(boxes))
	for i, b := range boxes {
		shortcut := ""
		for _, spec := range knownBoxes {
			if b.Kind != mailSourceKindFolder && strings.EqualFold(b.Name, spec.name) {
				shortcut = spec.key
				break
			}
		}
		label := b.Name
		if b.Kind == mailSourceKindFolder {
			label = terminalSafeFolderText(label)
		}
		items[i] = navItem{shortcut: shortcut, label: label}
	}
	return items
}

// boxForShortcut returns the index of the box matching a Shift+letter shortcut, or -1.
func boxForShortcut(key string, boxes []models.Box) int {
	for _, spec := range knownBoxes {
		if spec.key == key {
			for i, b := range boxes {
				if b.Kind != mailSourceKindFolder && strings.EqualFold(b.Name, spec.name) {
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
	if width <= 0 {
		return ""
	}
	rule := lipgloss.NewStyle().Foreground(colorChrome)
	if label == "" || width < 3 {
		return rule.Render(strings.Repeat("─", width))
	}
	label = truncateStr(label, width-2)
	padded := " " + label + " "
	padLen := lipgloss.Width(padded)
	ruleLen := max(width-padLen, 0)
	left := ruleLen / 2
	right := ruleLen - left
	line := strings.Repeat("─", left) + padded + strings.Repeat("─", right)
	return rule.Render(line)
}

// renderNavLabel renders a nav label in the given style, underlining the
// first occurrence of the shortcut letter (the Windows menu convention).
// A shortcut absent from the label — a number — is shown as an underlined
// prefix instead: "1 Imbox".
func renderNavLabel(label, shortcut string, base lipgloss.Style) string {
	if shortcut == "" {
		return base.Render(label)
	}
	idx := strings.Index(strings.ToLower(label), strings.ToLower(shortcut))
	if idx < 0 {
		return base.Underline(true).Render(shortcut) + base.Render(" "+label)
	}
	end := idx + len(shortcut)
	out := base.Underline(true).Render(label[idx:end])
	if idx > 0 {
		out = base.Render(label[:idx]) + out
	}
	if end < len(label) {
		out += base.Render(label[end:])
	}
	return out
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
		// Tabs are always bold. The selected tab uses the active color when
		// its row has focus and the less prominent primary color otherwise.
		style := lipgloss.NewStyle().Foreground(colorChrome).Bold(true)
		if i == selected {
			if focused {
				style = style.Foreground(colorActive)
			} else {
				style = style.Foreground(colorPrimary)
			}
		}
		s := renderNavLabel(item.label, item.shortcut, style)
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
			return centerText(row, width)
		}
		return row
	}

	// Scrolling: find the largest window of items around `selected` that fits.
	leftArrow := lipgloss.NewStyle().Foreground(colorChrome).Render("‹ ")
	rightArrow := lipgloss.NewStyle().Foreground(colorChrome).Render(" ›")
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
		return centerText(row, width)
	}
	return row
}

// centerText pads text so it sits in the middle of width.
func centerText(text string, width int) string {
	pad := max((width-lipgloss.Width(text))/2, 0)
	return strings.Repeat(" ", pad) + text
}

// renderTopRule draws the top rule with HEY centered and the account
// aligned to the right, both bold:
//
//	─────────── HEY ─────────── jz@example.com ──
func renderTopRule(width int, account string) string {
	ruleStyle := lipgloss.NewStyle().Foreground(colorChrome)
	labelStyle := lipgloss.NewStyle().Foreground(colorChrome).Bold(true)

	accountWidth := 0
	if account != "" {
		accountWidth = lipgloss.Width(account) + 2 // surrounding spaces
	}
	const heyWidth = 5 // " HEY "
	const tail = 2
	// Center HEY; when the account leaves no room, shift HEY left instead
	// of giving up the right alignment.
	left := min((width-heyWidth)/2, width-heyWidth-accountWidth-tail-1)
	mid := width - left - heyWidth - accountWidth - tail
	if left < 1 || mid < 1 {
		return renderRule(width, strings.TrimSuffix("HEY · "+account, " · "))
	}

	var b strings.Builder
	b.WriteString(ruleStyle.Render(strings.Repeat("─", left)))
	b.WriteString(" " + labelStyle.Render("HEY") + " ")
	b.WriteString(ruleStyle.Render(strings.Repeat("─", mid)))
	if account != "" {
		b.WriteString(" " + labelStyle.Render(account) + " ")
	}
	b.WriteString(ruleStyle.Render(strings.Repeat("─", tail)))
	return b.String()
}

// renderHeader renders the full 3-row navigation header.
func renderHeader(m *model) string {
	var b strings.Builder

	// Row 1: section rule + items
	b.WriteString(renderTopRule(m.width, m.mailAccount.label))
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
