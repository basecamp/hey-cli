package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// timeTrackIndexHandler serves the tracked time index one page at a time, keyed by the
// geared_pagination cursor the previous page handed out. It records every page parameter
// it was asked for, so a test can assert what the command actually walked.
func timeTrackIndexHandler(t *testing.T, pages map[string]string, asked *[]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/calendar/time_tracks.json" {
			t.Errorf("request = %s %s, want GET /calendar/time_tracks.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		page := r.URL.Query().Get("page")
		*asked = append(*asked, page)
		body, ok := pages[page]
		if !ok {
			t.Errorf("unexpected page %q", page)
			http.NotFound(w, r)
			return
		}
		if next, hasNext := pages[nextCursorFor(page)]; hasNext && next != "" {
			w.Header().Set("Link", `<`+r.URL.Path+`?page=`+nextCursorFor(page)+`>; rel="next"`)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// nextCursorFor is the fixtures' own convention: the page after "" is "b2", after "b2" is
// "b3", and so on, so a fixture map says how far the list goes.
func nextCursorFor(page string) string {
	switch page {
	case "":
		return "b2"
	case "b2":
		return "b3"
	default:
		return "b4"
	}
}

const timeTrackFirstPage = `{"time_tracks":[
  {"id":1042,"type":"Calendar::TimeTrack","title":"Time Track","category":"Client work","notes":"Invoice review","starts_at":"2026-08-21T09:00:00Z","ends_at":"2026-08-21T11:15:00Z","completed_at":"2026-08-21T11:15:00Z"},
  {"id":1041,"type":"Calendar::TimeTrack","title":"Time Track","notes":"","starts_at":"2026-08-20T22:30:00Z","ends_at":"2026-08-21T00:45:00Z","completed_at":"2026-08-21T00:45:00Z"}
], "categories":[{"id":42,"title":"Client work"}]}`

const timeTrackSecondPage = `{"time_tracks":[
  {"id":1040,"type":"Calendar::TimeTrack","title":"Time Track","category":"Planning","starts_at":"2026-08-19T14:00:00Z","ends_at":"2026-08-19T14:30:00Z","completed_at":"2026-08-19T14:30:00Z"}
], "categories":[{"id":42,"title":"Client work"}]}`

func TestTimetrackListReadsOnePageByDefault(t *testing.T) {
	var asked []string
	pages := map[string]string{"": timeTrackFirstPage, "b2": timeTrackSecondPage}
	response, err := runJSONCommand(t, timeTrackIndexHandler(t, pages, &asked), "timetrack", "list")
	if err != nil {
		t.Fatalf("execute timetrack list: %v", err)
	}
	if len(asked) != 1 || asked[0] != "" {
		t.Errorf("pages asked for = %#v, want the first page only", asked)
	}
	if response.Summary != "2 time tracks" {
		t.Errorf("summary = %q, want 2 time tracks", response.Summary)
	}
	if response.Notice != "Showing 2 time tracks. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
	tracks, ok := response.Data.([]any)
	if !ok || len(tracks) != 2 {
		t.Fatalf("data = %#v, want two time tracks", response.Data)
	}
}

func TestTimetrackListAllStopsAtAnEmptyPage(t *testing.T) {
	var asked []string
	pages := map[string]string{
		"":   timeTrackFirstPage,
		"b2": timeTrackSecondPage,
		"b3": `{"time_tracks":[],"categories":[]}`,
	}
	response, err := runJSONCommand(t, timeTrackIndexHandler(t, pages, &asked), "timetrack", "list", "--all")
	if err != nil {
		t.Fatalf("execute timetrack list --all: %v", err)
	}
	if strings.Join(asked, ",") != ",b2,b3" {
		t.Errorf("pages asked for = %#v, want the first three", asked)
	}
	if response.Summary != "3 time tracks" {
		t.Errorf("summary = %q, want 3 time tracks", response.Summary)
	}
	if response.Notice != "" {
		t.Errorf("notice = %q, want none once the list ran out", response.Notice)
	}
	if pagesFetched, _ := response.Meta["pages_fetched"].(float64); pagesFetched != 3 {
		t.Errorf("pages_fetched = %v, want 3", response.Meta["pages_fetched"])
	}
}

func TestTimetrackListHonoursLimit(t *testing.T) {
	var asked []string
	pages := map[string]string{"": timeTrackFirstPage, "b2": timeTrackSecondPage}
	response, err := runJSONCommand(t, timeTrackIndexHandler(t, pages, &asked), "timetrack", "list", "--limit", "1")
	if err != nil {
		t.Fatalf("execute timetrack list --limit 1: %v", err)
	}
	if len(asked) != 1 {
		t.Errorf("pages asked for = %#v, want one page for one row", asked)
	}
	tracks, ok := response.Data.([]any)
	if !ok || len(tracks) != 1 {
		t.Fatalf("data = %#v, want one time track", response.Data)
	}
	if response.Notice != "Showing 1 time track. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
}

func TestTimetrackListStyledShowsLengthAndCategory(t *testing.T) {
	var asked []string
	pages := map[string]string{"": timeTrackFirstPage, "b2": timeTrackSecondPage}
	stdout, err := runStyledCommand(t, timeTrackIndexHandler(t, pages, &asked), "timetrack", "list")
	if err != nil {
		t.Fatalf("execute timetrack list --styled: %v", err)
	}
	// The clock is the reader's own, not the UTC HEY serves — expectations are derived rather
	// than written out, or this would only pass in Greenwich.
	starts := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC).Local()
	ends := time.Date(2026, 8, 21, 11, 15, 0, 0, time.UTC).Local()
	for _, want := range []string{
		"1042", starts.Format("2006-01-02"), starts.Format("15:04"), ends.Format("15:04"),
		"2:15:00", "Client work", "Invoice review",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table missing %q:\n%s", want, stdout)
		}
	}

	// A track that crosses midnight on the reader's clock carries the date on its end, so it
	// cannot read as ending before it started. Whether it crosses at all depends on the zone.
	overnightStart := time.Date(2026, 8, 20, 22, 30, 0, 0, time.UTC).Local()
	overnightEnd := time.Date(2026, 8, 21, 0, 45, 0, 0, time.UTC).Local()
	wantEnd := overnightEnd.Format("15:04")
	if overnightEnd.Format("2006-01-02") != overnightStart.Format("2006-01-02") {
		wantEnd = overnightEnd.Format("2006-01-02T15:04")
	}
	if !strings.Contains(stdout, wantEnd) {
		t.Errorf("overnight end %q not shown:\n%s", wantEnd, stdout)
	}
	if !strings.Contains(stdout, "Use --all to see everything.") {
		t.Errorf("notice missing:\n%s", stdout)
	}
}

func TestTimetrackListFiltersByCategory(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.URL.Query().Get("category_id"); got != "42" {
			t.Errorf("category_id = %q, want 42", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, timeTrackSecondPage)
	}), "timetrack", "list", "--category", "42")
	if err != nil {
		t.Fatalf("execute timetrack list --category: %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}
	if response.Summary != "1 time track" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestTimetrackListUnknownCategory(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}), "timetrack", "list", "--category", "999")
	if err == nil || !strings.Contains(err.Error(), `time track category "999" not found`) {
		t.Fatalf("error = %v, want the category reported as missing", err)
	}
}

func TestTimetrackListRejectsCategoryZero(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}), "timetrack", "list", "--category", "0")
	if err == nil || !strings.Contains(err.Error(), "--category takes a category ID") {
		t.Fatalf("error = %v, want a usage error", err)
	}
	if requests.Load() != 0 {
		t.Errorf("requests = %d, want none", requests.Load())
	}
}

func TestTimetrackListNotice(t *testing.T) {
	tests := []struct {
		name      string
		shown     int
		pages     int
		more      bool
		truncated bool
		want      string
	}{
		{name: "complete list", shown: 3, pages: 2},
		{name: "more to read", shown: 2, pages: 1, more: true, want: "Showing 2 time tracks. Use --all to see everything."},
		{name: "page cap", shown: 500, pages: 100, more: true, truncated: true, want: "Stopped after 100 pages, at 500 time tracks. Narrow the list with --category."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := timetrackListNotice(test.shown, test.pages, test.more, test.truncated); got != test.want {
				t.Errorf("notice = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFormatTrackedLength(t *testing.T) {
	tests := []struct {
		name string
		of   time.Duration
		want string
	}{
		{name: "seconds", of: 45 * time.Second, want: "0:00:45"},
		{name: "minutes", of: 2*time.Hour + 15*time.Minute, want: "2:15:00"},
		{name: "past a day", of: 26*time.Hour + time.Second, want: "26:00:01"},
		{name: "backwards", of: -time.Hour, want: "0:00:00"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatTrackedLength(test.of); got != test.want {
				t.Errorf("length = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTimetrackStopFilesUnderCategory(t *testing.T) {
	var body map[string]any
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "ongoing"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":1043,"type":"Calendar::TimeTrack","starts_at":"2026-08-22T09:00:00Z"}`)
		case r.Method == http.MethodPut && r.URL.Path == "/calendar/time_tracks/1043.json":
			if decodeErr := json.NewDecoder(r.Body).Decode(&body); decodeErr != nil {
				t.Errorf("decode stop body: %v", decodeErr)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":1043,"type":"Calendar::TimeTrack"}`)
		default:
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}), "timetrack", "stop", "--category", "Client work")
	if err != nil {
		t.Fatalf("execute timetrack stop --category: %v", err)
	}
	if response.Summary != `Time tracking stopped and filed under "Client work"` {
		t.Errorf("summary = %q", response.Summary)
	}
	payload, ok := body["calendar_time_track"].(map[string]any)
	if !ok {
		t.Fatalf("body = %#v, want a calendar_time_track payload", body)
	}
	if payload["category_title"] != "Client work" {
		t.Errorf("category_title = %#v, want Client work", payload["category_title"])
	}
	if payload["ends_at"] == nil {
		t.Error("ends_at missing, so the track was never stopped")
	}
}

func TestTimetrackStopRejectsBlankCategory(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1043,"type":"Calendar::TimeTrack"}`)
	}), "timetrack", "stop", "--category", "  ")
	if err == nil || !strings.Contains(err.Error(), "does not clear a category") {
		t.Fatalf("error = %v, want a usage error", err)
	}
}

func TestTimetrackEditSendsOnlyTheFlagsGiven(t *testing.T) {
	var body map[string]any
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/calendar/time_tracks/1042.json" {
			t.Errorf("request = %s %s, want PUT /calendar/time_tracks/1042.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if decodeErr := json.NewDecoder(r.Body).Decode(&body); decodeErr != nil {
			t.Errorf("decode edit body: %v", decodeErr)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1042,"type":"Calendar::TimeTrack","notes":"Invoice review"}`)
	}), "timetrack", "edit", "1042", "--notes", "Invoice review", "--end", "2026-08-22T17:30")
	if err != nil {
		t.Fatalf("execute timetrack edit: %v", err)
	}
	if response.Summary != "Time track updated" {
		t.Errorf("summary = %q", response.Summary)
	}
	payload, ok := body["calendar_time_track"].(map[string]any)
	if !ok {
		t.Fatalf("body = %#v, want a calendar_time_track payload", body)
	}
	if payload["notes"] != "Invoice review" {
		t.Errorf("notes = %#v", payload["notes"])
	}
	wantEnd := time.Date(2026, 8, 22, 17, 30, 0, 0, time.Local).UTC().Format(time.RFC3339)
	if payload["ends_at"] != wantEnd {
		t.Errorf("ends_at = %#v, want %q", payload["ends_at"], wantEnd)
	}
	if _, sent := payload["starts_at"]; sent {
		t.Errorf("starts_at = %#v, want it left alone", payload["starts_at"])
	}
	if _, sent := payload["category_title"]; sent {
		t.Errorf("category_title = %#v, want it left alone", payload["category_title"])
	}
}

func TestTimetrackEditNeedsAFlag(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}), "timetrack", "edit", "1042")
	if err == nil || !strings.Contains(err.Error(), "provide at least one of --start, --end, --category, or --notes") {
		t.Fatalf("error = %v, want a usage error", err)
	}
	if requests.Load() != 0 {
		t.Errorf("requests = %d, want none", requests.Load())
	}
}

func TestTimetrackEditRejectsUnreadableTimestamp(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}), "timetrack", "edit", "1042", "--start", "yesterday morning")
	if err == nil || !strings.Contains(err.Error(), "invalid --start") {
		t.Fatalf("error = %v, want a usage error", err)
	}
	if requests.Load() != 0 {
		t.Errorf("requests = %d, want none", requests.Load())
	}
}

func TestTimetrackEditReadsBadRequestAsUsage(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad timestamp", http.StatusBadRequest)
	}), "timetrack", "edit", "1042", "--end", "2026-08-22T17:30")
	if err == nil || !strings.Contains(err.Error(), "HEY could not read the time track update") {
		t.Fatalf("error = %v, want the 400 read back as a usage problem", err)
	}
}

func TestTimetrackDelete(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodDelete || r.URL.Path != "/calendar/time_tracks/1042.json" {
			t.Errorf("request = %s %s, want DELETE /calendar/time_tracks/1042.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), "timetrack", "delete", "1042")
	if err != nil {
		t.Fatalf("execute timetrack delete: %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}
	if response.Summary != "Time track deleted" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestTimetrackDeleteRejectsBadID(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}), "timetrack", "delete", "nope")
	if err == nil || !strings.Contains(err.Error(), "invalid time track ID") {
		t.Fatalf("error = %v, want a usage error", err)
	}
	if requests.Load() != 0 {
		t.Errorf("requests = %d, want none", requests.Load())
	}
}
