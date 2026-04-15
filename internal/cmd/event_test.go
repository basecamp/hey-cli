package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// capturedHTTP records the method, path, and body of a captured request so
// tests can assert on whichever fields they care about.
type capturedHTTP struct {
	mu     sync.Mutex
	method string
	path   string
	body   string
}

func (c *capturedHTTP) set(method, path, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.method = method
	c.path = path
	c.body = body
}

func (c *capturedHTTP) getBody() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

func (c *capturedHTTP) getMethodPath() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.method, c.path
}

func defaultCalendarsPayload() []map[string]any {
	return []map[string]any{
		{
			"calendar": map[string]any{
				"id":       42,
				"name":     "Personal",
				"personal": true,
			},
		},
	}
}

func defaultEventRecordings() map[string]any {
	return map[string]any{
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
}

func runEvent(t *testing.T, server *httptest.Server, sub string, args ...string) (string, error) {
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
	fullArgs := append([]string{"event", sub, "--base-url", server.URL}, args...)
	root.SetArgs(fullArgs)

	err := root.Execute()
	return buf.String(), err
}

func TestEventListDefault(t *testing.T) {
	server := eventListCustomServer(t, defaultCalendarsPayload(), map[int64]map[string]any{
		42: defaultEventRecordings(),
	})
	defer server.Close()

	out, err := runEvent(t, server, "list", "--styled")
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
	server := eventListCustomServer(t, defaultCalendarsPayload(), map[int64]map[string]any{
		42: defaultEventRecordings(),
	})
	defer server.Close()

	out, err := runEvent(t, server, "list", "--styled", "--limit", "1")
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

func eventCreateCustomServer(t *testing.T, captured *capturedHTTP, calendarsPayload []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/calendars.json":
			resp := map[string]any{"calendars": calendarsPayload}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == "POST" && r.URL.Path == "/calendar/events":
			body, _ := io.ReadAll(r.Body)
			captured.set(r.Method, r.URL.Path, string(body))
			w.Header().Set("Location", "/calendar/events/999")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestEventCreateRequiresTitle(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventCreateCustomServer(t, captured, defaultCalendarsPayload())
	defer server.Close()

	_, err := runEvent(t, server, "create", "--date", "2024-06-15", "--all-day")
	if err == nil {
		t.Fatalf("expected error when --title missing")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "title") {
		t.Errorf("expected error to mention 'title', got: %v", err)
	}
}

func TestEventCreateRejectsTimezoneWithAllDay(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventCreateCustomServer(t, captured, defaultCalendarsPayload())
	defer server.Close()

	_, err := runEvent(t, server, "create",
		"--title", "Holiday",
		"--date", "2024-06-15",
		"--all-day",
		"--timezone", "America/New_York",
	)
	if err == nil {
		t.Fatalf("expected error when --timezone combined with --all-day")
	}
	if !strings.Contains(err.Error(), "--timezone") || !strings.Contains(err.Error(), "--all-day") {
		t.Errorf("expected error to name both flags, got: %v", err)
	}
}

func TestEventCreateTimed(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventCreateCustomServer(t, captured, defaultCalendarsPayload())
	defer server.Close()

	_, err := runEvent(t, server, "create",
		"--title", "Team sync",
		"--date", "2024-06-15",
		"--start", "09:00",
		"--end", "10:00",
		"--timezone", "America/New_York",
	)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	body := captured.getBody()
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
	captured := &capturedHTTP{}
	server := eventCreateCustomServer(t, captured, defaultCalendarsPayload())
	defer server.Close()

	_, err := runEvent(t, server, "create",
		"--title", "Holiday",
		"--date", "2024-06-15",
		"--all-day",
		"--reminder", "1d",
	)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	body := captured.getBody()
	if !strings.Contains(body, "calendar_event%5Ball_day%5D=1") {
		t.Errorf("body missing all_day=1; body=%s", body)
	}
	if !strings.Contains(body, "all_day_reminder_durations%5B%5D=86400") {
		t.Errorf("body missing reminder 86400; body=%s", body)
	}
}

func eventEditServer(t *testing.T, captured *capturedHTTP) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PATCH" && r.URL.Path == "/calendar/events/101":
			body, _ := io.ReadAll(r.Body)
			captured.set(r.Method, r.URL.Path, string(body))
			w.Header().Set("Location", "/calendar/events/101")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestEventEdit(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventEditServer(t, captured)
	defer server.Close()

	_, err := runEvent(t, server, "edit", "101", "--title", "Updated standup")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	body := captured.getBody()
	if !strings.Contains(body, "calendar_event%5Bsummary%5D=Updated+standup") {
		t.Errorf("body missing summary fragment; body=%s", body)
	}
}

func TestEventEditInvalidID(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventEditServer(t, captured)
	defer server.Close()

	_, err := runEvent(t, server, "edit", "notanumber", "--title", "x")
	if err == nil {
		t.Fatalf("expected error for invalid event ID")
	}
	if !strings.Contains(err.Error(), "invalid event ID") {
		t.Errorf("expected 'invalid event ID' in error, got: %v", err)
	}
}

func TestEventEditOnlyChangedFields(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventEditServer(t, captured)
	defer server.Close()

	_, err := runEvent(t, server, "edit", "101", "--title", "X")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	body := captured.getBody()
	forbidden := []string{"starts_at", "ends_at", "all_day", "starts_at_time", "ends_at_time"}
	for _, f := range forbidden {
		if strings.Contains(body, f) {
			t.Errorf("body should not contain %q when not changed; body=%s", f, body)
		}
	}
}

func TestEventEditRejectsTimezoneWithAllDay(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventEditServer(t, captured)
	defer server.Close()

	_, err := runEvent(t, server, "edit", "101", "--all-day", "--timezone", "America/New_York")
	if err == nil {
		t.Fatalf("expected error when --timezone combined with --all-day")
	}
	if !strings.Contains(err.Error(), "--timezone") || !strings.Contains(err.Error(), "--all-day") {
		t.Errorf("expected error to name both flags, got: %v", err)
	}
}

func eventDeleteServer(t *testing.T, captured *capturedHTTP) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE" && r.URL.Path == "/calendar/events/101":
			captured.set(r.Method, r.URL.Path, "")
			w.Header().Set("Location", "/calendar")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestEventDelete(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventDeleteServer(t, captured)
	defer server.Close()

	_, err := runEvent(t, server, "delete", "101")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	method, path := captured.getMethodPath()
	if method != "DELETE" {
		t.Errorf("expected DELETE method, got %q", method)
	}
	if path != "/calendar/events/101" {
		t.Errorf("expected path /calendar/events/101, got %q", path)
	}
}

func TestEventCreate_CalendarByName(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventCreateCustomServer(t, captured, []map[string]any{
		{"calendar": map[string]any{"id": 42, "name": "Personal", "personal": true, "owned": true}},
		{"calendar": map[string]any{"id": 791879, "name": "Work", "owned": true}},
	})
	defer server.Close()

	_, err := runEvent(t, server, "create",
		"--calendar", "Work",
		"--title", "T",
		"--date", "2024-06-15",
		"--all-day",
	)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := captured.getBody()
	if !strings.Contains(body, "calendar_event%5Bcalendar_id%5D=791879") {
		t.Errorf("body missing calendar_id=791879; body=%s", body)
	}
}

func TestEventCreate_CalendarByNameAmbiguous(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventCreateCustomServer(t, captured, []map[string]any{
		{"calendar": map[string]any{"id": 100, "name": "Personal", "owned": true}},
		{"calendar": map[string]any{"id": 200, "name": "Personal", "owned": true}},
	})
	defer server.Close()

	_, err := runEvent(t, server, "create",
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
	captured := &capturedHTTP{}
	server := eventCreateCustomServer(t, captured, []map[string]any{
		{"calendar": map[string]any{"id": 42, "name": "Personal", "personal": true, "owned": true}},
		{"calendar": map[string]any{"id": 99, "name": "Work", "owned": true}},
	})
	defer server.Close()

	_, err := runEvent(t, server, "create",
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
			seg := strings.TrimPrefix(r.URL.Path, "/calendars/")
			seg = strings.TrimSuffix(seg, "/recordings")
			id, err := strconv.ParseInt(seg, 10, 64)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				return
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

	out, err := runEvent(t, server, "list", "--styled", "--calendar", "Work")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "Work meeting") {
		t.Errorf("expected output to contain event from calendar 791879; out=%q", out)
	}
}

func TestEventCreate_DefaultCalendarFailsShowsList(t *testing.T) {
	captured := &capturedHTTP{}
	server := eventCreateCustomServer(t, captured, []map[string]any{
		{"calendar": map[string]any{"id": 6037, "name": "Maybe", "owned": true}},
		{"calendar": map[string]any{"id": 791879, "name": "Work", "owned": true}},
	})
	defer server.Close()

	_, err := runEvent(t, server, "create",
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
	server := eventListCustomServer(t, defaultCalendarsPayload(), map[int64]map[string]any{
		42: defaultEventRecordings(),
	})
	defer server.Close()

	out, err := runEvent(t, server, "list", "--ids-only")
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
