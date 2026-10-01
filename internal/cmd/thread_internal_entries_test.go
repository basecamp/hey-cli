package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// A HEY for Domains thread with a prospect: Dana wrote in (11), Marcus answered her (12),
// then Priya left an internal note (13) and Marcus shared the thread with Owen (14). The
// entries are shaped as HEY's entries/_entry.jbuilder serves them, and the messages as
// messages/_message.jbuilder does — a note and a share notice are read through the same
// /messages/{id} route, with nobody addressed.
const (
	danaContact   = `{"id":301,"name":"Dana Whitaker","email_address":"dana@example.org"}`
	marcusContact = `{"id":302,"name":"Marcus Lee","email_address":"marcus@example.com"}`
	priyaContact  = `{"id":303,"name":"Priya Raman","email_address":"priya@example.com"}`
)

func internalEntryJSON(id int64, kind, creator, summary string) string {
	return fmt.Sprintf(`{"id":%d,"created_at":"2026-09-%02dT15:30:00Z","updated_at":"2026-09-%02dT15:30:00Z","creator":%s,"alternative_sender_name":null,"summary":%q,"kind":%q,"app_url":"https://app.hey.com/topics/7#__entry_%d"}`,
		id, id%28+1, id%28+1, creator, summary, kind, id)
}

func internalMessageJSON(id int64, creator, subject, content, directly string) string {
	return fmt.Sprintf(`{"id":%d,"created_at":"2026-09-%02dT15:30:00Z","updated_at":"2026-09-%02dT15:30:00Z","url":"https://app.hey.com/messages/%d","creator":%s,"is_reply":%t,"subject":%q,"content":%q,"addressed":{"directly":[%s],"copied":[],"blindcopied":[]},"show_addressed_selector":false,"scheduled_delivery_at":null}`,
		id, id%28+1, id%28+1, id, creator, id != 11, subject, content, directly)
}

var (
	danaWritesIn     = internalEntryJSON(11, "message", danaContact, "Could we try the onboarding pilot with our support team?")
	marcusAnswers    = internalEntryJSON(12, "message", marcusContact, "Happy to set that up — does Tuesday work for a kickoff call?")
	priyasNote       = internalEntryJSON(13, "comment", priyaContact, "Dana ran support at Contoso before Northwind; lead with ticket deflection.")
	marcusSharesWith = internalEntryJSON(14, "access_notice", marcusContact, "Owen, can you own the kickoff agenda?")

	internalThreadMessages = map[int64]string{
		11: internalMessageJSON(11, danaContact, "Onboarding pilot for Northwind",
			"<div>Could we try the onboarding pilot with our support team?</div>", marcusContact),
		12: internalMessageJSON(12, marcusContact, "Re: Onboarding pilot for Northwind",
			"<div>Happy to set that up — does Tuesday work for a kickoff call?</div>", danaContact),
		13: internalMessageJSON(13, priyaContact, "",
			"<div>Dana ran support at Contoso before Northwind; lead with ticket deflection.</div>", ""),
		14: internalMessageJSON(14, marcusContact, "",
			"<div>Owen, can you own the kickoff agenda?</div>", ""),
	}
)

// internalThread is what the server holds for thread 7: the entries Topics.Get serves
// (its newest page, oldest first), the entry index's pages (newest first), each entry's
// message, and HEY's reply prefill per entry.
type internalThread struct {
	topicEntries []string
	indexPages   [][]string
	messages     map[int64]string
	prefills     map[int64]string

	mu       sync.Mutex
	requests []string
	reply    *sentReply
}

func internalThreadServer(t *testing.T, thread *internalThread) *httptest.Server {
	t.Helper()
	thread.reply = &sentReply{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		thread.mu.Lock()
		thread.requests = append(thread.requests, r.Method+" "+r.URL.Path)
		thread.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		var entryID int64
		switch {
		case r.URL.Path == "/identity.json":
			fmt.Fprint(w, `{"id":1,"accounts":[{"id":9,"status":"active"}],"senders":[{"id":42,"account_id":9,"name":"Marcus Lee","email_address":"marcus@example.com","default":true}]}`)
		case r.URL.Path == "/topics/7.json":
			fmt.Fprintf(w, `{"id":7,"account_id":9,"name":"Onboarding pilot for Northwind","entries":[%s]}`, strings.Join(thread.topicEntries, ","))
		case r.URL.Path == "/topics/7/entries.json":
			index := threadEntriesPageIndex(len(thread.indexPages), r.URL.Query().Get("page"))
			if index+1 < len(thread.indexPages) {
				w.Header().Set("Link", fmt.Sprintf(`<http://%s/topics/7/entries.json?page=%s>; rel="next"`, r.Host, threadEntriesCursor(index+1)))
			}
			page := []string{}
			if index < len(thread.indexPages) {
				page = thread.indexPages[index]
			}
			fmt.Fprintf(w, "[%s]", strings.Join(page, ","))
		case scanPath(r.URL.Path, "/messages/%d.json", &entryID):
			message, ok := thread.messages[entryID]
			if !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			fmt.Fprint(w, message)
		case scanPath(r.URL.Path, "/entries/%d/replies/new.json", &entryID):
			prefill, ok := thread.prefills[entryID]
			if !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			fmt.Fprint(w, prefill)
		case scanPath(r.URL.Path, "/entries/%d/forwards/new.json", &entryID):
			fmt.Fprintf(w, `{"subject":"Fwd: Onboarding pilot for Northwind","content":"<div>Quoted entry %d</div>"}`, entryID)
		case r.URL.Path == "/messages.json" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		case scanPath(r.URL.Path, "/entries/%d/replies.json", &entryID) && r.Method == http.MethodPost:
			var body struct {
				Message struct {
					Subject string `json:"subject"`
				} `json:"message"`
				Entry struct {
					Addressed struct {
						Directly []string `json:"directly"`
						Copied   []string `json:"copied"`
					} `json:"addressed"`
				} `json:"entry"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			thread.reply.Path = r.URL.Path
			thread.reply.Subject = body.Message.Subject
			thread.reply.To = body.Entry.Addressed.Directly
			thread.reply.CC = body.Entry.Addressed.Copied
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func scanPath(path, format string, id *int64) bool {
	var rest string
	n, _ := fmt.Sscanf(path+" end", format+" %s", id, &rest)
	return n == 2 && rest == "end"
}

// requested reports whether any request the server saw started with the given prefix.
func (thread *internalThread) requested(prefix string) bool {
	thread.mu.Lock()
	defer thread.mu.Unlock()
	for _, request := range thread.requests {
		if strings.HasPrefix(request, prefix) {
			return true
		}
	}
	return false
}

func assertNothingAnsweredTheNotes(t *testing.T, thread *internalThread) {
	t.Helper()
	for _, id := range []int{13, 14} {
		for _, route := range []string{"GET /entries/%d/", "POST /entries/%d/", "GET /messages/%d.json"} {
			if prefix := fmt.Sprintf(route, id); thread.requested(prefix) {
				t.Errorf("read or wrote %s…, which is internal and never emailed", prefix)
			}
		}
	}
}

// The web app replies to the last message that was emailed, not to a note or a share
// notice posted after it: a reply built on Priya's note would go to Priya.
func TestReplyDryRunAnswersTheLastEmailedMessageNotANote(t *testing.T) {
	thread := &internalThread{
		topicEntries: []string{danaWritesIn, marcusAnswers, priyasNote, marcusSharesWith},
		messages:     internalThreadMessages,
		prefills: map[int64]string{
			12: `{"subject":"Re: Onboarding pilot for Northwind","addressed":{"directly":[{"id":301,"name":"Dana Whitaker","email_address":"dana@example.org"}]}}`,
			13: `{"subject":"Re:","addressed":{"directly":[{"id":303,"name":"Priya Raman","email_address":"priya@example.com"}]}}`,
			14: `{"subject":"Re:","addressed":{"directly":[{"id":302,"name":"Marcus Lee","email_address":"marcus@example.com"}]}}`,
		},
	}
	server := internalThreadServer(t, thread)

	out, err := runCLIOutput(t, server, "reply", "7", "--dry-run")
	if err != nil {
		t.Fatalf("reply dry run: %v\n%s", err, out)
	}
	var response struct {
		Data replyPreview `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if response.Data.EntryID != 12 {
		t.Errorf("entry = %d, want Marcus's message (12), not the note or share notice after it", response.Data.EntryID)
	}
	if want := []string{"dana@example.org"}; !reflect.DeepEqual(response.Data.To, want) {
		t.Errorf("to = %v, want %v", response.Data.To, want)
	}
	if response.Data.Subject != "Re: Onboarding pilot for Northwind" {
		t.Errorf("subject = %q", response.Data.Subject)
	}
	assertNothingAnsweredTheNotes(t, thread)
}

// Without HEY's prefill the reply falls back to the message itself — and it is the
// message, not the note, whose recipients and subject it reads.
func TestReplyFallsBackToTheLastEmailedMessagesRecipients(t *testing.T) {
	thread := &internalThread{
		topicEntries: []string{danaWritesIn, marcusAnswers, priyasNote, marcusSharesWith},
		messages:     internalThreadMessages,
	}
	server := internalThreadServer(t, thread)

	if err := runCLI(t, server, "reply", "7", "-m", "Tuesday at 10 works — calendar invite on its way."); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if thread.reply.Path != "/entries/12/replies.json" {
		t.Errorf("reply path = %q, want Marcus's message's reply route", thread.reply.Path)
	}
	if want := []string{"dana@example.org", "marcus@example.com"}; !reflect.DeepEqual(thread.reply.To, want) {
		t.Errorf("to = %v, want %v", thread.reply.To, want)
	}
	if thread.reply.Subject != "Re: Onboarding pilot for Northwind" {
		t.Errorf("subject = %q, want the message's", thread.reply.Subject)
	}
	assertNothingAnsweredTheNotes(t, thread)
}

// The topic's own page holds nothing but notes, so the message is found further down the
// entry index — newest first, as HEY serves it.
func TestReplyFindsTheMessageBehindAPageOfNotes(t *testing.T) {
	var notes []string
	for id := int64(40); id > 20; id-- {
		notes = append(notes, internalEntryJSON(id, "comment", priyaContact, "Following up internally."))
	}
	thread := &internalThread{
		topicEntries: reversed(notes[:10]),
		indexPages:   [][]string{notes[:10], append(notes[10:], marcusAnswers, danaWritesIn)},
		messages:     internalThreadMessages,
		prefills: map[int64]string{
			12: `{"subject":"Re: Onboarding pilot for Northwind","addressed":{"directly":[{"email_address":"dana@example.org"}]}}`,
		},
	}
	server := internalThreadServer(t, thread)

	out, err := runCLIOutput(t, server, "reply", "7", "--dry-run")
	if err != nil {
		t.Fatalf("reply dry run: %v\n%s", err, out)
	}
	var response struct {
		Data replyPreview `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if response.Data.EntryID != 12 {
		t.Errorf("entry = %d, want 12", response.Data.EntryID)
	}
	if thread.requested("GET /entries/40/") || thread.requested("GET /entries/21/") {
		t.Error("asked HEY how to reply to a note")
	}
}

// A thread of nothing but notes and share notices has no message a reply could answer,
// and the reply is refused rather than sent to whoever wrote the last note.
func TestReplyToAThreadWithoutAnEmailedMessageIsRefused(t *testing.T) {
	thread := &internalThread{
		topicEntries: []string{priyasNote, marcusSharesWith},
		indexPages:   [][]string{{marcusSharesWith, priyasNote}},
		messages:     internalThreadMessages,
	}
	server := internalThreadServer(t, thread)

	for _, args := range [][]string{
		{"reply", "7", "--dry-run"},
		{"reply", "7", "-m", "Tuesday works."},
		{"compose", "--thread-id", "7", "-m", "Tuesday works."},
		{"forward", "7", "--to", "owen@example.com"},
	} {
		err := runCLI(t, server, args...)
		var cliErr *apierr.Error
		if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeNotFound || !strings.Contains(cliErr.Message, "no emailed message") {
			t.Errorf("%v: error = %v, want a refusal naming the missing message", args, err)
		}
	}
	assertNothingAnsweredTheNotes(t, thread)
	if thread.requested("POST ") {
		t.Errorf("wrote to HEY: %v", thread.requests)
	}
}

// A forward goes out of the thread too, so it forwards the last emailed message: a note
// forwarded to someone outside would hand them what the team wrote to itself.
func TestForwardSendsTheLastEmailedMessageNotANote(t *testing.T) {
	thread := &internalThread{
		topicEntries: []string{danaWritesIn, marcusAnswers, priyasNote, marcusSharesWith},
		messages:     internalThreadMessages,
	}
	server := internalThreadServer(t, thread)

	if err := runCLI(t, server, "forward", "7", "--to", "owen@example.com"); err != nil {
		t.Fatalf("forward: %v", err)
	}
	if !thread.requested("GET /entries/12/forwards/new.json") {
		t.Errorf("requests = %v, want the forward of Marcus's message (12)", thread.requests)
	}
	assertNothingAnsweredTheNotes(t, thread)
}

func TestComposeIntoAThreadAnswersTheLastEmailedMessage(t *testing.T) {
	thread := &internalThread{
		topicEntries: []string{danaWritesIn, marcusAnswers, priyasNote, marcusSharesWith},
		messages:     internalThreadMessages,
	}
	server := internalThreadServer(t, thread)

	if err := runCLI(t, server, "compose", "--thread-id", "7", "-m", "Tuesday at 10 works."); err != nil {
		t.Fatalf("compose into thread: %v", err)
	}
	if thread.reply.Path != "/entries/12/replies.json" {
		t.Errorf("reply path = %q, want Marcus's message's reply route", thread.reply.Path)
	}
	assertNothingAnsweredTheNotes(t, thread)
}

// A reader of the text formats is told a note and a share notice were never emailed,
// instead of reading a From line as mail that went out.
func TestThreadTextFormatsLabelNotesAndShareNotices(t *testing.T) {
	newServer := func() *httptest.Server {
		return internalThreadServer(t, &internalThread{
			indexPages: [][]string{{marcusSharesWith, priyasNote, marcusAnswers, danaWritesIn}},
			messages:   internalThreadMessages,
		})
	}
	stdoutTerminal(t, false)

	markdown, _, err := runCLIRaw(t, newServer(), "--markdown", "thread", "read", "7")
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	for _, want := range []string{
		"## From: Marcus Lee — ",
		"## Note by Priya Raman — 2026-09-14T15:30 (#13)\n\n*(internal note, not emailed)*\n\nDana ran support",
		"## Marcus Lee shared this thread — 2026-09-15T15:30 (#14)\n\n*(share notice, not emailed)*\n\nOwen, can you own",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown = %q, want %q in it", markdown, want)
		}
	}
	if strings.Contains(markdown, "From: Priya Raman") {
		t.Errorf("markdown = %q, a note is headed as mail", markdown)
	}

	styled, _, err := runCLIRaw(t, newServer(), "--styled", "thread", "read", "7")
	if err != nil {
		t.Fatalf("styled: %v", err)
	}
	for _, want := range []string{
		"From: Dana Whitaker  [",
		"Note by Priya Raman  [2026-09-14T15:30]  #13  (internal note, not emailed)",
		"Marcus Lee shared this thread  [2026-09-15T15:30]  #14  (share notice, not emailed)",
	} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled = %q, want %q in it", styled, want)
		}
	}

	html, _, err := runCLIRaw(t, newServer(), "--html", "thread", "read", "7")
	if err != nil {
		t.Fatalf("html: %v", err)
	}
	for _, want := range []string{
		`data-entry-id="12" data-kind="message"`,
		`data-entry-id="13" data-kind="comment" data-created-at="2026-09-14T15:30" data-body-state="hydrated">` +
			"\n<header>\n<div>Note by Priya Raman — 2026-09-14T15:30 (internal note, not emailed)</div>\n</header>",
		`data-kind="access_notice"`,
		"<div>Marcus Lee shared this thread — 2026-09-15T15:30 (share notice, not emailed)</div>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html = %q, want %q in it", html, want)
		}
	}
}

// JSON already says what each entry is, and says it unchanged.
func TestThreadJSONKeepsEachEntrysKind(t *testing.T) {
	server := internalThreadServer(t, &internalThread{
		indexPages: [][]string{{marcusSharesWith, priyasNote, marcusAnswers, danaWritesIn}},
		messages:   internalThreadMessages,
	})
	stdoutTerminal(t, false)

	stdout, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7")
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	var response struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	kinds := make([]any, 0, len(response.Data))
	for _, entry := range response.Data {
		kinds = append(kinds, entry["kind"])
		for _, key := range []string{"heading", "label", "internal"} {
			if _, ok := entry[key]; ok {
				t.Errorf("entry %v carries %q; JSON is unchanged", entry["id"], key)
			}
		}
	}
	if want := []any{"message", "message", "comment", "access_notice"}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	if note := response.Data[2]; note["body"] != "Dana ran support at Contoso before Northwind; lead with ticket deflection." {
		t.Errorf("note body = %v", note["body"])
	}
}

func reversed(entries []string) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[len(entries)-1-i] = entry
	}
	return out
}
