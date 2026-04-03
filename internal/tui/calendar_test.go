package tui

import (
	"testing"

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
		{ID: 200, Title: "Standup", StartsAt: "2025-03-01T09:00:00Z", Type: "event"},
		{ID: 201, Title: "Lunch", StartsAt: "2025-03-01T12:00:00Z", AllDay: false, Type: "event"},
	}
}

func calendarWithRecordings() *calendarView {
	v := newCalendarView(testVC())
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
	v.calendars = testCalendars()
	v.loading = true

	_, consumed := v.Update(recordingsLoadedMsg{recordings: testRecordings()})
	if !consumed {
		t.Error("recordingsLoadedMsg should be consumed")
	}
	if v.loading {
		t.Error("loading should be false after recordings loaded")
	}
	if len(v.recordingL.recordings) != 2 {
		t.Errorf("expected 2 recordings, got %d", len(v.recordingL.recordings))
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

func TestCalendarViewIgnoresUnrelatedMessages(t *testing.T) {
	v := newCalendarView(testVC())
	_, consumed := v.Update(boxesLoadedMsg{})
	if consumed {
		t.Error("boxesLoadedMsg should not be consumed by calendarView")
	}
}

// --- Content key handling ---

func TestCalendarViewContentKeyUpDown(t *testing.T) {
	v := calendarWithRecordings()

	if v.recordingL.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", v.recordingL.cursor)
	}

	v.HandleContentKey(keyPress("down"))
	if v.recordingL.cursor != 1 {
		t.Errorf("after down: cursor = %d, want 1", v.recordingL.cursor)
	}

	v.HandleContentKey(keyPress("up"))
	if v.recordingL.cursor != 0 {
		t.Errorf("after up: cursor = %d, want 0", v.recordingL.cursor)
	}
}

func TestCalendarViewContentKeyEnter(t *testing.T) {
	v := calendarWithRecordings()
	v.Resize(80, 30)

	cmd := v.HandleContentKey(keyPress("enter"))
	if cmd == nil {
		t.Fatal("enter on a recording should return a command")
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
	if label != "Work" {
		t.Errorf("label = %q, want Work", label)
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

func TestCalendarViewHelpBindingsEmpty(t *testing.T) {
	v := calendarWithRecordings()
	bindings := v.HelpBindings()
	if len(bindings) != 0 {
		t.Errorf("calendar should have no extra bindings, got %d", len(bindings))
	}
}
