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

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// at is a timestamp as HEY answers one: RFC 3339 in UTC, which is what every fixture here
// stands in for.
func at(ts string) time.Time {
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		panic("bad fixture timestamp " + ts + ": " + err.Error())
	}
	return parsed
}

// atLocal is the same instant said the other way round: whatever UTC time puts this clock
// time on the reader's own zone. A test about where an event lands in the grid has to name
// the hour the reader sees, since that is the hour the column is.
func atLocal(ts string) time.Time {
	parsed, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSuffix(ts, "Z"), time.Local)
	if err != nil {
		panic("bad fixture timestamp " + ts + ": " + err.Error())
	}
	return parsed.UTC()
}

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
		{ID: 200, Title: "Standup", StartsAt: at("2025-03-01T09:00:00Z"), EndsAt: at("2025-03-01T09:30:00Z"), Type: "CalendarEvent"},
		{ID: 201, Title: "Lunch", StartsAt: at("2025-03-01T12:00:00Z"), EndsAt: at("2025-03-01T13:00:00Z"), AllDay: false, Type: "CalendarEvent"},
		{ID: 202, Title: "Read a book", StartsAt: at("2025-03-01T06:00:00Z"), Type: "Habit"},
		{ID: 203, Title: "Buy milk", StartsAt: at("2025-03-01T00:00:00Z"), Type: "CalendarTodo"},
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
		{ID: 300, Title: "Design review", StartsAt: at("2025-03-04T15:00:00Z"), EndsAt: at("2025-03-04T16:00:00Z"), Type: "CalendarEvent"},
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
		[]Recording{{ID: 200, StartsAt: at("2025-03-01T09:00:00Z"), Type: "CalendarEvent", Label: "Launch day"}},
		[]Recording{{ID: 203, StartsAt: at("2025-03-02T00:00:00Z"), Type: "CalendarTodo", Label: "Moving day"}},
		[]Recording{{ID: 202, StartsAt: at("2025-03-03T06:00:00Z"), Type: "Habit", Label: "Rest day"}},
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

// --- Walking the events with the arrows ---

// dayWithEvents is a calendar sitting on one day that holds two timed events and an all-day
// one, so the order the arrows walk can be checked against the order they are drawn in.
func dayWithEvents(t *testing.T) *calendarView {
	t.Helper()

	v := newCalendarView(testVC())
	v.Resize(100, 30)
	v.now = func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local) }
	v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 2, Title: "Product Planning", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T17:00:00"), EndsAt: atLocal("2026-08-20T18:00:00")},
		{ID: 1, Title: "Stanko & Kevin", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T14:00:00"), EndsAt: atLocal("2026-08-20T15:00:00")},
		{ID: 3, Title: "Summer friday", AllDay: true, Type: "Calendar::Event",
			StartsAt: at("2026-08-20T00:00:00Z"), EndsAt: at("2026-08-20T00:00:00Z")},
	}})
	return v
}

// On the day, ← and → walk the grid — the timed events, in the order the clock puts them — and
// the all-day band under it is somewhere ↑ and ↓ go. Stepping onto an all-day event sideways off
// a 17:00 meeting never read as going anywhere: it belongs to no hour, so there is nothing to
// the right of five o'clock about it.
func TestDayArrowsWalkTheGridAndCrossToTheAllDayBand(t *testing.T) {
	v := dayWithEvents(t)

	timed, allDay := v.selectableGroups()
	if len(timed) != 2 || timed[0].ID != 1 || timed[1].ID != 2 {
		t.Fatalf("the grid holds %+v, want the two timed events by the clock", timed)
	}
	if len(allDay) != 1 || allDay[0].ID != 3 {
		t.Fatalf("the band holds %+v, want the all-day event", allDay)
	}

	// Nothing is selected until an arrow is pressed, and → picks up the first.
	if v.selectedEvent != "" {
		t.Errorf("a day opens with %q selected, want nothing", v.selectedEvent)
	}
	v.HandleContentKey(keyPress("right"))
	if v.selectedEvent != "1" {
		t.Errorf("first → selected %q, want the 14:00", v.selectedEvent)
	}
	v.HandleContentKey(keyPress("right"))
	if v.selectedEvent != "2" {
		t.Errorf("→ selected %q, want the 17:00", v.selectedEvent)
	}

	// ↓ crosses onto the band, ↑ comes back to the event that was left rather than to the top
	// of the day.
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "3" {
		t.Errorf("↓ selected %q, want the all-day event", v.selectedEvent)
	}
	v.HandleContentKey(keyPress("up"))
	if v.selectedEvent != "2" {
		t.Errorf("↑ off the band selected %q, want the 17:00 it was left from", v.selectedEvent)
	}
}

// A day of a repeating event has no id of its own. HEY serves it as a virtual occurrence — id
// 0, an occurrence_id naming the series and the day — so an arrow holding on to the numeric id
// could never pick it out, and a weekly all-day event was unselectable in every view.
func TestARepeatingEventsOwnDayCanBeSelected(t *testing.T) {
	occurrence := sdkRecordingToModel(generated.Recording{
		Title: "Summer friday", AllDay: true, Type: "Calendar::Event",
		ParentId: 153688907, OccurrenceId: "153688907_2026-08-21",
		StartsAt: at("2026-08-21T00:00:00Z"), EndsAt: at("2026-08-21T00:00:00Z"),
	})
	if occurrence.ID != 0 {
		t.Fatalf("the fixture is not a virtual occurrence: id=%d", occurrence.ID)
	}
	if occurrence.key() != "153688907_2026-08-21" {
		t.Errorf("key = %q, want the occurrence id", occurrence.key())
	}

	v := newCalendarView(testVC())
	v.Resize(100, 30)
	v.now = func() time.Time { return time.Date(2026, 8, 21, 9, 0, 0, 0, time.Local) }
	v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{occurrence}})

	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != occurrence.key() {
		t.Fatalf("↓ selected %q, want the repeating event's own day", v.selectedEvent)
	}
	if _, ok := v.selectedRecording(); !ok {
		t.Error("the selection does not resolve back to the occurrence")
	}
	if !v.selection().has(occurrence) {
		t.Error("the renderers would not draw it as selected")
	}
}

// On the year, b manages habits but does not keep them. A year read carries no recordings, so
// nothing on that screen knows what was kept on the day the cursor is on — and a ring drawn
// empty there would be answering a question nobody asked the server.
func TestTheYearManagesHabitsWithoutKeepingThem(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(100, 30)
	v.now = func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local) }
	v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 40, Title: "Read", Type: "Calendar::Habit", Icon: "📖",
			StartsAt: atLocal("2024-01-01T00:00:00"), CompletedAt: atLocal("2026-08-20T08:00:00")},
	}})

	// On the day it is a day's habits, so keeping one is on offer.
	v.HandleContentKey(keyPress("b"))
	if v.habitPicker == nil || !v.habitPicker.completable {
		t.Fatal("the day does not offer keeping a habit")
	}
	if !hasBinding(v.HelpBindings(), "enter") {
		t.Error("the day's picker does not say enter keeps a habit")
	}
	v.habitPicker = nil

	v.viewMode = viewYear
	v.HandleContentKey(keyPress("b"))
	if v.habitPicker == nil {
		t.Fatal("b did not open the picker on the year")
	}
	if v.habitPicker.completable {
		t.Error("the year offers keeping a habit")
	}
	if hasBinding(v.HelpBindings(), "enter") {
		t.Error("the year's picker still says enter keeps a habit")
	}

	// enter says where to do it rather than doing nothing at all.
	if cmd := v.HandleContentKey(keyPress("enter")); cmd != nil {
		t.Error("enter kept a habit from the year")
	}
	if v.habitPicker.status == "" {
		t.Error("enter said nothing about why")
	}

	// Editing and deleting still work, since both go by the habit's id.
	if cmd := v.HandleContentKey(keyPress("e")); cmd == nil {
		t.Error("e does not edit a habit from the year")
	}
}

// Every week row of the year names its month, in the accent rather than the grey the day labels
// wear. Naming it only on the first of the month meant that the moment that one cell scrolled
// past, nothing on screen said what month the reader was looking at.
func TestTheYearNamesItsMonthsOnEveryRow(t *testing.T) {
	out, _, _ := renderYearView(nil, time.Date(2026, 8, 22, 0, 0, 0, 0, time.Local),
		time.Monday, 100, 30, "p/n year", selection{}, false)

	accent := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, accent.Render("AUG")) {
			rows++
		}
	}
	if rows < 4 {
		t.Errorf("only %d rows of August name it in the accent, want every one", rows)
	}

	// A day outside the year keeps the grey: the December before it shouting from the first row
	// would read as somewhere the reader can go. 2026 starts on a Thursday, so that row is the
	// tail of 2025.
	first := strings.Split(out, "\n")[1]
	if strings.HasPrefix(first, accent.Render("DEC")) {
		t.Errorf("a month outside the year drawn is picked out: %q", first)
	}

	// The month is dropped rather than the date when a column is too narrow for both.
	month, day := yearDayLabel(time.Date(2026, 8, 17, 0, 0, 0, 0, time.Local), true)
	if month != "AUG" || day != "MON 17" {
		t.Errorf("label = %q %q", month, day)
	}
	narrow, _, _ := renderYearView(nil, time.Date(2026, 8, 22, 0, 0, 0, 0, time.Local),
		time.Monday, 40, 30, "p/n year", selection{}, false)
	if strings.Contains(stripANSI(narrow), "AUG MON 17") {
		t.Error("a narrow column kept the month and truncated the date")
	}
}

// A repeating event's own day is written through its own route: the series id and the date,
// because the day has no id. It is changed on its own rather than for the whole series.
func TestARepeatingEventsOwnDayIsWrittenThroughItsOccurrence(t *testing.T) {
	v, recorded := calendarWithEventServer(t)
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		sdkRecordingToModel(generated.Recording{
			Title: "Summer friday", AllDay: true, Type: "Calendar::Event",
			ParentId: 153688907, OccurrenceId: "153688907_2026-08-20",
			StartsAt: at("2026-08-20T00:00:00Z"), EndsAt: at("2026-08-20T00:00:00Z"),
		}),
	}})

	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "153688907_2026-08-20" {
		t.Fatalf("selected %q, want the occurrence", v.selectedEvent)
	}

	// Editing it patches the occurrence, not event 0.
	v.HandleContentKey(keyPress("e"))
	if v.eventForm == nil {
		t.Fatal("e did not open the form on a repeating event's day")
	}
	cmd := v.HandleContentKey(keyPress("ctrl+s"))
	if cmd == nil {
		t.Fatal("ctrl+s did not save")
	}
	msg, ok := cmd().(calendarMutationMsg)
	if !ok || msg.err != nil {
		t.Fatalf("save = %T %v", cmd(), msg.err)
	}
	requests, _ := recorded.snapshot()
	if len(requests) == 0 || requests[0] != "PATCH /calendar/events/153688907/occurrences/2026-08-20.json" {
		t.Fatalf("requests = %v", requests)
	}

	// The answer has to land before anything else is pressed: a write in flight holds every key,
	// which is what keeps two saves off the same event.
	v.Update(msg)
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: v.events})

	// And deleting it takes off that day alone.
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("x"))
	cmd = v.HandleContentKey(keyPress("x"))
	if cmd == nil {
		t.Fatal("the second x did not delete")
	}
	if msg, ok := cmd().(calendarMutationMsg); !ok || msg.err != nil {
		t.Fatalf("delete = %T %v", cmd(), msg.err)
	}
	requests, _ = recorded.snapshot()
	if len(requests) < 2 || requests[1] != "DELETE /calendar/events/153688907/occurrences/2026-08-20.json" {
		t.Errorf("requests = %v", requests)
	}
}

// Within the band ↑ and ↓ walk it, and ↑ off the top of it is the way back to the grid.
func TestDayAllDayBandIsWalkedUpAndDown(t *testing.T) {
	v := dayWithEvents(t)
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 1, Title: "Stanko & Kevin", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T14:00:00"), EndsAt: atLocal("2026-08-20T15:00:00")},
		{ID: 3, Title: "Summer friday", AllDay: true, Type: "Calendar::Event",
			StartsAt: at("2026-08-20T00:00:00Z"), EndsAt: at("2026-08-20T00:00:00Z")},
		{ID: 4, Title: "On call", AllDay: true, Type: "Calendar::Event",
			StartsAt: at("2026-08-20T00:00:00Z"), EndsAt: at("2026-08-20T00:00:00Z")},
	}})

	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "3" {
		t.Fatalf("↓ with nothing selected chose %q, want the first all-day event", v.selectedEvent)
	}
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "4" {
		t.Errorf("↓ within the band chose %q, want the second", v.selectedEvent)
	}
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "4" {
		t.Errorf("↓ off the end of the band chose %q, want it to stay", v.selectedEvent)
	}

	v.HandleContentKey(keyPress("up"))
	v.HandleContentKey(keyPress("up"))
	if v.selectedEvent != "1" {
		t.Errorf("↑ off the top of the band chose %q, want the grid", v.selectedEvent)
	}

	// And on an all-day event ← and → are the day either side, since the band has no sideways.
	v.selectedEvent = "3"
	today := v.day()
	if cmd := v.HandleContentKey(keyPress("right")); cmd == nil {
		t.Fatal("→ on an all-day event did not read the next day")
	}
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 1)) {
		t.Errorf("→ moved to %s, want tomorrow", got.Format(time.DateOnly))
	}
}

// Stepping off either end moves the span, and lands on the far end of the one it arrives at,
// so holding an arrow walks through the calendar rather than stopping at every screen.
func TestArrowsStepTheSpanOffEitherEnd(t *testing.T) {
	v := dayWithEvents(t)
	today := v.day()

	// → from the last event asks for the next day.
	v.selectedEvent = "3"
	if cmd := v.HandleContentKey(keyPress("right")); cmd == nil {
		t.Fatal("→ off the end did not read the next day")
	}
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 1)) {
		t.Errorf("→ off the end moved to %s, want tomorrow", got.Format(time.DateOnly))
	}
	if v.selectFromEdge != 1 {
		t.Errorf("selectFromEdge = %d, want the first event of what arrives", v.selectFromEdge)
	}

	// What comes back is walked into from the near side.
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 9, Title: "Standup", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-21T09:00:00"), EndsAt: atLocal("2026-08-21T09:30:00")},
		{ID: 8, Title: "Retro", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-21T16:00:00"), EndsAt: atLocal("2026-08-21T17:00:00")},
	}})
	if v.selectedEvent != "9" {
		t.Errorf("landed on %q, want the first event of the new day", v.selectedEvent)
	}
	if v.selectFromEdge != 0 {
		t.Error("selectFromEdge should be spent once it has been used")
	}

	// ← from the first event asks for the day before, and lands on its last event.
	v.HandleContentKey(keyPress("left"))
	if v.selectFromEdge != -1 {
		t.Errorf("selectFromEdge = %d, want the last event of what arrives", v.selectFromEdge)
	}
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 5, Title: "Morning", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T08:00:00"), EndsAt: atLocal("2026-08-20T09:00:00")},
		{ID: 6, Title: "Evening", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T20:00:00"), EndsAt: atLocal("2026-08-20T21:00:00")},
	}})
	if v.selectedEvent != "6" {
		t.Errorf("landed on %q, want the last event of the day before", v.selectedEvent)
	}
}

// A day with nothing on it has no event to step off, so an arrow moves the span directly.
func TestArrowsStepAnEmptySpan(t *testing.T) {
	v := dayWithEvents(t)
	today := v.day()
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: nil})

	if cmd := v.HandleContentKey(keyPress("right")); cmd == nil {
		t.Fatal("→ on an empty day did not read the next one")
	}
	if got := v.day(); !sameDay(got, today.AddDate(0, 0, 1)) {
		t.Errorf("moved to %s, want tomorrow", got.Format(time.DateOnly))
	}
}

// A selection that is no longer there is let go of rather than kept pointing at nothing: the
// event was deleted, or the reader moved to a day it is not on.
func TestSelectionIsReleasedWhenItsEventGoes(t *testing.T) {
	v := dayWithEvents(t)
	v.selectedEvent = "1"

	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 2, Title: "Product Planning", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T17:00:00"), EndsAt: atLocal("2026-08-20T18:00:00")},
	}})
	if v.selectedEvent != "" {
		t.Errorf("selection = %q, want it let go of", v.selectedEvent)
	}
}

// In the week ← and → are the days and ↑ and ↓ the events of the day they are on. That is what
// gives b, s and a something to act on: the cursor is the anchor, so the day they file on is
// the column the reader is pointing at rather than whichever one the week starts with.
func TestWeekArrowsSelectDaysAndThenEvents(t *testing.T) {
	v := dayWithEvents(t)
	v.viewMode = viewWeek
	v.anchor = time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local) // a Thursday
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 1, Title: "Standup", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-19T09:00:00"), EndsAt: atLocal("2026-08-19T09:30:00")},
		{ID: 2, Title: "Design review", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-19T14:00:00"), EndsAt: atLocal("2026-08-19T15:00:00")},
		{ID: 3, Title: "On call", AllDay: true, Type: "Calendar::Event",
			StartsAt: at("2026-08-17T00:00:00Z"), EndsAt: at("2026-08-23T00:00:00Z")},
	}})

	// Thursday holds only the week-long event, so that is all ↑ and ↓ have there.
	if events := v.selectableEvents(); len(events) != 1 || events[0].ID != 3 {
		t.Fatalf("Thursday offers %+v, want the week-long event alone", events)
	}

	// ← moves to Wednesday, which is inside the same week and so needs no second read.
	v.HandleContentKey(keyPress("left"))
	if got := v.day(); !sameDay(got, time.Date(2026, 8, 19, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("← landed on %s, want Wednesday", got.Format(time.DateOnly))
	}
	if v.requests.loading {
		t.Error("moving within the week read the week again")
	}

	// And there ↑ and ↓ walk its events: the timed ones by the clock, then the all-day band.
	events := v.selectableEvents()
	if len(events) != 3 || events[0].ID != 1 || events[1].ID != 2 || events[2].ID != 3 {
		t.Fatalf("Wednesday offers %+v, want Standup, Design review, then the all-day one", events)
	}
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "1" {
		t.Errorf("↓ selected %q, want the first event of the day", v.selectedEvent)
	}
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "2" {
		t.Errorf("a second ↓ selected %q, want the next event", v.selectedEvent)
	}

	// And the week says which column that is, or neither pair of arrows would have anything
	// visible to aim at.
	week := renderWeekView(v.events, nil, nil, v.day(), time.Monday, 100, 14, "p/n week", nil, v.selection())
	header := strings.Split(week, "\n")[1]
	if !strings.Contains(header, cursorDayStyle(false).Render(centerPad("WED 19", 13))) {
		t.Errorf("the week does not mark the day the cursor is on: %q", header)
	}

	// ↓ stops at the end rather than walking into another day: the days are ← and →'s.
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "3" {
		t.Errorf("↓ past the last event selected %q, want it to stay on the last", v.selectedEvent)
	}
	if got := v.day(); !sameDay(got, time.Date(2026, 8, 19, 0, 0, 0, 0, time.Local)) {
		t.Errorf("↓ moved the day to %s", got.Format(time.DateOnly))
	}
}

// The highlight only moves while there is something to draw it on and somebody looking at it.
// A loop that outlived either would be re-rendering a screen nobody is reading, forever.
func TestTheHighlightStopsWhenThereIsNothingToDraw(t *testing.T) {
	v := dayWithEvents(t)

	// Picking an event out is what starts it, and only the key that changed the selection does.
	if cmd := v.HandleContentKey(keyPress("right")); cmd == nil {
		t.Fatal("selecting an event did not start the highlight")
	}
	if !v.animating {
		t.Fatal("the loop is not running")
	}
	if cmd := v.HandleContentKey(keyPress("t")); cmd != nil {
		t.Error("a key that changed no selection started a second loop")
	}

	// A tick keeps it going for as long as the calendar is what is on screen.
	v.View()
	phase := v.selectPhase
	cmd, _ := v.Update(calendarTickMsg{})
	if cmd == nil {
		t.Error("a tick on a drawn calendar did not schedule the next")
	}
	if v.selectPhase == phase {
		t.Error("the tick did not move the highlight")
	}

	// Nothing drawn since the last tick means the reader is in another section, so it stops
	// rather than animating out of sight. There is no hook for leaving a section, and this
	// needs none: only the view on screen is drawn.
	if cmd, _ := v.Update(calendarTickMsg{}); cmd != nil {
		t.Error("the loop went on with the calendar off screen")
	}

	// And it stops when the selection goes, whoever took it away.
	v.View()
	v.selectedEvent = ""
	if cmd, _ := v.Update(calendarTickMsg{}); cmd != nil {
		t.Error("the loop went on with nothing selected")
	}
}

// b acts on the day the cursor is on, which over a week or a year is not today. A habit's
// completions are folded into one timestamp when the recordings are split, so the picker has to
// read the day's own out of the completions — or it says a habit was done when the day the
// reader is pointing at is blank, and toggling it clears some other day's.
func TestHabitsAreOfferedForTheDayTheCursorIsOn(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(100, 30)
	v.now = func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local) }
	v.viewMode = viewWeek
	v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 40, Title: "Read", Type: "Calendar::Habit", Icon: "📖",
			StartsAt: atLocal("2024-01-01T00:00:00")},
		{ID: 41, ParentID: 40, Type: "Calendar::Habit::Completion",
			StartsAt: atLocal("2026-08-18T08:00:00")},
	}})

	// Thursday: nothing kept.
	if habits := v.manageableHabits(); len(habits) != 1 || habits[0].Done() {
		t.Fatalf("Thursday says %+v, want the habit undone", habits)
	}

	// Tuesday, where it was kept.
	v.anchor = time.Date(2026, 8, 18, 9, 0, 0, 0, time.Local)
	if habits := v.manageableHabits(); len(habits) != 1 || !habits[0].Done() {
		t.Fatalf("Tuesday says %+v, want the habit done", habits)
	}
}

// A year is a grid before it is a list of events, so the arrows move between cells and only
// belong to a day's events once the reader has stepped into one. Without the two stages ↑ and ↓
// would have to be both a week's worth of movement and an event's.
func TestYearArrowsMoveCellsUntilOneIsOpened(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(100, 30)
	v.now = func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local) }
	v.viewMode = viewYear
	v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	v.Update(yearLoadedMsg{requestResult: currentRequest(v), year: CalendarYear{
		SpannedEvents: []Recording{
			{ID: 7, Title: "Off to Split", AllDay: true, Type: "Calendar::Event",
				StartsAt: at("2026-08-21T00:00:00Z"), EndsAt: at("2026-08-21T00:00:00Z")},
			{ID: 8, Title: "MEETUP", AllDay: true, Type: "Calendar::Event",
				StartsAt: at("2026-08-21T00:00:00Z"), EndsAt: at("2026-08-21T00:00:00Z")},
		},
	}})

	// ↓ is a week while the reader is moving between cells, not an event.
	v.HandleContentKey(keyPress("down"))
	if got := v.day(); !sameDay(got, time.Date(2026, 8, 27, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("↓ landed on %s, want the week after", got.Format(time.DateOnly))
	}
	if v.selectedEvent != "" {
		t.Errorf("moving between cells selected event %q", v.selectedEvent)
	}
	v.HandleContentKey(keyPress("up"))

	// Nothing is selectable until a cell is opened, however much is in it.
	v.HandleContentKey(keyPress("right"))
	if events := v.selectableEvents(); len(events) != 0 {
		t.Fatalf("a cell nobody stepped into offers %+v", events)
	}

	// enter opens it and takes the first event, which is how the reader sees that ↑ and ↓
	// have changed hands.
	v.HandleContentKey(keyPress("enter"))
	if !v.inYearCell || v.selectedEvent != "7" {
		t.Fatalf("enter left inYearCell=%v selected=%q", v.inYearCell, v.selectedEvent)
	}
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "8" {
		t.Errorf("↓ inside the cell selected %q, want the next event", v.selectedEvent)
	}
	if got := v.day(); !sameDay(got, time.Date(2026, 8, 21, 0, 0, 0, 0, time.Local)) {
		t.Errorf("↓ inside the cell moved the day to %s", got.Format(time.DateOnly))
	}

	// esc comes through the model's own seam rather than as a key, and steps back out.
	if !v.CancelPendingDetail() {
		t.Fatal("esc did not step out of the cell")
	}
	if v.inYearCell || v.selectedEvent != "" {
		t.Errorf("leaving left inYearCell=%v selected=%q", v.inYearCell, v.selectedEvent)
	}
	if v.CancelPendingDetail() {
		t.Error("esc outside a cell should be the model's to deal with")
	}
}

// The all-day band is at the foot of the whole week, but the events in it belong to days, so ↑
// and ↓ reach the cursor day's own — and the band draws it as selected once they have.
func TestWeekReachesTheAllDayBand(t *testing.T) {
	v := dayWithEvents(t)
	v.viewMode = viewWeek
	v.anchor = time.Date(2026, 8, 21, 9, 0, 0, 0, time.Local) // the Friday
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 1, Title: "Changelog", CalendarColor: "blue", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-21T09:30:00"), EndsAt: atLocal("2026-08-21T10:00:00")},
		{ID: 3, Title: "Summer friday", AllDay: true, CalendarColor: "gold", Type: "Calendar::Event",
			StartsAt: at("2026-08-21T00:00:00Z"), EndsAt: at("2026-08-21T00:00:00Z")},
	}})

	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "3" {
		t.Fatalf("↓ ended on %q, want the all-day event under the cursor day", v.selectedEvent)
	}

	band := weekAllDayBand([]weekDayInfo{{
		date:   v.day(),
		allDay: []Recording{{ID: 3, Title: "Summer friday", AllDay: true, CalendarColor: "gold"}},
	}}, 13, v.selection())
	if len(band) != 1 {
		t.Fatalf("the band drew %d rows", len(band))
	}
	if band[0][0] == eventPill(Recording{ID: 3, Title: "Summer friday", CalendarColor: "gold"}, 13, selection{}) {
		t.Error("the band drew the selected all-day event as an unselected one")
	}
}

// Stepping the cursor off the end of the week is what reads the week either side of it, and the
// weekday is kept: ← off Monday lands on the Sunday before, not on the new week's Monday.
func TestWeekCursorStepsIntoTheWeekEitherSide(t *testing.T) {
	v := calendarWithRecordings()
	v.viewMode = viewWeek
	v.firstWeekDay = time.Monday
	v.anchor = time.Date(2026, 8, 17, 9, 0, 0, 0, time.Local) // the Monday

	v.HandleContentKey(keyPress("left"))
	if got := v.day(); !sameDay(got, time.Date(2026, 8, 16, 0, 0, 0, 0, time.Local)) {
		t.Errorf("← off Monday landed on %s, want the Sunday before", got.Format(time.DateOnly))
	}
	if !v.requests.loading {
		t.Error("leaving the week did not read the one before it")
	}
}

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
	events, todos, habits, completions, _ := splitRecordings([]Recording{
		{ID: 14796085, Title: "Read", Type: "Calendar::Habit", Icon: "read"},
		{ID: 14113260, Title: "Meditate", Type: "Calendar::Habit", Icon: "meditate"},
		{ID: 171477412, Type: "Calendar::Habit::Completion", ParentID: 14796085, StartsAt: at("2026-08-22T00:00:00Z")},
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
	if habits[0].Title != "Read" || !habits[0].CompletedAt.Equal(at("2026-08-22T00:00:00Z")) {
		t.Errorf("the completed habit was not marked done: %+v", habits[0])
	}
	if habits[1].Title != "Meditate" || habits[1].Done() {
		t.Errorf("a habit with no completion was marked done: %+v", habits[1])
	}
}

// HEY answers every timestamp in UTC, so a reader east or west of it sees an event on the
// wrong hour unless the view converts. The week said 14:00 for a 14:00Z meeting whatever
// time it was where they were.
func TestEventsAreShownOnTheReadersClock(t *testing.T) {
	zone, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skipf("time zone database unavailable: %v", err)
	}

	// 23:30 in Tokyo on the 20th is 14:30Z on the same day; 23:30Z is 08:30 on the 21st.
	utc := at("2026-08-20T23:30:00Z")
	event := Recording{Title: "Late call", Type: "Calendar::Event", StartsAt: utc, EndsAt: utc.Add(time.Hour)}

	// The instant is untouched, only the clock it is read on.
	if !event.Starts().Equal(utc) {
		t.Errorf("Starts() moved the instant: %v against %v", event.Starts(), utc)
	}

	// And the day it belongs to follows that clock, which is what puts it in the right
	// column of the week.
	inTokyo := utc.In(zone)
	if inTokyo.Day() != 21 {
		t.Fatalf("fixture is wrong: 23:30Z is %s in Tokyo", inTokyo.Format(time.RFC3339))
	}
	if got := dateKey(inTokyo); got == dateKey(utc) {
		t.Fatalf("fixture is wrong: the UTC and Tokyo dates should differ, both %s", got)
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
			StartsAt: at("2026-08-20T14:00:00Z"), EndsAt: at("2026-08-20T15:00:00Z")},
	}

	anchor := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	out := renderWeekView(events, habits, completions, anchor, time.Monday, 100, weekViewRows, "p/n week", nil, selection{})
	return strings.Split(stripANSI(out), "\n")
}

func habitDone(habitID int64, day string) Recording {
	return Recording{Type: "Calendar::Habit::Completion", ParentID: habitID, StartsAt: at(day + "T00:00:00Z")}
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

// An all-day event belongs to no hour, so the week gathers them at its foot rather than
// leaving them at whatever depth each day's timed events reached. An event keeps one row
// across every day it covers, so a week-long one is a single bar straight across.
func TestWeekGathersAllDayEventsAtTheFoot(t *testing.T) {
	events := []Recording{
		{ID: 9, Title: "Stanko & Kevin", CalendarColor: "blue", Type: "Calendar::Event",
			StartsAt: at("2026-08-20T14:00:00Z"), EndsAt: at("2026-08-20T15:00:00Z")},
		{ID: 10, Title: "Summer friday", CalendarColor: "gold", AllDay: true, Type: "Calendar::Event",
			StartsAt: at("2026-08-21T00:00:00Z"), EndsAt: at("2026-08-21T23:59:59Z")},
		{ID: 11, Title: "On call", CalendarColor: "green", AllDay: true, Type: "Calendar::Event",
			StartsAt: at("2026-08-17T00:00:00Z"), EndsAt: at("2026-08-23T23:59:59Z")},
	}

	anchor := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	out := renderWeekView(events, nil, nil, anchor, time.Monday, 100, weekViewRows, "p/n week", nil, selection{})
	lines := strings.Split(stripANSI(out), "\n")

	// The band is under the grid, headed as the day view heads its own.
	header := rowContaining(t, lines, "All day")
	if timed := rowContaining(t, lines, "Stanko & Kev"); timed > header {
		t.Errorf("a timed event is below the all-day band: row %d against %d", timed, header)
	}
	if header != len(lines)-3 {
		t.Errorf("the band is at row %d of %d, want it at the foot", header, len(lines))
	}

	// The week-long event holds one row all the way across; the single day goes below it.
	across := lines[header+1]
	if got := strings.Count(across, "On call"); got != 7 {
		t.Errorf("the week-long event covers %d days, want 7: %q", got, across)
	}
	if strings.Contains(across, "Summer friday") {
		t.Errorf("the one-day event took a row from the one spanning the week: %q", across)
	}
	if below := lines[header+2]; !strings.Contains(below, "Summer friday") {
		t.Errorf("the one-day event is not on the row under it: %q", below)
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

// The year is drawn as the week is: it names itself with the keys that move it, its days
// are dotted apart, and there is no box around the lot. It keeps a rule between weeks,
// which the week view has no need of — a row there is one line, and here it is as tall as
// its busiest day.
func TestYearIsDrawnLikeTheWeek(t *testing.T) {
	events := []Recording{
		{ID: 1, Title: "Switch out contact lense", CalendarColor: "blue", AllDay: true,
			StartsAt: at("2026-01-26T00:00:00Z"), EndsAt: at("2026-01-26T23:59:59Z"), Type: "Calendar::Event"},
		{ID: 3, Title: "MEETUP", CalendarColor: "gold", AllDay: true,
			StartsAt: at("2026-02-05T00:00:00Z"), EndsAt: at("2026-02-08T23:59:59Z"), Type: "Calendar::Event"},
	}

	out, _, _ := renderYearView(events, time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC),
		time.Monday, 100, 30, "p/n year · t today", selection{}, false)
	lines := strings.Split(stripANSI(out), "\n")

	// The year names itself and says what moves it, on its own first line.
	if !strings.HasPrefix(lines[0], "2026 ") || !strings.Contains(lines[0], "p/n year · t today") {
		t.Errorf("the year does not name itself with its keys: %q", lines[0])
	}

	// No box: nothing draws the old grid's corners or walls.
	for i, line := range lines {
		for _, boxed := range []string{"┌", "┬", "┐", "│", "├", "┼", "┤", "└", "┴", "┘"} {
			if strings.Contains(line, boxed) {
				t.Errorf("row %d still carries %q from the boxed grid: %q", i, boxed, line)
			}
		}
	}

	// Each cell says its own weekday, so there is no header row naming the columns.
	if !strings.Contains(lines[1], "MON") || !strings.Contains(lines[1], "JAN THU 1") {
		t.Errorf("the first week should be dates, not column names: %q", lines[1])
	}

	// Six dotted rules for seven days, and a solid one closing each week off.
	if got := strings.Count(lines[1], string(hourRule)); got != 6 {
		t.Errorf("a week row has %d dotted rules, want 6: %q", got, lines[1])
	}
	if !strings.HasPrefix(lines[2], strings.Repeat("─", 10)) {
		t.Errorf("no rule under the first week: %q", lines[2])
	}

	// A multi-day event fills every day it covers, each in its calendar's color.
	meetup := rowContaining(t, lines, "MEETUP")
	if got := strings.Count(lines[meetup], "MEETUP"); got != 4 {
		t.Errorf("MEETUP spans four days but appears %d times: %q", got, lines[meetup])
	}
}

// A calendar carries a day's own records alongside its events. Only the events are drawn:
// a journal entry taken for one came out as a bar of bare color across the day.
func TestOnlyEventsAreDrawnOnTheGrid(t *testing.T) {
	events, todos, habits, _, _ := splitRecordings([]Recording{
		{ID: 169118695, Title: "Stanko & Kevin", Type: "Calendar::Event", StartsAt: at("2026-08-20T14:00:00Z")},
		// The journal entry behind the stray stripe, as HEY answered it: no title.
		{ID: 171477000, Type: "Calendar::JournalEntry", AllDay: true, StartsAt: at("2026-08-20T00:00:00Z")},
		{ID: 171477001, Type: "Calendar::DayBackground", AllDay: true, StartsAt: at("2026-08-20T00:00:00Z")},
		// A time track has a name, and still is not an event.
		{ID: 171477002, Title: "Design work", Type: "Calendar::TimeTrack", StartsAt: at("2026-08-20T09:00:00Z")},
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
		{ID: 8, Title: "Evening walk", CompletedAt: at("2026-08-22T00:00:00Z")},
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
		{ID: 2, Title: "Send the invoice", CompletedAt: at("2026-08-24T08:00:00Z")},
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
		{ID: 1, Title: "Design review with Ryan", StartsAt: atLocal("2026-08-24T11:00:00"), EndsAt: atLocal("2026-08-24T12:00:00")},
		{ID: 2, Title: "Dentist", AllDay: true},
	}
	habits := []Recording{{ID: 4, Title: "Read 20 pages"}}

	day := time.Date(2026, 8, 24, 9, 0, 0, 0, time.Local)
	view := stripANSI(renderDayView(events, habits, nil, day, "p/n day", 100, 24, selection{}, nil))
	for _, label := range []string{"Habits", "Monday, August 24", "p/n day", "All day"} {
		if !strings.Contains(view, label) {
			t.Errorf("day view did not label its %q section: %q", label, view)
		}
	}
}

// An all-day event is a block in its calendar's color lying across the day, the same thing
// a timed one is standing up in the grid. It used to be drawn [like this─────], which said
// "all day" by reaching across and said nothing about whose it was.
func TestAllDayEventsAreBlocksInTheirCalendarsColor(t *testing.T) {
	events := []Recording{
		{ID: 1, Title: "Summer friday", CalendarColor: "gold", AllDay: true, Type: "Calendar::Event"},
		{ID: 2, Title: "Rosa and Stanko (On Call)", CalendarColor: "green", AllDay: true, Type: "Calendar::Event"},
	}
	day := time.Date(2026, 8, 21, 9, 0, 0, 0, time.Local)

	rendered := renderDayView(events, nil, nil, day, "", 100, 14, selection{}, nil)
	lines := strings.Split(rendered, "\n")

	gold := lines[rowContaining(t, lines, "Summer friday")]
	green := lines[rowContaining(t, lines, "Rosa and Stanko")]

	// Filled, and each in its own calendar's color rather than one style for both.
	for _, bar := range []string{gold, green} {
		if !strings.Contains(bar, "\x1b[") {
			t.Errorf("all-day bar carries no fill: %q", bar)
		}
		if strings.ContainsAny(stripANSI(bar), "[]─") {
			t.Errorf("all-day bar still drawn with brackets and dashes: %q", stripANSI(bar))
		}
	}
	if gold == green {
		t.Error("two all-day events on different calendars drew identically")
	}

	// The bar reaches across the day, so it reads as covering all of it.
	if got := lipgloss.Width(stripANSI(gold)); got < 90 {
		t.Errorf("all-day bar is %d columns of the ~97 the grid spans", got)
	}
}

func TestDayViewRulesFallFromEveryHourWithoutCuttingIntoAnEvent(t *testing.T) {
	events := []Recording{
		{ID: 1, Title: "Design review with Ryan", StartsAt: atLocal("2026-08-24T11:00:00"), EndsAt: atLocal("2026-08-24T12:00:00")},
	}
	day := time.Date(2026, 8, 24, 9, 0, 0, 0, time.Local)

	// 98 columns leaves 96 for the hours once the closing label has its two, which puts
	// an hour every four columns and the 11:00 event's block on the four from 44. The
	// day's header and the hour axis take the first two rows of the 40 it is given,
	// leaving 38 for the grid.
	lines := strings.Split(stripANSI(renderDayView(events, nil, nil, day, "", 98, 40, selection{}, nil)), "\n")
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
	view := stripANSI(renderDayView(nil, nil, nil, day, "", 96, 20, selection{}, nil))

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
		StartsAt: atLocal("2026-08-24T07:00:00"), EndsAt: atLocal("2026-08-24T10:00:00")}}

	// An hour every four columns, so 07:00 starts on column 28 and the block covers the
	// rules at 28 and 32 for every one of the grid's 38 rows.
	grid := strings.Split(stripANSI(renderDayView(events, nil, nil, day, "", 98, 40, selection{}, nil)), "\n")[2:]
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
		style := dayCell{kind: cellTitle, color: calendarColor}.style(styleMuted, 0)
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

	style := dayCell{kind: cellTitle, color: "blue"}.style(styleMuted, 0)
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
	onDark := eventPill(Recording{Title: "Summer friday", CalendarColor: "green"}, 20, selection{})

	applyTheme(Theme{
		Accent: lipgloss.BrightBlue, Bright: lipgloss.Color("#1c1c1c"), Dark: false,
		Background: lipgloss.Color("#fafafa"),
		Hues:       map[string]color.Color{"green": lipgloss.Color("#1f5c2f")},
	})
	onLight := eventPill(Recording{Title: "Summer friday", CalendarColor: "green"}, 20, selection{})

	if onDark == onLight {
		t.Errorf("the pill did not follow the theme: %q", onDark)
	}
}

// The day marks where the clock is, as the web app does: the time over the axis and a dashed
// line down the column, drawn over whatever it crosses.
func TestTheDayMarksWhereTheClockIs(t *testing.T) {
	now := time.Now()
	event := Recording{ID: 1, Title: "Design review", CalendarColor: "green", Type: "Calendar::Event",
		StartsAt: now.Truncate(time.Hour).Add(-time.Hour), EndsAt: now.Truncate(time.Hour).Add(2 * time.Hour)}

	lines := strings.Split(stripANSI(renderDayView([]Recording{event}, nil, nil,
		now, "p/n day", 100, 16, selection{}, nil)), "\n")
	if !strings.Contains(lines[1], now.Format("15")) || !strings.Contains(lines[1], now.Format("04")) {
		t.Errorf("the clock is not named over the axis: %q", lines[1])
	}

	// The line goes over the event happening now rather than behind it: hidden there, it would
	// be missing at the one moment it is worth having.
	crossings := 0
	for _, line := range lines {
		crossings += strings.Count(line, string(nowRule))
	}
	if crossings < 3 {
		t.Errorf("the line crosses only %d rows", crossings)
	}
	if firstRow := lines[3]; !strings.ContainsRune(firstRow, nowRule) {
		t.Errorf("no line on the first grid row: %q", firstRow)
	}

	// Where the line falls on an event's name it breaks around it. The name is the one thing on
	// the grid a reader has to be able to read, and a dash through the middle of it costs a
	// letter.
	placed := placedEvent{
		rec:      Recording{ID: 1, Title: "Design review", CalendarColor: "green"},
		startCol: 8, endCol: 20,
	}
	titleCol := placed.startCol + (placed.endCol-placed.startCol-1)/2
	grid := stripANSI(renderDayGrid([][]placedEvent{{placed}}, 40, 4, 15, titleCol, styleMuted, selection{}))
	if !strings.Contains(grid, "D") || !strings.Contains(grid, "w") {
		t.Errorf("the line ate the event's name:\n%s", grid)
	}
	// And it is still drawn everywhere else down that column — above and below the name.
	if strings.Count(grid, string(nowRule)) == 0 {
		t.Errorf("the line vanished behind the event:\n%s", grid)
	}

	// The colon blinks a second on and a second off, and is swapped for a space rather than
	// dropped so the digits either side of it never move.
	on := time.Date(2026, 8, 23, 15, 55, 0, 0, time.Local)
	lit := nowRow(on, 40, 100, nil)
	unlit := nowRow(on.Add(time.Second), 40, 100, nil)
	if !strings.Contains(lit, "15:55") {
		t.Errorf("an even second reads %q, want the colon", stripANSI(lit))
	}
	if !strings.Contains(unlit, "15 55") {
		t.Errorf("an odd second reads %q, want the colon blinked out", stripANSI(unlit))
	}
	if lipgloss.Width(stripANSI(lit)) != lipgloss.Width(stripANSI(unlit)) {
		t.Error("the blink moves the digits")
	}

	// A day the reader has stepped away from has no now on it, and gives back the row.
	yesterday := stripANSI(renderDayView(nil, nil, nil, now.AddDate(0, 0, -1), "p/n day", 100, 16, selection{}, nil))
	if strings.ContainsRune(yesterday, nowRule) {
		t.Error("yesterday is marked with a now line")
	}
	if strings.Contains(strings.Split(yesterday, "\n")[1], ":") {
		t.Error("yesterday kept the clock's row")
	}
}

// A running time track sits on the clock's row, at whichever end the clock has left room at. The
// clock crosses the day as the hours pass, so a badge fixed to one side would end up under it.
func TestARunningTrackTakesTheEndTheClockLeaves(t *testing.T) {
	now := time.Date(2026, 8, 23, 15, 55, 0, 0, time.Local)
	track := &runningTrack{category: "Deep work", since: now.Add(-95 * time.Minute)}

	// The clock near the right: the badge goes left.
	left := stripANSI(nowRow(now, 90, 100, track))
	if !strings.HasPrefix(left, "● Deep work 1:35:00") {
		t.Errorf("with the clock at the right the row reads %q", left)
	}

	// And near the left it goes right, ending at the edge.
	right := stripANSI(nowRow(now, 6, 100, track))
	if !strings.HasSuffix(right, "● Deep work 1:35:00") {
		t.Errorf("with the clock at the left the row reads %q", right)
	}

	// The clock is never given up for the badge: a row too narrow for both keeps the time.
	narrow := stripANSI(nowRow(now, 10, 22, track))
	if !strings.Contains(narrow, "15:55") {
		t.Errorf("the badge crowded out the clock: %q", narrow)
	}
	if strings.Contains(narrow, "Deep work") {
		t.Errorf("the badge was drawn with nowhere to put it: %q", narrow)
	}

	// Elapsed is hours and minutes, not seconds: it sits beside a colon already blinking once a
	// second, and two things counting at once is one too many.
	if got := (runningTrack{category: "Errands", since: now.Add(-45 * time.Second)}).badge(now); got != "● Errands 0:00:45" {
		t.Errorf("badge = %q", got)
	}
	// A track with no category still says something rather than nothing.
	if got := (runningTrack{since: now.Add(-time.Hour)}).badge(now); got != "● Tracking 1:00:00" {
		t.Errorf("badge = %q", got)
	}
}

// The badge reads what the time tracking menu left on the view, so it is on screen while the
// menu is closed — which is the only time anybody would see it.
func TestTheDayShowsWhatIsBeingTracked(t *testing.T) {
	v := dayWithEvents(t)
	v.now = time.Now

	if v.trackBadge() != nil {
		t.Fatal("a badge with nothing running")
	}

	started := time.Now().Add(-2 * time.Hour)
	v.ongoing = &OngoingTrack{ID: 7, Category: "Client work", StartedAt: started}
	badge := v.trackBadge()
	if badge == nil || badge.category != "Client work" || !badge.since.Equal(started) {
		t.Fatalf("badge = %+v", badge)
	}

	v.rebuildView()
	if !strings.Contains(stripANSI(v.View()), "● Client work 2:00:00") {
		t.Errorf("the day does not show what is being tracked:\n%s", stripANSI(v.View()))
	}
}

// The clock's own tick is separate from the highlight's, and each stops when nothing has drawn
// the calendar since it last fired — which is how they notice the reader went to another section
// without a hook saying so. Two flags, because one tick would keep clearing the other's.
func TestTheClockFollowsOnlyTheDayThatIsToday(t *testing.T) {
	// Reading a day is what starts it: every span change and every step lands there.
	v := dayWithEvents(t)
	if !v.tickingClock {
		t.Fatal("reading a day that is today did not start the clock")
	}
	if cmd := v.followClock(); cmd != nil {
		t.Error("a second clock was started alongside the first")
	}
	v.now = time.Now

	v.View()
	if cmd, _ := v.Update(calendarClockMsg{}); cmd == nil {
		t.Error("a tick on a drawn day did not schedule the next")
	}
	if cmd, _ := v.Update(calendarClockMsg{}); cmd != nil {
		t.Error("the clock went on with the calendar off screen")
	}

	// The week and the year draw no line, and a day the reader stepped off has no now.
	v.View()
	v.viewMode = viewWeek
	if cmd := v.followClock(); cmd != nil {
		t.Error("the week runs a clock it does not draw")
	}
	v.viewMode = viewDay
	v.anchor = time.Now().AddDate(0, 0, -3)
	if cmd := v.followClock(); cmd != nil {
		t.Error("a day that is not today runs a clock")
	}
}

// A countdown is a recording of its own — a child of the event, spanning the days from the
// notice period up to it — and it goes above the date rather than on the grid, the way the web
// app puts it. It has no title of its own: what it is counting down to is its parent's.
func TestTheDayCountsDownToWhatIsComing(t *testing.T) {
	countdown := sdkRecordingToModel(generated.Recording{
		Id: 171603042, ParentId: 171603041, Type: "Calendar::Countdown", AllDay: true,
		Label:    "10 days before",
		StartsAt: at("2026-08-15T00:00:00Z"), EndsAt: at("2026-08-25T00:00:00Z"),
		Parent: &generated.Recording{Id: 171603041, Title: "Kevin's leaving do", Type: "Calendar::Event"},
	})
	if countdown.ParentTitle != "Kevin's leaving do" {
		t.Fatalf("the countdown does not know what it counts down to: %+v", countdown)
	}

	day := time.Date(2026, 8, 23, 9, 0, 0, 0, time.Local)
	lines := strings.Split(stripANSI(renderDayView(nil, nil, []Recording{countdown},
		day, "p/n day", 100, 20, selection{}, nil)), "\n")
	if got := strings.TrimSpace(lines[0]); got != "2 days until Kevin's leaving do" {
		t.Errorf("the day says %q", got)
	}

	// One day out reads as a day, not as days.
	lines = strings.Split(stripANSI(renderDayView(nil, nil, []Recording{countdown},
		day.AddDate(0, 0, 1), "p/n day", 100, 20, selection{}, nil)), "\n")
	if got := strings.TrimSpace(lines[0]); got != "1 day until Kevin's leaving do" {
		t.Errorf("the day before says %q", got)
	}

	// And on the day itself there is nothing left to count: HEY stops serving the countdown,
	// and a day that was handed one anyway does not say "0 days".
	view := stripANSI(renderDayView(nil, nil, []Recording{countdown},
		time.Date(2026, 8, 25, 9, 0, 0, 0, time.Local), "p/n day", 100, 20, selection{}, nil))
	if strings.Contains(view, "until") {
		t.Errorf("the event's own day still counts down: %q", strings.Split(view, "\n")[0])
	}
}

// A countdown is not an event, so it never reaches the grid — it used to be dropped altogether,
// which is why the day never showed one.
func TestCountdownsAreKeptApartFromEvents(t *testing.T) {
	events, todos, habits, _, countdowns := splitRecordings([]Recording{
		{ID: 1, Title: "Design review", Type: "Calendar::Event"},
		{ID: 2, ParentID: 1, Type: "Calendar::Countdown", Label: "10 days before"},
	})
	if len(events) != 1 || len(countdowns) != 1 {
		t.Errorf("split gave %d events and %d countdowns", len(events), len(countdowns))
	}
	if len(todos) != 0 || len(habits) != 0 {
		t.Errorf("a countdown landed among the todos or habits")
	}
}

// Two events at the same hour share the height, with a row of grid between them: touching, a
// tall block over a short one read as one block with a step in it.
func TestOverlappingEventsAreDrawnApart(t *testing.T) {
	out := stripANSI(renderDayView([]Recording{
		{ID: 1, Title: "Standup", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T09:00:00"), EndsAt: atLocal("2026-08-20T11:00:00")},
		{ID: 2, Title: "Design review", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T10:00:00"), EndsAt: atLocal("2026-08-20T12:00:00")},
	}, nil, nil, time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), "p/n day", 100, 20, selection{}, nil))

	// The row between the lanes belongs to the grid, so it carries an hour rule where an
	// event's own rows carry the event.
	gaps := 0
	for _, row := range strings.Split(out, "\n") {
		if strings.Count(row, string(hourRule)) == 25 {
			gaps++
		}
	}
	if gaps == 0 {
		t.Errorf("the two lanes are drawn without a row between them:\n%s", out)
	}
}

// Two events back to back are two events. Drawn as touching blocks in the same color, a
// 15:00–17:00 and a 17:00–19:00 came out as one bar from three to seven, and the hour axis above
// them is not enough to say where one stops.
func TestBackToBackEventsAreDividedFromEachOther(t *testing.T) {
	out := stripANSI(renderDayView([]Recording{
		{ID: 1, Title: "Stanko & Kevin", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T15:00:00"), EndsAt: atLocal("2026-08-20T17:00:00")},
		{ID: 2, Title: "Product Hangout", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T17:00:00"), EndsAt: atLocal("2026-08-20T19:00:00")},
	}, nil, nil, time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), "p/n day", 100, 20, selection{}, nil))

	if !strings.ContainsRune(out, eventEdge) {
		t.Errorf("nothing separates the two events:\n%s", out)
	}
}

// A week column says when an event runs, both ends of it where there is room: when a meeting
// finishes is half of what a reader is looking for.
func TestWeekColumnSaysWhenAnEventRuns(t *testing.T) {
	event := Recording{ID: 1, Title: "Design review", Type: "Calendar::Event",
		StartsAt: atLocal("2026-08-20T14:00:00"), EndsAt: atLocal("2026-08-20T15:30:00")}

	if got := eventTimeSpan(event, 13); got != "14:00–15:30" {
		t.Errorf("a wide column says %q, want both ends", got)
	}
	// A narrow one gets the start alone rather than a truncated range.
	if got := eventTimeSpan(event, 8); got != "14:00" {
		t.Errorf("a narrow column says %q, want the start alone", got)
	}
	// An event with no end has only the one.
	if got := eventTimeSpan(Recording{StartsAt: atLocal("2026-08-20T14:00:00")}, 13); got != "14:00" {
		t.Errorf("an event with no end says %q", got)
	}
}

// The selected event is marked by a highlight travelling along it rather than by a color of its
// own, so it keeps its calendar's color — which is what says whose it is.
func TestTheSelectedEventIsHighlightedAndMoves(t *testing.T) {
	t.Cleanup(func() { applyTheme(Theme{Accent: lipgloss.BrightBlue, Bright: lipgloss.BrightWhite, Dark: true}) })
	applyTheme(Theme{
		Accent: lipgloss.BrightBlue, Bright: lipgloss.Color("#ffcead"), Dark: true,
		Background: lipgloss.Color("#060B1E"),
		Hues:       map[string]color.Color{"green": lipgloss.Color("#92a593")},
	})

	event := Recording{ID: 4, Title: "Design review", CalendarColor: "green"}
	at := func(phase int) string {
		return eventPill(event, 20, selection{eventKey: event.key(), phase: phase})
	}

	if at(0) == eventPill(event, 20, selection{}) {
		t.Error("the selected event is drawn the same as an unselected one")
	}
	if at(0) == at(1) {
		t.Error("the highlight did not move when the phase did")
	}
	// The text is untouched: only how it is painted changes.
	if got := stripANSI(at(3)); got != "Design review       " {
		t.Errorf("the highlight rewrote the title: %q", got)
	}

	// A lap of the pill brings the light back to where it started, which is what makes it a
	// loop rather than a one-way slide off the end.
	if at(0) != at(20) {
		t.Error("the light does not come round again")
	}
}

// On a block the light runs round the edge and leaves the inside alone, which is what makes it
// read as turning rather than as a wave washing over the event — and it keeps a name written
// down the middle of a day's block as legible as any other event's.
func TestTheLightRunsRoundABlocksEdge(t *testing.T) {
	// A 6×4 block: the edge is every cell but the four in the middle.
	inside, outside := 0, 0
	for phase := range 40 {
		for row := range 4 {
			for col := range 6 {
				lit := sweepIntensity(col, row, 6, 4, phase)
				if col > 0 && col < 5 && row > 0 && row < 3 {
					inside += int(lit * 100)
				} else {
					outside += int(lit * 100)
				}
			}
		}
	}
	if inside != 0 {
		t.Errorf("the light reached inside the block (%d)", inside)
	}
	if outside == 0 {
		t.Error("the light never touched the edge")
	}

	// One cell thick, there is no inside to leave alone, so the light runs the length of it.
	if sweepIntensity(0, 0, 12, 1, 0) == 0 && sweepIntensity(1, 0, 12, 1, 0) == 0 {
		t.Error("a single row is never lit")
	}
}

// A color the terminal draws out of its own palette must not be mixed: lipgloss.Blue carries
// #000080 whatever the theme paints it, so blending it would put a color from a palette nobody
// is using on screen. Without a theme the selected event inverts instead, as it always did.
func TestTheHighlightWillNotMixAnANSISlot(t *testing.T) {
	if _, ok := mixColors(lipgloss.Blue, lipgloss.BrightWhite, 0.5); ok {
		t.Error("two ANSI slots were mixed")
	}
	if _, ok := mixColors(lipgloss.Color("#7d82d9"), lipgloss.Blue, 0.5); ok {
		t.Error("a theme hue was mixed with an ANSI slot")
	}

	mixed, ok := mixColors(lipgloss.Color("#000000"), lipgloss.Color("#ffffff"), 0.5)
	if !ok {
		t.Fatal("two theme colors would not mix")
	}
	if mixed != (color.RGBA{R: 0x7f, G: 0x7f, B: 0x7f, A: 0xff}) {
		t.Errorf("half way between black and white is %v", mixed)
	}
}

// The week and the year draw an event as a filled bar too, off the same field and padded to
// the cell so the fill reads as a block rather than as a highlight behind some words.
func TestEventPillFillsTheCellInItsCalendarsColor(t *testing.T) {
	pill := eventPill(Recording{Title: "Standup", CalendarColor: "gold"}, 12, selection{})

	if got := stripANSI(pill); got != "Standup     " {
		t.Errorf("pill text = %q, want the title padded to 12", got)
	}
	if !strings.Contains(pill, "\x1b[") {
		t.Errorf("pill carries no styling: %q", pill)
	}

	// A title longer than the cell is cut to it rather than spilling into the next day.
	long := eventPill(Recording{Title: "Design review with the whole team", CalendarColor: "teal"}, 12, selection{})
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
			StartsAt: atLocal("2026-08-24T11:00:00"), EndsAt: atLocal("2026-08-24T12:00:00")},
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
	// The day view offers the arrows and a new event, the calendars, time tracking and the
	// habits modal. Creating, editing and deleting a habit are the modal's own keys; the keys
	// that move the day are on the day's own line; and each span's number is in its own tab.
	bindings := v.HelpBindings()
	for _, want := range []string{"←→", "a", "g", "l", "b"} {
		found := false
		for _, binding := range bindings {
			found = found || binding.key == want
		}
		if !found {
			t.Errorf("missing binding %q: %+v", want, bindings)
		}
	}

	// e and x act on the event under the arrows, so they wait for one. This fixture's
	// events are on another day, so nothing here is selectable.
	for _, binding := range bindings {
		if binding.key == "e" || binding.key == "x" {
			t.Errorf("%q is offered with nothing selected: %+v", binding.key, bindings)
		}
	}

	onEvents := dayWithEvents(t)
	onEvents.HandleContentKey(keyPress("right"))
	for _, want := range []string{"e", "x"} {
		found := false
		for _, binding := range onEvents.HelpBindings() {
			found = found || binding.key == want
		}
		if !found {
			t.Errorf("missing binding %q once an event is selected: %+v", want, onEvents.HelpBindings())
		}
	}

	// And x says it will ask again, as deleting a habit does.
	onEvents.HandleContentKey(keyPress("x"))
	asked := false
	for _, binding := range onEvents.HelpBindings() {
		asked = asked || binding.desc == "press x again to delete"
	}
	if !asked {
		t.Errorf("x did not ask twice: %+v", onEvents.HelpBindings())
	}

	// Every span names itself and carries its own keys now, as the day always did, so the
	// help bar repeats none of them.
	for _, mode := range []calendarViewMode{viewDay, viewWeek, viewYear} {
		v.viewMode = mode
		for _, binding := range v.HelpBindings() {
			if binding.key == "p/n" || binding.key == "t" {
				t.Errorf("%v: its own line says %q; the help bar should not: %+v",
					mode, binding.key, v.HelpBindings())
			}
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
