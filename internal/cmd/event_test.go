package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
