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

func TestThreadListPage(t *testing.T) {
	first := heyJSON(t, "thread", "list", "--in", "sent")
	nextPage, _ := first.Meta["next_page"].(string)
	if nextPage == "" {
		t.Skip("Sent has no next page in this fixture")
	}

	response := heyJSON(t, "thread", "list", "--in", "sent", "--page", nextPage)
	if strings.TrimSpace(response.Summary) == "" {
		t.Fatal("expected a result summary")
	}
	if string(response.Data) == "null" {
		t.Fatal("expected an array, got null")
	}
}
