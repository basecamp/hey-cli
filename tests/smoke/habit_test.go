package smoke_test

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
)

func createTestHabit(t *testing.T, name string) int {
	t.Helper()

	stdout, stderr, code := hey(t, "habit", "create", name, "--icon", "star", "--json")
	if code != 0 {
		t.Fatalf("habit create failed (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("failed to parse habit create response: %v", err)
	}
	id := extractIDFromMap(t, dataAs[map[string]any](t, response))
	habitID, err := strconv.Atoi(id)
	if err != nil || habitID <= 0 {
		t.Fatalf("could not parse created habit ID %q: %v", id, err)
	}
	return habitID
}

func deleteTestHabit(t *testing.T, habitID int) {
	t.Helper()
	_, _, _ = hey(t, "habit", "delete", intStr(habitID))
}

func TestHabitCreateEditDelete(t *testing.T) {
	uid := uniqueID()
	name := fmt.Sprintf("Morning stretches %s", uid)
	habitID := createTestHabit(t, name)
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			deleteTestHabit(t, habitID)
		}
	})

	updatedName := fmt.Sprintf("Evening stretches %s", uid)
	stdout := heyOK(t, "habit", "edit", intStr(habitID), "--name", updatedName, "--color", "green", "--days", "mon,wed,fri", "--json")
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("failed to parse habit edit response: %v", err)
	}
	assertContains(t, response.Summary, "updated")
	data := dataAs[map[string]any](t, response)
	if data["title"] != updatedName {
		t.Errorf("updated title = %v, want %q", data["title"], updatedName)
	}

	stdout = heyOK(t, "habit", "delete", intStr(habitID), "--json")
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("failed to parse habit delete response: %v", err)
	}
	assertContains(t, response.Summary, "deleted")
	deleted = true
}

func TestHabitComplete(t *testing.T) {
	uid := uniqueID()
	name := fmt.Sprintf("Morning reading %s", uid)
	habitID := createTestHabit(t, name)
	t.Cleanup(func() { deleteTestHabit(t, habitID) })

	stdout := heyOK(t, "habit", "complete", intStr(habitID), "--json")
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	assertContains(t, resp.Summary, "completed")

	html := fetchHTML(t, baseURL+"/calendar/habits")
	assertContains(t, html, name)

	stdout = heyOK(t, "habit", "uncomplete", intStr(habitID), "--json")
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	assertContains(t, resp.Summary, "uncompleted")
}

func TestHabitCompleteWithDate(t *testing.T) {
	uid := uniqueID()
	name := fmt.Sprintf("Dated reading habit %s", uid)
	habitID := createTestHabit(t, name)
	t.Cleanup(func() { deleteTestHabit(t, habitID) })

	stdout := heyOK(t, "habit", "complete", intStr(habitID), "--date", "2099-06-15", "--json")
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	assertContains(t, resp.Summary, "completed")

	html := fetchHTML(t, baseURL+"/calendar/habits")
	assertContains(t, html, name)

	hey(t, "habit", "uncomplete", intStr(habitID), "--date", "2099-06-15")
}
