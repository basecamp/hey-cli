package smoke_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestTodoCRUD(t *testing.T) {
	uid := uniqueID()
	title := fmt.Sprintf("Todo %s", uid)

	// --- Add ---
	stdout, stderr, code := hey(t, "todo", "add", title, "--json")
	if code != 0 {
		t.Fatalf("todo add failed (exit %d): %s", code, stderr)
	}
	var addResp Response
	if err := json.Unmarshal([]byte(stdout), &addResp); err != nil {
		t.Fatalf("failed to parse add response: %v", err)
	}
	assertContains(t, addResp.Summary, "Todo created")

	// Extract the ID from the response data.
	addData := dataAs[map[string]any](t, addResp)
	todoID := extractIDFromMap(t, addData)
	if todoID == "" {
		t.Fatal("could not extract todo ID from add response")
	}

	// Cross-verify: the todo should appear on the calendar page.
	html := fetchHTML(t, baseURL+"/calendar")
	assertContains(t, html, title)

	// --- Complete ---
	stdout, stderr, code = hey(t, "todo", "complete", todoID, "--json")
	if code != 0 {
		t.Fatalf("todo complete failed (exit %d): %s", code, stderr)
	}
	var completeResp Response
	if err := json.Unmarshal([]byte(stdout), &completeResp); err != nil {
		t.Fatalf("failed to parse complete response: %v", err)
	}
	assertContains(t, completeResp.Summary, "Todo completed")

	// --- Uncomplete ---
	stdout, stderr, code = hey(t, "todo", "uncomplete", todoID, "--json")
	if code != 0 {
		t.Fatalf("todo uncomplete failed (exit %d): %s", code, stderr)
	}
	var uncompleteResp Response
	if err := json.Unmarshal([]byte(stdout), &uncompleteResp); err != nil {
		t.Fatalf("failed to parse uncomplete response: %v", err)
	}
	assertContains(t, uncompleteResp.Summary, "Todo marked incomplete")

	// --- Delete ---
	stdout, stderr, code = hey(t, "todo", "delete", todoID, "--json")
	if code != 0 {
		t.Fatalf("todo delete failed (exit %d): %s", code, stderr)
	}
	var deleteResp Response
	if err := json.Unmarshal([]byte(stdout), &deleteResp); err != nil {
		t.Fatalf("failed to parse delete response: %v", err)
	}
	assertContains(t, deleteResp.Summary, "Todo deleted")

	// --- Verify it's gone (completing a deleted todo should fail) ---
	_, _, code = hey(t, "todo", "complete", todoID, "--json")
	if code == 0 {
		t.Error("expected completing a deleted todo to fail")
	}

	// Cross-verify: the todo should no longer appear on the calendar page.
	html = fetchHTML(t, baseURL+"/calendar")
	assertNotContains(t, html, title)
}

func TestTodoAddWithDate(t *testing.T) {
	uid := uniqueID()
	title := fmt.Sprintf("Dated todo %s", uid)

	stdout, stderr, code := hey(t, "todo", "add", title, "--date", "2099-12-31", "--json")
	if code != 0 {
		t.Fatalf("todo add failed (exit %d): %s", code, stderr)
	}
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	assertContains(t, resp.Summary, "Todo created")

	todoID := extractIDFromMap(t, dataAs[map[string]any](t, resp))
	if todoID != "" {
		t.Cleanup(func() {
			hey(t, "todo", "delete", todoID)
		})
	}

	// Cross-verify: the dated todo should appear on its specific day page.
	html := fetchHTML(t, baseURL+"/calendar/days/2099-12-31")
	assertContains(t, html, title)
}

func TestTodoAddRequiresTitle(t *testing.T) {
	heyFail(t, "todo", "add", "--json")
}

func TestTodoListAll(t *testing.T) {
	resp := heyJSON(t, "todo", "list", "--all")
	_ = resp
}

func TestTodoListLimit(t *testing.T) {
	resp := heyJSON(t, "todo", "list", "--limit", "2")
	type Todo struct {
		ID int `json:"id"`
	}
	todos := dataAs[[]Todo](t, resp)
	if len(todos) > 2 {
		t.Errorf("expected at most 2 todos with --limit 2, got %d", len(todos))
	}
}

// extractIDFromMap tries to get an "id" field from a map as a string.
func extractIDFromMap(t *testing.T, m map[string]any) string {
	t.Helper()
	if m == nil {
		return ""
	}
	id, ok := m["id"]
	if !ok {
		return ""
	}
	switch v := id.(type) {
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%d", int64(v))
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}
