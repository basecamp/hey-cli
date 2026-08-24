package smoke_test

import (
	"testing"
)

func TestCalendarList(t *testing.T) {
	resp := heyJSON(t, "calendar", "list")

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
