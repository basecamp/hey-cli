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
