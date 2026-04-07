package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

// calendarViewMode represents the calendar display mode.
type calendarViewMode int

const (
	viewDay calendarViewMode = iota
	viewWeek
	viewYear
)

func (m calendarViewMode) String() string {
	switch m {
	case viewDay:
		return "Day"
	case viewWeek:
		return "Week"
	case viewYear:
		return "Year"
	}
	return "Day"
}

func (m calendarViewMode) next() calendarViewMode {
	return (m + 1) % 3
}

// dateRangeForMode returns the start and end dates for fetching recordings.
func dateRangeForMode(mode calendarViewMode, anchor time.Time, firstWeekDay time.Weekday) (start, end time.Time) {
	loc := anchor.Location()
	switch mode {
	case viewDay:
		start = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, loc)
		end = start.AddDate(0, 0, 1)
	case viewWeek:
		start = weekStartDate(anchor, firstWeekDay)
		end = start.AddDate(0, 0, 7)
	case viewYear:
		yearStart := time.Date(anchor.Year(), 1, 1, 0, 0, 0, 0, loc)
		yearEnd := time.Date(anchor.Year()+1, 1, 1, 0, 0, 0, 0, loc)
		start = weekStartDate(yearStart, firstWeekDay)
		endWeekStart := weekStartDate(yearEnd.AddDate(0, 0, -1), firstWeekDay)
		end = endWeekStart.AddDate(0, 0, 7)
	}
	return
}

// weekStartDate returns the start of the week containing t.
func weekStartDate(t time.Time, firstDay time.Weekday) time.Time {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	diff := (int(d.Weekday()) - int(firstDay) + 7) % 7
	return d.AddDate(0, 0, -diff)
}

// splitRecordings separates recordings into events, todos, and habits.
// The API returns Type values like "CalendarEvent", "CalendarTodo", "Habit".
func splitRecordings(recs []models.Recording) (events, todos, habits []models.Recording) {
	for _, r := range recs {
		t := strings.ToLower(r.Type)
		switch {
		case strings.Contains(t, "todo"):
			todos = append(todos, r)
		case strings.Contains(t, "habit"):
			habits = append(habits, r)
		default:
			events = append(events, r)
		}
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].StartsAt < events[j].StartsAt
	})
	return
}

// parseEventTime parses a recording timestamp to time.Time.
func parseEventTime(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t
		}
	}
	return time.Time{}
}

// eventsByDate groups events by date (YYYY-MM-DD), expanding multi-day events
// so they appear on every day they span.
func eventsByDate(events []models.Recording) map[string][]models.Recording {
	m := make(map[string][]models.Recording)
	for _, e := range events {
		st := parseEventTime(e.StartsAt)
		if st.IsZero() {
			continue
		}
		et := parseEventTime(e.EndsAt)

		// Single-day or no end time: just the start date
		if et.IsZero() || !et.After(st) || dateKey(st) == dateKey(et) {
			m[dateKey(st)] = append(m[dateKey(st)], e)
			continue
		}

		// Multi-day: add to every day from start through end (inclusive of
		// end date only if it doesn't start at midnight, i.e. the event
		// actually occupies part of that day).
		d := time.Date(st.Year(), st.Month(), st.Day(), 0, 0, 0, 0, st.Location())
		endDay := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, et.Location())
		// If the event ends exactly at midnight, the last occupied day is the day before
		if et.Equal(endDay) {
			endDay = endDay.AddDate(0, 0, -1)
		}
		for !d.After(endDay) {
			m[dateKey(d)] = append(m[dateKey(d)], e)
			d = d.AddDate(0, 0, 1)
		}
	}
	return m
}

func dateKey(t time.Time) string {
	return t.Format("2006-01-02")
}

// dayLabelsFromEvents builds a map of date → custom label from recordings
// that have a Label set (named days in HEY).
func dayLabelsFromEvents(events []models.Recording) map[string]string {
	m := make(map[string]string)
	for _, e := range events {
		if e.Label == "" {
			continue
		}
		t := parseEventTime(e.StartsAt)
		if t.IsZero() {
			continue
		}
		// First label wins
		key := dateKey(t)
		if _, exists := m[key]; !exists {
			m[key] = e.Label
		}
	}
	return m
}

// ============================================================
// Day View — hours as columns, event names rendered vertically
// ============================================================

// placedEvent stores an event's position in the day grid.
type placedEvent struct {
	rec      models.Recording
	startCol int
	endCol   int
	lane     int
}

func renderDayView(events, todos, habits []models.Recording, _ time.Time, width, _ int) string {
	var b strings.Builder
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	primary := lipgloss.NewStyle().Foreground(colorPrimary)

	// Habits ribbon above columns
	if len(habits) > 0 {
		b.WriteString(renderHabitsRibbon(habits, width))
		b.WriteString("\n")
	}

	colWidth := max(width/24, 3)
	gridWidth := colWidth * 24

	// Hour header
	var header strings.Builder
	for h := range 24 {
		label := fmt.Sprintf("%02d", h)
		pad := colWidth - 2
		header.WriteString(label)
		if pad > 0 {
			header.WriteString(strings.Repeat(" ", pad))
		}
	}
	b.WriteString(muted.Render(header.String()))
	b.WriteString("\n")

	// Separate timed and all-day events
	var timed, allDay []models.Recording
	for _, e := range events {
		if e.AllDay {
			allDay = append(allDay, e)
		} else {
			timed = append(timed, e)
		}
	}

	// Place events into lanes (non-overlapping groups)
	placed := make([]placedEvent, 0, len(timed))
	for _, e := range timed {
		st := parseEventTime(e.StartsAt)
		et := parseEventTime(e.EndsAt)
		if st.IsZero() {
			continue
		}
		if et.IsZero() || !et.After(st) {
			et = st.Add(time.Hour)
		}

		startPos := (st.Hour()*60 + st.Minute()) * gridWidth / (24 * 60)
		endPos := (et.Hour()*60 + et.Minute()) * gridWidth / (24 * 60)
		if et.Day() != st.Day() || (et.Hour() == 0 && et.Minute() == 0 && et.After(st)) {
			endPos = gridWidth
		}
		if endPos <= startPos {
			endPos = startPos + colWidth
		}
		startPos = min(startPos, gridWidth-1)
		endPos = min(endPos, gridWidth)
		if endPos-startPos < 3 {
			endPos = min(startPos+3, gridWidth)
		}

		placed = append(placed, placedEvent{rec: e, startCol: startPos, endCol: endPos})
	}

	// Assign lanes: find the lowest lane where the event doesn't overlap
	laneEnds := []int{} // tracks the rightmost endCol in each lane
	for i := range placed {
		assigned := false
		for l, laneEnd := range laneEnds {
			if placed[i].startCol >= laneEnd {
				placed[i].lane = l
				laneEnds[l] = placed[i].endCol
				assigned = true
				break
			}
		}
		if !assigned {
			placed[i].lane = len(laneEnds)
			laneEnds = append(laneEnds, placed[i].endCol)
		}
	}

	// Group events by lane
	numLanes := len(laneEnds)
	lanes := make([][]placedEvent, numLanes)
	for _, pe := range placed {
		lanes[pe.lane] = append(lanes[pe.lane], pe)
	}

	// Render each lane as a vertical band with boxes and rotated titles
	for _, lane := range lanes {
		b.WriteString(renderDayLane(lane, gridWidth, primary, muted))
	}

	if len(timed) == 0 && len(allDay) == 0 {
		b.WriteString(muted.Render("  (no events)"))
		b.WriteString("\n")
	}

	// All-day events as full-width horizontal bars at the bottom
	if len(allDay) > 0 {
		b.WriteString(muted.Render(strings.Repeat("─", width)))
		b.WriteString("\n")
		for _, e := range allDay {
			title := e.Title
			innerLen := gridWidth - 2
			if len(title) > innerLen {
				title = truncateStr(title, innerLen)
			}
			fill := max(innerLen-len(title), 0)
			box := "[" + title + strings.Repeat("─", fill) + "]"
			b.WriteString(primary.Render(box))
			b.WriteString("\n")
		}
	}

	// Todos ribbon
	if len(todos) > 0 {
		b.WriteString(muted.Render(strings.Repeat("─", width)))
		b.WriteString("\n")
		b.WriteString(renderTodosRibbon(todos, width))
		b.WriteString("\n")
	}

	return b.String()
}

// renderDayLane renders one lane of non-overlapping events as boxes with
// vertical (90-degree rotated) title text.
func renderDayLane(lane []placedEvent, gridWidth int, primary, muted lipgloss.Style) string {
	if len(lane) == 0 {
		return ""
	}

	// Find the tallest title to determine band height
	maxTitle := 0
	for _, pe := range lane {
		if len([]rune(pe.rec.Title)) > maxTitle {
			maxTitle = len([]rune(pe.rec.Title))
		}
	}
	bandHeight := maxTitle + 2 // top border + title rows + bottom border

	// Build a 2D grid of runes and a parallel "styled" flag
	grid := make([][]rune, bandHeight)
	isBox := make([][]bool, bandHeight)
	for row := range bandHeight {
		grid[row] = make([]rune, gridWidth)
		isBox[row] = make([]bool, gridWidth)
		for col := range gridWidth {
			grid[row][col] = ' '
		}
	}

	// Draw each event box
	for _, pe := range lane {
		sc, ec := pe.startCol, pe.endCol
		boxW := ec - sc
		titleRunes := []rune(pe.rec.Title)

		// Top border: ┌──┐
		grid[0][sc] = '┌'
		isBox[0][sc] = true
		for c := sc + 1; c < ec-1; c++ {
			grid[0][c] = '─'
			isBox[0][c] = true
		}
		if boxW > 1 {
			grid[0][ec-1] = '┐'
			isBox[0][ec-1] = true
		}

		// Middle rows: │c │  (vertical title text)
		for row := 1; row < bandHeight-1; row++ {
			grid[row][sc] = '│'
			isBox[row][sc] = true
			if boxW > 1 {
				grid[row][ec-1] = '│'
				isBox[row][ec-1] = true
			}
			// Title character
			titleIdx := row - 1
			if titleIdx < len(titleRunes) && sc+1 < ec-1 {
				grid[row][sc+1] = titleRunes[titleIdx]
				isBox[row][sc+1] = true
			}
			// Fill inner space
			for c := sc + 2; c < ec-1; c++ {
				isBox[row][c] = true
			}
		}

		// Bottom border: └──┘
		grid[bandHeight-1][sc] = '└'
		isBox[bandHeight-1][sc] = true
		for c := sc + 1; c < ec-1; c++ {
			grid[bandHeight-1][c] = '─'
			isBox[bandHeight-1][c] = true
		}
		if boxW > 1 {
			grid[bandHeight-1][ec-1] = '┘'
			isBox[bandHeight-1][ec-1] = true
		}
	}

	// Render the grid row by row, batching consecutive styled/unstyled segments
	var b strings.Builder
	for row := range bandHeight {
		var seg strings.Builder
		inStyled := false

		flush := func() {
			s := seg.String()
			if s == "" {
				return
			}
			if inStyled {
				b.WriteString(primary.Render(s))
			} else {
				b.WriteString(muted.Render(s))
			}
			seg.Reset()
		}

		for col := range gridWidth {
			styled := isBox[row][col]
			if styled != inStyled {
				flush()
				inStyled = styled
			}
			seg.WriteRune(grid[row][col])
		}
		flush()
		// Trim trailing spaces
		b.WriteString("\n")
	}

	return b.String()
}

// =============================================
// Week View — 7 day columns with bordered grid
// =============================================

type weekDayInfo struct {
	date   time.Time
	habits []models.Recording
	events []models.Recording
	allDay []models.Recording
}

func renderWeekView(events, todos, habits []models.Recording, anchor time.Time, firstWeekDay time.Weekday, width, _ int, dayLabels map[string]string) string {
	var b strings.Builder
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	bright := lipgloss.NewStyle().Foreground(colorBright)
	primary := lipgloss.NewStyle().Foreground(colorPrimary)

	ws := weekStartDate(anchor, firstWeekDay)

	colWidth := (width - 8) / 7
	if colWidth < 8 {
		colWidth = 8
	}

	byDate := eventsByDate(events)

	days := make([]weekDayInfo, 7)
	for i := range 7 {
		d := ws.AddDate(0, 0, i)
		days[i] = weekDayInfo{date: d}

		dateKey := d.Format("2006-01-02")
		for _, e := range byDate[dateKey] {
			if e.AllDay {
				days[i].allDay = append(days[i].allDay, e)
			} else {
				days[i].events = append(days[i].events, e)
			}
		}
	}

	// Assign habits to their dates
	for _, h := range habits {
		ht := parseEventTime(h.StartsAt)
		if ht.IsZero() {
			continue
		}
		for i := range days {
			if sameDay(days[i].date, ht) {
				days[i].habits = append(days[i].habits, h)
			}
		}
	}

	sep := muted.Render("│")

	// Top border
	b.WriteString(weekGridBorder("┌", "┬", "┐", colWidth, muted))
	b.WriteString("\n")

	// Column headers
	b.WriteString(sep)
	for i := range 7 {
		label := dayLabelOrDefault(days[i].date, i == 0, dayLabels, weekDayColumnLabel)
		padded := centerPad(label, colWidth)
		b.WriteString(bright.Render(padded))
		b.WriteString(sep)
	}
	b.WriteString("\n")

	// Header separator
	b.WriteString(weekGridBorder("├", "┼", "┤", colWidth, muted))
	b.WriteString("\n")

	// Build column content
	cols := make([][]string, 7)
	for i := range 7 {
		cols[i] = buildWeekDayColumn(days[i], colWidth, primary, bright, muted)
	}

	maxH := 0
	for _, col := range cols {
		if len(col) > maxH {
			maxH = len(col)
		}
	}
	if maxH == 0 {
		maxH = 1
	}

	// Render rows
	for row := range maxH {
		b.WriteString(sep)
		for i := range 7 {
			if row < len(cols[i]) {
				line := cols[i][row]
				pad := colWidth - lipgloss.Width(line)
				b.WriteString(line)
				if pad > 0 {
					b.WriteString(strings.Repeat(" ", pad))
				}
			} else {
				b.WriteString(strings.Repeat(" ", colWidth))
			}
			b.WriteString(sep)
		}
		b.WriteString("\n")
	}

	// Bottom border
	b.WriteString(weekGridBorder("└", "┴", "┘", colWidth, muted))
	b.WriteString("\n")

	// Todos ribbon
	if len(todos) > 0 {
		b.WriteString(renderTodosRibbon(todos, width))
		b.WriteString("\n")
	}

	return b.String()
}

func weekGridBorder(left, mid, right string, colWidth int, muted lipgloss.Style) string {
	var s strings.Builder
	s.WriteString(muted.Render(left))
	for i := range 7 {
		s.WriteString(muted.Render(strings.Repeat("─", colWidth)))
		if i < 6 {
			s.WriteString(muted.Render(mid))
		}
	}
	s.WriteString(muted.Render(right))
	return s.String()
}

// buildWeekDayColumn returns styled lines for one day column.
// Order: habits at top, timed events in the middle, all-day at bottom.
func buildWeekDayColumn(d weekDayInfo, width int, primary, bright, muted lipgloss.Style) []string {
	var lines []string

	for _, h := range d.habits {
		marker := "○"
		if h.CompletedAt != "" {
			marker = "●"
		}
		line := marker + " " + truncateStr(h.Title, width-2)
		lines = append(lines, muted.Render(line))
	}

	for _, e := range d.events {
		timeStr := ""
		if len(e.StartsAt) >= 16 {
			timeStr = e.StartsAt[11:16]
		}
		if timeStr != "" {
			lines = append(lines, muted.Render(timeStr))
		}
		lines = append(lines, bright.Render(truncateStr(e.Title, width)))
	}

	for _, e := range d.allDay {
		lines = append(lines, primary.Render(truncateStr(e.Title, width)))
	}

	return lines
}

// weekDayColumnLabel returns the header label for a week column.
func weekDayColumnLabel(d time.Time, isFirstCol bool) string {
	dayName := strings.ToUpper(d.Weekday().String()[:3])
	dayNum := d.Day()

	if dayNum == 1 {
		monthName := strings.ToUpper(d.Month().String()[:3])
		return fmt.Sprintf("%s %s %d", monthName, dayName, dayNum)
	}
	if isFirstCol {
		monthName := strings.ToUpper(d.Month().String()[:3])
		return fmt.Sprintf("%s %s %d", monthName, dayName, dayNum)
	}
	return fmt.Sprintf("%s %d", dayName, dayNum)
}

// ===============================================
// Year View — bordered grid, one box per day
// ===============================================

func renderYearView(events []models.Recording, anchor time.Time, firstWeekDay time.Weekday, width, _ int, dayLabels map[string]string) string {
	var b strings.Builder
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	bright := lipgloss.NewStyle().Foreground(colorBright)
	primary := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	faint := lipgloss.NewStyle().Foreground(colorMuted).Faint(true)

	loc := anchor.Location()
	yearStart := time.Date(anchor.Year(), 1, 1, 0, 0, 0, 0, loc)
	yearEnd := time.Date(anchor.Year()+1, 1, 1, 0, 0, 0, 0, loc)
	gridStart := weekStartDate(yearStart, firstWeekDay)
	gridEndWeek := weekStartDate(yearEnd.AddDate(0, 0, -1), firstWeekDay)
	gridEnd := gridEndWeek.AddDate(0, 0, 7)

	byDate := eventsByDate(events)

	colWidth := max((width-8)/7, 9)
	maxEventsPerCell := 2 // show at most 2 event titles per cell

	sep := muted.Render("│")

	// Top border
	b.WriteString(weekGridBorder("┌", "┬", "┐", colWidth, muted))
	b.WriteString("\n")

	// Weekday header row
	b.WriteString(sep)
	for i := range 7 {
		wd := time.Weekday((int(firstWeekDay) + i) % 7)
		name := strings.ToUpper(wd.String()[:3])
		padded := centerPad(name, colWidth)
		b.WriteString(muted.Render(padded))
		b.WriteString(sep)
	}
	b.WriteString("\n")

	// Grid rows — one multi-line row per week
	today := time.Now()
	d := gridStart
	for d.Before(gridEnd) {
		b.WriteString(weekGridBorder("├", "┼", "┤", colWidth, muted))
		b.WriteString("\n")

		// Build cell content for each day in the week
		weekDates := make([]time.Time, 7)
		cells := make([][]string, 7)
		for i := range 7 {
			weekDates[i] = d
			cells[i] = buildYearDayCell(d, byDate[dateKey(d)], colWidth, maxEventsPerCell,
				sameDay(d, today), d.Year() == anchor.Year(), primary, bright, muted, faint, dayLabels)
			d = d.AddDate(0, 0, 1)
		}

		// Find tallest cell
		maxH := 0
		for _, cell := range cells {
			if len(cell) > maxH {
				maxH = len(cell)
			}
		}
		if maxH == 0 {
			maxH = 1
		}

		// Render rows
		for row := range maxH {
			b.WriteString(sep)
			for i := range 7 {
				if row < len(cells[i]) {
					line := cells[i][row]
					pad := colWidth - lipgloss.Width(line)
					b.WriteString(line)
					if pad > 0 {
						b.WriteString(strings.Repeat(" ", pad))
					}
				} else {
					b.WriteString(strings.Repeat(" ", colWidth))
				}
				b.WriteString(sep)
			}
			b.WriteString("\n")
		}
	}

	// Bottom border
	b.WriteString(weekGridBorder("└", "┴", "┘", colWidth, muted))
	b.WriteString("\n")

	return b.String()
}

// buildYearDayCell returns styled lines for one day cell in the year grid.
// Line 0: day label. Lines 1+: truncated event titles.
func buildYearDayCell(d time.Time, dayEvents []models.Recording, colWidth, maxEvents int,
	isToday, isCurrentYear bool, primary, bright, muted, faint lipgloss.Style,
	dayLabels map[string]string,
) []string {
	label := dayLabelOrDefault(d, false, dayLabels, yearDayColumnLabel)

	// Pick the style for the header line
	headerStyle := muted
	switch {
	case isToday:
		headerStyle = primary
	case len(dayEvents) > 0 && isCurrentYear:
		headerStyle = bright
	case !isCurrentYear:
		headerStyle = faint
	}

	lines := []string{headerStyle.Render(truncateStr(label, colWidth))}

	if !isCurrentYear {
		return lines
	}

	// Event titles
	shown := min(len(dayEvents), maxEvents)
	for i := range shown {
		title := truncateStr(dayEvents[i].Title, colWidth)
		lines = append(lines, bright.Render(title))
	}
	if len(dayEvents) > maxEvents {
		more := fmt.Sprintf("+%d more", len(dayEvents)-maxEvents)
		lines = append(lines, muted.Render(truncateStr(more, colWidth)))
	}

	return lines
}

// yearDayColumnLabel returns the default label for a day in the year view.
func yearDayColumnLabel(d time.Time, _ bool) string {
	dayName := strings.ToUpper(d.Weekday().String()[:3])
	dayNum := d.Day()

	if dayNum == 1 {
		monthName := strings.ToUpper(d.Month().String()[:3])
		return fmt.Sprintf("%s %s %d", monthName, dayName, dayNum)
	}
	return fmt.Sprintf("%s %d", dayName, dayNum)
}

// dayLabelOrDefault returns the custom day label if one exists, otherwise
// falls back to the provided default label function.
func dayLabelOrDefault(d time.Time, isFirstCol bool, dayLabels map[string]string, fallback func(time.Time, bool) string) string {
	if label, ok := dayLabels[dateKey(d)]; ok {
		return label
	}
	return fallback(d, isFirstCol)
}

// --- Ribbons ---

func renderHabitsRibbon(habits []models.Recording, width int) string {
	parts := make([]string, 0, len(habits))
	for _, h := range habits {
		marker := "○"
		if h.CompletedAt != "" {
			marker = "●"
		}
		parts = append(parts, marker+" "+h.Title)
	}
	ribbon := strings.Join(parts, "  ")
	if lipgloss.Width(ribbon) > width {
		runes := []rune(ribbon)
		for lipgloss.Width(string(runes)) > width-1 && len(runes) > 0 {
			runes = runes[:len(runes)-1]
		}
		ribbon = string(runes) + "…"
	}
	return ribbon
}

func renderTodosRibbon(todos []models.Recording, width int) string {
	parts := make([]string, 0, len(todos))
	for _, t := range todos {
		marker := "□"
		if t.CompletedAt != "" {
			marker = "■"
		}
		parts = append(parts, marker+" "+t.Title)
	}
	ribbon := strings.Join(parts, "  ")
	if lipgloss.Width(ribbon) > width {
		runes := []rune(ribbon)
		for lipgloss.Width(string(runes)) > width-1 && len(runes) > 0 {
			runes = runes[:len(runes)-1]
		}
		ribbon = string(runes) + "…"
	}
	return ribbon
}

// --- Helpers ---

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func truncateStr(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return s[:maxLen-1] + "…"
}

func centerPad(s string, width int) string {
	pad := width - len(s)
	if pad <= 0 {
		return s[:width]
	}
	left := pad / 2
	right := pad - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}
