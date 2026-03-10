package cmd

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

func TestFormatTimestampUTC(t *testing.T) {
	// 2024-01-15 00:00:00 UTC
	// In a non-UTC local timezone (e.g. America/Los_Angeles, UTC-8) this
	// would render as 2024-01-14 if formatted in local time.
	ts := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	got := formatTimestamp(ts)
	want := "2024-01-15T00:00"
	if got != want {
		t.Errorf("formatTimestamp = %q, want %q", got, want)
	}
}

func TestFormatTimestampMidDay(t *testing.T) {
	ts := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)
	got := formatTimestamp(ts)
	want := "2024-01-15T14:00"
	if got != want {
		t.Errorf("formatTimestamp = %q, want %q", got, want)
	}
}

func TestFormatTimestampZero(t *testing.T) {
	var ts time.Time
	got := formatTimestamp(ts)
	if got != "" {
		t.Errorf("formatTimestamp(zero) = %q, want empty", got)
	}
}

func TestFormatDateUTC(t *testing.T) {
	// 2024-01-15 00:00:00 UTC — must stay 2024-01-15 regardless of local timezone
	ts := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	got := formatDate(ts)
	want := "2024-01-15"
	if got != want {
		t.Errorf("formatDate = %q, want %q", got, want)
	}
}

func TestFormatDateZero(t *testing.T) {
	var ts time.Time
	got := formatDate(ts)
	if got != "" {
		t.Errorf("formatDate(zero) = %q, want empty", got)
	}
}

func TestFindPersonalCalendarIDByFlag(t *testing.T) {
	calendars := []generated.Calendar{
		{Id: 1, Name: "Work", Personal: false},
		{Id: 110, Name: "", Personal: true},
	}
	id, err := findPersonalCalendarID(calendars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 110 {
		t.Errorf("findPersonalCalendarID = %d, want 110", id)
	}
}

func TestFindPersonalCalendarIDByName(t *testing.T) {
	calendars := []generated.Calendar{
		{Id: 1, Name: "Work", Personal: false},
		{Id: 42, Name: "Personal", Personal: false},
	}
	id, err := findPersonalCalendarID(calendars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Errorf("findPersonalCalendarID = %d, want 42", id)
	}
}

func TestFindPersonalCalendarIDNotFound(t *testing.T) {
	calendars := []generated.Calendar{
		{Id: 1, Name: "Work", Personal: false},
	}
	_, err := findPersonalCalendarID(calendars)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUnwrapCalendarsNil(t *testing.T) {
	result := unwrapCalendars(nil)
	if result != nil {
		t.Errorf("unwrapCalendars(nil) = %v, want nil", result)
	}
}

func TestUnwrapCalendars(t *testing.T) {
	payload := &generated.CalendarListPayload{
		Calendars: []generated.CalendarWithRecordingChangesUrl{
			{Calendar: generated.Calendar{Id: 1, Name: "Work"}},
			{Calendar: generated.Calendar{Id: 2, Name: "Personal"}},
		},
	}
	result := unwrapCalendars(payload)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].Id != 1 || result[1].Id != 2 {
		t.Errorf("IDs = [%d, %d], want [1, 2]", result[0].Id, result[1].Id)
	}
}

func TestFilterRecordingsByType(t *testing.T) {
	resp := &generated.CalendarRecordingsResponse{
		"Calendar::Todo":         {{Id: 1, Title: "Todo"}},
		"Calendar::JournalEntry": {{Id: 2, Title: "Journal"}},
	}
	todos := filterRecordingsByType(resp, "Calendar::Todo")
	if len(todos) != 1 || todos[0].Id != 1 {
		t.Errorf("unexpected todos: %v", todos)
	}
	missing := filterRecordingsByType(resp, "Calendar::TimeTrack")
	if missing != nil {
		t.Errorf("expected nil for missing type, got %v", missing)
	}
	nilResult := filterRecordingsByType(nil, "Calendar::Todo")
	if nilResult != nil {
		t.Errorf("expected nil for nil resp, got %v", nilResult)
	}
}
