package models

import (
	"encoding/json"
	"testing"
)

func TestBoxShowResponseUnmarshalFlatBoxFields(t *testing.T) {
	data := []byte(`{
		"id": 243,
		"kind": "imbox",
		"name": "Imbox",
		"app_url": "https://app.hey.com/imbox",
		"url": "https://app.hey.com/imbox.json",
		"posting_changes_url": "https://app.hey.com/boxes/243/postings/changes.json",
		"postings": [{"id": 1}],
		"next_history_url": "https://app.hey.com/imbox.json?page=2",
		"next_incremental_sync_url": "https://app.hey.com/imbox.json?updated_since=2026-03-04T20:00:00Z"
	}`)

	var resp BoxShowResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if resp.Box.ID != 243 {
		t.Fatalf("Box.ID = %d, want 243", resp.Box.ID)
	}
	if resp.Box.Kind != "imbox" {
		t.Fatalf("Box.Kind = %q, want %q", resp.Box.Kind, "imbox")
	}
	if resp.Box.Name != "Imbox" {
		t.Fatalf("Box.Name = %q, want %q", resp.Box.Name, "Imbox")
	}
	if len(resp.Postings) != 1 {
		t.Fatalf("len(Postings) = %d, want 1", len(resp.Postings))
	}
}

func TestBoxShowResponseUnmarshalNestedBox(t *testing.T) {
	data := []byte(`{
		"box": {
			"id": 42,
			"kind": "feedbox",
			"name": "The Feed"
		},
		"postings": []
	}`)

	var resp BoxShowResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if resp.Box.ID != 42 {
		t.Fatalf("Box.ID = %d, want 42", resp.Box.ID)
	}
	if resp.Box.Kind != "feedbox" {
		t.Fatalf("Box.Kind = %q, want %q", resp.Box.Kind, "feedbox")
	}
	if resp.Box.Name != "The Feed" {
		t.Fatalf("Box.Name = %q, want %q", resp.Box.Name, "The Feed")
	}
}
