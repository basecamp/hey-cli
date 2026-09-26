package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
)

// A note as HEY's web editor saves it, and as HEY answers it: to_plain_text drops the
// bold and bullets the list, and note_html is the stored HTML inside Action Text's
// layout.
const (
	webEditedNoteStored = "<div><strong>Anniversary:</strong> June 12<br><br></div>\n<ul>\n<li>Prefers texts after six</li>\n</ul>"
	webEditedNotePlain  = "Anniversary: June 12\n\n• Prefers texts after six"
	webEditedNoteHTML   = "<div class=\"trix-content\">\n  " + webEditedNoteStored + "\n</div>\n"
)

// noteServer stands in for HEY's contact note: it keeps what was written and answers it
// the way contacts/notes/show.jbuilder does, wrapped for the editor.
type noteServer struct {
	mu     sync.Mutex
	stored string
	plain  string
	writes []string
	status int
}

func newNoteServer(t *testing.T, stored, plain string) (*httptest.Server, *noteServer) {
	t.Helper()
	notes := &noteServer{stored: stored, plain: plain}
	server := httptest.NewServer(http.HandlerFunc(notes.serve))
	t.Cleanup(server.Close)
	return server, notes
}

func (s *noteServer) serve(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if s.status != 0 {
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(`{"error":"Something went wrong"}`))
		return
	}
	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/contacts/7.json":
		_, _ = w.Write([]byte(`{"id":7,"account_id":1,"name":"Jane Doe","email_address":"jane@example.com"}`))
	case req.Method == http.MethodGet && req.URL.Path == "/contacts/7/note.json":
		s.answer(w)
	case req.Method == http.MethodPatch && req.URL.Path == "/contacts/7/note.json":
		var body generated.ContactNoteRequestContent
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.writes = append(s.writes, body.Contact.Note)
		s.stored = body.Contact.Note
		s.plain = htmlutil.ToText(body.Contact.Note)
		s.answer(w)
	default:
		http.NotFound(w, req)
	}
}

func (s *noteServer) answer(w http.ResponseWriter) {
	note := generated.ContactNote{ContactId: 7}
	if s.stored != "" {
		note.Note = s.plain
		note.NoteHtml = "<div class=\"trix-content\">\n  " + s.stored + "\n</div>\n"
	}
	_ = json.NewEncoder(w).Encode(note)
}

func (s *noteServer) snapshot() (stored string, writes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stored, append([]string(nil), s.writes...)
}

type contactNoteJSON struct {
	ContactID    int64  `json:"contact_id"`
	Note         string `json:"note"`
	NoteHTML     string `json:"note_html"`
	NoteMarkdown string `json:"note_markdown"`
}

func showContactNote(t *testing.T, server *httptest.Server) contactNoteJSON {
	t.Helper()
	resp, err := runContacts(t, server, "note", "show", "7")
	if err != nil {
		t.Fatalf("note show: %v", err)
	}
	return decodeContactData[contactNoteJSON](t, resp.Data)
}

func TestContactNoteShowAnswersTheNoteAsMarkdown(t *testing.T) {
	server, _ := newNoteServer(t, webEditedNoteStored, webEditedNotePlain)
	note := showContactNote(t, server)
	if note.Note != webEditedNotePlain || note.NoteHTML != webEditedNoteHTML {
		t.Errorf("note = %q, note_html = %q, want both as HEY served them", note.Note, note.NoteHTML)
	}
	if want := "**Anniversary:** June 12  \n\n- Prefers texts after six"; note.NoteMarkdown != want {
		t.Errorf("note_markdown = %q, want %q", note.NoteMarkdown, want)
	}
}

func TestContactNoteShowAnswersAnEmptyNoteAsEmptyMarkdown(t *testing.T) {
	server, _ := newNoteServer(t, "", "")
	resp, err := runContacts(t, server, "note", "show", "7")
	if err != nil {
		t.Fatal(err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", resp.Data)
	}
	if markdown, present := data["note_markdown"]; !present || markdown != "" {
		t.Errorf("note_markdown = %#v (present %v), want an empty string", markdown, present)
	}
}

func TestContactsShowAnswersTheNoteAsMarkdown(t *testing.T) {
	server, _ := newNoteServer(t, webEditedNoteStored, webEditedNotePlain)
	resp, err := runContacts(t, server, "show", "7")
	if err != nil {
		t.Fatal(err)
	}
	contact := decodeContactData[contactNoteJSON](t, resp.Data)
	if contact.Note != webEditedNotePlain || contact.NoteHTML != webEditedNoteHTML {
		t.Errorf("note = %q, note_html = %q, want both as HEY served them", contact.Note, contact.NoteHTML)
	}
	if want := "**Anniversary:** June 12  \n\n- Prefers texts after six"; contact.NoteMarkdown != want {
		t.Errorf("note_markdown = %q, want %q", contact.NoteMarkdown, want)
	}
}

func TestContactNoteSetAnswersTheSavedNoteAsMarkdown(t *testing.T) {
	server, _ := newNoteServer(t, "", "")
	resp, err := runContacts(t, server, "note", "set", "7", "--note=**Prefers email**\n\n- Call after six")
	if err != nil {
		t.Fatal(err)
	}
	if note := decodeContactData[contactNoteJSON](t, resp.Data); note.NoteMarkdown != "**Prefers email**\n\n- Call after six" {
		t.Errorf("note_markdown = %q", note.NoteMarkdown)
	}
}

// Reading note_markdown, adding to it and setting it is the way to add to a note: what
// is written is what was read, so nothing is lost however many times it goes round.
func TestContactNoteMarkdownWritesBackWithoutLoss(t *testing.T) {
	server, notes := newNoteServer(t, webEditedNoteStored, webEditedNotePlain)

	first := showContactNote(t, server).NoteMarkdown
	if _, err := runContacts(t, server, "note", "set", "7", "--note="+first); err != nil {
		t.Fatal(err)
	}
	settled := showContactNote(t, server).NoteMarkdown
	if want := "**Anniversary:** June 12\n\n- Prefers texts after six"; settled != want {
		t.Fatalf("note_markdown after a write = %q, want %q", settled, want)
	}
	for range 2 {
		if _, err := runContacts(t, server, "note", "set", "7", "--note="+settled); err != nil {
			t.Fatal(err)
		}
		if again := showContactNote(t, server).NoteMarkdown; again != settled {
			t.Fatalf("note_markdown = %q, want it unchanged at %q", again, settled)
		}
	}
	stored, writes := notes.snapshot()
	if writes[1] != writes[0] || writes[2] != writes[0] {
		t.Errorf("writes = %q, want the same HTML each time", writes)
	}
	if want := "<p><strong>Anniversary:</strong> June 12</p>\n<ul>\n<li>Prefers texts after six</li>\n</ul>"; stored != want {
		t.Errorf("stored = %q, want %q", stored, want)
	}

	added := settled + "\n- Moved to the Lisbon office in March"
	if _, err := runContacts(t, server, "note", "set", "7", "--note="+added); err != nil {
		t.Fatal(err)
	}
	if got := showContactNote(t, server).NoteMarkdown; got != added {
		t.Errorf("note_markdown after adding = %q, want %q", got, added)
	}
}

// note_html carries HEY's editor wrapper, and writing it back used to store the wrapper
// too, so the note sank one div deeper on every append.
func TestContactNoteHTMLRoundTripDoesNotNest(t *testing.T) {
	server, notes := newNoteServer(t, webEditedNoteStored, webEditedNotePlain)
	for range 3 {
		noteHTML := showContactNote(t, server).NoteHTML
		if _, err := runContacts(t, server, "note", "set", "7", "--note-html", noteHTML+"<div>Call after six</div>"); err != nil {
			t.Fatal(err)
		}
	}
	_, writes := notes.snapshot()
	for _, written := range writes {
		if strings.Contains(written, "trix-content") {
			t.Errorf("wrote %q, want HEY's wrapper taken off", written)
		}
	}
	served := showContactNote(t, server)
	if strings.Count(served.NoteHTML, "trix-content") != 1 {
		t.Errorf("note_html = %q, want one wrapper", served.NoteHTML)
	}
	if strings.Count(served.NoteMarkdown, "Call after six") != 3 || !strings.Contains(served.NoteMarkdown, "**Anniversary:**") {
		t.Errorf("note_markdown = %q, want the note and every addition", served.NoteMarkdown)
	}
}

func TestContactNoteReadFailuresAreReported(t *testing.T) {
	for _, tt := range []struct {
		status int
		code   string
	}{
		{status: http.StatusNotFound, code: apierr.CodeNotFound},
		{status: http.StatusInternalServerError, code: apierr.CodeAPI},
	} {
		t.Run(tt.code, func(t *testing.T) {
			server, notes := newNoteServer(t, webEditedNoteStored, webEditedNotePlain)
			notes.status = tt.status
			for _, args := range [][]string{{"note", "show", "7"}, {"show", "7"}} {
				resp, err := runContacts(t, server, args...)
				var cliErr *apierr.Error
				if !errors.As(err, &cliErr) || cliErr.Code != tt.code {
					t.Errorf("%v: error = %#v, want %s", args, err, tt.code)
				}
				if resp.Data != nil {
					t.Errorf("%v: data = %#v, want none", args, resp.Data)
				}
			}
		})
	}
}
