package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

func runJSONCommand(t *testing.T, handler http.Handler, args ...string) (output.Response, error) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", configHome)

	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var response output.Response
	if stdout.Len() > 0 {
		if decodeErr := json.Unmarshal(stdout.Bytes(), &response); decodeErr != nil {
			t.Fatalf("decode command output %q: %v", stdout.String(), decodeErr)
		}
	}
	return response, err
}

func TestCalendarsCommand(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/calendars.json" {
			t.Errorf("request = %s %s, want GET /calendars.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":7,"name":"Personal","kind":"Calendar","owned":true,"personal":true}},{"calendar":{"id":9,"name":"Team","kind":"Calendar"}}]}`)
	}), "calendar", "list")
	if err != nil {
		t.Fatalf("execute calendars: %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}
	if response.Summary != "2 calendars" {
		t.Errorf("summary = %q, want 2 calendars", response.Summary)
	}
	items, ok := response.Data.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("data = %#v, want two calendars", response.Data)
	}
}

// Two accounts both keep a "Maybe", so the table names the account they belong to. With one
// account it would be the reader's own address written down the column, so it stays away.
func TestCalendarsNamesTheAccountOnlyWhenThereAreSeveral(t *testing.T) {
	if spansAccounts([]generated.Calendar{
		{Id: 7, Name: "Maybe", OwnerEmailAddress: "amelia@example.com"},
		{Id: 9, Name: "Work", OwnerEmailAddress: "amelia@example.com"},
	}) {
		t.Error("one account should not be named")
	}

	if !spansAccounts([]generated.Calendar{
		{Id: 7, Name: "Maybe", OwnerEmailAddress: "amelia@example.com"},
		{Id: 11, Name: "Maybe", OwnerEmailAddress: "amelia@example.org"},
	}) {
		t.Error("two accounts should be named")
	}

	// The personal calendar has no owner address, and one on its own is not two accounts.
	if spansAccounts([]generated.Calendar{{Id: 1}, {Id: 7, OwnerEmailAddress: "amelia@example.com"}}) {
		t.Error("a calendar with no account made the list look like several")
	}
}

func TestCalendarsCommandAPIError(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "calendar unavailable", http.StatusBadRequest)
	}), "calendar", "list")
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("error = %v, want HTTP failure", err)
	}
}

func TestEventsListCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/calendars/7/recordings.json" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("starts_on"); got != "2026-08-01" {
			t.Errorf("starts_on = %q", got)
		}
		if got := r.URL.Query().Get("ends_on"); got != "2026-08-31" {
			t.Errorf("ends_on = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Calendar::Event":[{"id":1,"title":"Planning","starts_at":"2026-08-02T09:00:00Z"},{"id":2,"title":"Review","starts_at":"2026-08-03T09:00:00Z"}],"Calendar::Todo":[{"id":3,"title":"Send notes"}]}`)
	}), "event", "list", "--calendar", "7", "--starts-on", "2026-08-01", "--ends-on", "2026-08-31", "--limit", "1")
	if err != nil {
		t.Fatalf("execute events list: %v", err)
	}
	if response.Summary != "1 events (2026-08-01 to 2026-08-31)" {
		t.Errorf("summary = %q", response.Summary)
	}
	if response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
	events, ok := response.Data.([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("data = %#v, want one limited event", response.Data)
	}
	event, ok := events[0].(map[string]any)
	if !ok || event["title"] != "Planning" {
		t.Errorf("event = %#v, want the first event and no to-dos", events[0])
	}
}

// Without --calendar the listing reads every calendar the identity has, since an event can
// be on any of them — unlike a to-do or a journal entry, which are on the personal one.
func TestEventsListReadsEveryCalendar(t *testing.T) {
	var read []string
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":7,"name":"Personal","personal":true}},{"calendar":{"id":9,"name":"Work","owned":true}}]}`)
		case "/calendars/7/recordings.json":
			read = append(read, "7")
			_, _ = io.WriteString(w, `{"Calendar::Event":[{"id":1,"title":"Dentist"}]}`)
		case "/calendars/9/recordings.json":
			read = append(read, "9")
			_, _ = io.WriteString(w, `{"Calendar::Event":[{"id":2,"title":"Design review"}]}`)
		default:
			http.NotFound(w, r)
		}
	}), "event", "list")
	if err != nil {
		t.Fatalf("execute events list: %v", err)
	}
	if len(read) != 2 || read[0] != "7" || read[1] != "9" {
		t.Errorf("calendars read = %v, want both in the order they were listed", read)
	}
	if events, ok := response.Data.([]any); !ok || len(events) != 2 {
		t.Errorf("data = %#v, want both calendars' events", response.Data)
	}
}

// Calendar recordings are geared-paginated newest start first. A recurring series that
// began long ago can sit behind a full page of newer one-off events even while it recurs in
// the requested window, so stopping at the first page makes the series disappear.
func TestEventsListFollowsEveryRecordingsPage(t *testing.T) {
	var pages []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "":
			w.Header().Set("Link", `</calendars/7/recordings.json?page=older-starts>; rel="next"`)
			_, _ = io.WriteString(w, `{"Calendar::Event":[{"id":1,"title":"Design review","starts_at":"2026-08-03T09:00:00Z"}]}`)
		case "older-starts":
			_, _ = io.WriteString(w, `{"Calendar::Event":[{"id":2,"title":"Weekly planning","recurring":true,"starts_at":"2025-01-06T09:00:00Z"}]}`)
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	})

	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"},
		"event", "list", "--calendar", "7", "--starts-on", "2026-08-01", "--ends-on", "2026-08-31")
	if err != nil {
		t.Fatalf("execute events list --ids-only: %v", err)
	}
	if got := strings.Join(pages, ","); got != ",older-starts" {
		t.Errorf("pages = %q, want the first page followed by HEY's cursor", got)
	}
	if got := strings.Fields(ids); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Errorf("ids = %q, want the one-off event and the recurring series from page two", ids)
	}
}

func TestEventsListDefaultsEndDateFromStart(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("ends_on"); got != "2026-03-03" {
			t.Errorf("ends_on = %q, want 30 days after 2026-02-01", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}), "event", "list", "--calendar", "7", "--starts-on", "2026-02-01")
	if err != nil {
		t.Fatalf("execute events list: %v", err)
	}
	if response.Notice != "" {
		t.Errorf("notice = %q, want empty", response.Notice)
	}
}

func TestEventsListCountsAndListsIDs(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Calendar::Event":[{"id":1,"title":"Planning"},{"id":2,"title":"Review"}],"Calendar::Todo":[{"id":3,"title":"Send notes"}]}`)
	})

	count, err := runFormattedCommand(t, handler, []string{"--count"}, "event", "list", "--calendar", "7")
	if err != nil {
		t.Fatalf("execute events list --count: %v", err)
	}
	if strings.TrimSpace(count) != "2" {
		t.Errorf("count = %q, want 2 — the events, not the to-do", count)
	}

	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"}, "event", "list", "--calendar", "7")
	if err != nil {
		t.Fatalf("execute events list --ids-only: %v", err)
	}
	if got := strings.Fields(ids); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Errorf("ids = %q, want the two event IDs", ids)
	}
}

func TestEventsValidationMakesNoRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "start date", args: []string{"event", "list", "--calendar", "7", "--starts-on", "tomorrow"}, want: "invalid starts-on date"},
		{name: "end date", args: []string{"event", "list", "--calendar", "7", "--ends-on", "alsobad"}, want: "invalid ends-on date"},
		{
			name: "backwards window",
			args: []string{"event", "list", "--calendar", "7", "--starts-on", "2026-08-31", "--ends-on", "2026-08-01"},
			want: "ends-on 2026-08-01 is before starts-on 2026-08-31",
		},
		{name: "event ID", args: []string{"event", "delete", "not-an-id"}, want: "invalid event ID"},
		{
			name: "repeat frequency",
			args: []string{"event", "add", "Standup", "--calendar", "7", "--repeat", "every_fortnight"},
			want: "invalid repeat: every_fortnight",
		},
		{
			name: "repeat window without a frequency",
			args: []string{"event", "add", "Standup", "--calendar", "7", "--repeat-times", "5"},
			want: "repeat-until and repeat-times need --repeat",
		},
		{
			name: "clock time",
			args: []string{"event", "add", "Standup", "--calendar", "7", "--start-time", "9am"},
			want: "invalid start-time: 9am",
		},
		{
			name: "notice period",
			args: []string{"event", "add", "Standup", "--calendar", "7", "--remind", "soon"},
			want: "invalid remind: soon",
		},
		{
			name: "countdown unit",
			args: []string{"event", "add", "Launch", "--calendar", "7", "--countdown", "3", "--countdown-unit", "fortnights"},
			want: "invalid countdown-unit: fortnights",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "unexpected request", http.StatusInternalServerError)
			}), tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", requests.Load())
			}
		})
	}
}

func personalCalendarHandler(t *testing.T, recordings string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":7,"name":"Personal","personal":true}}]}`)
		case "/calendars/7/recordings.json":
			if r.URL.Query().Get("starts_on") == "" || r.URL.Query().Get("ends_on") == "" {
				t.Error("personal recordings request omitted date window")
			}
			_, _ = io.WriteString(w, recordings)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestTodoListCommand(t *testing.T) {
	recordings := `{"Calendar::Todo":[{"id":1,"title":"First"},{"id":2,"title":"Second"}],"Calendar::TimeTrack":[{"id":3,"title":"Work"}]}`
	response, err := runJSONCommand(t, personalCalendarHandler(t, recordings), "todo", "list", "--limit", "1")
	if err != nil {
		t.Fatalf("execute todo list: %v", err)
	}
	if response.Summary != "1 todos" {
		t.Errorf("summary = %q", response.Summary)
	}
	if response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
	items, ok := response.Data.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("data = %#v, want one todo", response.Data)
	}
}

func TestTodoMutationCommands(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		method      string
		path        string
		status      int
		body        string
		wantSummary string
	}{
		{name: "complete", args: []string{"todo", "complete", "42"}, method: http.MethodPost, path: "/calendar/todos/42/completions.json", status: http.StatusOK, body: `{"id":42,"title":"Ship release"}`, wantSummary: "Todo completed"},
		{name: "uncomplete", args: []string{"todo", "uncomplete", "42"}, method: http.MethodDelete, path: "/calendar/todos/42/completions.json", status: http.StatusOK, body: `{"id":42,"title":"Ship release"}`, wantSummary: "Todo marked incomplete"},
		{name: "delete", args: []string{"todo", "delete", "42"}, method: http.MethodDelete, path: "/calendar/todos/42.json", status: http.StatusNoContent, wantSummary: "Todo deleted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != tt.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tt.method, tt.path)
				}
				if tt.body != "" {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}), tt.args...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if response.Summary != tt.wantSummary {
				t.Errorf("summary = %q, want %q", response.Summary, tt.wantSummary)
			}
		})
	}
}

func TestTodoMutationValidationMakesNoRequest(t *testing.T) {
	for _, action := range []string{"complete", "uncomplete", "delete"} {
		t.Run(action, func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
			}), "todo", action, "invalid")
			if err == nil || !strings.Contains(err.Error(), "invalid todo ID") {
				t.Fatalf("error = %v, want invalid todo ID", err)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", requests.Load())
			}
		})
	}
}

func TestTimetrackStartCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/calendar/ongoing_time_track.json" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42,"title":"Coverage work","starts_at":"2026-08-20T09:00:00Z"}`)
	}), "timetrack", "start")
	if err != nil {
		t.Fatalf("execute timetrack start: %v", err)
	}
	if response.Summary != "Time tracking started" {
		t.Errorf("summary = %q", response.Summary)
	}
	if len(response.Breadcrumbs) != 1 || response.Breadcrumbs[0].Action != "stop" {
		t.Errorf("breadcrumbs = %#v", response.Breadcrumbs)
	}
}

func TestTimetrackCurrentCommand(t *testing.T) {
	t.Run("active", func(t *testing.T) {
		response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":42,"title":"Coverage work","starts_at":"2026-08-20T09:00:00Z"}`)
		}), "timetrack", "current")
		if err != nil {
			t.Fatalf("execute current: %v", err)
		}
		if response.Summary != "Active time track #42" {
			t.Errorf("summary = %q", response.Summary)
		}
	})

	t.Run("inactive", func(t *testing.T) {
		response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}), "timetrack", "current")
		if err != nil {
			t.Fatalf("execute current: %v", err)
		}
		if response.Summary != "No active time track" || response.Data != nil {
			t.Errorf("response = %#v", response)
		}
	})
}

func TestTimetrackStopCommand(t *testing.T) {
	var requests []string
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/calendar/ongoing_time_track.json":
			_, _ = io.WriteString(w, `{"id":42,"title":"Coverage work"}`)
		case r.Method == http.MethodPut && r.URL.Path == "/calendar/time_tracks/42.json":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode stop body: %v", err)
			}
			track, ok := body["calendar_time_track"].(map[string]any)
			if !ok || track["ends_at"] == nil {
				t.Errorf("stop body = %#v, want ends_at", body)
			}
			_, _ = io.WriteString(w, `{"id":42}`)
		default:
			http.NotFound(w, r)
		}
	}), "timetrack", "stop")
	if err != nil {
		t.Fatalf("execute stop: %v", err)
	}
	if response.Summary != "Time tracking stopped" {
		t.Errorf("summary = %q", response.Summary)
	}
	wantRequests := []string{"GET /calendar/ongoing_time_track.json", "PUT /calendar/time_tracks/42.json"}
	if fmt.Sprint(requests) != fmt.Sprint(wantRequests) {
		t.Errorf("requests = %v, want %v", requests, wantRequests)
	}
}

func TestTimetrackStopWithoutActiveTrack(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}), "timetrack", "stop")
	if err == nil || !strings.Contains(err.Error(), `time track "active" not found`) {
		t.Fatalf("error = %v, want active track not found", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want only the lookup", requests.Load())
	}
}

func TestTimetrackCategoriesCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/calendar/time_tracks/categories.json" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":31,"title":"Client work"},{"id":32,"title":"Planning"}]`)
	}), "timetrack", "categories")
	if err != nil {
		t.Fatalf("execute timetrack categories: %v", err)
	}
	if response.Summary != "2 time track categories" {
		t.Errorf("summary = %q", response.Summary)
	}
	if len(response.Breadcrumbs) != 1 || response.Breadcrumbs[0].Action != "create" {
		t.Errorf("breadcrumbs = %#v", response.Breadcrumbs)
	}
}

func TestTimetrackCategoryStyledOutputSanitizesTitles(t *testing.T) {
	dangerous := "Client\x1b]52;c;secret\a\nForged"
	assertSafe := func(t *testing.T, stdout string) {
		t.Helper()
		if strings.Contains(stdout, "\x1b]52") || strings.ContainsRune(stdout, '\a') || strings.Contains(stdout, "\nForged") {
			t.Errorf("styled category output retained terminal controls: %q", stdout)
		}
	}

	t.Run("list", func(t *testing.T) {
		stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 31, "title": dangerous}})
		}), "timetrack", "categories")
		if err != nil {
			t.Fatalf("execute timetrack categories: %v", err)
		}
		assertSafe(t, stdout)
	})

	t.Run("mutation", func(t *testing.T) {
		stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if got := r.Form.Get("category[title]"); got != dangerous {
				t.Errorf("category title = %q, want unchanged %q", got, dangerous)
			}
			w.WriteHeader(http.StatusNoContent)
		}), "timetrack", "category", "create", dangerous)
		if err != nil {
			t.Fatalf("execute category create: %v", err)
		}
		assertSafe(t, stdout)
	})
}

func TestTimetrackCategoryMutations(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		method      string
		path        string
		title       string
		wantSummary string
	}{
		{
			name:        "create",
			args:        []string{"timetrack", "category", "create", "Client work"},
			method:      http.MethodPost,
			path:        "/calendar/time_tracks/categories",
			title:       "Client work",
			wantSummary: `Time track category "Client work" created`,
		},
		{
			name:        "rename",
			args:        []string{"timetrack", "category", "rename", "31", "Planning"},
			method:      http.MethodPatch,
			path:        "/calendar/time_tracks/categories/31",
			title:       "Planning",
			wantSummary: `Time track category 31 renamed to "Planning"`,
		},
		{
			name:        "delete",
			args:        []string{"timetrack", "category", "delete", "31"},
			method:      http.MethodDelete,
			path:        "/calendar/time_tracks/categories/31",
			wantSummary: "Time track category 31 deleted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != tt.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tt.method, tt.path)
				}
				if tt.title != "" {
					if err := r.ParseForm(); err != nil {
						t.Fatalf("parse form: %v", err)
					}
					if got := r.Form.Get("category[title]"); got != tt.title {
						t.Errorf("category title = %q, want %q", got, tt.title)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}), tt.args...)
			if err != nil {
				t.Fatalf("execute %s: %v", tt.name, err)
			}
			if response.Summary != tt.wantSummary {
				t.Errorf("summary = %q, want %q", response.Summary, tt.wantSummary)
			}
			if len(response.Breadcrumbs) != 1 || response.Breadcrumbs[0].Action != "list" {
				t.Errorf("breadcrumbs = %#v", response.Breadcrumbs)
			}
		})
	}
}

func TestTimetrackCategoryMutationValidationMakesNoRequest(t *testing.T) {
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	})

	for _, args := range [][]string{
		{"timetrack", "category", "create", "   "},
		{"timetrack", "category", "rename", "invalid", "Planning"},
		{"timetrack", "category", "delete", "0"},
	} {
		if _, err := runJSONCommand(t, handler, args...); err == nil {
			t.Errorf("%v: expected validation error", args)
		}
	}
	if requests.Load() != 0 {
		t.Errorf("requests = %d, want 0", requests.Load())
	}
}
