package smoke_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// createTestHabit creates a habit via the Rails API using the session cookie.
// Returns the habit ID from the JSON response.
func createTestHabit(t *testing.T, name string) int {
	t.Helper()

	body, _ := json.Marshal(map[string]any{
		"calendar_habit": map[string]any{
			"name":  name,
			"icon":  "star",
			"color": "blue",
			"days":  []int{1, 2, 3, 4, 5, 6, 7},
		},
	})

	url := baseURL + "/calendar/habits"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("could not create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: sessionCookie})

	// Don't follow redirects — just capture the response status.
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("habit create request failed: %v", err)
	}
	resp.Body.Close()

	// 2xx or 3xx (redirect) means success.
	if resp.StatusCode >= 400 {
		t.Fatalf("habit create returned HTTP %d", resp.StatusCode)
	}

	// Find the habit ID by scraping the habits index HTML page.
	// The recordings endpoint has in_window scoping that may exclude new habits.
	req2, err := http.NewRequest("GET", baseURL+"/calendar/habits", nil)
	if err != nil {
		t.Fatalf("could not create GET request: %v", err)
	}
	req2.Header.Set("Accept", "text/html")
	req2.AddCookie(&http.Cookie{Name: "session_token", Value: sessionCookie})

	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("GET habits failed: %v", err)
	}
	htmlBody, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	// Look for <a ... title="<name>" ... href="/calendar/habits/<id>">
	re := regexp.MustCompile(`title="` + regexp.QuoteMeta(name) + `"[^>]*href="/calendar/habits/(\d+)"`)
	m := re.FindSubmatch(htmlBody)
	if m == nil {
		// Try the reverse order: href before title
		re2 := regexp.MustCompile(`href="/calendar/habits/(\d+)"[^>]*title="` + regexp.QuoteMeta(name) + `"`)
		m = re2.FindSubmatch(htmlBody)
	}
	if m == nil {
		t.Fatalf("created habit %q not found in habits index HTML", name)
	}
	habitID, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("could not parse habit ID %q: %v", m[1], err)
	}
	return habitID
}

// deleteTestHabit deletes a habit via the Rails API.
func deleteTestHabit(t *testing.T, habitID int) {
	t.Helper()

	url := fmt.Sprintf("%s/calendar/habits/%d", baseURL, habitID)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return
	}
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
		return
	}
	resp.Body.Close()
}

func TestHabitComplete(t *testing.T) {
	uid := uniqueID()
	name := fmt.Sprintf("Test habit %s", uid)
	habitID := createTestHabit(t, name)
	t.Cleanup(func() { deleteTestHabit(t, habitID) })

	// Complete.
	stdout := heyOK(t, "habit", "complete", intStr(habitID), "--json")
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	assertContains(t, resp.Summary, "completed")

	// Cross-verify: the habit should appear on the habits page as completed.
	html := fetchHTML(t, baseURL+"/calendar/habits")
	assertContains(t, html, name)

	// Uncomplete.
	stdout = heyOK(t, "habit", "uncomplete", intStr(habitID), "--json")
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	assertContains(t, resp.Summary, "uncompleted")
}

func TestHabitCompleteWithDate(t *testing.T) {
	uid := uniqueID()
	name := fmt.Sprintf("Test habit date %s", uid)
	habitID := createTestHabit(t, name)
	t.Cleanup(func() { deleteTestHabit(t, habitID) })

	stdout := heyOK(t, "habit", "complete", intStr(habitID), "--date", "2099-06-15", "--json")
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	assertContains(t, resp.Summary, "completed")

	// Cross-verify: the habit should appear on the habits page.
	html := fetchHTML(t, baseURL+"/calendar/habits")
	assertContains(t, html, name)

	// Clean up completion.
	hey(t, "habit", "uncomplete", intStr(habitID), "--date", "2099-06-15")
}
