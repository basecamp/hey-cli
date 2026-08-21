package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

func journalServer(t *testing.T) *httptest.Server {
	t.Helper()
	return journalServerWithReadBehavior(t, "200")
}

// journalServerWithReadBehavior creates a journal test server.
// readBehavior controls GET /calendar/days/{date}/journal_entry:
//
//	"200"               — returns a Recording with content
//	"204"               — returns 204 No Content (SDK returns nil), no legacy fallback
func journalServerWithReadBehavior(t *testing.T, readBehavior string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/calendar/days/") && strings.HasSuffix(r.URL.Path, "/journal_entry/edit"):
			// Legacy HTML-scrape path
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body></body></html>`)
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/calendar/days/") && (strings.HasSuffix(r.URL.Path, "/journal_entry") || strings.HasSuffix(r.URL.Path, "/journal_entry.json")):
			path := r.URL.Path
			path = strings.TrimPrefix(path, "/calendar/days/")
			date := strings.TrimSuffix(strings.TrimSuffix(path, ".json"), "/journal_entry")
			switch readBehavior {
			case "204":
				w.WriteHeader(204)
			default:
				resp := map[string]any{
					"id":        1,
					"content":   "<div>Entry for " + date + "</div>",
					"type":      "Calendar::JournalEntry",
					"title":     "Journal Entry",
					"starts_at": "2024-01-15T00:00:00Z",
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			}
		case r.Method == "PATCH" && strings.Contains(r.URL.Path, "/calendar/days/") && (strings.HasSuffix(r.URL.Path, "/journal_entry") || strings.HasSuffix(r.URL.Path, "/journal_entry.json")):
			w.WriteHeader(204)
		default:
			w.WriteHeader(200)
		}
	}))
}

// journalServerRecordingEditFetches answers 204 for the journal entry and notes whether
// anything asked for the legacy edit page.
func journalServerRecordingEditFetches(t *testing.T, editFetched *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/journal_entry/edit"):
			editFetched.Store(true)
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><input id="journal_trix_input" value="&lt;div&gt;Fallback content&lt;/div&gt;"></body></html>`)
		case strings.Contains(r.URL.Path, "/journal_entry"):
			w.WriteHeader(204)
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

	if !strings.Contains(resp.Summary, "Journal entry") {
		t.Errorf("summary = %q, want to contain %q", resp.Summary, "Journal entry")
	}
}

func TestJournalWritePositionalDateAndContent(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	resp, err := runJournalWrite(t, server, "2024-01-15", "Retrospective")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(resp.Summary, "2024-01-15") {
		t.Errorf("summary = %q, want to contain date", resp.Summary)
	}
}

func TestJournalWriteShortFlag(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	resp, err := runJournalWrite(t, server, "-c", "Content via short flag")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(resp.Summary, "Journal entry") {
		t.Errorf("summary = %q, want to contain %q", resp.Summary, "Journal entry")
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

// A failed pre-fill read used to hand $EDITOR an empty file, and saving that replaced
// the day's entry. An empty day is a 204 and reads as "" with no error, so an error
// means we do not know what the day holds and must not offer to overwrite it.
func TestJournalEntryFromEditorRefusesAFailedPrefillRead(t *testing.T) {
	opened := false
	_, err := journalEntryFromEditor(t.Context(), "2026-01-31",
		func(context.Context, string) (string, error) {
			return "", errors.New("500 Internal Server Error")
		},
		func(string) (string, error) {
			opened = true
			return "", nil
		})

	if err == nil {
		t.Fatal("a failed pre-fill read must not open the editor at all")
	}
	if opened {
		t.Error("the editor was opened over an unknown entry")
	}
}

func TestJournalEntryFromEditorPrefillsTheDaysEntry(t *testing.T) {
	prefilled := ""
	content, err := journalEntryFromEditor(t.Context(), "2026-01-31",
		func(context.Context, string) (string, error) {
			return "<div>Ran six miles</div>", nil
		},
		func(existing string) (string, error) {
			prefilled = existing
			return existing + " and swam", nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prefilled != "<div>Ran six miles</div>" {
		t.Errorf("editor was pre-filled with %q", prefilled)
	}
	if content != "<div>Ran six miles</div> and swam" {
		t.Errorf("content = %q", content)
	}
}

// Empty content removes the day's entry, so saying "saved" was a lie about a deletion.
func TestJournalWriteReportsAnEmptyEntryAsRemoved(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	resp, err := runJournalWrite(t, server, "2026-01-31", "   ")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(resp.Summary, "removed") {
		t.Errorf("summary = %q, want it to say the entry was removed", resp.Summary)
	}
}

// --- Journal read tests ---

func runJournalRead(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{"journal", "read", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestJournalReadRejectsAnUnreadableDate(t *testing.T) {
	server := journalServer(t)
	defer server.Close()

	_, err := runJournalRead(t, server, "last tuesday")
	if err == nil {
		t.Fatal("expected error for an unreadable date")
	}
	if !strings.Contains(err.Error(), "invalid date: last tuesday") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestJournalReadReturns200WithContent(t *testing.T) {
	server := journalServerWithReadBehavior(t, "200")
	defer server.Close()

	resp, err := runJournalRead(t, server, "2024-01-15")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	content, _ := data["content"].(string)
	if !strings.Contains(content, "Entry for 2024-01-15") {
		t.Errorf("content = %q, want to contain %q", content, "Entry for 2024-01-15")
	}
}

// A 204 used to mean "ask the edit page instead": HEY answered 204 for a day that had an
// entry, so the SDK scraped the Trix input for its content. HEY answers the entry itself
// now, so a 204 means what it says -- there is no entry that day -- and the edit page is
// never fetched.
func TestJournalReadReturns204MeansNoEntry(t *testing.T) {
	var editFetched atomic.Bool
	server := journalServerRecordingEditFetches(t, &editFetched)
	defer server.Close()

	resp, err := runJournalRead(t, server, "2024-01-15")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if resp.Data != nil {
		t.Errorf("data = %v, want nil for a day with no entry", resp.Data)
	}
	if !strings.Contains(resp.Summary, "No journal entry") {
		t.Errorf("summary = %q, want it to say there is no entry", resp.Summary)
	}
	if editFetched.Load() {
		t.Error("the edit page should not be fetched any more")
	}
}

func TestJournalReadReturns204NoFallbackContent(t *testing.T) {
	server := journalServerWithReadBehavior(t, "204")
	defer server.Close()

	resp, err := runJournalRead(t, server, "2024-01-15")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// 204 from SDK and legacy /edit returns empty page — no content available.
	if !strings.Contains(resp.Summary, "No journal entry") {
		t.Errorf("summary = %q, want to contain %q", resp.Summary, "No journal entry")
	}
}
