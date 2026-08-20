package smoke_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestBoxesList(t *testing.T) {
	resp := heyJSON(t, "boxes")

	type Box struct {
		ID   int    `json:"id"`
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	boxes := dataAs[[]Box](t, resp)

	if len(boxes) == 0 {
		t.Fatal("expected at least one mailbox")
	}

	// The standard HEY account should have an imbox.
	found := false
	for _, b := range boxes {
		if b.Kind == "imbox" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find a box with kind=imbox")
	}

	// Cross-verify: the box names should appear on the main page.
	html := fetchHTML(t, baseURL+"/boxes")
	for _, b := range boxes {
		if b.Name != "" {
			assertContains(t, html, b.Name)
		}
	}
}

func TestBoxesLimit(t *testing.T) {
	resp := heyJSON(t, "boxes", "--limit", "2")
	type Box struct {
		ID int `json:"id"`
	}
	boxes := dataAs[[]Box](t, resp)
	if len(boxes) > 2 {
		t.Errorf("expected at most 2 boxes with --limit 2, got %d", len(boxes))
	}
}

func TestBoxImbox(t *testing.T) {
	resp := heyJSON(t, "box", "imbox")

	type Posting struct {
		ID      int    `json:"id"`
		AppURL  string `json:"app_url"`
		Summary string `json:"summary"`
	}
	type BoxResponse struct {
		Name     string    `json:"name"`
		Kind     string    `json:"kind"`
		Postings []Posting `json:"postings"`
	}
	data := dataAs[BoxResponse](t, resp)

	if data.Kind != "imbox" {
		t.Errorf("expected kind=imbox, got %s", data.Kind)
	}

	// Cross-verify: pick a posting and verify its topic page exists on the server.
	if len(data.Postings) > 0 {
		topicID := extractTopicID(data.Postings[0].AppURL)
		if topicID != "" {
			html := fetchHTML(t, fmt.Sprintf("%s/topics/%s", baseURL, topicID))
			if len(html) == 0 {
				t.Error("topic page returned empty HTML for first imbox posting")
			}
		}
	}
}

func TestBoxByName(t *testing.T) {
	names := []string{"feedbox", "trailbox"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			resp := heyJSON(t, "box", name)
			type BoxResponse struct {
				Kind string `json:"kind"`
			}
			data := dataAs[BoxResponse](t, resp)
			if data.Kind == "" {
				t.Errorf("expected non-empty kind for box %s", name)
			}
		})
	}
}

func TestBoxByID(t *testing.T) {
	// First get a box list to find a real ID.
	resp := heyJSON(t, "boxes")
	type Box struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	boxes := dataAs[[]Box](t, resp)
	if len(boxes) == 0 {
		t.Fatal("no boxes available")
	}

	id := boxes[0].ID
	boxResp := heyJSON(t, "box", intStr(id))

	// Cross-verify: the box name from the ID lookup should match the list.
	type BoxResponse struct {
		Name string `json:"name"`
	}
	data := dataAs[BoxResponse](t, boxResp)
	if data.Name != boxes[0].Name {
		t.Errorf("box by ID returned name %q, expected %q", data.Name, boxes[0].Name)
	}
}

func TestBoxImboxLimit(t *testing.T) {
	resp := heyJSON(t, "box", "imbox", "--limit", "3")
	type BoxResponse struct {
		Postings []json.RawMessage `json:"postings"`
	}
	data := dataAs[BoxResponse](t, resp)
	if len(data.Postings) > 3 {
		t.Errorf("expected at most 3 postings with --limit 3, got %d", len(data.Postings))
	}
}

func TestBoxAdditionalNames(t *testing.T) {
	names := []string{"asidebox", "laterbox", "bubblebox"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			resp := heyJSON(t, "box", name)
			type BoxResponse struct {
				Kind string `json:"kind"`
			}
			data := dataAs[BoxResponse](t, resp)
			if data.Kind == "" {
				t.Errorf("expected non-empty kind for box %s", name)
			}
		})
	}
}

func TestBoxAll(t *testing.T) {
	resp := heyJSON(t, "box", "imbox", "--all")
	type BoxResponse struct {
		Postings []any `json:"postings"`
	}
	// Just verify the command succeeds with --all.
	_ = dataAs[BoxResponse](t, resp)
}

func TestBoxesAll(t *testing.T) {
	resp := heyJSON(t, "boxes", "--all")
	type Box struct {
		ID int `json:"id"`
	}
	boxes := dataAs[[]Box](t, resp)
	if len(boxes) == 0 {
		t.Fatal("expected at least one mailbox with --all")
	}
}

func TestMovePosting(t *testing.T) {
	resp := heyJSON(t, "box", "imbox", "--limit", "10")
	type Posting struct {
		ID   int    `json:"id"`
		Kind string `json:"kind"`
		Seen bool   `json:"seen"`
	}
	type BoxResponse struct {
		Postings []Posting `json:"postings"`
	}
	imbox := dataAs[BoxResponse](t, resp)
	postingID := 0
	postingKind := ""
	for _, posting := range imbox.Postings {
		if posting.Kind == "topic" && posting.Seen {
			postingID = posting.ID
			postingKind = posting.Kind
			break
		}
	}
	if postingID == 0 {
		t.Skip("no seen postings in Imbox to move without changing unread state")
	}

	stdout, stderr, code := hey(t, "move", intStr(postingID), "--to", "feedbox", "--kind", postingKind, "--json")
	if code != 0 {
		t.Skipf("moving postings is unavailable on this server (exit %d): %s", code, stderr)
	}
	t.Cleanup(func() {
		_, cleanupStderr, cleanupCode := hey(t, "move", intStr(postingID), "--to", "imbox", "--kind", postingKind, "--json")
		if cleanupCode != 0 {
			t.Logf("could not restore posting %d to Imbox: %s", postingID, cleanupStderr)
		}
	})

	var moveResp Response
	if err := json.Unmarshal([]byte(stdout), &moveResp); err != nil {
		t.Fatalf("failed to parse move response: %v", err)
	}
	assertContains(t, moveResp.Summary, "moved to The Feed")

	feedResp := heyJSON(t, "box", "feedbox", "--all")
	feed := dataAs[BoxResponse](t, feedResp)
	for _, posting := range feed.Postings {
		if posting.ID == postingID {
			return
		}
	}
	t.Errorf("posting %d did not appear in The Feed", postingID)
}

func TestMoveNoPostingID(t *testing.T) {
	heyFail(t, "move", "--to", "feed", "--json")
}

func TestMoveNoDestination(t *testing.T) {
	heyFail(t, "move", "12345", "--json")
}

func TestMoveRejectsBubbleUp(t *testing.T) {
	heyFail(t, "move", "12345", "--to", "bubblebox", "--json")
}

func TestBoxNoArgument(t *testing.T) {
	heyFail(t, "box", "--json")
}

func TestBoxInvalidName(t *testing.T) {
	heyFail(t, "box", "nonexistent_box_name_xyz", "--json")
}
