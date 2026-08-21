package tui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/models"
)

func testCalendars() []models.Calendar {
	return []models.Calendar{
		{ID: 10, Name: "Work", Kind: "owned"},
		{ID: 11, Name: "Personal", Kind: "personal", Personal: true},
	}
}

func testRecordings() []models.Recording {
	return []models.Recording{
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

func TestCalendarViewModeCycle(t *testing.T) {
	v := calendarWithRecordings()

	if v.viewMode != viewDay {
		t.Fatalf("initial mode = %v, want Day", v.viewMode)
	}

	v.HandleContentKey(keyPress("v"))
	if v.viewMode != viewWeek {
		t.Errorf("after first v: mode = %v, want Week", v.viewMode)
	}

	v.HandleContentKey(keyPress("v"))
	if v.viewMode != viewYear {
		t.Errorf("after second v: mode = %v, want Year", v.viewMode)
	}

	v.HandleContentKey(keyPress("v"))
	if v.viewMode != viewDay {
		t.Errorf("after third v: mode = %v, want Day (wrap around)", v.viewMode)
	}
}

// --- Subnav ---

func TestCalendarViewSubnavItems(t *testing.T) {
	v := calendarWithRecordings()
	items, selected, label, centered := v.SubnavItems()

	if len(items) != 2 {
		t.Errorf("expected 2 subnav items, got %d", len(items))
	}
	if selected != 0 {
		t.Errorf("selected = %d, want 0", selected)
	}
	if label != "Work · Day" {
		t.Errorf("label = %q, want \"Work · Day\"", label)
	}
	if !centered {
		t.Error("calendar subnav should be centered")
	}
}

func TestCalendarViewSubnavLeftRight(t *testing.T) {
	v := calendarWithRecordings()

	v.SubnavLeft()
	if v.calIndex != 0 {
		t.Errorf("SubnavLeft at 0: calIndex = %d, want 0", v.calIndex)
	}

	v.SubnavRight()
	if v.calIndex != 1 {
		t.Errorf("after SubnavRight: calIndex = %d, want 1", v.calIndex)
	}
	if !v.requests.loading {
		t.Error("SubnavRight should start a read")
	}

	v.requests.finish(v.requests.id)
	v.SubnavRight()
	if v.calIndex != 1 {
		t.Errorf("SubnavRight at end: calIndex = %d, want 1", v.calIndex)
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
	fresh := recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []models.Recording{
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
	v.Update(calendarsLoadedMsg{requestResult: currentRequest(v), calendars: []models.Calendar{{ID: 12, Name: "Rob Zolkos", Personal: true}}})

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
	v.deleteHabit(models.Recording{ID: 202, Title: "Read a book"})

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
		[]models.Recording{{ID: 200, StartsAt: "2025-03-01T09:00:00Z", Type: "CalendarEvent", Label: "Launch day"}},
		[]models.Recording{{ID: 203, StartsAt: "2025-03-02T00:00:00Z", Type: "CalendarTodo", Label: "Moving day"}},
		[]models.Recording{{ID: 202, StartsAt: "2025-03-03T06:00:00Z", Type: "Habit", Label: "Rest day"}},
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

// --- Help bindings ---

func TestCalendarViewHelpBindingsShowsViewToggle(t *testing.T) {
	v := calendarWithRecordings()
	v.calIndex = 1
	bindings := v.HelpBindings()
	if len(bindings) != 6 {
		t.Fatalf("expected 6 bindings, got %d", len(bindings))
	}
	for _, want := range []string{"v", "c", "a", "[/]", "e", "x"} {
		found := false
		for _, binding := range bindings {
			found = found || binding.key == want
		}
		if !found {
			t.Errorf("missing binding %q: %+v", want, bindings)
		}
	}
}
