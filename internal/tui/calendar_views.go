package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/terminal"
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
func splitRecordings(recs []Recording) (events, todos, habits []Recording) {
	// Doing a habit is a recording of its own — a `Calendar::Habit::Completion`
	// carrying nothing but the habit it belongs to, since HEY records the doing rather
	// than flagging the habit. So a completion marks the habit it names and is never
	// listed itself: left in, it read as a habit with no name and left every habit
	// looking undone.
	completed := make(map[int64]string)
	for _, r := range recs {
		if isHabitCompletion(r.Type) {
			completed[r.ParentID] = r.StartsAt
		}
	}

	for _, r := range recs {
		t := strings.ToLower(r.Type)
		switch {
		case isHabitCompletion(r.Type):
			// Already folded into the habit it completes.
		case strings.Contains(t, "todo"):
			todos = append(todos, r)
		case strings.Contains(t, "habit"):
			if done, ok := completed[r.ID]; ok {
				r.CompletedAt = done
			}
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

func isHabitCompletion(recordingType string) bool {
	t := strings.ToLower(recordingType)
	return strings.Contains(t, "habit") && strings.Contains(t, "completion")
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
func eventsByDate(events []Recording) map[string][]Recording {
	m := make(map[string][]Recording)
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

// dayLabelsFromRecordings builds a map of date → custom label from recordings
// that have a Label set (named days in HEY). A named day carries its label on
// whichever recording HEY hung it on, which can be a todo or a habit as much as
// an event, so every group the day holds is read.
func dayLabelsFromRecordings(groups ...[]Recording) map[string]string {
	labels := make(map[string]string)
	for _, group := range groups {
		for _, recording := range group {
			if recording.Label == "" {
				continue
			}
			day := parseEventTime(recording.StartsAt)
			if day.IsZero() {
				continue
			}
			// First label wins
			key := dateKey(day)
			if _, exists := labels[key]; !exists {
				labels[key] = recording.Label
			}
		}
	}
	return labels
}

// daysBetween counts whole calendar days from one date to another. The clock time
// and the hours in the days are beside the point: an hour lost or gained to a
// daylight saving shift in between must not move the count, which subtracting the
// timestamps and dividing by 24 hours does.
func daysBetween(from, to time.Time) int {
	fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	toDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
	return int(math.Round(toDay.Sub(fromDay).Hours() / 24))
}

// ============================================================
// Day View — hours as columns, event names rendered vertically
// ============================================================

// placedEvent stores an event's position in the day grid.
type placedEvent struct {
	rec      Recording
	startCol int
	endCol   int
	lane     int
}

// A to-do in HEY is not due at an hour, it is due around now, which is what the web app
// means by "Sometime this week". The day and the week both say it, since both are showing
// the same week's to-dos under a grid that is precise about time.
const todosSectionLabel = "Sometime this week"

// cellKind is what one cell of the day grid holds, and so which style draws it.
type cellKind int

const (
	cellEmpty cellKind = iota
	cellRule
	cellChrome
	cellTitle
)

// hourRule is dotted rather than solid so an hour's line reads as a guide behind the
// events and not as another box's border.
const hourRule = '┊'

func renderDayView(events, habits []Recording, anchor time.Time, width, height int) string {
	var b strings.Builder

	// The day borrows the mail list's vocabulary: chrome for the structure a reader
	// looks past — the hour axis, a box's border, a section's rule — and the bright
	// bold that a subject wears for the one thing on the row they came to read.
	muted := styleMuted
	chrome := lipgloss.NewStyle().Foreground(colorChrome)
	eventTitle := lipgloss.NewStyle().Foreground(colorBright).Bold(true)

	if len(habits) > 0 {
		b.WriteString(hintedSectionHeader("Habits", "b to manage", width))
		b.WriteString("\n")
		b.WriteString(renderHabitsRibbon(habits, width))
		b.WriteString("\n")
	}

	// A day ends where the next one begins, so the axis closes on another 00 with a
	// rule under it: twenty-four hours are twenty-five lines, and the day reads as a
	// span rather than as columns that stop. The two columns that last label needs are
	// what the hours are sized against.
	colWidth := max((width-2)/24, 3)
	daySpan := colWidth * 24
	gridWidth := daySpan + 1

	// The day names itself above its hours: the subnav carries the calendar and the
	// view mode, so which day this is has nowhere else to be said.
	b.WriteString(sectionHeader(anchor.Local().Format("Monday, January 2"), width))
	b.WriteString("\n")

	// Hour header
	var header strings.Builder
	for h := range 24 {
		fmt.Fprintf(&header, "%02d", h)
		if pad := colWidth - 2; pad > 0 {
			header.WriteString(strings.Repeat(" ", pad))
		}
	}
	header.WriteString("00")
	b.WriteString(chrome.Render(header.String()))
	b.WriteString("\n")

	// Separate timed and all-day events
	var timed, allDay []Recording
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

		startPos := (st.Hour()*60 + st.Minute()) * daySpan / (24 * 60)
		endPos := (et.Hour()*60 + et.Minute()) * daySpan / (24 * 60)
		if et.Day() != st.Day() || (et.Hour() == 0 && et.Minute() == 0 && et.After(st)) {
			endPos = daySpan
		}
		if endPos <= startPos {
			endPos = startPos + colWidth
		}
		startPos = min(startPos, daySpan-1)
		endPos = min(endPos, daySpan)
		if endPos-startPos < 3 {
			endPos = min(startPos+3, daySpan)
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

	// The grid fills the room the rest of the day view leaves it, so the hours reach
	// the bottom of the screen on a day with nothing on them.
	spent := 2 // the day's own header and the hour axis
	if len(habits) > 0 {
		spent += 2
	}
	if len(allDay) > 0 {
		spent += 1 + len(allDay)
	}
	b.WriteString(renderDayGrid(lanes, gridWidth, colWidth, height-spent, chrome, eventTitle, muted))

	// All-day events as full-width horizontal bars at the bottom
	if len(allDay) > 0 {
		b.WriteString(sectionHeader("All day", width))
		b.WriteString("\n")
		for _, e := range allDay {
			innerLen := gridWidth - 2
			title := truncateStr(terminal.SanitizeLine(e.Title), innerLen)
			fill := max(innerLen-lipgloss.Width(title), 0)
			b.WriteString(chrome.Render("[") + eventTitle.Render(title) +
				chrome.Render(strings.Repeat("─", fill)+"]"))
			b.WriteString("\n")
		}
	}

	// Every section here ends its own last line, so the day would otherwise carry a
	// blank line the viewport counts — one row of scroll on a day that fits exactly.
	return strings.TrimRight(b.String(), "\n")
}

// renderDayGrid draws the day's hours as one canvas: the lanes of events stacked down
// it, and an hour rule falling down every hour column no event stands on. The rules are
// the grid, not a decoration on the events, so a day with nothing on it still reads as
// a day. It is never shorter than the rows it is given, and grows past them for a day
// too full to fit, which is what the viewport scrolls.
func renderDayGrid(lanes [][]placedEvent, gridWidth, colWidth, rows int, chrome, title, muted lipgloss.Style) string {
	height := 0
	for _, lane := range lanes {
		height += laneHeight(lane)
	}
	height = max(height, rows, 1)

	// A 2D grid of runes and a parallel note of what each cell is: the empty grid
	// between events, an hour's rule, a box's own chrome, or a rune of its title.
	// The four are styled separately so an event's name stands out of its border the
	// way a subject stands out of the mail list's rules.
	grid := make([][]rune, height)
	cells := make([][]cellKind, height)
	for row := range height {
		grid[row] = make([]rune, gridWidth)
		cells[row] = make([]cellKind, gridWidth)
		for col := range gridWidth {
			grid[row][col] = ' '
		}
	}

	offset := 0
	for _, lane := range lanes {
		drawDayLane(grid, cells, lane, offset)
		offset += laneHeight(lane)
	}

	// The rules go in last and only where nothing else stands: a box is drawn over an
	// hour, never cut by it.
	for row := range height {
		for col := 0; col < gridWidth; col += colWidth {
			if cells[row][col] == cellEmpty {
				grid[row][col] = hourRule
				cells[row][col] = cellRule
			}
		}
	}

	styleFor := map[cellKind]lipgloss.Style{
		cellEmpty:  muted,
		cellRule:   muted,
		cellChrome: chrome,
		cellTitle:  title,
	}

	// Render row by row, batching consecutive cells of the same kind
	var b strings.Builder
	for row := range height {
		var seg strings.Builder
		kind := cellEmpty

		flush := func() {
			if s := seg.String(); s != "" {
				b.WriteString(styleFor[kind].Render(s))
				seg.Reset()
			}
		}

		for col := range gridWidth {
			if cells[row][col] != kind {
				flush()
				kind = cells[row][col]
			}
			seg.WriteRune(grid[row][col])
		}
		flush()
		b.WriteString("\n")
	}

	return b.String()
}

// laneHeight is the rows a lane needs: its longest title read downwards, between the
// top and bottom borders of its boxes.
func laneHeight(lane []placedEvent) int {
	longest := 0
	for _, pe := range lane {
		longest = max(longest, len([]rune(terminal.SanitizeLine(pe.rec.Title))))
	}
	return longest + 2
}

// drawDayLane draws one lane of non-overlapping events into the grid at rowOffset, as
// boxes with vertical (90-degree rotated) title text.
func drawDayLane(grid [][]rune, cells [][]cellKind, lane []placedEvent, rowOffset int) {
	top := rowOffset
	bottom := rowOffset + laneHeight(lane) - 1

	for _, pe := range lane {
		sc, ec := pe.startCol, pe.endCol
		boxW := ec - sc
		titleRunes := []rune(terminal.SanitizeLine(pe.rec.Title))

		// Top border: ┌──┐
		grid[top][sc] = '┌'
		cells[top][sc] = cellChrome
		for c := sc + 1; c < ec-1; c++ {
			grid[top][c] = '─'
			cells[top][c] = cellChrome
		}
		if boxW > 1 {
			grid[top][ec-1] = '┐'
			cells[top][ec-1] = cellChrome
		}

		// Middle rows: │c │  (vertical title text)
		for row := top + 1; row < bottom; row++ {
			grid[row][sc] = '│'
			cells[row][sc] = cellChrome
			if boxW > 1 {
				grid[row][ec-1] = '│'
				cells[row][ec-1] = cellChrome
			}
			// Title character
			titleIdx := row - top - 1
			if titleIdx < len(titleRunes) && sc+1 < ec-1 {
				grid[row][sc+1] = titleRunes[titleIdx]
				cells[row][sc+1] = cellTitle
			}
			// Fill inner space
			for c := sc + 2; c < ec-1; c++ {
				cells[row][c] = cellChrome
			}
		}

		// Bottom border: └──┘
		grid[bottom][sc] = '└'
		cells[bottom][sc] = cellChrome
		for c := sc + 1; c < ec-1; c++ {
			grid[bottom][c] = '─'
			cells[bottom][c] = cellChrome
		}
		if boxW > 1 {
			grid[bottom][ec-1] = '┘'
			cells[bottom][ec-1] = cellChrome
		}
	}
}

// =============================================
// Week View — 7 day columns with bordered grid
// =============================================

type weekDayInfo struct {
	date   time.Time
	habits []Recording
	events []Recording
	allDay []Recording
}

func renderWeekView(events, habits []Recording, anchor time.Time, firstWeekDay time.Weekday, width, _ int, dayLabels map[string]string) string {
	var b strings.Builder
	muted := styleMuted
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
		line := marker + " " + truncateStr(terminal.SanitizeLine(h.Title), width-2)
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
		lines = append(lines, bright.Render(truncateStr(terminal.SanitizeLine(e.Title), width)))
	}

	for _, e := range d.allDay {
		lines = append(lines, primary.Render(truncateStr(terminal.SanitizeLine(e.Title), width)))
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

func renderYearView(events []Recording, anchor time.Time, firstWeekDay time.Weekday, width, _ int, dayLabels map[string]string) string {
	var b strings.Builder
	muted := styleMuted
	bright := lipgloss.NewStyle().Foreground(colorBright)
	primary := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	faint := styleMuted.Foreground(colorMuted) // extra-dim filler days outside the year

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
func buildYearDayCell(d time.Time, dayEvents []Recording, colWidth, maxEvents int,
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
		title := truncateStr(terminal.SanitizeLine(dayEvents[i].Title), colWidth)
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

// renderHabitsRibbon is the day's habits, each wearing the ring HEY fills in when it is
// done, the color HEY gave it, and the emoji standing in for its icon.
func renderHabitsRibbon(habits []Recording, width int) string {
	return renderRibbon(habits, width, func(habit Recording) (string, lipgloss.Style, string) {
		return habitMarker(habit.CompletedAt != ""), habitMarkerStyle(habit.Color), habitLabel(habit)
	})
}

func renderTodosRibbon(todos []Recording, width int) string {
	return renderRibbon(todos, width, func(todo Recording) (string, lipgloss.Style, string) {
		label := terminal.SanitizeLine(todo.Title)
		if todo.CompletedAt != "" {
			return "■", styleMuted, label
		}
		return "□", lipgloss.NewStyle().Foreground(colorAlert).Bold(true), label
	})
}

// renderRibbon lays out one line of markers and labels in the mail list's vocabulary:
// something still waiting wears a bright label the way an unseen thread does, and
// something done is muted the way a seen one is. The marker and the label are the
// caller's, since a habit's ring is colored by the habit and carries its icon while a
// to-do's box is colored by whether it is waiting. What is left over at the end of the
// line is an ellipsis rather than a label cut mid-word.
func renderRibbon(items []Recording, width int, describe func(Recording) (string, lipgloss.Style, string)) string {
	var b strings.Builder
	used := 0
	for i, item := range items {
		marker, markerStyle, label := describe(item)
		labelStyle := lipgloss.NewStyle().Foreground(colorBright)
		if item.CompletedAt != "" {
			labelStyle = styleMuted
		}

		gap := ""
		if i > 0 {
			gap = "  "
		}
		if used+lipgloss.Width(gap+marker+" "+label) > width {
			if used < width {
				b.WriteString(styleMuted.Render("…"))
			}
			break
		}
		used += lipgloss.Width(gap + marker + " " + label)
		b.WriteString(gap + markerStyle.Render(marker) + " " + labelStyle.Render(label))
	}
	return b.String()
}

// --- Helpers ---

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func truncateStr(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+lipgloss.Width("…") > maxLen {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func centerPad(s string, width int) string {
	sw := lipgloss.Width(s)
	pad := width - sw
	if pad <= 0 {
		runes := []rune(s)
		for len(runes) > 0 && lipgloss.Width(string(runes)) > width {
			runes = runes[:len(runes)-1]
		}
		return string(runes)
	}
	left := pad / 2
	right := pad - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}
