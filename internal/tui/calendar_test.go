package tui

import (
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/models"
)

func testCalendars() []models.Calendar {
	return []models.Calendar{
		{ID: 10, Name: "Work", Kind: "owned"},
		{ID: 11, Name: "Personal", Kind: "personal"},
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
	v.Update(calendarsLoadedMsg(testCalendars()))
	v.Update(recordingsLoadedMsg{recordings: testRecordings()})
	return v
}

// --- Init ---

func TestCalendarViewInitFetchesCalendars(t *testing.T) {
	v := newCalendarView(testVC())
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init with no calendars should return a fetch command")
	}
	if !v.loading {
		t.Error("Init should set loading = true")
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
	_, consumed := v.Update(calendarsLoadedMsg(testCalendars()))
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
	v.loading = true

	_, consumed := v.Update(recordingsLoadedMsg{recordings: testRecordings()})
	if !consumed {
		t.Error("recordingsLoadedMsg should be consumed")
	}
	if v.loading {
		t.Error("loading should be false after recordings loaded")
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

func TestCalendarViewHandlesRecordingDetail(t *testing.T) {
	v := calendarWithRecordings()

	_, consumed := v.Update(recordingDetailMsg{title: "Standup", body: "Some detail"})
	if !consumed {
		t.Error("recordingDetailMsg should be consumed")
	}
	if !v.inThread {
		t.Error("should be in thread after detail loaded")
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
	if !v.loading {
		t.Error("SubnavRight should set loading")
	}

	v.loading = false
	v.SubnavRight()
	if v.calIndex != 1 {
		t.Errorf("SubnavRight at end: calIndex = %d, want 1", v.calIndex)
	}
}

// --- Thread state ---

func TestCalendarViewInThread(t *testing.T) {
	v := newCalendarView(testVC())
	if v.InThread() {
		t.Error("should not be in thread initially")
	}
	v.inThread = true
	if !v.InThread() {
		t.Error("InThread should return true")
	}
	v.ExitThread()
	if v.InThread() {
		t.Error("ExitThread should clear thread state")
	}
}

// --- Help bindings ---

func TestCalendarViewHelpBindingsShowsViewToggle(t *testing.T) {
	v := calendarWithRecordings()
	bindings := v.HelpBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].key != "v" {
		t.Errorf("binding key = %q, want \"v\"", bindings[0].key)
	}
}
