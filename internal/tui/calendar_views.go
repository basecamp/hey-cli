package tui

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	habitvalues "github.com/basecamp/hey-cli/internal/habit"
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

// unit is what one step of ← or → moves in this view, as the help bar says it.
func (m calendarViewMode) unit() string {
	switch m {
	case viewDay:
		return "day"
	case viewWeek:
		return "week"
	case viewYear:
		return "year"
	}
	return "day"
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

// splitRecordings picks the events, the todos and the habits out of what a calendar holds,
// and takes nothing else. A calendar carries a day's own records alongside its events — a
// `Calendar::JournalEntry` where the day has been written on, a `Calendar::DayBackground`
// where it has a picture, a `Calendar::TimeTrack` where time was logged — and none of those
// belong on a grid of events. Naming what is wanted rather than skipping what is not is the
// point: the grid drew a journal entry as a bar of bare color, because it was an event by
// default and had no name to put in it.
//
// HEY's type names are namespaced — `Calendar::Event`, `Calendar::Habit::Completion` — which
// is why these match on a substring rather than on the whole string.
func splitRecordings(recs []Recording) (events, todos, habits, completions []Recording) {
	// Doing a habit is a recording of its own — a `Calendar::Habit::Completion`
	// carrying nothing but the habit it belongs to, since HEY records the doing rather
	// than flagging the habit. So a completion marks the habit it names and is never
	// listed itself: left in, it read as a habit with no name and left every habit
	// looking undone.
	//
	// The completions are answered as well as folded, because folding is lossy over more
	// than a day: a habit done on three days of a week has three of them, and only the
	// last would survive as a CompletedAt. The day view wants the fold, the week wants
	// the list.
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
			completions = append(completions, r)
		case strings.Contains(t, "todo"):
			todos = append(todos, r)
		case strings.Contains(t, "habit"):
			if done, ok := completed[r.ID]; ok {
				r.CompletedAt = done
			}
			habits = append(habits, r)
		case strings.Contains(t, "event"):
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

// dayCell is a cell's kind and, for the cells an event owns, the color of the calendar it
// is filed on. The color rides along with the kind so consecutive cells are batched by
// both: two events touching in the same row are two runs, not one.
type dayCell struct {
	kind  cellKind
	color string
}

// style is how a cell is drawn. An event is a block filled with its calendar's color and
// its title inverted over it, the way the web app draws its bars. Anything outside an event
// keeps the grid's own styles.
func (cell dayCell) style(_, _, muted lipgloss.Style) lipgloss.Style {
	switch cell.kind {
	case cellChrome:
		return lipgloss.NewStyle().Background(eventFillColor(cell.color))
	case cellTitle:
		return eventTextStyle(cell.color)
	default:
		return muted
	}
}

// eventFillColor is the block an event is drawn as. HEY leaves the personal calendar's
// color out of its JSON, so the reader's own events fall back to the theme's accent rather
// than to no fill at all — an unfilled event among filled ones reads as a different kind of
// thing rather than as one without a color.
func eventFillColor(calendarColor string) color.Color {
	// The theme's own value for the hue where it gave one, since that is what the reader
	// sees; the ANSI slot otherwise, so a terminal with no theme file still gets color.
	if hue, ok := colorHues[calendarColor]; ok {
		return hue
	}
	if slot, ok := heyColors[calendarColor]; ok {
		return slot
	}
	return colorPrimary
}

// eventTextStyle is a title over the fill of the calendar it is on, in whichever of the
// theme's paper and its own ink reads better there. Both candidates are the reader's own,
// so the answer is the theme's rather than a guess about it.
//
// It is a real measurement now because the theme says what its hues actually are. Against
// an ANSI slot's nominal value it could only ever be wrong: #000080 for blue reads as
// nearly black, so contrast picked white text for what a dark theme draws as a light
// periwinkle. With the theme's own #7d82d9 the same arithmetic picks dark text, which is
// what the eye wanted all along.
func eventTextStyle(calendarColor string) lipgloss.Style {
	fill := eventFillColor(calendarColor)

	text := colorPaper
	if ink := themeInk(); contrastRatio(ink, fill) > contrastRatio(colorPaper, fill) {
		text = ink
	}

	return lipgloss.NewStyle().Background(fill).Foreground(text).Bold(true)
}

// themeInk is the color the theme writes its own text in, which is the other candidate for
// a title on a fill.
func themeInk() color.Color {
	if colorBright != nil {
		return colorBright
	}
	return lipgloss.BrightWhite
}

// hourRule is dotted rather than solid so an hour's line reads as a guide behind the
// events and not as another box's border.
const hourRule = '┊'

func renderDayView(events, habits []Recording, anchor time.Time, hint string, width, height int) string {
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

	// The day names itself above its hours — the subnav carries the calendar and the
	// view mode, so which day this is has nowhere else to be said — and the keys that
	// move it sit on the same line, where the cover puts "x to peek".
	b.WriteString(hintedSectionHeader(anchor.Local().Format("Monday, January 2"), hint, width))
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
//
// The lanes share the height between them: one event on its own is as tall as the day, two
// that overlap take half each, three a third. An event's box was as tall as its title used
// to be, which left a short name looking like a short event and a long one looking like a
// long one — the box is the span, so its size has to come from the day rather than from
// the words in it.
func renderDayGrid(lanes [][]placedEvent, gridWidth, colWidth, rows int, chrome, title, muted lipgloss.Style) string {
	laneRows := shareDayRows(max(rows, 1), len(lanes))
	height := max(rows, 1)
	if total := sumOf(laneRows); total > height {
		height = total
	}

	// A 2D grid of runes and a parallel note of what each cell is: the empty grid
	// between events, an hour's rule, a box's own chrome, or a rune of its title —
	// carrying, for the last two, the color of the calendar the event is filed on.
	// They are styled separately so an event's name stands out of its border the way a
	// subject stands out of the mail list's rules.
	grid := make([][]rune, height)
	cells := make([][]dayCell, height)
	for row := range height {
		grid[row] = make([]rune, gridWidth)
		cells[row] = make([]dayCell, gridWidth)
		for col := range gridWidth {
			grid[row][col] = ' '
		}
	}

	offset := 0
	for i, lane := range lanes {
		drawDayLane(grid, cells, lane, offset, laneRows[i])
		offset += laneRows[i]
	}

	// The rules go in last and only where nothing else stands: a box is drawn over an
	// hour, never cut by it.
	for row := range height {
		for col := 0; col < gridWidth; col += colWidth {
			if cells[row][col].kind == cellEmpty {
				grid[row][col] = hourRule
				cells[row][col] = dayCell{kind: cellRule}
			}
		}
	}

	// Render row by row, batching consecutive cells that draw the same way
	var b strings.Builder
	for row := range height {
		var seg strings.Builder
		cell := dayCell{}

		flush := func() {
			if s := seg.String(); s != "" {
				b.WriteString(cell.style(chrome, title, muted).Render(s))
				seg.Reset()
			}
		}

		for col := range gridWidth {
			if cells[row][col] != cell {
				flush()
				cell = cells[row][col]
			}
			seg.WriteRune(grid[row][col])
		}
		flush()
		b.WriteString("\n")
	}

	return b.String()
}

// shareDayRows splits the grid's height between the lanes, giving the earlier ones the odd
// row left over. A lane never gets less than a box needs — two borders and a row of title —
// so a day with more overlapping events than rows grows the grid and scrolls instead of
// drawing boxes with no inside.
func shareDayRows(rows, lanes int) []int {
	if lanes == 0 {
		return nil
	}

	const minLaneRows = 3
	share := max(rows/lanes, minLaneRows)
	extra := 0
	if share > minLaneRows {
		extra = rows - share*lanes
	}

	shares := make([]int, lanes)
	for i := range shares {
		shares[i] = share
		if i < extra {
			shares[i]++
		}
	}
	return shares
}

func sumOf(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

// drawDayLane draws one lane of non-overlapping events into the grid at rowOffset, as
// boxes rows tall with vertical (90-degree rotated) title text. Every cell a box owns
// carries the color of the calendar the event is filed on, so which calendar an event
// belongs to is answered by looking at it.
func drawDayLane(grid [][]rune, cells [][]dayCell, lane []placedEvent, rowOffset, rows int) {
	top := rowOffset
	bottom := rowOffset + rows - 1

	for _, pe := range lane {
		sc, ec := pe.startCol, pe.endCol
		fill := dayCell{kind: cellChrome, color: pe.rec.CalendarColor}
		titled := dayCell{kind: cellTitle, color: pe.rec.CalendarColor}

		// The whole block is the event: filled with its calendar's color and carrying no
		// border, because the fill already says where it starts and stops. Borders drawn
		// in the color left the box reading as an outline around empty grid.
		for row := top; row <= bottom; row++ {
			for col := sc; col < ec; col++ {
				grid[row][col] = ' '
				cells[row][col] = fill
			}
		}

		// The title reads downwards, centred in the block: a name at the top of a
		// full-height column reads as an event that starts there and stops. It is
		// clipped rather than shrinking the block, since the block is the span.
		titleRunes := []rune(terminal.SanitizeLine(pe.rec.Title))
		rows := bottom - top + 1
		titleRow := top + max((rows-len(titleRunes))/2, 0)
		titleCol := sc + max((ec-sc-1)/2, 0)

		for i, r := range titleRunes {
			row := titleRow + i
			if row > bottom {
				break
			}
			grid[row][titleCol] = r
			cells[row][titleCol] = titled
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

func renderWeekView(events, habits, completions []Recording, anchor time.Time, firstWeekDay time.Weekday, width, _ int, dayLabels map[string]string) string {
	var b strings.Builder
	muted := styleMuted
	bright := lipgloss.NewStyle().Foreground(colorBright)

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

	// Which habits were done on which day. It comes from the completions rather than from
	// the habits: a habit's StartsAt is the day it was taken up — "Read" starts in 2024 —
	// so matching a habit against a day in this week never hit, and the week has been
	// showing none of them.
	byID := make(map[int64]Recording, len(habits))
	for _, habit := range habits {
		byID[habit.ID] = habit
	}
	for _, completion := range completions {
		done := parseEventTime(completion.StartsAt)
		if done.IsZero() {
			continue
		}
		habit, ok := byID[completion.ParentID]
		if !ok {
			continue
		}
		for i := range days {
			if sameDay(days[i].date, done) {
				days[i].habits = append(days[i].habits, habit)
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

	// The habits done each day, as a band across the top with a rule under it. Every day
	// gets the same number of rows so the rule is straight and a day's events start where
	// its neighbours' do — the band is a row of the grid, not something each column grew.
	// A week nobody kept a habit in has no band and no rule.
	if band := weekHabitBand(days, colWidth); len(band) > 0 {
		for _, row := range band {
			b.WriteString(sep)
			for _, cell := range row {
				b.WriteString(cell)
				b.WriteString(sep)
			}
			b.WriteString("\n")
		}
		b.WriteString(weekGridBorder("├", "┼", "┤", colWidth, muted))
		b.WriteString("\n")
	}

	// Build column content
	cols := make([][]string, 7)
	for i := range 7 {
		cols[i] = buildWeekDayColumn(days[i], colWidth, muted)
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

// weekHabitBand is the habits kept each day, as their icons alone — the week has room for
// seven days of them and none for seven days of names. It answers rows of seven cells, each
// already padded to the column, and nothing at all for a week with no habits kept in it.
func weekHabitBand(days []weekDayInfo, colWidth int) [][]string {
	// An icon is two cells wide and wants one between it and the next.
	const iconWidth = 3
	perRow := max(colWidth/iconWidth, 1)

	icons := make([][]string, len(days))
	rows := 0
	for i, day := range days {
		for _, habit := range day.habits {
			if emoji := habitvalues.EmojiFor(habit.Icon); emoji != "" {
				icons[i] = append(icons[i], habitMarkerStyle(habit.Color).Render(emoji))
			}
		}
		rows = max(rows, (len(icons[i])+perRow-1)/perRow)
	}
	if rows == 0 {
		return nil
	}

	band := make([][]string, rows)
	for row := range band {
		band[row] = make([]string, len(days))
		for day := range days {
			var cell strings.Builder
			for i := row * perRow; i < min((row+1)*perRow, len(icons[day])); i++ {
				cell.WriteString(icons[day][i])
				cell.WriteString(" ")
			}
			band[row][day] = padTo(cell.String(), colWidth)
		}
	}
	return band
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
func buildWeekDayColumn(d weekDayInfo, width int, muted lipgloss.Style) []string {
	// The day's habits are the band above the grid, not lines in the column.
	var lines []string

	for _, e := range d.events {
		timeStr := ""
		if len(e.StartsAt) >= 16 {
			timeStr = e.StartsAt[11:16]
		}
		if timeStr != "" {
			lines = append(lines, muted.Render(timeStr))
		}
		lines = append(lines, eventPill(e, width))
	}

	for _, e := range d.allDay {
		lines = append(lines, eventPill(e, width))
	}

	return lines
}

// eventPill is an event as the week and the year draw it: a bar filled with its calendar's
// color, its name inverted over it, padded to the cell so the fill reads as a block rather
// than as a highlight behind some words. It is the same thing the day view fills its column
// with, and the same thing the web app draws in all three.
func eventPill(event Recording, width int) string {
	title := truncateStr(terminal.SanitizeLine(event.Title), width)
	if pad := width - lipgloss.Width(title); pad > 0 {
		title += strings.Repeat(" ", pad)
	}

	return eventTextStyle(event.CalendarColor).Render(title)
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

// The year takes no day labels: a named day is a title on a recording, and a year read
// carries no recordings to hang one on. The web app's year does not show them either.
func renderYearView(events []Recording, anchor time.Time, firstWeekDay time.Weekday, width, _ int) string {
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
			cells[i] = buildYearDayCell(d, byDate[dateKey(d)], colWidth,
				sameDay(d, today), d.Year() == anchor.Year(), primary, bright, muted, faint)
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
// Line 0: day label. Lines 1+: one truncated title per event, all of them — the week's row
// is as tall as its busiest day, which is how the web app's grid behaves too.
func buildYearDayCell(d time.Time, dayEvents []Recording, colWidth int,
	isToday, isCurrentYear bool, primary, bright, muted, faint lipgloss.Style,
) []string {
	label := yearDayColumnLabel(d, false)

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

	// Event titles, each a bar in its calendar's color
	for _, event := range dayEvents {
		lines = append(lines, eventPill(event, colWidth))
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

// padTo fills a cell out to its column width, measuring what is visible so the styling a
// cell already carries is not counted.
func padTo(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
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
