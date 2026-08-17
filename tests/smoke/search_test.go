package smoke_test

import "testing"

type SearchTopic struct {
	ID int64 `json:"id"`
}

func TestSearch(t *testing.T) {
	resp := heyJSON(t, "search", "Basecamp")
	topics := dataAs[[]SearchTopic](t, resp)
	for _, topic := range topics {
		if topic.ID <= 0 {
			t.Errorf("expected positive topic ID, got %d", topic.ID)
		}
	}
}

func TestSearchPage(t *testing.T) {
	_ = heyJSON(t, "search", "Basecamp", "--page", "2")
}

func TestSearchRequiresQuery(t *testing.T) {
	heyFail(t, "search", "--json")
}
