package smoke_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hey omarchy poll is what the Omarchy bar plugin runs: it must answer with the
// same box shape hey box imbox --json does, honour --limit, and with --notify
// keep its toast fingerprints in the state directory without ever failing on
// the toast itself (omarchy-notification-send is not on a smoke machine).

func TestOmarchyPollMatchesBoxImbox(t *testing.T) {
	poll := heyJSON(t, "omarchy", "poll", "--limit", "5")
	box := heyJSON(t, "box", "imbox", "--limit", "5")

	var pollBox, imbox struct {
		ID       int64             `json:"id"`
		Name     string            `json:"name"`
		Postings []json.RawMessage `json:"postings"`
	}
	if err := json.Unmarshal(poll.Data, &pollBox); err != nil {
		t.Fatalf("poll data: %v", err)
	}
	if err := json.Unmarshal(box.Data, &imbox); err != nil {
		t.Fatalf("box data: %v", err)
	}
	if pollBox.ID != imbox.ID || pollBox.Name != imbox.Name {
		t.Errorf("poll answers for %d %q, box imbox for %d %q", pollBox.ID, pollBox.Name, imbox.ID, imbox.Name)
	}
	if len(pollBox.Postings) > 5 {
		t.Errorf("--limit 5 returned %d postings", len(pollBox.Postings))
	}
	if len(pollBox.Postings) != len(imbox.Postings) {
		t.Errorf("poll returned %d postings, box imbox %d", len(pollBox.Postings), len(imbox.Postings))
	}
}

func TestOmarchyPollNotifySeedsState(t *testing.T) {
	statePath := filepath.Join(stateDir, "hey-cli", "omarchy-poll.json")
	_ = os.Remove(statePath)

	resp := heyJSON(t, "omarchy", "poll", "--limit", "5", "--notify")
	if !resp.OK {
		t.Fatal("poll --notify should succeed without a notification daemon")
	}
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("the first --notify poll should seed %s: %v", statePath, err)
	}
	if !strings.Contains(string(state), `"identity"`) || !strings.Contains(string(state), `"seen"`) {
		t.Errorf("unexpected state file: %s", state)
	}

	// A poll without --notify forgets the seed, so turning toasts back on
	// starts silent.
	heyJSON(t, "omarchy", "poll", "--limit", "5")
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Error("a poll without --notify should remove the fingerprints")
	}
}

func TestOmarchyPollRejectsABadLimit(t *testing.T) {
	_, stderr := heyFail(t, "omarchy", "poll", "--limit", "0")
	if !strings.Contains(stderr, "--limit") {
		t.Errorf("expected a usage error about --limit, got %q", stderr)
	}
}
