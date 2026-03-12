package smoke_test

import (
	"encoding/json"
	"testing"
)

func TestCalendarsList(t *testing.T) {
	resp := heyJSON(t, "calendars")

	type Calendar struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Owned bool   `json:"owned"`
	}
	calendars := dataAs[[]Calendar](t, resp)

	if len(calendars) == 0 {
		t.Fatal("expected at least one calendar")
	}

	// Should have a personal calendar.
	found := false
	for _, c := range calendars {
		if c.Owned {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one owned calendar")
	}

	// Cross-verify: the calendar page should be accessible and non-empty.
	html := fetchHTML(t, baseURL+"/calendar")
	if len(html) == 0 {
		t.Error("calendar page returned empty HTML")
	}
}

func TestRecordings(t *testing.T) {
	// Get a calendar ID first.
	resp := heyJSON(t, "calendars")
	type Calendar struct {
		ID int `json:"id"`
	}
	calendars := dataAs[[]Calendar](t, resp)
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}

	calID := calendars[0].ID
	recResp := heyJSON(t, "recordings", intStr(calID))
	// Recordings response is a map of type → []recording.
	// Just verify the command succeeds and returns a map.
	if recResp.Data == nil || string(recResp.Data) == "null" {
		// Empty recordings returns null — that's acceptable.
		return
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(recResp.Data, &data); err != nil {
		t.Fatalf("recordings data is not a map: %v", err)
	}

	// Cross-verify: if there are recordings, pick one and verify its title
	// exists on the calendar page.
	type Recording struct {
		Title string `json:"title"`
	}
	for _, raw := range data {
		var recordings []Recording
		if err := json.Unmarshal(raw, &recordings); err != nil {
			continue
		}
		if len(recordings) > 0 && recordings[0].Title != "" {
			html := fetchHTML(t, baseURL+"/calendar")
			assertContains(t, html, recordings[0].Title)
			break
		}
	}
}

func TestRecordingsWithDateRange(t *testing.T) {
	resp := heyJSON(t, "calendars")
	type Calendar struct {
		ID int `json:"id"`
	}
	calendars := dataAs[[]Calendar](t, resp)
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}

	calID := calendars[0].ID
	heyJSON(t, "recordings", intStr(calID),
		"--starts-on", "2024-01-01",
		"--ends-on", "2024-12-31",
	)
}

func TestRecordingsLimit(t *testing.T) {
	resp := heyJSON(t, "calendars")
	type Calendar struct {
		ID int `json:"id"`
	}
	calendars := dataAs[[]Calendar](t, resp)
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}

	calID := calendars[0].ID
	heyJSON(t, "recordings", intStr(calID), "--limit", "5")
}

func TestRecordingsAll(t *testing.T) {
	resp := heyJSON(t, "calendars")
	type Calendar struct {
		ID int `json:"id"`
	}
	calendars := dataAs[[]Calendar](t, resp)
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}

	calID := calendars[0].ID
	heyJSON(t, "recordings", intStr(calID), "--all")
}

func TestRecordingsNoArgument(t *testing.T) {
	heyFail(t, "recordings", "--json")
}

func TestRecordingsInvalidCalendarID(t *testing.T) {
	heyFail(t, "recordings", "999999999", "--json")
}
