package smoke_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type smokeSearchResult struct {
	ID       int               `json:"id"`
	TopicID  int               `json:"topic_id"`
	Subject  string            `json:"subject"`
	Messages []json.RawMessage `json:"messages"`
}

func TestSearchFilters(t *testing.T) {
	resp := heyJSON(t, "search", "filters")
	filters := dataAs[struct {
		Boxes       []json.RawMessage `json:"boxes"`
		Dates       []json.RawMessage `json:"dates"`
		Labels      []json.RawMessage `json:"labels"`
		Attachments []json.RawMessage `json:"attachments"`
	}](t, resp)
	if len(filters.Boxes) == 0 {
		t.Error("expected at least one box search filter")
	}
	if len(filters.Dates) == 0 {
		t.Error("expected at least one date search filter")
	}
	if len(filters.Attachments) == 0 {
		t.Error("expected at least one attachment search filter")
	}
}

func TestSearchFreeTextAndRefinements(t *testing.T) {
	boxResp := heyJSON(t, "box", "imbox")
	box := dataAs[struct {
		Postings []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"postings"`
	}](t, boxResp)
	var subject string
	for _, posting := range box.Postings {
		if posting.Kind != "bundle" && strings.TrimSpace(posting.Name) != "" {
			subject = posting.Name
			break
		}
	}
	if subject == "" {
		t.Skip("no Imbox thread available for read-only search validation")
	}

	var freeText []smokeSearchResult
	deadline := time.Now().Add(10 * time.Second)
	for len(freeText) == 0 && time.Now().Before(deadline) {
		freeText = dataAs[[]smokeSearchResult](t, heyJSON(t, "search", subject))
		if len(freeText) == 0 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if len(freeText) == 0 {
		t.Fatal("free-text search did not find the known Imbox thread after waiting for indexing")
	}
	for _, result := range freeText {
		if result.TopicID == 0 {
			t.Error("free-text search result has no topic_id")
		}
	}

	refined := dataAs[[]smokeSearchResult](t, heyJSON(t, "search", "--subject", subject, "--in", "imbox", "--page", "1"))
	if len(refined) == 0 {
		t.Fatal("subject and box refinements did not find the known Imbox thread")
	}
	for _, result := range refined {
		if result.TopicID == 0 {
			t.Error("refined search result has no topic_id")
		}
	}
}

func TestSearchRequiresCriteria(t *testing.T) {
	heyFail(t, "search")
}
