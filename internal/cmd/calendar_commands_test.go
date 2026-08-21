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
	}), "calendars")
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

func TestCalendarsCommandAPIError(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "calendar unavailable", http.StatusBadRequest)
	}), "calendars")
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("error = %v, want HTTP failure", err)
	}
}

func TestRecordingsCommand(t *testing.T) {
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
	}), "recordings", "7", "--starts-on", "2026-08-01", "--ends-on", "2026-08-31", "--limit", "1")
	if err != nil {
		t.Fatalf("execute recordings: %v", err)
	}
	if response.Summary != "Recordings for calendar 7 (2026-08-01 to 2026-08-31)" {
		t.Errorf("summary = %q", response.Summary)
	}
	if response.Notice != "Showing 2 of 3 results. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %T, want map", response.Data)
	}
	if events, ok := data["Calendar::Event"].([]any); !ok || len(events) != 1 {
		t.Errorf("events = %#v, want one limited event", data["Calendar::Event"])
	}
}

func TestRecordingsDefaultsEndDateFromStart(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("ends_on"); got != "2026-03-03" {
			t.Errorf("ends_on = %q, want 30 days after 2026-02-01", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}), "recordings", "7", "--starts-on", "2026-02-01")
	if err != nil {
		t.Fatalf("execute recordings: %v", err)
	}
	if response.Notice != "" {
		t.Errorf("notice = %q, want empty", response.Notice)
	}
}

func TestRecordingsValidationMakesNoRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "calendar ID", args: []string{"recordings", "not-an-id"}, want: "invalid calendar ID"},
		{name: "start date", args: []string{"recordings", "7", "--starts-on", "tomorrow"}, want: "invalid starts-on date"},
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

func TestTimetrackListCommand(t *testing.T) {
	recordings := `{"Calendar::TimeTrack":[{"id":1,"title":"First"},{"id":2,"title":"Second"}],"Calendar::Todo":[{"id":3,"title":"Todo"}]}`
	response, err := runJSONCommand(t, personalCalendarHandler(t, recordings), "timetrack", "list", "--limit", "1")
	if err != nil {
		t.Fatalf("execute timetrack list: %v", err)
	}
	if response.Summary != "1 time tracks" {
		t.Errorf("summary = %q", response.Summary)
	}
	if response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
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
