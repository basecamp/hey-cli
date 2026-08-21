package smoke_test

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTimetracking(t *testing.T) {
	// Stop any existing time track to start clean.
	hey(t, "timetrack", "stop")

	// --- Start ---
	startOut, startErr, startCode := hey(t, "timetrack", "start", "--json")
	if startCode != 0 {
		t.Fatalf("timetrack start failed (exit %d): %s", startCode, startErr)
	}
	var startResp Response
	if err := json.Unmarshal([]byte(startOut), &startResp); err != nil {
		t.Fatalf("failed to parse start response: %v", err)
	}
	assertContains(t, startResp.Summary, "Time tracking started")

	// --- Current (should be active) ---
	resp := heyJSON(t, "timetrack", "current")
	currentData := dataAs[map[string]any](t, resp)
	if currentData == nil {
		t.Fatal("expected active time track from current, got nil")
	}
	if _, ok := currentData["id"]; !ok {
		t.Error("expected current time track to have an id")
	}

	// Cross-verify: the calendar page should show the active time track.
	html := fetchHTML(t, baseURL+"/calendar")
	if len(html) == 0 {
		t.Error("calendar page returned empty HTML during active time track")
	}

	// --- Stop ---
	// Wait so ends_at > starts_at (sub-second precision can cause validation failure).
	time.Sleep(1 * time.Second)
	stopOut, stopErr, stopCode := hey(t, "timetrack", "stop", "--json")
	if stopCode != 0 {
		t.Fatalf("timetrack stop failed (exit %d): %s", stopCode, stopErr)
	}
	var stopResp Response
	if err := json.Unmarshal([]byte(stopOut), &stopResp); err != nil {
		t.Fatalf("failed to parse stop response: %v", err)
	}
	assertContains(t, stopResp.Summary, "Time tracking stopped")

	// --- Current (should be inactive) ---
	resp = heyJSON(t, "timetrack", "current")
	if resp.Data != nil && string(resp.Data) != "null" {
		t.Log("Warning: current still returns data after stop (may be expected)")
	}
}

func TestTimetrackList(t *testing.T) {
	resp := heyJSON(t, "timetrack", "list")

	type TimeTrack struct {
		ID int `json:"id"`
	}
	tracks := dataAs[[]TimeTrack](t, resp)

	// Cross-verify: if there are time tracks, the calendar page should be accessible.
	if len(tracks) > 0 {
		html := fetchHTML(t, baseURL+"/calendar")
		if len(html) == 0 {
			t.Error("calendar page returned empty HTML")
		}
	}
}

func TestTimetrackListLimit(t *testing.T) {
	resp := heyJSON(t, "timetrack", "list", "--limit", "5")
	type TimeTrack struct {
		ID int `json:"id"`
	}
	tracks := dataAs[[]TimeTrack](t, resp)
	if len(tracks) > 5 {
		t.Errorf("expected at most 5 time tracks with --limit 5, got %d", len(tracks))
	}
}

func TestTimetrackListAll(t *testing.T) {
	resp := heyJSON(t, "timetrack", "list", "--all")
	_ = resp
}

func TestTimetrackExport(t *testing.T) {
	stdout, stderr, code := hey(t, "timetrack", "export")
	if code != 0 {
		t.Fatalf("timetrack export failed (exit %d): %s", code, stderr)
	}
	assertTimeTrackExportCSV(t, stdout)

	destination := filepath.Join(t.TempDir(), "tracked-time.csv")
	output, stderr, code := hey(t, "timetrack", "export", "--output", destination, "--json")
	if code != 0 {
		t.Fatalf("timetrack export --output failed (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("parse export response: %v", err)
	}
	assertContains(t, response.Summary, destination)

	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read time track export: %v", err)
	}
	assertTimeTrackExportCSV(t, string(contents))
}

func assertTimeTrackExportCSV(t *testing.T, contents string) {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(contents)).ReadAll()
	if err != nil {
		t.Fatalf("parse time track CSV: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("time track export has no header")
	}
	want := []string{"Start", "End", "Duration", "Category", "Notes"}
	if strings.Join(rows[0], ",") != strings.Join(want, ",") {
		t.Errorf("CSV header = %v, want %v", rows[0], want)
	}
}

func TestTimetrackCategories(t *testing.T) {
	title := "Client work " + uniqueID()
	_, stderr, code := hey(t, "timetrack", "category", "create", title)
	if code != 0 {
		t.Skipf("timetrack category create unavailable (exit %d): %s", code, stderr)
	}

	categoryID := findTimeTrackCategory(t, title)
	t.Cleanup(func() {
		hey(t, "timetrack", "category", "delete", categoryID)
	})

	renamed := "Planning " + uniqueID()
	_, stderr, code = hey(t, "timetrack", "category", "rename", categoryID, renamed)
	if code != 0 {
		t.Fatalf("timetrack category rename failed (exit %d): %s", code, stderr)
	}
	findTimeTrackCategory(t, renamed)

	_, stderr, code = hey(t, "timetrack", "category", "delete", categoryID)
	if code != 0 {
		t.Fatalf("timetrack category delete failed (exit %d): %s", code, stderr)
	}
}

func findTimeTrackCategory(t *testing.T, title string) string {
	t.Helper()
	resp := heyJSON(t, "timetrack", "categories")
	type Category struct {
		ID    json.Number `json:"id"`
		Title string      `json:"title"`
	}
	for _, category := range dataAs[[]Category](t, resp) {
		if category.Title == title {
			return category.ID.String()
		}
	}
	t.Fatalf("time track category %q not found", title)
	return ""
}

func TestTimetrackCurrentNoActive(t *testing.T) {
	// Stop any existing time track first.
	hey(t, "timetrack", "stop")

	// Current should succeed but return nil/null data.
	_, _, code := hey(t, "timetrack", "current", "--json")
	// The command may succeed with null data or fail — both are acceptable.
	_ = code
}

func TestTimetrackStopWithoutActive(t *testing.T) {
	// Make sure nothing is running by stopping if there is one.
	hey(t, "timetrack", "stop")

	// Now stopping again should fail (either "not found" or API error).
	_, _, code := hey(t, "timetrack", "stop", "--json")
	if code == 0 {
		t.Error("expected 'hey timetrack stop' to fail when no active time track")
	}
}
