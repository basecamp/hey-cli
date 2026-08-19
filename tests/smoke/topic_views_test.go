package smoke_test

import (
	"strings"
	"testing"
)

type topicViewItem struct {
	ID int64 `json:"id"`
}

func TestTopicViews(t *testing.T) {
	for _, command := range []string{"sent", "spammed", "trashed", "everything"} {
		t.Run(command, func(t *testing.T) {
			response := heyJSON(t, command)
			if strings.TrimSpace(response.Summary) == "" {
				t.Fatal("expected a result summary")
			}
			if string(response.Data) == "null" {
				t.Fatal("expected an array, got null")
			}
			for _, topic := range dataAs[[]topicViewItem](t, response) {
				if topic.ID <= 0 {
					t.Errorf("expected positive topic ID, got %d", topic.ID)
				}
			}
		})
	}
}

func TestTopicViewPage(t *testing.T) {
	response := heyJSON(t, "sent", "--page", "2")
	if strings.TrimSpace(response.Summary) == "" {
		t.Fatal("expected a result summary")
	}
	if string(response.Data) == "null" {
		t.Fatal("expected an array, got null")
	}
}
