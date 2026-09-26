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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
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
		case r.Method == "GET" && r.URL.Path == "/identity.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, newYorkIdentity)
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

func TestJournalWriteMarkdownContent(t *testing.T) {
	var sent struct {
		CalendarJournalEntry struct {
			Content string `json:"content"`
		} `json:"calendar_journal_entry"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	_, err := runJournalWrite(t, server, "2026-03-15", "**Bold** start to the week")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	want := "<p><strong>Bold</strong> start to the week</p>"
	if sent.CalendarJournalEntry.Content != want {
		t.Errorf("content = %q, want %q", sent.CalendarJournalEntry.Content, want)
	}

	_, err = runJournalWrite(t, server, "2026-03-15", "--content-html", "<div>Raw <strong>HTML</strong> entry</div>")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := "<div>Raw <strong>HTML</strong> entry</div>"; sent.CalendarJournalEntry.Content != want {
		t.Errorf("content = %q, want %q", sent.CalendarJournalEntry.Content, want)
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

func TestJournalEntryFromEditorPrefillsTheDaysEntryAsMarkdown(t *testing.T) {
	prefilled := ""
	content, err := journalEntryFromEditor(t.Context(), "2026-01-31",
		func(context.Context, string) (string, error) {
			return "<div>Ran <strong>six</strong> miles</div>", nil
		},
		func(existing string) (string, error) {
			prefilled = existing
			return existing + " and swam", nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prefilled != "Ran **six** miles" {
		t.Errorf("editor was pre-filled with %q", prefilled)
	}
	if content != "Ran **six** miles and swam" {
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

// An entry as HEY's web editor saves it; journalStore answers it the way
// _journal_entry.jbuilder does, with content_html inside Action Text's layout.
const webEditedJournalStored = "<div><strong>Shipped</strong> the pagination fix<br><br></div>\n<ul>\n<li>Paired with Jane on the cover art</li>\n</ul>"

// journalStore stands in for HEY's journal: it keeps what was written and serves it back
// wrapped for the editor, as every read of it does.
type journalStore struct {
	mu     sync.Mutex
	stored string
	writes []string
}

func newJournalStore(t *testing.T, stored string) (*httptest.Server, *journalStore) {
	t.Helper()
	store := &journalStore{stored: stored}
	server := httptest.NewServer(http.HandlerFunc(store.serve))
	t.Cleanup(server.Close)
	return server, store
}

func (s *journalStore) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.URL.Path != "/calendar/days/2026-03-15/journal_entry.json" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPatch:
		var body struct {
			CalendarJournalEntry struct {
				Content string `json:"content"`
			} `json:"calendar_journal_entry"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.writes = append(s.writes, body.CalendarJournalEntry.Content)
		s.stored = body.CalendarJournalEntry.Content
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.stored == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":           1,
		"type":         "Calendar::JournalEntry",
		"starts_at":    "2026-03-15T00:00:00Z",
		"content":      htmlutil.ToText(s.stored),
		"content_html": "<div class=\"trix-content\">\n  " + s.stored + "\n</div>\n",
	})
}

func (s *journalStore) snapshot() (stored string, writes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stored, append([]string(nil), s.writes...)
}

func readJournalEntry(t *testing.T, server *httptest.Server) map[string]any {
	t.Helper()
	resp, err := runJournalRead(t, server, "2026-03-15")
	if err != nil {
		t.Fatalf("journal read: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want the entry", resp.Data)
	}
	return data
}

// content is served inside HEY's editor wrapper, and writing it back used to store the
// wrapper too, so the entry sank one div deeper on every round trip.
func TestJournalHTMLRoundTripDoesNotNest(t *testing.T) {
	server, store := newJournalStore(t, webEditedJournalStored)
	for range 3 {
		content, _ := readJournalEntry(t, server)["content"].(string)
		if _, err := runJournalWrite(t, server, "2026-03-15", "--content-html", content+"<div>Reviewed the Q3 numbers with Alice</div>"); err != nil {
			t.Fatal(err)
		}
	}
	stored, writes := store.snapshot()
	for _, written := range writes {
		if strings.Contains(written, "trix-content") {
			t.Errorf("wrote %q, want HEY's wrapper taken off", written)
		}
	}
	if strings.Count(stored, "Reviewed the Q3 numbers") != 3 || !strings.Contains(stored, "<strong>Shipped</strong>") {
		t.Errorf("stored = %q, want the entry and every addition", stored)
	}
	content, _ := readJournalEntry(t, server)["content"].(string)
	if strings.Count(content, "trix-content") != 1 {
		t.Errorf("content = %q, want one wrapper", content)
	}
}

// Divs of the writer's own are theirs: one named like the wrapper, one inside another,
// and even the wrapper's class where it does not stand at the top level.
func TestJournalWriteKeepsTheWritersOwnDivs(t *testing.T) {
	server, store := newJournalStore(t, "")
	own := `<div class="trix-content-note"><div>Retrospective: the migration took two days longer than planned.</div></div>` +
		`<blockquote><div class="trix-content">Quoted from the offsite notes</div></blockquote>`
	if _, err := runJournalWrite(t, server, "2026-03-15", "--content-html", own); err != nil {
		t.Fatal(err)
	}
	if stored, _ := store.snapshot(); stored != own {
		t.Errorf("stored = %q, want %q", stored, own)
	}
}

func TestJournalReadAnswersTheEntryAsMarkdown(t *testing.T) {
	server, _ := newJournalStore(t, webEditedJournalStored)
	entry := readJournalEntry(t, server)
	if want := "<div class=\"trix-content\">\n  " + webEditedJournalStored + "\n</div>\n"; entry["content"] != want {
		t.Errorf("content = %q, want it as HEY served it", entry["content"])
	}
	if want := "**Shipped** the pagination fix\n\n- Paired with Jane on the cover art"; entry["content_markdown"] != want {
		t.Errorf("content_markdown = %q, want %q", entry["content_markdown"], want)
	}
}

// content_markdown is what journal write takes, so it goes round without loss.
func TestJournalMarkdownWritesBackWithoutLoss(t *testing.T) {
	server, store := newJournalStore(t, webEditedJournalStored)
	first, _ := readJournalEntry(t, server)["content_markdown"].(string)
	if _, err := runJournalWrite(t, server, "2026-03-15", first); err != nil {
		t.Fatal(err)
	}
	settled, _ := readJournalEntry(t, server)["content_markdown"].(string)
	for range 2 {
		if _, err := runJournalWrite(t, server, "2026-03-15", settled); err != nil {
			t.Fatal(err)
		}
		if again, _ := readJournalEntry(t, server)["content_markdown"].(string); again != settled {
			t.Fatalf("content_markdown = %q, want it unchanged at %q", again, settled)
		}
	}
	if _, writes := store.snapshot(); writes[1] != writes[0] || writes[2] != writes[0] {
		t.Errorf("writes = %q, want the same HTML each time", writes)
	}
}

// A journal entry can hold what Markdown cannot carry — HEY's web editor attaches files
// and images to one — so content_markdown_lossless says so, and a figure survives being
// changed as HTML.
const attachedJournalStored = `<div>Offsite agenda, signed off:</div><figure data-trix-attachment='{"contentType":"application/pdf","filename":"offsite-agenda.pdf","url":"/rails/active_storage/blobs/redirect/eyJfcmFpbHMiOnt9fQ--9c2d/offsite-agenda.pdf"}'><figcaption>offsite-agenda.pdf</figcaption></figure>`

func TestJournalReadSaysWhenItsMarkdownIsLossless(t *testing.T) {
	for _, tt := range []struct {
		name   string
		stored string
		want   bool
	}{
		{name: "an entry from HEY's editor", stored: webEditedJournalStored, want: true},
		{name: "an entry with an attachment", stored: attachedJournalStored, want: false},
		{name: "an entry with a colour", stored: `<div style="color: red">Retrospective: the migration took two days longer than planned.</div>`, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server, _ := newJournalStore(t, tt.stored)
			if lossless, present := readJournalEntry(t, server)["content_markdown_lossless"]; !present || lossless != tt.want {
				t.Errorf("content_markdown_lossless = %#v (present %v), want %v", lossless, present, tt.want)
			}
		})
	}
}

func TestJournalEntryWithAnAttachmentKeepsItWhenChangedAsHTML(t *testing.T) {
	server, store := newJournalStore(t, attachedJournalStored)
	content, _ := readJournalEntry(t, server)["content"].(string)
	if _, err := runJournalWrite(t, server, "2026-03-15", "--content-html", content+"<div>Booked the venue for the second day.</div>"); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.snapshot()
	attachments := htmlutil.ExtractAttachments(stored)
	if len(attachments) != 1 || attachments[0].Filename != "offsite-agenda.pdf" || attachments[0].URL != "/rails/active_storage/blobs/redirect/eyJfcmFpbHMiOnt9fQ--9c2d/offsite-agenda.pdf" ||
		!strings.Contains(stored, "Booked the venue for the second day.") {
		t.Errorf("stored = %q, want the attachment and the addition", stored)
	}
	if strings.Contains(stored, "trix-content") {
		t.Errorf("stored = %q, want HEY's wrapper taken off", stored)
	}
}

// Saving the Markdown of an entry that holds an attachment would drop the attachment, so
// the editor is not opened on one, and nothing is written.
func TestJournalEntryFromEditorRefusesAnEntryMarkdownCannotCarry(t *testing.T) {
	opened := false
	_, err := journalEntryFromEditor(t.Context(), "2026-03-15",
		func(context.Context, string) (string, error) {
			return "<div class=\"trix-content\">\n  " + attachedJournalStored + "\n</div>\n", nil
		},
		func(string) (string, error) {
			opened = true
			return "", nil
		})

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Hint, "--content-html") {
		t.Fatalf("error = %#v, want a usage error pointing at --content-html", err)
	}
	if opened {
		t.Error("the editor was opened on an entry its Markdown cannot carry")
	}
}

func TestJournalWriteAtATerminalRefusesAnEntryMarkdownCannotCarry(t *testing.T) {
	previous := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = previous })
	// An editor that saves what it was handed, so a missing guard writes rather than hangs.
	t.Setenv("EDITOR", "true")

	server, store := newJournalStore(t, attachedJournalStored)
	_, err := runJournalWrite(t, server, "2026-03-15")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage {
		t.Fatalf("error = %#v, want a usage error", err)
	}
	if stored, writes := store.snapshot(); len(writes) != 0 || stored != attachedJournalStored {
		t.Errorf("writes = %q, stored = %q, want the entry untouched", writes, stored)
	}
}

func TestJournalEntryFromEditorOpensAnEmptyDay(t *testing.T) {
	prefilled := "unset"
	content, err := journalEntryFromEditor(t.Context(), "2026-03-15",
		func(context.Context, string) (string, error) { return "", nil },
		func(existing string) (string, error) {
			prefilled = existing
			return "Booked the venue for the offsite.", nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prefilled != "" || content != "Booked the venue for the offsite." {
		t.Errorf("prefilled = %q, content = %q, want an empty editor and what was typed", prefilled, content)
	}
}
