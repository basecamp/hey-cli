package smoke_test

import (
	"strconv"
	"strings"
	"testing"
)

type smokeClip struct {
	ID      int64  `json:"id"`
	Content string `json:"content"`
	EntryID int64  `json:"entry_id"`
	Topic   struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		AppURL string `json:"app_url"`
	} `json:"topic"`
}

func TestClipLifecycle(t *testing.T) {
	topicID, subject := longThread(t, 0)
	entries := dataAs[[]threadEntry](t, heyJSON(t, "threads", topicID))
	if len(entries) == 0 || entries[0].ID == 0 {
		t.Fatalf("thread %s has no source entry", topicID)
	}
	entryID := strconv.FormatInt(entries[0].ID, 10)
	content := "**quarterly** numbers are at https://example.com/reports/q3"

	_, stderr := heyFail(t, "clip", "create", entryID, "--content", "Text that is not present in the source entry", "--json")
	if !strings.Contains(stderr, "does not match text in entry") {
		t.Fatalf("unmatched clip error = %q", stderr)
	}

	_, stderr, code := hey(t, "clip", "create", entryID, "--content", content, "--json")
	if code != 0 {
		skipf(t, "clip create unavailable (exit %d): %s", code, stderr)
	}

	deleted := false
	t.Cleanup(func() {
		if deleted {
			return
		}
		for _, listed := range listClips(t) {
			if listed.EntryID == entries[0].ID {
				_, _, _ = hey(t, "clip", "delete", strconv.FormatInt(listed.ID, 10))
			}
		}
	})

	clip := findClipByEntry(t, entries[0].ID)
	if clip.ID == 0 || clip.Content != content || strconv.FormatInt(clip.Topic.ID, 10) != topicID || clip.Topic.Name != subject || clip.Topic.AppURL == "" {
		t.Fatalf("created clip = %+v", clip)
	}
	clipID := strconv.FormatInt(clip.ID, 10)

	page := browserPageText(t, baseURL+"/clips")
	if !strings.Contains(page, content) || !strings.Contains(page, subject) {
		t.Errorf("browser clips page does not show the exact clip text and source thread")
	}
	html := fetchHTML(t, baseURL+"/clips")
	if strings.Contains(html, "<strong>quarterly</strong>") {
		t.Errorf("browser clips page interpreted plain clip content as HTML")
	}

	_, stderr, code = hey(t, "clip", "delete", clipID, "--json")
	if code != 0 {
		t.Fatalf("clip delete failed (exit %d): %s", code, stderr)
	}
	deleted = true
	for _, listed := range listClips(t) {
		if listed.ID == clip.ID {
			t.Fatalf("deleted clip %d is still listed", clip.ID)
		}
	}
}

func TestClipOutputFormatsAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"clips", "--quiet"},
		{"clips", "--ids-only"},
		{"clips", "--count"},
		{"clips", "--markdown"},
		{"clips", "--styled"},
	} {
		_, stderr, code := hey(t, args...)
		if code != 0 {
			t.Errorf("hey %s failed (exit %d): %s", strings.Join(args, " "), code, stderr)
		}
	}

	heyFail(t, "clip", "create", "987")
	heyFail(t, "clip", "create", "not-an-id", "--content", "Keep this")
	heyFail(t, "clip", "delete", "not-an-id")
}

func listClips(t *testing.T) []smokeClip {
	t.Helper()
	return dataAs[[]smokeClip](t, heyJSON(t, "clips"))
}

func findClipByEntry(t *testing.T, entryID int64) smokeClip {
	t.Helper()
	for _, clip := range listClips(t) {
		if clip.EntryID == entryID {
			return clip
		}
	}
	t.Fatalf("clip from entry %d not found", entryID)
	return smokeClip{}
}
