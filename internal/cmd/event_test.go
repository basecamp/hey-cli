package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

func eventServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/calendars.json":
			resp := map[string]any{
				"calendars": []map[string]any{
					{
						"calendar": map[string]any{
							"id":       42,
							"name":     "Personal",
							"personal": true,
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/calendars/") && strings.HasSuffix(r.URL.Path, "/recordings"):
			resp := map[string]any{
				"Calendar::Event": []map[string]any{
					{
						"id":        101,
						"title":     "Team standup",
						"starts_at": "2024-05-01T09:00:00Z",
						"ends_at":   "2024-05-01T09:30:00Z",
					},
					{
						"id":        102,
						"title":     "Lunch meeting",
						"starts_at": "2024-05-02T12:00:00Z",
						"ends_at":   "2024-05-02T13:00:00Z",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func runEventList(t *testing.T, server *httptest.Server, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	fullArgs := append([]string{"event", "list", "--base-url", server.URL}, args...)
	root.SetArgs(fullArgs)

	err := root.Execute()
	return buf.String(), err
}

func TestEventListDefault(t *testing.T) {
	server := eventServer(t)
	defer server.Close()

	out, err := runEventList(t, server, "--styled")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "Team standup") {
		t.Errorf("output missing event title: %q", out)
	}
	if !strings.Contains(out, "Lunch meeting") {
		t.Errorf("output missing event title: %q", out)
	}
}

func TestEventListLimit(t *testing.T) {
	server := eventServer(t)
	defer server.Close()

	out, err := runEventList(t, server, "--styled", "--limit", "1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "Team standup") {
		t.Errorf("output missing first event: %q", out)
	}
	if strings.Contains(out, "Lunch meeting") {
		t.Errorf("output should not contain second event when limit=1: %q", out)
	}
}

// eventCreateServer captures the POST body so assertions can verify the
// form-encoded payload sent to the server.
type capturedRequest struct {
	mu   sync.Mutex
	body string
}

func (c *capturedRequest) set(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = s
}

func (c *capturedRequest) get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

func eventCreateServer(t *testing.T, captured *capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/calendars.json":
			resp := map[string]any{
				"calendars": []map[string]any{
					{
						"calendar": map[string]any{
							"id":       42,
							"name":     "Personal",
							"personal": true,
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == "POST" && r.URL.Path == "/calendar/events":
			body, _ := io.ReadAll(r.Body)
			captured.set(string(body))
			w.Header().Set("Location", "/calendar/events/999")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func runEventCreate(t *testing.T, server *httptest.Server, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	fullArgs := append([]string{"event", "create", "--base-url", server.URL}, args...)
	root.SetArgs(fullArgs)

	err := root.Execute()
	return buf.String(), err
}

func TestEventCreateRequiresTitle(t *testing.T) {
	captured := &capturedRequest{}
	server := eventCreateServer(t, captured)
	defer server.Close()

	_, err := runEventCreate(t, server, "--date", "2024-06-15", "--all-day")
	if err == nil {
		t.Fatalf("expected error when --title missing")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "title") {
		t.Errorf("expected error to mention 'title', got: %v", err)
	}
}

func TestEventCreateTimed(t *testing.T) {
	captured := &capturedRequest{}
	server := eventCreateServer(t, captured)
	defer server.Close()

	_, err := runEventCreate(t, server,
		"--title", "Team sync",
		"--date", "2024-06-15",
		"--start", "09:00",
		"--end", "10:00",
		"--timezone", "America/New_York",
	)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	body := captured.get()
	wantFragments := []string{
		"calendar_event%5Bsummary%5D=Team+sync",
		"calendar_event%5Bstarts_at%5D=2024-06-15",
		"calendar_event%5Bstarts_at_time%5D=09%3A00%3A00",
		"calendar_event%5Ball_day%5D=0",
		"calendar_event%5Bstarts_at_time_zone_name%5D=America%2FNew_York",
		"calendar_event%5Bcalendar_id%5D=42",
	}
	for _, frag := range wantFragments {
		if !strings.Contains(body, frag) {
			t.Errorf("body missing fragment %q; body=%s", frag, body)
		}
	}
}

func TestEventCreateAllDay(t *testing.T) {
	captured := &capturedRequest{}
	server := eventCreateServer(t, captured)
	defer server.Close()

	_, err := runEventCreate(t, server,
		"--title", "Holiday",
		"--date", "2024-06-15",
		"--all-day",
		"--reminder", "1d",
	)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	body := captured.get()
	if !strings.Contains(body, "calendar_event%5Ball_day%5D=1") {
		t.Errorf("body missing all_day=1; body=%s", body)
	}
	if !strings.Contains(body, "all_day_reminder_durations%5B%5D=86400") {
		t.Errorf("body missing reminder 86400; body=%s", body)
	}
}

func eventEditServer(t *testing.T, captured *capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PATCH" && r.URL.Path == "/calendar/events/101":
			body, _ := io.ReadAll(r.Body)
			captured.set(string(body))
			w.Header().Set("Location", "/calendar/events/101")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func runEventEdit(t *testing.T, server *httptest.Server, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	fullArgs := append([]string{"event", "edit", "--base-url", server.URL}, args...)
	root.SetArgs(fullArgs)

	err := root.Execute()
	return buf.String(), err
}

func TestEventEdit(t *testing.T) {
	captured := &capturedRequest{}
	server := eventEditServer(t, captured)
	defer server.Close()

	_, err := runEventEdit(t, server, "101", "--title", "Updated standup")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	body := captured.get()
	if !strings.Contains(body, "calendar_event%5Bsummary%5D=Updated+standup") {
		t.Errorf("body missing summary fragment; body=%s", body)
	}
}

func TestEventEditInvalidID(t *testing.T) {
	captured := &capturedRequest{}
	server := eventEditServer(t, captured)
	defer server.Close()

	_, err := runEventEdit(t, server, "notanumber", "--title", "x")
	if err == nil {
		t.Fatalf("expected error for invalid event ID")
	}
	if !strings.Contains(err.Error(), "invalid event ID") {
		t.Errorf("expected 'invalid event ID' in error, got: %v", err)
	}
}

func TestEventEditOnlyChangedFields(t *testing.T) {
	captured := &capturedRequest{}
	server := eventEditServer(t, captured)
	defer server.Close()

	_, err := runEventEdit(t, server, "101", "--title", "X")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	body := captured.get()
	forbidden := []string{"starts_at", "ends_at", "all_day", "starts_at_time", "ends_at_time"}
	for _, f := range forbidden {
		if strings.Contains(body, f) {
			t.Errorf("body should not contain %q when not changed; body=%s", f, body)
		}
	}
}

type capturedMethodPath struct {
	mu     sync.Mutex
	method string
	path   string
}

func (c *capturedMethodPath) set(method, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.method = method
	c.path = path
}

func (c *capturedMethodPath) get() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.method, c.path
}

func eventDeleteServer(t *testing.T, captured *capturedMethodPath) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE" && r.URL.Path == "/calendar/events/101":
			captured.set(r.Method, r.URL.Path)
			w.Header().Set("Location", "/calendar")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func runEventDelete(t *testing.T, server *httptest.Server, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	fullArgs := append([]string{"event", "delete", "--base-url", server.URL}, args...)
	root.SetArgs(fullArgs)

	err := root.Execute()
	return buf.String(), err
}

func TestEventDelete(t *testing.T) {
	captured := &capturedMethodPath{}
	server := eventDeleteServer(t, captured)
	defer server.Close()

	_, err := runEventDelete(t, server, "101")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	method, path := captured.get()
	if method != "DELETE" {
		t.Errorf("expected DELETE method, got %q", method)
	}
	if path != "/calendar/events/101" {
		t.Errorf("expected path /calendar/events/101, got %q", path)
	}
}

// eventCreateMultiCalendarServer returns a calendar list with multiple owned
// calendars. The caller specifies the JSON payload via calendarsPayload so
// tests can model name-match scenarios (unique, ambiguous, missing, no
// personal).
func eventCreateCustomServer(t *testing.T, captured *capturedRequest, calendarsPayload []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/calendars.json":
			resp := map[string]any{"calendars": calendarsPayload}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == "POST" && r.URL.Path == "/calendar/events":
			body, _ := io.ReadAll(r.Body)
			captured.set(string(body))
			w.Header().Set("Location", "/calendar/events/999")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestEventCreate_CalendarByName(t *testing.T) {
	captured := &capturedRequest{}
	server := eventCreateCustomServer(t, captured, []map[string]any{
		{"calendar": map[string]any{"id": 42, "name": "Personal", "personal": true, "owned": true}},
		{"calendar": map[string]any{"id": 791879, "name": "Work", "owned": true}},
	})
	defer server.Close()

	_, err := runEventCreate(t, server,
		"--calendar", "Work",
		"--title", "T",
		"--date", "2024-06-15",
		"--all-day",
	)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := captured.get()
	if !strings.Contains(body, "calendar_event%5Bcalendar_id%5D=791879") {
		t.Errorf("body missing calendar_id=791879; body=%s", body)
	}
}

func TestEventCreate_CalendarByNameAmbiguous(t *testing.T) {
	captured := &capturedRequest{}
	server := eventCreateCustomServer(t, captured, []map[string]any{
		{"calendar": map[string]any{"id": 100, "name": "Personal", "owned": true}},
		{"calendar": map[string]any{"id": 200, "name": "Personal", "owned": true}},
	})
	defer server.Close()

	_, err := runEventCreate(t, server,
		"--calendar", "Personal",
		"--title", "T",
		"--date", "2024-06-15",
		"--all-day",
	)
	if err == nil {
		t.Fatalf("expected error for ambiguous calendar name")
	}
	msg := err.Error()
	if !strings.Contains(msg, "100") || !strings.Contains(msg, "200") {
		t.Errorf("error should mention both IDs, got: %v", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "id") {
		t.Errorf("error should say to pick by ID, got: %v", msg)
	}
}

func TestEventCreate_CalendarNotFound(t *testing.T) {
	captured := &capturedRequest{}
	server := eventCreateCustomServer(t, captured, []map[string]any{
		{"calendar": map[string]any{"id": 42, "name": "Personal", "personal": true, "owned": true}},
		{"calendar": map[string]any{"id": 99, "name": "Work", "owned": true}},
	})
	defer server.Close()

	_, err := runEventCreate(t, server,
		"--calendar", "Nope",
		"--title", "T",
		"--date", "2024-06-15",
		"--all-day",
	)
	if err == nil {
		t.Fatalf("expected error for missing calendar name")
	}
	ae := apierr.AsError(err)
	combined := ae.Message + " " + ae.Hint
	if !strings.Contains(combined, "hey calendars") {
		t.Errorf("error should hint at 'hey calendars', got msg=%q hint=%q", ae.Message, ae.Hint)
	}
}

func eventListCustomServer(t *testing.T, calendarsPayload []map[string]any, recordingsByID map[int64]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/calendars.json":
			resp := map[string]any{"calendars": calendarsPayload}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/calendars/") && strings.HasSuffix(r.URL.Path, "/recordings"):
			// extract id
			seg := strings.TrimPrefix(r.URL.Path, "/calendars/")
			seg = strings.TrimSuffix(seg, "/recordings")
			var id int64
			for _, c := range seg {
				if c < '0' || c > '9' {
					id = 0
					break
				}
				id = id*10 + int64(c-'0')
			}
			resp, ok := recordingsByID[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestEventList_CalendarByName(t *testing.T) {
	server := eventListCustomServer(t,
		[]map[string]any{
			{"calendar": map[string]any{"id": 791879, "name": "Work", "owned": true}},
		},
		map[int64]map[string]any{
			791879: {
				"Calendar::Event": []map[string]any{
					{"id": 555, "title": "Work meeting", "starts_at": "2024-05-01T09:00:00Z", "ends_at": "2024-05-01T09:30:00Z"},
				},
			},
		},
	)
	defer server.Close()

	out, err := runEventList(t, server, "--styled", "--calendar", "Work")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "Work meeting") {
		t.Errorf("expected output to contain event from calendar 791879; out=%q", out)
	}
}

func TestEventCreate_DefaultCalendarFailsShowsList(t *testing.T) {
	captured := &capturedRequest{}
	server := eventCreateCustomServer(t, captured, []map[string]any{
		{"calendar": map[string]any{"id": 6037, "name": "Maybe", "owned": true}},
		{"calendar": map[string]any{"id": 791879, "name": "Work", "owned": true}},
	})
	defer server.Close()

	_, err := runEventCreate(t, server,
		"--title", "T",
		"--date", "2024-06-15",
		"--all-day",
	)
	if err == nil {
		t.Fatalf("expected error when no default calendar")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--calendar") {
		t.Errorf("error should mention --calendar, got: %v", msg)
	}
	if !strings.Contains(msg, "6037") || !strings.Contains(msg, "791879") {
		t.Errorf("error should list available calendar IDs, got: %v", msg)
	}
	if !strings.Contains(msg, "Maybe") || !strings.Contains(msg, "Work") {
		t.Errorf("error should list available calendar names, got: %v", msg)
	}
}

func TestEventListIdsOnly(t *testing.T) {
	server := eventServer(t)
	defer server.Close()

	out, err := runEventList(t, server, "--ids-only")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 ID lines, got %d: %q", len(lines), out)
	}
	if lines[0] != "101" || lines[1] != "102" {
		t.Errorf("unexpected IDs: %v", lines)
	}
}
