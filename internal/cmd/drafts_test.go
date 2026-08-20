package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runStyledCommand(t *testing.T, handler http.Handler, args ...string) (string, error) {
	t.Helper()
	return runFormattedCommand(t, handler, []string{"--styled"}, args...)
}

func runFormattedCommand(t *testing.T, handler http.Handler, formatArgs []string, args ...string) (string, error) {
	t.Helper()
	previousColorDisabled := colorDisabled
	colorDisabled = false
	t.Cleanup(func() { colorDisabled = previousColorDisabled })

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
	rootArgs := append([]string{}, formatArgs...)
	rootArgs = append(rootArgs, "--base-url", server.URL)
	root.SetArgs(append(rootArgs, args...))

	err := root.Execute()
	return stdout.String(), err
}

func TestDraftsCommandLimitsResults(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/entries/drafts.json" {
			t.Errorf("request = %s %s, want GET /entries/drafts.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"id":101,"summary":"Agenda and decisions","subject":"Quarterly planning follow-up","updated_at":"2026-08-20T09:30:00Z"},
			{"id":102,"summary":"Travel details","subject":"Team retreat itinerary","updated_at":"2026-08-19T14:00:00Z"}
		]`)
	}), "drafts", "--limit", "1")
	if err != nil {
		t.Fatalf("execute drafts: %v", err)
	}
	if response.Summary != "1 drafts" {
		t.Errorf("summary = %q, want 1 drafts", response.Summary)
	}
	if response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
	items, ok := response.Data.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("data = %#v, want one draft", response.Data)
	}
	draft, ok := items[0].(map[string]any)
	if !ok || draft["id"] != float64(101) {
		t.Errorf("draft = %#v, want ID 101", items[0])
	}
}

func TestDraftsCommandAllOverridesLimit(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"id":101,"subject":"Quarterly planning follow-up"},
			{"id":102,"subject":"Team retreat itinerary"}
		]`)
	}), "drafts", "--limit", "1", "--all")
	if err != nil {
		t.Fatalf("execute drafts: %v", err)
	}
	items, ok := response.Data.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("data = %#v, want two drafts", response.Data)
	}
	if response.Summary != "2 drafts" || response.Notice != "" {
		t.Errorf("response = %#v, want complete two-draft result", response)
	}
}

func TestDraftsCommandStyledTable(t *testing.T) {
	const fullSummary = "Notes from the quarterly planning meeting including decisions and follow-up assignments"
	stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"id":101,"summary":"`+fullSummary+`","subject":"Quarterly planning follow-up","updated_at":"2026-08-20T09:30:00Z"},
			{"id":102,"summary":"Travel details","subject":"Team retreat itinerary","updated_at":"2026-08-19T14:00:00Z"}
		]`)
	}), "drafts", "--limit", "1")
	if err != nil {
		t.Fatalf("execute styled drafts: %v", err)
	}
	for _, want := range []string{"ID", "Summary", "Subject", "Date", "101", "Quarterly planning follow-up", "2026-08-20", "...", "Showing 1 of 2 results"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output %q does not contain %q", stdout, want)
		}
	}
	if strings.Contains(stdout, fullSummary) {
		t.Errorf("output contains untruncated summary: %q", stdout)
	}
}

func TestDraftsCommandStyledEmpty(t *testing.T) {
	stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "drafts")
	if err != nil {
		t.Fatalf("execute styled drafts: %v", err)
	}
	if stdout != "No drafts.\n" {
		t.Errorf("output = %q, want empty-state message", stdout)
	}
}

func TestDraftsCommandAPIError(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "drafts unavailable", http.StatusBadRequest)
	}), "drafts")
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("error = %v, want HTTP failure", err)
	}
}
