package smoke_test

import (
	"fmt"
	"strconv"
	"testing"
)

func TestBubbleUpNowAndPopExactPosting(t *testing.T) {
	type posting struct {
		ID        int64  `json:"id"`
		AppURL    string `json:"app_url"`
		BubbledUp bool   `json:"bubbled_up"`
	}
	type boxResponse struct {
		Postings []posting `json:"postings"`
	}
	type actionResult struct {
		PostingID int64 `json:"posting_id"`
		TopicID   int64 `json:"topic_id"`
		Changed   bool  `json:"changed"`
		NoOp      bool  `json:"no_op"`
		Verified  bool  `json:"verified"`
		After     struct {
			InImbox   bool `json:"in_imbox"`
			BubbledUp bool `json:"bubbled_up"`
		} `json:"after"`
	}

	box := dataAs[boxResponse](t, heyJSON(t, "box", "imbox", "--all"))
	var target posting
	var topicID string
	for _, candidate := range box.Postings {
		candidateTopicID := extractTopicID(candidate.AppURL)
		if candidate.ID > 0 && candidateTopicID != "" && !candidate.BubbledUp {
			target = candidate
			topicID = candidateTopicID
			break
		}
	}
	if target.ID == 0 {
		t.Skip("no unbubbled Imbox posting available for Bubble Up smoke test")
	}
	if _, err := strconv.ParseInt(topicID, 10, 64); err != nil {
		t.Fatalf("invalid topic ID %q from %q", topicID, target.AppURL)
	}

	postingID := fmt.Sprintf("%d", target.ID)
	cleanupNeeded := true
	t.Cleanup(func() {
		if !cleanupNeeded {
			return
		}
		_, stderr, code := hey(t, "pop", postingID, "--topic-id", topicID, "--json")
		if code != 0 {
			t.Logf("Bubble Up smoke cleanup failed (exit %d): %s", code, stderr)
		}
	})

	now := dataAs[actionResult](t, heyJSON(t, "bubble-up-now", postingID, "--topic-id", topicID))
	if now.PostingID != target.ID || fmt.Sprintf("%d", now.TopicID) != topicID || !now.Verified || !now.After.InImbox || !now.After.BubbledUp {
		t.Fatalf("unexpected Bubble Up Now result: %+v", now)
	}
	if !now.Changed && !now.NoOp {
		t.Fatalf("Bubble Up Now was neither changed nor no-op: %+v", now)
	}

	// Independently verify through the public box read before cleaning up.
	box = dataAs[boxResponse](t, heyJSON(t, "box", "imbox", "--all"))
	foundBubbled := false
	for _, candidate := range box.Postings {
		if candidate.ID == target.ID && extractTopicID(candidate.AppURL) == topicID && candidate.BubbledUp {
			foundBubbled = true
			break
		}
	}
	if !foundBubbled {
		t.Fatalf("exact posting %d / topic %s was not independently observed as bubbled", target.ID, topicID)
	}

	popped := dataAs[actionResult](t, heyJSON(t, "pop", postingID, "--topic-id", topicID))
	if popped.PostingID != target.ID || fmt.Sprintf("%d", popped.TopicID) != topicID || !popped.Verified || !popped.After.InImbox || popped.After.BubbledUp {
		t.Fatalf("unexpected Pop result: %+v", popped)
	}
	if !popped.Changed && !popped.NoOp {
		t.Fatalf("Pop was neither changed nor no-op: %+v", popped)
	}
	cleanupNeeded = false
}
