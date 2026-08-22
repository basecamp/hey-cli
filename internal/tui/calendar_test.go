package tui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func testCalendars() []Calendar {
	return []Calendar{
		{ID: 10, Name: "Work"},
		{ID: 11, Name: "Personal", Personal: true},
	}
}

func testRecordings() []Recording {
	return []Recording{
		{ID: 200, Title: "Standup", StartsAt: "2025-03-01T09:00:00Z", EndsAt: "2025-03-01T09:30:00Z", Type: "CalendarEvent"},
		{ID: 201, Title: "Lunch", StartsAt: "2025-03-01T12:00:00Z", EndsAt: "2025-03-01T13:00:00Z", AllDay: false, Type: "CalendarEvent"},
		{ID: 202, Title: "Read a book", StartsAt: "2025-03-01T06:00:00Z", Type: "Habit"},
		{ID: 203, Title: "Buy milk", StartsAt: "2025-03-01T00:00:00Z", Type: "CalendarTodo"},
	}
}

func calendarWithRecordings() *calendarView {
	v := newCalendarView(testVC())
	v.Resize(80, 30)
	v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: testRecordings()})
	return v
}

// currentRequest tags a response as the answer to the read the calendar is
// waiting on, the way the fetch command that started it would.
func currentRequest(v *calendarView) requestResult {
	return requestResult{requestID: v.requests.id}
}

// --- Init ---

func TestCalendarViewInitFetchesCalendars(t *testing.T) {
	v := newCalendarView(testVC())
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init with no calendars should return a fetch command")
	}
	if !v.requests.loading || v.requests.kind != calendarRequestCalendars {
		t.Errorf("Init lane = loading:%v kind:%v", v.requests.loading, v.requests.kind)
	}
}

func TestCalendarViewInitRefetchesWhenLoaded(t *testing.T) {
	v := newCalendarView(testVC())
	v.calendars = testCalendars()
	v.calIndex = 0
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init with calendars should return a fetch command")
	}
}

// --- Update: message routing ---

func TestCalendarViewHandlesCalendarsLoaded(t *testing.T) {
	v := newCalendarView(testVC())
	_, consumed := v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	if !consumed {
		t.Error("calendarsLoadedMsg should be consumed")
	}
	if len(v.calendars) != 2 {
		t.Errorf("expected 2 calendars, got %d", len(v.calendars))
	}
}

func TestCalendarViewHandlesRecordingsLoaded(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(80, 30)
	v.calendars = testCalendars()
	v.requestRecordings(10)

	_, consumed := v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: testRecordings()})
	if !consumed {
		t.Error("recordingsLoadedMsg should be consumed")
	}
	if v.requests.loading || v.requests.kind != calendarRequestNone {
		t.Errorf("lane after recordings loaded = loading:%v kind:%v", v.requests.loading, v.requests.kind)
	}
	if len(v.events) != 2 {
		t.Errorf("expected 2 events, got %d", len(v.events))
	}
	if len(v.habits) != 1 {
		t.Errorf("expected 1 habit, got %d", len(v.habits))
	}
	if len(v.todos) != 1 {
		t.Errorf("expected 1 todo, got %d", len(v.todos))
	}
}

func TestCalendarViewHandlesIdentityLoaded(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(80, 30)

	_, consumed := v.Update(identityLoadedMsg{firstWeekDay: time.Sunday})
	if !consumed {
		t.Error("identityLoadedMsg should be consumed")
	}
	if v.firstWeekDay != time.Sunday {
		t.Errorf("firstWeekDay = %v, want Sunday", v.firstWeekDay)
	}
}

func TestCalendarViewIgnoresUnrelatedMessages(t *testing.T) {
	v := newCalendarView(testVC())
	_, consumed := v.Update(boxesLoadedMsg{})
	if consumed {
		t.Error("boxesLoadedMsg should not be consumed by calendarView")
	}
}

// --- View mode cycling ---

// The span is picked by number, as a box is in the mail list.
func TestCalendarViewModeNumberKeys(t *testing.T) {
	v := calendarWithRecordings()

	if v.viewMode != viewDay {
		t.Fatalf("initial mode = %v, want Day", v.viewMode)
	}
	for _, tt := range []struct {
		key  string
		want calendarViewMode
	}{{"2", viewWeek}, {"3", viewYear}, {"1", viewDay}} {
		if cmd := v.HandleContentKey(keyPress(tt.key)); cmd == nil {
			t.Errorf("%s did not read the span it switched to", tt.key)
		}
		if v.viewMode != tt.want {
			t.Errorf("after %s: mode = %v, want %v", tt.key, v.viewMode, tt.want)
		}
	}

	// The span already on screen is not read again.
	if cmd := v.HandleContentKey(keyPress("1")); cmd != nil {
		t.Error("1 read the day again while the day was already showing")
	}
}

// --- Subnav ---

// The row above the grid is the span, and the rule above it names the calendar being
// read and the key that changes it.
func TestCalendarViewSubnavItems(t *testing.T) {
	v := calendarWithRecordings()
	items, selected, label, centered := v.SubnavItems()

	if len(items) != 3 || items[0].label != "Day" || items[2].label != "Year" {
		t.Errorf("subnav items = %+v, want Day, Week and Year", items)
	}
	if selected != int(viewDay) {
		t.Errorf("selected = %d, want the day", selected)
	}
	if label != "Work · C to switch" {
		t.Errorf("label = %q", label)
	}
	if !centered {
		t.Error("calendar subnav should be centered")
	}

	// One calendar is nothing to switch between.
	v.calendars = v.calendars[:1]
	if _, _, label, _ = v.SubnavItems(); label != "Work" {
		t.Errorf("label with one calendar = %q", label)
	}
}

func TestCalendarViewSubnavLeftRightMovesTheSpan(t *testing.T) {
	v := calendarWithRecordings()

	if cmd := v.SubnavLeft(); cmd != nil || v.viewMode != viewDay {
		t.Errorf("SubnavLeft on the day = cmd:%v mode:%v, want the row to stop", cmd != nil, v.viewMode)
	}

	if cmd := v.SubnavRight(); cmd == nil || v.viewMode != viewWeek {
		t.Errorf("SubnavRight = cmd:%v mode:%v, want the week read", cmd != nil, v.viewMode)
	}
	if !v.requests.loading {
		t.Error("SubnavRight should start a read")
	}

	v.requests.finish(v.requests.id)
	v.SubnavRight()
	if v.viewMode != viewYear {
		t.Errorf("mode = %v, want the year", v.viewMode)
	}
	if cmd := v.SubnavRight(); cmd != nil || v.viewMode != viewYear {
		t.Errorf("SubnavRight on the year = cmd:%v mode:%v, want the row to stop", cmd != nil, v.viewMode)
	}
}

func TestCalendarPickerSwitchesTheCalendarItReads(t *testing.T) {
	v := calendarWithRecordings()
	v.vc.width, v.vc.height = 80, 20
	v.requests.finish(v.requests.id)

	if cmd := v.HandleContentKey(keyPress("C")); cmd != nil || v.calendarPicker == nil {
		t.Fatal("C did not open the calendars modal")
	}
	if !v.CapturingInput() {
		t.Error("the calendars modal does not hold the keys")
	}
	view := stripANSI(v.View())
	if !strings.Contains(view, "Calendars") || !strings.Contains(view, "Personal") {
		t.Errorf("the modal does not list the calendars: %q", view)
	}

	v.HandleContentKey(keyPress("down"))
	cmd := v.HandleContentKey(keyPress("enter"))
	if cmd == nil || v.calendarPicker != nil {
		t.Fatal("enter should read the chosen calendar and close the modal")
	}
	if v.calIndex != 1 {
		t.Errorf("calIndex = %d, want the second calendar", v.calIndex)
	}

	// Choosing the calendar already open is not a read.
	v.requests.finish(v.requests.id)
	v.HandleContentKey(keyPress("C"))
	if cmd := v.HandleContentKey(keyPress("enter")); cmd != nil {
		t.Error("choosing the open calendar read it again")
	}
}

func TestCalendarPickerStaysShutWithOneCalendar(t *testing.T) {
	v := calendarWithRecordings()
	v.calendars = v.calendars[:1]

	v.HandleContentKey(keyPress("C"))
	if v.calendarPicker != nil {
		t.Error("C opened a modal with nothing to choose between")
	}
}

// --- Request lane ---

func TestCalendarViewIgnoresStaleRecordings(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(80, 30)
	v.calendars = testCalendars()

	v.requestRecordings(10)
	stale := recordingsLoadedMsg{requestResult: currentRequest(v), recordings: testRecordings()}

	v.viewMode = viewWeek
	v.requestRecordings(10)
	fresh := recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 300, Title: "Design review", StartsAt: "2025-03-04T15:00:00Z", EndsAt: "2025-03-04T16:00:00Z", Type: "CalendarEvent"},
	}}

	v.Update(fresh)
	v.Update(stale)

	if len(v.events) != 1 || v.events[0].ID != 300 {
		t.Errorf("stale range replaced the current one: %+v", v.events)
	}
	if len(v.todos) != 0 || len(v.habits) != 0 {
		t.Errorf("stale range added recordings: todos=%+v habits=%+v", v.todos, v.habits)
	}
}

func TestCalendarViewIgnoresStaleCalendars(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(80, 30)

	v.requestCalendars()
	stale := calendarsLoadedMsg{requestResult: currentRequest(v), calendars: testCalendars()}

	v.requestCalendars()
	v.Update(calendarsLoadedMsg{requestResult: currentRequest(v), calendars: []Calendar{{ID: 12, Name: "Rob Zolkos", Personal: true}}})

	v.Update(stale)
	if len(v.calendars) != 1 || v.calendars[0].ID != 12 {
		t.Errorf("stale calendars replaced the current ones: %+v", v.calendars)
	}
}

func TestCalendarViewFailedReadFinishesTheLane(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(80, 30)
	v.requestCalendars()

	cmd, consumed := v.Update(calendarsLoadedMsg{requestResult: newRequestResult(v.requests.id, errors.New("calendars are away"))})
	if !consumed || cmd == nil {
		t.Fatalf("failed read = consumed:%v cmd:%v", consumed, cmd != nil)
	}
	if v.requests.loading || v.requests.kind != calendarRequestNone {
		t.Errorf("failed read left the lane open: loading:%v kind:%v", v.requests.loading, v.requests.kind)
	}
	if failure, ok := cmd().(errMsg); !ok || failure.err.Error() != "calendars are away" {
		t.Errorf("failed read reported %v", cmd())
	}
}

func TestCalendarViewKeysDoNotSupersedeAHabitWrite(t *testing.T) {
	v := calendarWithRecordings()
	v.calIndex = 1
	v.deleteHabit(Recording{ID: 202, Title: "Read a book"})

	requestID := v.requests.id
	if cmd := v.HandleContentKey(keyPress("v")); cmd != nil || v.viewMode != viewDay || v.requests.id != requestID {
		t.Errorf("key during a habit write = cmd:%v mode:%v request:%d", cmd != nil, v.viewMode, v.requests.id)
	}
	if !v.AccountSwitchBlocked() {
		t.Error("a habit write should hold the mail account")
	}
}

// --- Today ---

func TestCalendarViewFetchesAroundTheCurrentDay(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		queries = append(queries, req.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newCalendarView(vc)
	v.Resize(80, 30)

	firstDay := time.Date(2025, 3, 9, 23, 45, 0, 0, time.UTC)
	v.now = func() time.Time { return firstDay }
	v.requestRecordings(10)()

	v.now = func() time.Time { return firstDay.AddDate(0, 0, 1) }
	v.requestRecordings(10)()

	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 2 {
		t.Fatalf("queries = %v", queries)
	}
	if queries[0] != "ends_on=2025-03-10&starts_on=2025-03-09" {
		t.Errorf("first query = %q", queries[0])
	}
	if queries[1] != "ends_on=2025-03-11&starts_on=2025-03-10" {
		t.Errorf("second query still asks for the day the TUI opened on: %q", queries[1])
	}
}

func TestDaysBetweenIgnoresDaylightSavingShifts(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("time zone database unavailable: %v", err)
	}
	yearStart := time.Date(2025, 1, 1, 0, 0, 0, 0, location)
	afterTheShift := time.Date(2025, 3, 10, 0, 30, 0, 0, location)

	if days := daysBetween(yearStart, afterTheShift); days != 68 {
		t.Errorf("daysBetween = %d, want 68", days)
	}
	if weeks := daysBetween(yearStart, afterTheShift) / 7; weeks != 9 {
		t.Errorf("weeks = %d, want 9", weeks)
	}
}

func TestDayLabelsCoverTodosAndHabits(t *testing.T) {
	labels := dayLabelsFromRecordings(
		[]Recording{{ID: 200, StartsAt: "2025-03-01T09:00:00Z", Type: "CalendarEvent", Label: "Launch day"}},
		[]Recording{{ID: 203, StartsAt: "2025-03-02T00:00:00Z", Type: "CalendarTodo", Label: "Moving day"}},
		[]Recording{{ID: 202, StartsAt: "2025-03-03T06:00:00Z", Type: "Habit", Label: "Rest day"}},
	)
	want := map[string]string{
		"2025-03-01": "Launch day",
		"2025-03-02": "Moving day",
		"2025-03-03": "Rest day",
	}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v", labels)
	}
	for day, label := range want {
		if labels[day] != label {
			t.Errorf("label for %s = %q, want %q", day, labels[day], label)
		}
	}
}

// --- Moving between days ---

func TestCalendarStepsThroughDaysAndBackToToday(t *testing.T) {
	v := calendarWithRecordings()
	today := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)
	v.now = func() time.Time { return today }

	for _, key := range []string{"right", "n"} {
		v.HandleContentKey(keyPress(key))
	}
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 2)) {
		t.Errorf("two steps forward = %s, want %s", got.Format(time.DateOnly), today.AddDate(0, 0, 2).Format(time.DateOnly))
	}
	for _, key := range []string{"left", "p", "p"} {
		v.HandleContentKey(keyPress(key))
	}
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, -1)) {
		t.Errorf("three steps back = %s, want yesterday", got.Format(time.DateOnly))
	}

	// The day on screen stays the one that was read until the new one's answer lands —
	// a step is not a blank screen and a spinner.
	if v.Loading() {
		t.Error("stepping to another day claimed the spinner")
	}
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v)})
	if view := stripANSI(v.View()); !strings.Contains(view, today.AddDate(0, 0, -1).Format("Monday, January 2")) {
		t.Errorf("the view does not name the day it moved to: %q", view)
	}
	// The keys that move the day are said on the day's own line, and t joins them once
	// it would do something.
	if hint := v.stepHint(); hint != "←→ day · t today" {
		t.Errorf("hint on the date line = %q", hint)
	}

	// t goes back to following the clock rather than to the date that is today now,
	// so a view left open overnight keeps up.
	if cmd := v.HandleContentKey(keyPress("t")); cmd == nil {
		t.Error("t should read the day it returned to")
	}
	if !v.onToday() {
		t.Error("t did not return the view to today")
	}
	v.now = func() time.Time { return today.AddDate(0, 0, 1) }
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 1)) {
		t.Errorf("a view on today did not follow the clock: %s", got.Format(time.DateOnly))
	}
	if cmd := v.HandleContentKey(keyPress("t")); cmd != nil {
		t.Error("t read the day again while already on today")
	}
}

func TestCalendarStepsByTheUnitTheViewShows(t *testing.T) {
	v := calendarWithRecordings()
	today := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)
	v.now = func() time.Time { return today }

	v.viewMode = viewWeek
	v.HandleContentKey(keyPress("right"))
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 7)) {
		t.Errorf("a step in the week view = %s, want a week on", got.Format(time.DateOnly))
	}

	v.viewMode = viewYear
	v.HandleContentKey(keyPress("left"))
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 7).AddDate(-1, 0, 0)) {
		t.Errorf("a step in the year view = %s, want a year back", got.Format(time.DateOnly))
	}
}

// --- Habits ---

func TestHabitCompletionsMarkTheirHabitRatherThanListingThemselves(t *testing.T) {
	// HEY answers a habit and its completion as separate recordings, the completion
	// carrying no title and naming its habit in parent_id.
	events, todos, habits := splitRecordings([]Recording{
		{ID: 14796085, Title: "Read", Type: "Calendar::Habit", Icon: "read"},
		{ID: 14113260, Title: "Meditate", Type: "Calendar::Habit", Icon: "meditate"},
		{ID: 171477412, Type: "Calendar::Habit::Completion", ParentID: 14796085, StartsAt: "2026-08-22T00:00:00Z"},
	})

	if len(events) != 0 || len(todos) != 0 {
		t.Errorf("a completion is neither an event nor a to-do: events=%v todos=%v", events, todos)
	}
	if len(habits) != 2 {
		t.Fatalf("habits = %+v, want the two habits without the completion", habits)
	}
	if habits[0].Title != "Read" || habits[0].CompletedAt != "2026-08-22T00:00:00Z" {
		t.Errorf("the completed habit was not marked done: %+v", habits[0])
	}
	if habits[1].Title != "Meditate" || habits[1].CompletedAt != "" {
		t.Errorf("a habit with no completion was marked done: %+v", habits[1])
	}
}

func TestHabitsModalOpensOverTheCalendarAndManagesHabits(t *testing.T) {
	v := newCalendarView(testVC())
	v.vc.width, v.vc.height = 80, 20
	v.calendars = []Calendar{{ID: 10, Name: "Personal", Personal: true}}
	v.habits = []Recording{
		{ID: 7, Title: "Read before bed"},
		{ID: 8, Title: "Evening walk", CompletedAt: "2026-08-22T00:00:00Z"},
	}
	v.rebuildView()

	v.HandleContentKey(keyPress("b"))
	if v.habitPicker == nil || !v.CapturingInput() {
		t.Fatal("b did not open the habits modal")
	}

	view := stripANSI(v.View())
	if !strings.Contains(view, "Habits") || !strings.Contains(view, "○ Read before bed") {
		t.Errorf("modal did not list the habits: %q", view)
	}
	if !strings.Contains(view, "● Evening walk") {
		t.Errorf("a habit done today is not marked done: %q", view)
	}
	if !strings.Contains(view, "╭") {
		t.Errorf("habits modal drew no frame: %q", view)
	}

	v.HandleContentKey(keyPress("esc"))
	if v.habitPicker != nil {
		t.Error("esc did not close the habits modal")
	}
}

// --- Day view ---

func TestRibbonMarksWhatIsDoneAndStopsAtTheWidth(t *testing.T) {
	todos := []Recording{
		{ID: 1, Title: "Renew passport"},
		{ID: 2, Title: "Send the invoice", CompletedAt: "2026-08-24T08:00:00Z"},
	}

	ribbon := renderTodosRibbon(todos, 80)
	if stripANSI(ribbon) != "□ Renew passport  ■ Send the invoice" {
		t.Errorf("ribbon = %q", stripANSI(ribbon))
	}
	if !strings.Contains(ribbon, "\x1b[2m■") {
		t.Errorf("a finished to-do should be muted like a seen thread: %q", ribbon)
	}

	// A ribbon too long for its line ends in an ellipsis rather than a cut title,
	// and never draws past the width it was given.
	narrow := renderTodosRibbon(todos, 20)
	if stripANSI(narrow) != "□ Renew passport…" {
		t.Errorf("narrow ribbon = %q", stripANSI(narrow))
	}
	if width := lipgloss.Width(narrow); width > 20 {
		t.Errorf("narrow ribbon width = %d, want at most 20", width)
	}
}

func TestDayViewLabelsItsSections(t *testing.T) {
	events := []Recording{
		{ID: 1, Title: "Design review with Ryan", StartsAt: "2026-08-24T11:00:00Z", EndsAt: "2026-08-24T12:00:00Z"},
		{ID: 2, Title: "Dentist", AllDay: true},
	}
	habits := []Recording{{ID: 4, Title: "Read 20 pages"}}

	day := time.Date(2026, 8, 24, 9, 0, 0, 0, time.Local)
	view := stripANSI(renderDayView(events, habits, day, "←→ day", 100, 24))
	for _, label := range []string{"Habits", "Monday, August 24", "←→ day", "All day"} {
		if !strings.Contains(view, label) {
			t.Errorf("day view did not label its %q section: %q", label, view)
		}
	}
}

func TestDayViewRulesFallFromEveryHourWithoutCuttingIntoAnEvent(t *testing.T) {
	events := []Recording{
		{ID: 1, Title: "Design review with Ryan", StartsAt: "2026-08-24T11:00:00Z", EndsAt: "2026-08-24T12:00:00Z"},
	}
	day := time.Date(2026, 8, 24, 9, 0, 0, 0, time.Local)

	// 98 columns leaves 96 for the hours once the closing label has its two, which puts
	// an hour every four columns and the 11:00 event's box on the four from 44. The
	// day's header and the hour axis take the first two rows of the 40 it is given,
	// leaving 38 for the grid — more than the 25 rows the event's title needs read
	// downwards.
	lines := strings.Split(stripANSI(renderDayView(events, nil, day, "", 98, 40)), "\n")
	grid := lines[2:]
	if len(grid) != 38 {
		t.Fatalf("grid is %d rows of the 38 left to it: %q", len(grid), grid)
	}

	const eventRows = 25 // "Design review with Ryan" between its two borders
	for i, line := range grid {
		if cell := []rune(line)[0]; cell != hourRule {
			t.Errorf("grid row %d lost midnight's rule: %q", i, line)
		}
		cell := []rune(line)[44]
		if i < eventRows && cell == hourRule {
			t.Errorf("grid row %d ruled through the event's own box: %q", i, line)
		}
		if i >= eventRows && cell != hourRule {
			t.Errorf("grid row %d below the event kept no rule at 11: %q", i, line)
		}

		// Twenty-four hours are twenty-five rules: the day closes where the next one
		// starts. The event's box holds one of them for its own height.
		want := 25
		if i < eventRows {
			want = 24
		}
		if rules := strings.Count(line, string(hourRule)); rules != want {
			t.Errorf("grid row %d has %d rules, want %d: %q", i, rules, want, line)
		}
	}

	// The axis closes on the hour the day ends at.
	if axis := lines[1]; !strings.HasPrefix(axis, "00") || !strings.HasSuffix(axis, "23  00") {
		t.Errorf("hour axis does not run 00 through 00: %q", axis)
	}
}

func TestEmptyDayIsItsHoursRatherThanANotice(t *testing.T) {
	day := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)
	view := stripANSI(renderDayView(nil, nil, day, "", 96, 20))

	if strings.Contains(view, "no events") {
		t.Errorf("an empty day still announces itself: %q", view)
	}
	rows := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(rows) != 20 {
		t.Fatalf("empty day is %d rows of the 20 it was given: %q", len(rows), rows)
	}
	for i, row := range rows[2:] {
		if !strings.HasPrefix(row, string(hourRule)) {
			t.Errorf("grid row %d of an empty day is not ruled: %q", i, row)
		}
	}
}

// A day sized to the room it has must not scroll. It used to by exactly one row: every
// section ended its own last line, so the day carried a blank line after it that the
// viewport counted.
func TestDayThatFitsDoesNotScroll(t *testing.T) {
	v := newCalendarView(testVC())
	v.vc.width, v.vc.height = 90, 20
	v.habits = []Recording{{ID: 1, Title: "Read", Icon: "read", Color: "green"}}
	v.todos = []Recording{{ID: 2, Title: "Clean the attic"}}
	v.rebuildView()

	lines := strings.Count(v.contentVP.View(), "\n") + 1
	if lines != v.contentVP.Height() {
		t.Errorf("the day is %d lines in a %d row viewport", lines, v.contentVP.Height())
	}
	if !v.contentVP.AtBottom() {
		t.Error("a day that fits its viewport still has somewhere to scroll")
	}
}

func TestCalendarPinsTodosBelowTheGrid(t *testing.T) {
	v := newCalendarView(testVC())
	v.vc.width, v.vc.height = 80, 20
	v.todos = []Recording{{ID: 1, Title: "Renew passport"}}
	v.events = []Recording{
		{ID: 2, Title: "A design review long enough to fill the day view twice over",
			StartsAt: "2026-08-24T11:00:00Z", EndsAt: "2026-08-24T12:00:00Z"},
	}
	v.rebuildView()

	// The grid is taller than the screen, so the to-dos would scroll out of sight if
	// they were part of it. They are the last two rows of the view either way, and
	// the grid above them is what gave up the room.
	lines := strings.Split(stripANSI(v.View()), "\n")
	if len(lines) > 20 {
		t.Fatalf("view height = %d lines, want at most 20: %q", len(lines), lines)
	}
	if header := lines[len(lines)-2]; !strings.HasPrefix(header, todosSectionLabel) {
		t.Errorf("to-dos header is not the second-to-last row: %q", header)
	}
	if ribbon := lines[len(lines)-1]; !strings.Contains(ribbon, "Renew passport") {
		t.Errorf("to-dos ribbon is not the last row: %q", ribbon)
	}
	if v.contentVP.Height() != 16 {
		t.Errorf("grid height = %d, want 16 with two rows given to the to-dos", v.contentVP.Height())
	}

	// The year has no to-dos under it, so the grid gets those rows back.
	v.viewMode = viewYear
	v.rebuildView()
	if footer := v.todosFooter(); footer != "" {
		t.Errorf("year view drew a to-dos footer: %q", footer)
	}
}

// --- Help bindings ---

func TestCalendarViewHelpBindingsShowsViewToggle(t *testing.T) {
	v := calendarWithRecordings()
	v.calIndex = 1
	// The day view offers the span, the categories and the habits modal; creating,
	// editing and deleting a habit are the modal's own keys, and the keys that move the
	// day are on the day's own line rather than in here.
	bindings := v.HelpBindings()
	if len(bindings) != 3 {
		t.Fatalf("expected 3 bindings, got %d: %+v", len(bindings), bindings)
	}
	for _, want := range []string{"1-3", "c", "b"} {
		found := false
		for _, binding := range bindings {
			found = found || binding.key == want
		}
		if !found {
			t.Errorf("missing binding %q: %+v", want, bindings)
		}
	}

	// The week and the year have no date line, so the help bar carries their steps.
	v.viewMode = viewWeek
	for _, want := range []string{"←→", "1-3", "c"} {
		found := false
		for _, binding := range v.HelpBindings() {
			found = found || binding.key == want
		}
		if !found {
			t.Errorf("the week view is missing binding %q: %+v", want, v.HelpBindings())
		}
	}
	v.viewMode = viewDay

	v.HandleContentKey(keyPress("b"))
	for _, want := range []string{"↑↓", "a", "e", "x", "esc"} {
		found := false
		for _, binding := range v.HelpBindings() {
			found = found || binding.key == want
		}
		if !found {
			t.Errorf("habits modal is missing binding %q: %+v", want, v.HelpBindings())
		}
	}
}
