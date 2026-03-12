package smoke_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestCLITodoVisibleInBrowser creates a todo via CLI and verifies it appears
// when viewing the calendar page in the browser.
func TestCLITodoVisibleInBrowser(t *testing.T) {
	uid := uniqueID()
	title := fmt.Sprintf("Browser check %s", uid)

	// Create todo via CLI.
	stdout, stderr, code := hey(t, "todo", "add", title, "--json")
	if code != 0 {
		t.Fatalf("todo add failed (exit %d): %s", code, stderr)
	}

	// Extract the todo ID so we can clean up reliably.
	var addResp Response
	if err := json.Unmarshal([]byte(stdout), &addResp); err == nil {
		if todoID := extractIDFromMap(t, dataAs[map[string]any](t, addResp)); todoID != "" {
			t.Cleanup(func() { hey(t, "todo", "delete", todoID) })
		}
	}

	// Navigate browser to the calendar page where todos are shown.
	text := browserPageText(t, baseURL+"/calendar")
	assertContains(t, text, title)
}

// TestCLIComposeVisibleInBrowser composes a message via CLI and checks that
// the subject appears in the browser's inbox view.
func TestCLIComposeVisibleInBrowser(t *testing.T) {
	uid := uniqueID()
	subject := fmt.Sprintf("Browser compose %s", uid)

	_, stderr, code := hey(t, "compose",
		"--to", "david@basecamp.com",
		"--subject", subject,
		"-m", "Checking browser visibility",
		"--json",
	)
	if code != 0 {
		t.Fatalf("compose failed (exit %d): %s", code, stderr)
	}

	// Navigate to inbox in the browser and check for the subject.
	text := browserPageText(t, baseURL)
	assertContains(t, text, subject)
}

// TestBrowserActionVisibleInCLI creates a journal entry via an HTTP request
// (simulating a browser session) and verifies it appears in CLI output.
func TestBrowserActionVisibleInCLI(t *testing.T) {
	uid := uniqueID()
	content := "Browser journal " + uid
	date := "2099-09-25"

	// Write journal entry via direct HTTP using the session cookie,
	// simulating what a browser would do.
	body, _ := json.Marshal(map[string]any{
		"calendar_journal_entry": map[string]any{
			"content": content,
		},
	})

	url := fmt.Sprintf("%s/calendar/days/%s/journal_entry", baseURL, date)
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("could not create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: sessionCookie})

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("journal write request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		t.Fatalf("journal write returned HTTP %d", resp.StatusCode)
	}

	// Verify the journal entry appears in CLI output.
	stdout := heyOK(t, "journal", "read", date, "--json")
	assertContains(t, stdout, uid)
}

// TestCLIJournalVisibleInBrowser writes a journal entry via CLI and verifies
// the content appears when viewing the journal page in the browser.
func TestCLIJournalVisibleInBrowser(t *testing.T) {
	uid := uniqueID()
	content := "Browser journal check " + uid
	date := "2099-08-20"

	_, stderr, code := hey(t, "journal", "write", date, "-c", content, "--json")
	if code != 0 {
		t.Fatalf("journal write failed (exit %d): %s", code, stderr)
	}

	// Navigate to the journal entry edit page where content is visible.
	text := browserPageText(t, baseURL+"/calendar/days/"+date+"/journal_entry/edit")
	// The content should appear somewhere on the page.
	assertContains(t, text, uid)
}

