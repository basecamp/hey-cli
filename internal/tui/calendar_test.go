package tui

import (
	"errors"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// The personal calendar carries no name and no color, which is how HEY serves it — the
// web app labels that row from the identity instead.
func testCalendars() []Calendar {
	return []Calendar{
		{ID: 10, Name: "Design Team", Color: "teal"},
		{ID: 11, Personal: true},
	}
}

func testSelection(ids ...int64) map[int64]bool {
	return selectionSet(ids)
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
	v.requestRecordings()

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

// The row above the grid is the span, numbered as the boxes are, and the rule above it
// names the one that is on.
func TestCalendarViewSubnavItems(t *testing.T) {
	v := calendarWithRecordings()
	items, selected, label, centered := v.SubnavItems()

	if len(items) != 3 || items[0].label != "Day" || items[2].label != "Year" {
		t.Errorf("subnav items = %+v, want Day, Week and Year", items)
	}
	// Each span wears its own number, as the boxes do, which is why the help bar does
	// not carry them.
	for i, want := range []string{"1", "2", "3"} {
		if items[i].shortcut != want {
			t.Errorf("%s tab shortcut = %q, want %q", items[i].label, items[i].shortcut, want)
		}
	}
	if selected != int(viewDay) {
		t.Errorf("selected = %d, want the day", selected)
	}
	// The rule names the span that is on, as the box row's rule names the open box.
	if label != "Day" {
		t.Errorf("label = %q", label)
	}
	if !centered {
		t.Error("calendar subnav should be centered")
	}

	v.viewMode = viewYear
	if _, selected, label, _ = v.SubnavItems(); label != "Year" || selected != int(viewYear) {
		t.Errorf("year row = selected:%d label:%q", selected, label)
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

// --- Year view ---

// The year is read as a year, not as the recordings inside it: HEY answers a grid, and one
// request draws it.
func TestCalendarYearReadsTheYearItself(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		paths = append(paths, req.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"starts_at":"2026-01-01T00:00:00Z","ends_at":"2026-12-31T23:59:59Z","kind":"year",
			"padding_days_count":3,
			"days":[{"starts_at":"2026-01-01T00:00:00Z","backgrounded":false},
			        {"starts_at":"2026-01-02T00:00:00Z","backgrounded":true}],
			"spanned_events":[{"id":1,"type":"CalendarEvent","title":"Summer break","all_day":true,
			                   "starts_at":"2026-07-06T00:00:00Z","ends_at":"2026-07-17T23:59:59Z"}]}`))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newCalendarView(vc)
	v.Resize(80, 30)
	v.now = func() time.Time { return time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC) }
	v.viewMode = viewYear

	msg := v.requestRecordings()()
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/calendar/years/2026-08-22.json" {
		t.Fatalf("paths = %v, want one read of the year", paths)
	}

	loaded, ok := msg.(yearLoadedMsg)
	if !ok {
		t.Fatalf("msg = %T, want yearLoadedMsg", msg)
	}
	if loaded.year.PaddingDays != 3 {
		t.Errorf("padding = %d, want 3", loaded.year.PaddingDays)
	}
	if len(loaded.year.Days) != 2 || !loaded.year.Days[1].Backgrounded {
		t.Errorf("days = %+v", loaded.year.Days)
	}
	if len(loaded.year.SpannedEvents) != 1 || loaded.year.SpannedEvents[0].Title != "Summer break" {
		t.Errorf("spanned events = %+v", loaded.year.SpannedEvents)
	}
}

// The picker lists what can be switched, marks what is on, and stays open across a
// toggle: switching calendars is a few decisions at once rather than one.
func TestCalendarPickerTogglesTheCalendarsItLists(t *testing.T) {
	v := calendarWithRecordings()
	v.vc.width, v.vc.height = 80, 20
	v.requests.finish(v.requests.id)

	if cmd := v.HandleContentKey(keyPress("g")); cmd != nil || v.calendarPicker == nil {
		t.Fatal("g did not open the calendars modal")
	}
	if !v.CapturingInput() {
		t.Error("the calendars modal does not hold the keys")
	}
	view := stripANSI(v.View())
	if !strings.Contains(view, "Calendars") || !strings.Contains(view, "Design Team") {
		t.Errorf("the modal does not list the calendars: %q", view)
	}

	cmd := v.HandleContentKey(keyPress(" "))
	if cmd == nil {
		t.Fatal("space did not switch the calendar")
	}
	if v.calendarPicker == nil {
		t.Error("the modal closed on a toggle")
	}
	if !v.togglePending() {
		t.Errorf("lane = loading:%v kind:%v, want the toggle", v.requests.loading, v.requests.kind)
	}

	// A second toggle while the first is in flight would race the selection HEY answers.
	if cmd := v.HandleContentKey(keyPress(" ")); cmd != nil {
		t.Error("space switched a second calendar while the first was still in flight")
	}

	// The answer replaces the selection wholesale and reads the period again.
	toggled := calendarToggledMsg{
		requestResult: currentRequest(v),
		selected:      testSelection(10, 11),
		name:          "Design Team",
		on:            true,
	}
	if _, consumed := v.Update(toggled); !consumed {
		t.Error("calendarToggledMsg should be consumed")
	}
	if !v.selected[10] {
		t.Errorf("selection = %v, want the shared calendar on", v.selected)
	}
}

// The personal calendar is not in the picker: it has no name to show, it is on in every
// client, and HEY offers no way to switch it off.
func TestCalendarPickerLeavesOutThePersonalCalendar(t *testing.T) {
	v := calendarWithRecordings()
	v.vc.width, v.vc.height = 80, 20

	listed := v.listedCalendars()
	if len(listed) != 1 || listed[0].ID != 10 {
		t.Errorf("listed calendars = %+v, want the shared one alone", listed)
	}

	// And it stays selected regardless, which is what lets habits be managed.
	if !v.viewingPersonalCalendar() {
		t.Error("the personal calendar should always count as being drawn")
	}
}

func TestCalendarPickerStaysShutWithNothingToSwitch(t *testing.T) {
	v := calendarWithRecordings()
	v.calendars = v.calendars[1:] // the personal one alone

	v.HandleContentKey(keyPress("g"))
	if v.calendarPicker != nil {
		t.Error("g opened a modal with nothing to switch")
	}
}

// --- Request lane ---

func TestCalendarViewIgnoresStaleRecordings(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(80, 30)
	v.calendars = testCalendars()

	v.requestRecordings()
	stale := recordingsLoadedMsg{requestResult: currentRequest(v), recordings: testRecordings()}

	v.viewMode = viewWeek
	v.requestRecordings()
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

// The clock is read on every fetch, so a TUI left open overnight reads the new day rather
// than the one it started on.
func TestCalendarViewFetchesAroundTheCurrentDay(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		paths = append(paths, req.URL.Path)
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
	v.requestRecordings()()

	v.now = func() time.Time { return firstDay.AddDate(0, 0, 1) }
	v.requestRecordings()()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"/calendar/days/2025-03-09.json", "/calendar/days/2025-03-10.json"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

// The week reads the week the day falls in — one request, not seven days' worth.
func TestCalendarViewReadsTheWeekForTheWeekSpan(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		paths = append(paths, req.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"starts_at":"2025-03-03T00:00:00Z","ends_at":"2025-03-09T23:59:59Z","kind":"week","recordings":{}}`))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newCalendarView(vc)
	v.Resize(80, 30)
	v.now = func() time.Time { return time.Date(2025, 3, 9, 12, 0, 0, 0, time.UTC) }
	v.viewMode = viewWeek

	v.requestRecordings()()
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/calendar/weeks/2025-03-09.json" {
		t.Errorf("paths = %v, want one read of the week", paths)
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

	for _, key := range []string{"n", "n"} {
		v.HandleContentKey(keyPress(key))
	}
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 2)) {
		t.Errorf("two steps forward = %s, want %s", got.Format(time.DateOnly), today.AddDate(0, 0, 2).Format(time.DateOnly))
	}
	for _, key := range []string{"p", "p", "p"} {
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
	if hint := v.stepHint(); hint != "p/n day · t today" {
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

// Stepping away and back leaves the anchor pinned to today's own date rather than cleared,
// so the view is on today without following the clock. The hint asks the second question:
// a reader looking at today does not need to be told how to get to it.
func TestCalendarHidesTheTodayHintWhenTodayIsOnScreen(t *testing.T) {
	v := calendarWithRecordings()
	today := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)
	v.now = func() time.Time { return today }

	v.HandleContentKey(keyPress("n"))
	if hint := v.stepHint(); hint != "p/n day · t today" {
		t.Errorf("a day away, hint = %q, want t offered", hint)
	}

	v.HandleContentKey(keyPress("p"))
	if v.onToday() {
		t.Fatal("stepping back should leave the anchor pinned, not clear it")
	}
	if hint := v.stepHint(); hint != "p/n day" {
		t.Errorf("back on today, hint = %q, want t left out", hint)
	}

	// t still has something to do — it returns the view to following the clock — it just
	// is not worth a hint while today is already on screen.
	if cmd := v.HandleContentKey(keyPress("t")); cmd == nil || !v.onToday() {
		t.Error("t should clear the anchor even from today's own date")
	}
}

func TestCalendarStepsByTheUnitTheViewShows(t *testing.T) {
	v := calendarWithRecordings()
	today := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)
	v.now = func() time.Time { return today }

	v.viewMode = viewWeek
	v.HandleContentKey(keyPress("n"))
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 7)) {
		t.Errorf("a step in the week view = %s, want a week on", got.Format(time.DateOnly))
	}

	v.viewMode = viewYear
	v.HandleContentKey(keyPress("p"))
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 7).AddDate(-1, 0, 0)) {
		t.Errorf("a step in the year view = %s, want a year back", got.Format(time.DateOnly))
	}
}

// --- Habits ---

func TestHabitCompletionsMarkTheirHabitRatherThanListingThemselves(t *testing.T) {
	// HEY answers a habit and its completion as separate recordings, the completion
	// carrying no title and naming its habit in parent_id.
	events, todos, habits, completions := splitRecordings([]Recording{
		{ID: 14796085, Title: "Read", Type: "Calendar::Habit", Icon: "read"},
		{ID: 14113260, Title: "Meditate", Type: "Calendar::Habit", Icon: "meditate"},
		{ID: 171477412, Type: "Calendar::Habit::Completion", ParentID: 14796085, StartsAt: "2026-08-22T00:00:00Z"},
	})

	// The completion is answered as well as folded: a week needs to know which day each
	// one landed on, which a single CompletedAt cannot say.
	if len(completions) != 1 || completions[0].ParentID != 14796085 {
		t.Errorf("completions = %+v, want the one that marked Read", completions)
	}

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

// --- Week: the habits band ---

// weekWithHabits is a week where five habits were kept on the Monday, one on the Tuesday
// and two on the Thursday, plus one event, rendered at 100 columns.
func weekWithHabits(t *testing.T, completions []Recording) []string {
	t.Helper()
	return weekView(t, testHabits(), completions)
}

func testHabits() []Recording {
	return []Recording{
		{ID: 1, Title: "Meditate", Icon: "meditate", Color: "purple", Type: "Calendar::Habit"},
		{ID: 2, Title: "Work out", Icon: "weights", Color: "red", Type: "Calendar::Habit"},
		{ID: 3, Title: "Write", Icon: "write", Color: "gold", Type: "Calendar::Habit"},
		{ID: 4, Title: "Read", Icon: "read", Color: "green", Type: "Calendar::Habit"},
		{ID: 5, Title: "Learn a language", Icon: "study", Color: "teal", Type: "Calendar::Habit"},
	}
}

const weekViewRows = 20

func weekView(t *testing.T, habits, completions []Recording) []string {
	t.Helper()

	events := []Recording{
		{ID: 9, Title: "Stanko & Kevin", CalendarColor: "blue", Type: "Calendar::Event",
			StartsAt: "2026-08-20T14:00:00Z", EndsAt: "2026-08-20T15:00:00Z"},
	}

	anchor := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	out := renderWeekView(events, habits, completions, anchor, time.Monday, 100, weekViewRows, "p/n week", nil)
	return strings.Split(stripANSI(out), "\n")
}

func habitDone(habitID int64, day string) Recording {
	return Recording{Type: "Calendar::Habit::Completion", ParentID: habitID, StartsAt: day + "T00:00:00Z"}
}

// The week says what was kept each day, as icons — it has room for seven days of those and
// none for seven days of names. Every day's band is the same height, so the rule under it
// is straight and each day's events start level with its neighbours'.
func TestWeekDrawsTheHabitsKeptEachDay(t *testing.T) {
	lines := weekWithHabits(t, []Recording{
		habitDone(1, "2026-08-17"), habitDone(2, "2026-08-17"), habitDone(3, "2026-08-17"),
		habitDone(4, "2026-08-17"), habitDone(5, "2026-08-17"),
		habitDone(1, "2026-08-18"),
		habitDone(1, "2026-08-20"), habitDone(4, "2026-08-20"),
	})

	// Found by what they say rather than by their row number, so the layout can move
	// without the test having to be told.
	header := rowContaining(t, lines, "Habits")
	label := rowContaining(t, lines, "August 17 – 23")

	band := lines[header+1 : label]
	if len(band) != 2 {
		t.Fatalf("band is %d rows, want 2 — five icons do not fit one cell: %q", len(band), band)
	}
	if !strings.Contains(band[0], "🧘") || !strings.Contains(band[0], "📖") {
		t.Errorf("Monday's habits are not in the band: %q", band[0])
	}
	if !strings.Contains(band[1], "📚") {
		t.Errorf("the fifth habit did not wrap onto a second row: %q", band[1])
	}
	// Both rows are the full width, so the header under them is straight and each day's
	// events start level with its neighbours'.
	if lipgloss.Width(band[0]) != lipgloss.Width(band[1]) {
		t.Errorf("band rows are %d and %d wide", lipgloss.Width(band[0]), lipgloss.Width(band[1]))
	}
	for _, row := range band {
		if strings.Contains(row, "Meditate") || strings.Contains(row, "Stanko") {
			t.Errorf("the band carries a name, not just icons: %q", row)
		}
	}

	// The week's own line is what closes the band off, and the day's events are below it.
	if event := rowContaining(t, lines, "Stanko & Kev"); event <= label {
		t.Errorf("the event is not under the week's line: row %d against %d", event, label)
	}
}

// rowContaining is the index of the one row holding s, so a test can say where it looked.
func rowContaining(t *testing.T, lines []string, s string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(line, s) {
			return i
		}
	}
	t.Fatalf("no row contains %q: %q", s, lines)
	return -1
}

// The band keeps its place in a week where nothing was kept, so stepping from week to week
// does not shift the grid up and down underneath the reader.
func TestWeekKeepsTheHabitsBandWhenNothingWasKept(t *testing.T) {
	kept := weekWithHabits(t, []Recording{habitDone(1, "2026-08-17")})
	none := weekWithHabits(t, nil)

	if rowContaining(t, kept, "August 17 – 23") != rowContaining(t, none, "August 17 – 23") {
		t.Error("the week's line moved between a week with habits kept and one without")
	}
	for i, line := range none {
		if strings.Contains(line, "🧘") || strings.Contains(line, "📖") {
			t.Errorf("row %d drew a habit nobody kept: %q", i, line)
		}
	}
	if !strings.Contains(none[0], "Habits") {
		t.Errorf("the band lost its header: %q", none[0])
	}
}

// Somebody who keeps no habits gets no band at all, since there is nothing to head.
func TestWeekWithoutAnyHabitsHasNoBand(t *testing.T) {
	lines := weekView(t, nil, nil)

	if got := rowContaining(t, lines, "August 17 – 23"); got != 0 {
		t.Errorf("the week's line is row %d, want the first: %q", got, lines[:got+1])
	}
	if !strings.Contains(lines[1], "MON 17") {
		t.Errorf("the day names should follow the week's line, got %q", lines[1])
	}
	for i, line := range lines {
		if strings.Contains(line, "Habits") {
			t.Errorf("row %d headed a band that is not there: %q", i, line)
		}
	}
}

// The days run to the bottom of the screen: the rules between them are the grid, so a quiet
// week still reads as seven days rather than as a paragraph that stops.
func TestWeekRunsItsDaysToTheBottom(t *testing.T) {
	lines := weekView(t, nil, nil)

	if len(lines) != weekViewRows {
		t.Fatalf("week is %d rows of the %d it was given: %q", len(lines), weekViewRows, lines)
	}
	// Six rules for seven days, on the last row as on the first.
	for _, row := range []int{1, len(lines) - 1} {
		if got := strings.Count(lines[row], string(hourRule)); got != 6 {
			t.Errorf("row %d has %d rules, want 6: %q", row, got, lines[row])
		}
	}
}

// A calendar carries a day's own records alongside its events. Only the events are drawn:
// a journal entry taken for one came out as a bar of bare color across the day.
func TestOnlyEventsAreDrawnOnTheGrid(t *testing.T) {
	events, todos, habits, _ := splitRecordings([]Recording{
		{ID: 169118695, Title: "Stanko & Kevin", Type: "Calendar::Event", StartsAt: "2026-08-20T14:00:00Z"},
		// The journal entry behind the stray stripe, as HEY answered it: no title.
		{ID: 171477000, Type: "Calendar::JournalEntry", AllDay: true, StartsAt: "2026-08-20T00:00:00Z"},
		{ID: 171477001, Type: "Calendar::DayBackground", AllDay: true, StartsAt: "2026-08-20T00:00:00Z"},
		// A time track has a name, and still is not an event.
		{ID: 171477002, Title: "Design work", Type: "Calendar::TimeTrack", StartsAt: "2026-08-20T09:00:00Z"},
		{ID: 171477003, Title: "Clean the attic", Type: "Calendar::Todo"},
		{ID: 14796085, Title: "Read", Type: "Calendar::Habit"},
	})

	if len(events) != 1 || events[0].Title != "Stanko & Kevin" {
		t.Errorf("events = %+v, want the one event alone", events)
	}
	if len(todos) != 1 || todos[0].Title != "Clean the attic" {
		t.Errorf("todos = %+v", todos)
	}
	if len(habits) != 1 || habits[0].Title != "Read" {
		t.Errorf("habits = %+v", habits)
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
	view := stripANSI(renderDayView(events, habits, day, "p/n day", 100, 24))
	for _, label := range []string{"Habits", "Monday, August 24", "p/n day", "All day"} {
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
	// an hour every four columns and the 11:00 event's block on the four from 44. The
	// day's header and the hour axis take the first two rows of the 40 it is given,
	// leaving 38 for the grid.
	lines := strings.Split(stripANSI(renderDayView(events, nil, day, "", 98, 40)), "\n")
	grid := lines[2:]
	if len(grid) != 38 {
		t.Fatalf("grid is %d rows of the 38 left to it: %q", len(grid), grid)
	}

	// The event is the only thing on the day, so its block is as tall as the grid and
	// holds the 11:00 rule for every row of it. Twenty-four hours are twenty-five rules;
	// the block keeps one of them covered all the way down.
	for i, line := range grid {
		if cell := []rune(line)[0]; cell != hourRule {
			t.Errorf("grid row %d lost midnight's rule: %q", i, line)
		}
		if cell := []rune(line)[44]; cell == hourRule {
			t.Errorf("grid row %d ruled through the event's own block: %q", i, line)
		}
		if rules := strings.Count(line, string(hourRule)); rules != 24 {
			t.Errorf("grid row %d has %d rules, want 24: %q", i, rules, line)
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

// An event's box is the span it covers, not the length of its name: alone it is as tall as
// the day, and events that overlap share the height between them.
func TestOverlappingEventsShareTheDaysHeight(t *testing.T) {
	for _, tt := range []struct {
		rows, lanes int
		want        []int
	}{
		{38, 1, []int{38}},
		{38, 2, []int{19, 19}},
		{38, 3, []int{13, 13, 12}}, // the odd rows go to the earlier lanes
		{38, 0, nil},
		// More overlapping events than rows: a lane keeps the three a block needs and
		// the grid grows past the screen instead, which is what the viewport scrolls.
		{10, 5, []int{3, 3, 3, 3, 3}},
	} {
		got := shareDayRows(tt.rows, tt.lanes)
		if len(got) != len(tt.want) {
			t.Errorf("%d rows over %d lanes = %v, want %v", tt.rows, tt.lanes, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%d rows over %d lanes = %v, want %v", tt.rows, tt.lanes, got, tt.want)
				break
			}
		}
	}
}

// And on screen: one event holds its hour's rule all the way down, because its block is the
// whole grid rather than as tall as its title.
func TestDayViewGivesASingleEventTheWholeGrid(t *testing.T) {
	day := time.Date(2026, 8, 24, 9, 0, 0, 0, time.Local)
	events := []Recording{{ID: 1, Title: "Charge the car", Type: "CalendarEvent",
		StartsAt: "2026-08-24T07:00:00Z", EndsAt: "2026-08-24T10:00:00Z"}}

	// An hour every four columns, so 07:00 starts on column 28 and the block covers the
	// rules at 28 and 32 for every one of the grid's 38 rows.
	grid := strings.Split(stripANSI(renderDayView(events, nil, day, "", 98, 40)), "\n")[2:]
	if len(grid) != 38 {
		t.Fatalf("grid is %d rows, want 38: %q", len(grid), grid)
	}
	for i, row := range grid {
		if cell := []rune(row)[28]; cell == hourRule {
			t.Errorf("grid row %d ruled through the event's block at 07:00: %q", i, row)
		}
	}
}

// An event is drawn in the color of the calendar it is filed on, so which calendar it
// belongs to is answered by looking at it. HEY leaves the personal calendar's color out of
// its JSON, and those fall back to the theme's own accent rather than to no fill.
func TestCalendarColorFillsAnEventsBlock(t *testing.T) {
	ansiTheme := Theme{Accent: lipgloss.BrightBlue, Bright: lipgloss.BrightWhite, Dark: true}
	t.Cleanup(func() { applyTheme(ansiTheme) })

	// With no theme file the ANSI slots are all there is, and their nominal values are
	// what the terminal really draws.
	applyTheme(ansiTheme)
	if got := eventFillColor("teal"); got != lipgloss.Cyan {
		t.Errorf("teal filled with %v, want cyan", got)
	}
	if got := eventFillColor(""); got != colorPrimary {
		t.Errorf("an event with no calendar color filled with %v, want the accent", got)
	}

	// A theme that states its hues is taken at its word, because that is what the reader
	// sees. This is the dark Omarchy palette from the screenshots: its blue is a light
	// periwinkle, nothing like ANSI blue's nominal #000080.
	applyTheme(Theme{
		Accent: lipgloss.Color("#7d82d9"), Bright: lipgloss.Color("#ffcead"), Dark: true,
		Background: lipgloss.Color("#060B1E"),
		Hues: map[string]color.Color{
			"blue":  lipgloss.Color("#7d82d9"),
			"green": lipgloss.Color("#92a593"),
			"red":   lipgloss.Color("#ED5B5A"),
			"gold":  lipgloss.Color("#f7dc9c"),
		},
	})

	if got := eventFillColor("blue"); got != lipgloss.Color("#7d82d9") {
		t.Errorf("blue filled with %v, want the theme's own periwinkle", got)
	}

	// Every one of those is light, so every one takes the theme's dark paper as its ink —
	// which is what nominal contrast got backwards on green and blue.
	for _, calendarColor := range []string{"blue", "green", "red", "gold"} {
		style := dayCell{kind: cellTitle, color: calendarColor}.style(styleMuted, styleMuted, styleMuted)
		if style.GetBackground() != eventFillColor(calendarColor) {
			t.Errorf("%q sits on %v, want its own fill", calendarColor, style.GetBackground())
		}
		if style.GetForeground() != colorPaper {
			t.Errorf("%q drew %v on a light hue, want the theme's paper %v",
				calendarColor, style.GetForeground(), colorPaper)
		}
	}
}

// The ink is whichever of the theme's paper and its own text color reads better on the
// fill, so a light theme — where the same hues arrive deep rather than pale — gets the
// other one without any of this knowing which mode it is in.
func TestEventInkFollowsTheThemesOwnPalette(t *testing.T) {
	t.Cleanup(func() { applyTheme(Theme{Accent: lipgloss.BrightBlue, Bright: lipgloss.BrightWhite, Dark: true}) })

	applyTheme(Theme{
		Accent: lipgloss.Color("#2b4c8c"), Bright: lipgloss.Color("#1c1c1c"), Dark: false,
		Background: lipgloss.Color("#fafafa"),
		Hues:       map[string]color.Color{"blue": lipgloss.Color("#2b4c8c")},
	})

	style := dayCell{kind: cellTitle, color: "blue"}.style(styleMuted, styleMuted, styleMuted)
	if style.GetForeground() != colorPaper {
		t.Errorf("a deep blue on a light theme drew %v, want the pale paper", style.GetForeground())
	}
	if contrastRatio(style.GetForeground(), style.GetBackground()) < 4.5 {
		t.Errorf("ink on fill is only %.1f:1", contrastRatio(style.GetForeground(), style.GetBackground()))
	}
}

// Omarchy retints a running terminal on a keyboard shortcut, so an event's ink cannot be
// decided once at startup. The styles are built while rendering rather than cached in
// newStyles, so applyTheme is all a theme switch has to do.
func TestEventInkFollowsALiveThemeChange(t *testing.T) {
	t.Cleanup(func() { applyTheme(Theme{Accent: lipgloss.BrightBlue, Bright: lipgloss.BrightWhite, Dark: true}) })

	applyTheme(Theme{
		Accent: lipgloss.BrightBlue, Bright: lipgloss.Color("#ffcead"), Dark: true,
		Background: lipgloss.Color("#060B1E"),
		Hues:       map[string]color.Color{"green": lipgloss.Color("#92a593")},
	})
	onDark := eventPill(Recording{Title: "Summer friday", CalendarColor: "green"}, 20)

	applyTheme(Theme{
		Accent: lipgloss.BrightBlue, Bright: lipgloss.Color("#1c1c1c"), Dark: false,
		Background: lipgloss.Color("#fafafa"),
		Hues:       map[string]color.Color{"green": lipgloss.Color("#1f5c2f")},
	})
	onLight := eventPill(Recording{Title: "Summer friday", CalendarColor: "green"}, 20)

	if onDark == onLight {
		t.Errorf("the pill did not follow the theme: %q", onDark)
	}
}

// The week and the year draw an event as a filled bar too, off the same field and padded to
// the cell so the fill reads as a block rather than as a highlight behind some words.
func TestEventPillFillsTheCellInItsCalendarsColor(t *testing.T) {
	pill := eventPill(Recording{Title: "Standup", CalendarColor: "gold"}, 12)

	if got := stripANSI(pill); got != "Standup     " {
		t.Errorf("pill text = %q, want the title padded to 12", got)
	}
	if !strings.Contains(pill, "\x1b[") {
		t.Errorf("pill carries no styling: %q", pill)
	}

	// A title longer than the cell is cut to it rather than spilling into the next day.
	long := eventPill(Recording{Title: "Design review with the whole team", CalendarColor: "teal"}, 12)
	if got := lipgloss.Width(stripANSI(long)); got != 12 {
		t.Errorf("pill is %d columns wide, want 12", got)
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
	// The day view offers the categories and the habits modal. Creating, editing and
	// deleting a habit are the modal's own keys; the keys that move the day are on the
	// day's own line; and each span's number is in its own tab above the grid.
	bindings := v.HelpBindings()
	if len(bindings) != 3 {
		t.Fatalf("expected 3 bindings, got %d: %+v", len(bindings), bindings)
	}
	for _, want := range []string{"g", "c", "b"} {
		found := false
		for _, binding := range bindings {
			found = found || binding.key == want
		}
		if !found {
			t.Errorf("missing binding %q: %+v", want, bindings)
		}
	}

	// The week names itself and carries its own keys now, as the day does, so the help bar
	// stops repeating them.
	v.viewMode = viewWeek
	for _, binding := range v.HelpBindings() {
		if binding.key == "p/n" || binding.key == "t" {
			t.Errorf("the week's own line says %q; the help bar should not: %+v", binding.key, v.HelpBindings())
		}
	}

	// The year has no line of its own, so the help bar still carries its steps.
	v.viewMode = viewYear
	for _, want := range []string{"p/n", "c"} {
		found := false
		for _, binding := range v.HelpBindings() {
			found = found || binding.key == want
		}
		if !found {
			t.Errorf("the year view is missing binding %q: %+v", want, v.HelpBindings())
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
