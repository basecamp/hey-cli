package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

func runPostingAction(t *testing.T, server *httptest.Server, command string, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{command, "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestPostingActions(t *testing.T) {
	tests := []struct {
		command string
		path    string
		summary string
	}{
		{command: "paper-trail", path: "/postings/12345/move/trailbox.json", summary: "Posting 12345 moved to Paper Trail"},
		{command: "feed", path: "/postings/12345/move/feedbox.json", summary: "Posting 12345 moved to The Feed"},
		{command: "set-aside", path: "/postings/12345/move/asidebox.json", summary: "Posting 12345 moved to Set Aside"},
		{command: "reply-later", path: "/postings/12345/move/laterbox.json", summary: "Posting 12345 moved to Reply Later"},
		{command: "ignore", path: "/postings/12345/ignore.json", summary: "Posting 12345 ignored"},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tt.path {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			resp, err := runPostingAction(t, server, tt.command, "12345")
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if resp.Summary != tt.summary {
				t.Errorf("summary = %q, want %q", resp.Summary, tt.summary)
			}
		})
	}
}

func TestPostingActionRejectsMultipleIDs(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	_, err := runPostingAction(t, server, "feed", "12345", "67890")
	if err == nil {
		t.Fatal("expected multiple ID usage error")
	}
	if requests != 0 {
		t.Errorf("requests = %d, want 0", requests)
	}
}

func TestTrash(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if r.Method != http.MethodPost || r.URL.Path != "/postings/12345/trash.json" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	resp, err := runPostingAction(t, server, "trash", "12345")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "Posting 12345 moved to Trash" {
		t.Errorf("summary = %q", resp.Summary)
	}
	if path != "/postings/12345/trash.json" {
		t.Errorf("path = %q, want %q", path, "/postings/12345/trash.json")
	}
}

func TestPostingActionReturnsSDKError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	_, err := runPostingAction(t, server, "trash", "12345")
	if err == nil {
		t.Fatal("expected SDK error")
	}
	if !strings.Contains(err.Error(), "resource not found") {
		t.Errorf("error = %q, want resource not found", err)
	}
}

func TestPostingActionRejectsInvalidID(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, err := runPostingAction(t, server, "trash", "invalid")
	if err == nil {
		t.Fatal("expected invalid ID error")
	}
	if !strings.Contains(err.Error(), "invalid posting ID") {
		t.Errorf("error = %q, want invalid posting ID", err)
	}
}
