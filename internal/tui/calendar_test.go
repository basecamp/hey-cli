package tui

import (
	"testing"

	"github.com/basecamp/hey-cli/internal/models"
)

func TestCalendarSetItemsKeyOrdering(t *testing.T) {
	cal := models.Calendar{Name: "Test"}
	resp := models.RecordingsResponse{
		"Calendar::Event":        {{ID: 1, Title: "Meeting", StartsAt: "2026-03-04T10:00:00Z"}},
		"Calendar::Appointment":  {{ID: 2, Title: "Doctor", StartsAt: "2026-03-04T14:00:00Z"}},
		"Calendar::JournalEntry": {{ID: 3, Title: "Notes", StartsAt: "2026-03-04T00:00:00Z"}},
	}

	m := newCalendarModel()
	m.setItems(cal, resp)

	items := m.list.Items()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Keys should be alphabetically sorted
	got := []string{
		items[0].(recordingItem).recType,
		items[1].(recordingItem).recType,
		items[2].(recordingItem).recType,
	}
	want := []string{"Calendar::Appointment", "Calendar::Event", "Calendar::JournalEntry"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d: got type %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCalendarSetItemsWithinKeyOrder(t *testing.T) {
	cal := models.Calendar{Name: "Test"}
	resp := models.RecordingsResponse{
		"Calendar::Event": {
			{ID: 1, Title: "First", StartsAt: "2026-03-04T09:00:00Z"},
			{ID: 2, Title: "Second", StartsAt: "2026-03-04T10:00:00Z"},
			{ID: 3, Title: "Third", StartsAt: "2026-03-04T11:00:00Z"},
		},
	}

	m := newCalendarModel()
	m.setItems(cal, resp)

	items := m.list.Items()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Within a key, insertion order is preserved
	for i, wantTitle := range []string{"First", "Second", "Third"} {
		got := items[i].(recordingItem).recording.Title
		if got != wantTitle {
			t.Errorf("item %d: got title %q, want %q", i, got, wantTitle)
		}
	}
}
