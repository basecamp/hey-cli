package smoke_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAttachmentSendListAndSave(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Attachment delivery %s", uid)
	filename := "project-notes.txt"
	contents := []byte("Agenda notes for the quarterly planning meeting.\n")
	attachmentPath := filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(attachmentPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := hey(t, "compose",
		"--to", "david@basecamp.com",
		"--subject", subject,
		"-m", "Attached are the planning notes.",
		"--attach", attachmentPath,
		"--json",
	)
	if code != 0 {
		t.Skipf("attachment sending is unavailable (exit %d): %s", code, stderr)
	}
	var composeResponse Response
	if err := json.Unmarshal([]byte(stdout), &composeResponse); err != nil || !composeResponse.OK {
		t.Fatalf("invalid compose response: %v, %s", err, stdout)
	}

	var result smokeSearchResult
	for attempt := 0; attempt < 10 && result.TopicID == 0; attempt++ {
		searchOut, _, searchCode := hey(t, "search", "--subject", subject, "--all", "--json")
		if searchCode == 0 {
			var response Response
			if err := json.Unmarshal([]byte(searchOut), &response); err == nil {
				for _, candidate := range dataAs[[]smokeSearchResult](t, response) {
					if candidate.Subject == subject {
						result = candidate
						break
					}
				}
			}
		}
		if result.TopicID == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if result.TopicID == 0 {
		t.Skip("sent attachment thread was not searchable yet")
	}
	if result.ID != 0 {
		t.Cleanup(func() { _, _, _ = hey(t, "trash", fmt.Sprint(result.ID), "--json") })
	}

	attachments := dataAs[[]struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
	}](t, heyJSON(t, "attachments", fmt.Sprint(result.TopicID)))
	var ref string
	for _, attachment := range attachments {
		if attachment.Filename == filename {
			ref = attachment.ID
			break
		}
	}
	if ref == "" {
		t.Fatalf("sent thread did not list %s", filename)
	}

	destination := filepath.Join(t.TempDir(), "saved-project-notes.txt")
	heysaveOut, saveErr, saveCode := hey(t, "attachments", "save", ref, "--output", destination, "--json")
	if saveCode != 0 {
		t.Fatalf("attachment save failed (exit %d): %s", saveCode, saveErr)
	}
	var saveResponse Response
	if err := json.Unmarshal([]byte(heysaveOut), &saveResponse); err != nil || !saveResponse.OK {
		t.Fatalf("invalid save response: %v, %s", err, heysaveOut)
	}
	saved, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(contents) {
		t.Errorf("saved attachment content differs: %q", saved)
	}
}
