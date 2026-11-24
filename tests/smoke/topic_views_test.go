package smoke_test

import (
	"strings"
	"testing"
)

type threadListItem struct {
	ID      int64 `json:"id"`
	TopicID int64 `json:"topic_id"`
}

func TestThreadListSystemViews(t *testing.T) {
	for _, view := range []string{"sent", "spam", "trash", "everything"} {
		t.Run(view, func(t *testing.T) {
			response := heyJSON(t, "thread", "list", "--in", view)
			if strings.TrimSpace(response.Summary) == "" {
				t.Fatal("expected a result summary")
			}
			if string(response.Data) == "null" {
				t.Fatal("expected an array, got null")
			}
			for _, topic := range dataAs[[]threadListItem](t, response) {
				if topic.ID <= 0 || topic.TopicID != topic.ID {
					t.Errorf("expected matching positive id and topic_id, got %#v", topic)
				}
			}
		})
	}
}

// Trash pages by a keyset cursor and filters in its query, so a full first page with a
// next_page is a page that continues: the next page must hold other threads.
func TestThreadListPage(t *testing.T) {
	first := heyJSON(t, "thread", "list", "--in", "trash")
	nextPage, _ := first.Meta["next_page"].(string)
	firstRows := dataAs[[]threadListItem](t, first)
	if nextPage == "" || len(firstRows) == 0 {
		skipf(t, "Trash has no second page in this fixture")
	}

	response := heyJSON(t, "thread", "list", "--in", "trash", "--page", nextPage)
	if strings.TrimSpace(response.Summary) == "" {
		t.Fatal("expected a result summary")
	}
	rows := dataAs[[]threadListItem](t, response)
	if len(rows) == 0 {
		t.Fatalf("--page %s answered no threads", nextPage)
	}
	firstIDs := map[int64]bool{}
	for _, topic := range firstRows {
		firstIDs[topic.ID] = true
	}
	for _, topic := range rows {
		if firstIDs[topic.ID] {
			t.Errorf("thread %d is on both the first page and the next", topic.ID)
		}
	}
}
