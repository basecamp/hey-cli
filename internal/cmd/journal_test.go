package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

func journalServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PATCH" && strings.Contains(r.URL.Path, "/journal_entry.json"):
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			resp := map[string]any{
				"id":   1,
				"body": req["body"],
				"date": strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/calendar/days/"), "/journal_entry.json"),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/journal_entry/edit"):
			// Return a minimal HTML page with an empty trix input
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body><input id="journal_trix_input" value=""></body></html>`))
		default:
			w.WriteHeader(200)
		}
	}))
}

func runJournalWrite(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{"journal", "write", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestJournalWritePositionalContent(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	resp, err := runJournalWrite(t, server, "Today was great")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	if got := data["body"]; got != "Today was great" {
		t.Errorf("body = %q, want %q", got, "Today was great")
	}
}

func TestJournalWritePositionalDateAndContent(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	resp, err := runJournalWrite(t, server, "2024-01-15", "Retrospective")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	if got := data["body"]; got != "Retrospective" {
		t.Errorf("body = %q, want %q", got, "Retrospective")
	}
	if got := data["date"]; got != "2024-01-15" {
		t.Errorf("date = %q, want %q", got, "2024-01-15")
	}
}

func TestJournalWriteShortFlag(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	resp, err := runJournalWrite(t, server, "-c", "Content via short flag")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	if got := data["body"]; got != "Content via short flag" {
		t.Errorf("body = %q, want %q", got, "Content via short flag")
	}
}

func TestJournalWriteConflictFlagAndPositional(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	_, err := runJournalWrite(t, server, "--content", "X", "Y")
	if err == nil {
		t.Fatal("expected error for conflicting flag and positional")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "mutually exclusive")
	}
}

func TestJournalWriteTwoPositionalsInvalidDate(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	_, err := runJournalWrite(t, server, "not-a-date", "Content")
	if err == nil {
		t.Fatal("expected error for invalid date in 2-arg form")
	}
	if !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("error = %q, want to mention YYYY-MM-DD", err.Error())
	}
}

func TestJournalWriteConflictFlagAndTwoPositionals(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	_, err := runJournalWrite(t, server, "--content", "X", "2024-01-15", "Y")
	if err == nil {
		t.Fatal("expected error for conflicting flag and positional")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "mutually exclusive")
	}
}
