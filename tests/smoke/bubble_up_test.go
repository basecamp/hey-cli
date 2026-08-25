package smoke_test

import (
	"fmt"
	"testing"
)

type bubbleSmokePosting struct {
	ID        int64 `json:"id"`
	TopicID   int64 `json:"topic_id"`
	BubbledUp bool  `json:"bubbled_up"`
}

func TestBubbleUpNowAndPopExactBoxItem(t *testing.T) {
	type boxResponse struct {
		Postings []bubbleSmokePosting `json:"postings"`
	}
	type actionResult struct {
		PostingID int64 `json:"posting_id"`
		TopicID   int64 `json:"topic_id"`
		Changed   bool  `json:"changed"`
		NoOp      bool  `json:"no_op"`
		Verified  bool  `json:"verified"`
		After     struct {
			InImbox    bool `json:"in_imbox"`
			InBubbleUp bool `json:"in_bubble_up"`
			BubbledUp  bool `json:"bubbled_up"`
			Scheduled  bool `json:"scheduled"`
		} `json:"after"`
	}

	box := dataAs[boxResponse](t, heyJSON(t, "box", "view", "imbox", "--all"))
	var target bubbleSmokePosting
	for _, candidate := range box.Postings {
		if candidate.ID > 0 && candidate.TopicID > 0 && !candidate.BubbledUp {
			target = candidate
			break
		}
	}
	if target.ID == 0 {
		skipf(t, "no unbubbled Imbox box item available for Bubble Up smoke test")
	}

	postingID := fmt.Sprintf("%d", target.ID)
	topicID := fmt.Sprintf("%d", target.TopicID)
	cleanupNeeded := true
	t.Cleanup(func() {
		if !cleanupNeeded {
			return
		}
		_, stderr, code := hey(t, "pop", postingID, "--topic-id", topicID, "--json")
		if code != 0 {
			t.Errorf("Bubble Up smoke cleanup failed (exit %d): %s", code, stderr)
		}
	})

	now := dataAs[actionResult](t, heyJSON(t, "bubble-up-now", postingID, "--topic-id", topicID))
	if now.PostingID != target.ID || now.TopicID != target.TopicID || !now.Verified || !now.After.InImbox || !now.After.BubbledUp {
		t.Fatalf("unexpected Bubble Up Now result: %+v", now)
	}
	if !now.Changed && !now.NoOp {
		t.Fatalf("Bubble Up Now was neither changed nor no-op: %+v", now)
	}

	// Independently verify through the public box read before cleaning up.
	box = dataAs[boxResponse](t, heyJSON(t, "box", "view", "imbox", "--all"))
	if !hasBubblePosting(box.Postings, target.ID, target.TopicID, true) {
		t.Fatalf("exact box item %d / thread %d was not independently observed as bubbled", target.ID, target.TopicID)
	}

	popped := dataAs[actionResult](t, heyJSON(t, "pop", postingID, "--topic-id", topicID))
	if popped.PostingID != target.ID || popped.TopicID != target.TopicID || !popped.Verified || !popped.After.InImbox || popped.After.InBubbleUp || popped.After.BubbledUp || popped.After.Scheduled {
		t.Fatalf("unexpected Pop result: %+v", popped)
	}
	if !popped.Changed && !popped.NoOp {
		t.Fatalf("Pop was neither changed nor no-op: %+v", popped)
	}

	box = dataAs[boxResponse](t, heyJSON(t, "box", "view", "imbox", "--all"))
	if !hasBubblePosting(box.Postings, target.ID, target.TopicID, false) {
		t.Fatalf("exact box item %d / thread %d was not independently observed as popped in Imbox", target.ID, target.TopicID)
	}
	bubbleBox := dataAs[boxResponse](t, heyJSON(t, "box", "view", "bubblebox", "--all"))
	if hasExactBubblePosting(bubbleBox.Postings, target.ID, target.TopicID) {
		t.Fatalf("exact box item %d / thread %d remained in Bubble Up after Pop", target.ID, target.TopicID)
	}
	cleanupNeeded = false
}

func hasBubblePosting(postings []bubbleSmokePosting, postingID, topicID int64, bubbled bool) bool {
	for _, candidate := range postings {
		if candidate.ID == postingID && candidate.TopicID == topicID && candidate.BubbledUp == bubbled {
			return true
		}
	}
	return false
}

func hasExactBubblePosting(postings []bubbleSmokePosting, postingID, topicID int64) bool {
	for _, candidate := range postings {
		if candidate.ID == postingID && candidate.TopicID == topicID {
			return true
		}
	}
	return false
}
