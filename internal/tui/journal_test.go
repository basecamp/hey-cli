package tui

import (
	"testing"

	"github.com/basecamp/hey-cli/internal/models"
)

func TestJournalSetItemsFiltersAndSorts(t *testing.T) {
	recordings := []models.Recording{
		{ID: 1, Title: "Event", Type: "Calendar::Event", StartsAt: "2026-03-04T10:00:00Z"},
		{ID: 2, Title: "Entry B", Type: "Calendar::JournalEntry", StartsAt: "2026-03-02T00:00:00Z"},
		{ID: 3, Title: "Entry A", Type: "Calendar::JournalEntry", StartsAt: "2026-03-04T00:00:00Z"},
		{ID: 4, Title: "Entry C", Type: "Calendar::JournalEntry", StartsAt: "2026-03-01T00:00:00Z"},
	}

	m := newJournalModel()
	m.setItems(recordings)

	items := m.list.Items()
	if len(items) != 3 {
		t.Fatalf("expected 3 journal entries, got %d", len(items))
	}

	// Should be reverse-chronological
	wantIDs := []int{3, 2, 4}
	for i, wantID := range wantIDs {
		got := items[i].(journalItem).recording.ID
		if got != wantID {
			t.Errorf("item %d: got ID %d, want %d", i, got, wantID)
		}
	}
}
