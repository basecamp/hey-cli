package smoke_test

import (
	"encoding/json"
	"testing"
)

// Bulk reply smoke coverage exercises read-only preview and validation.
// Mutation behavior runs against isolated HTTP servers in the unit suite.
func TestBulkReplyPreview(t *testing.T) {
	box := dataAs[struct {
		Postings []struct {
			ID int `json:"id"`
		} `json:"postings"`
	}](t, heyJSON(t, "box", "imbox", "--limit", "10"))
	if len(box.Postings) == 0 {
		skipf(t, "no Imbox postings available for a read-only bulk reply preview")
	}

	args := []string{"bulk-reply", "preview"}
	for i, posting := range box.Postings {
		if i == 3 {
			break
		}
		args = append(args, intStr(posting.ID))
	}
	stdout, stderr, code := hey(t, append(args, "--json")...)
	if code != 0 {
		t.Fatalf("bulk reply preview failed (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	if !response.OK {
		t.Fatalf("preview response was not OK: %s", stdout)
	}
	var entries []struct {
		ID        int    `json:"id"`
		TopicID   int    `json:"topic_id"`
		TopicName string `json:"topic_name"`
		To        []any  `json:"to"`
		CC        []any  `json:"cc"`
		BCC       []any  `json:"bcc"`
	}
	if err := json.Unmarshal(response.Data, &entries); err != nil {
		t.Fatalf("decode preview entries: %v", err)
	}
	for _, entry := range entries {
		if entry.ID <= 0 || entry.TopicID <= 0 || entry.TopicName == "" {
			t.Errorf("incomplete preview entry: %+v", entry)
		}
	}
}

func TestBulkReplyRejectsUnsafeSelections(t *testing.T) {
	heyFail(t, "bulk-reply", "preview", "0", "--json")
	heyFail(t, "bulk-reply", "send", "12345", "12345", "-m", "This must not send", "--json")
	heyFail(t, "bulk-reply", "undo", "0", "--json")
}
