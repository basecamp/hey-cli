package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

const addressableJSON = `[
	["rick@example.com","Rick Sanchez"],
	["jane@example.com","Jane Doe"],
	["annie@example.com","Bryan, Annie"],
	["joanna@example.org","Joanna Lumley"],
	["morty@example.com","morty@example.com"],
	["jane@example.com,rick@example.com,annie@example.com","Everyone at Honcho Design","@example.com"],
	["morty@example.com,summer@example.com","Book club","Contact group with 2 people"]
]`

// recipientsRecorder is what the test server saw: how often the list was read
// and with what, and the last write.
type recipientsRecorder struct {
	mu           sync.Mutex
	listReads    int
	includeSelf  string
	writeMethod  string
	writePath    string
	writeBody    map[string]any
	failDrafts   bool
	failList     bool
	addressables string
}

func (r *recipientsRecorder) reads() int {
	return r.seen().listReads
}

// recorded is what the server has seen, copied out under the lock so a test
// never reads a field the handler goroutine is writing.
type recorded struct {
	listReads   int
	includeSelf string
	writeMethod string
	writePath   string
	writeBody   map[string]any
}

func (r *recipientsRecorder) seen() recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return recorded{
		listReads:   r.listReads,
		includeSelf: r.includeSelf,
		writeMethod: r.writeMethod,
		writePath:   r.writePath,
		writeBody:   r.writeBody,
	}
}

// configure changes how the server answers, under the lock the handler reads it with.
func (r *recipientsRecorder) configure(change func(r *recipientsRecorder)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	change(r)
}

// recipientsTestView is a mail view whose HEY serves a recipient list, a thread
// to reply to and forward, and accepts sends and drafts.
func recipientsTestView(t *testing.T) (*mailView, *recipientsRecorder) {
	t.Helper()
	rec := &recipientsRecorder{addressables: addressableJSON}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/autocompletable/contacts/addressable.json":
			rec.listReads++
			rec.includeSelf = r.URL.Query().Get("include_self")
			if rec.failList {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, rec.addressables)
		case "/identity.json":
			_, _ = io.WriteString(w, `{"id":1,"accounts":[{"id":9,"status":"active"}],"senders":[{"id":42,"account_id":9,"default":true}]}`)
		case "/topics/100.json":
			_, _ = io.WriteString(w, `{"id":100,"account_id":9,"name":"Quarterly planning","entries":[{"id":500},{"id":501}]}`)
		case "/entries/501/replies/new.json":
			_, _ = io.WriteString(w, `{"subject":"Re: Quarterly planning",
				"addressed":{"directly":[{"id":3,"name":"Rick Sanchez","email_address":"rick@example.com"}]}}`)
		case "/entries/501/forwards/new.json":
			_, _ = io.WriteString(w, `{"subject":"Fwd: Quarterly planning","content":"<div>Quoted message</div>"}`)
		default:
			rec.writeMethod, rec.writePath = r.Method, r.URL.Path
			rec.writeBody = nil
			if b, _ := io.ReadAll(r.Body); len(b) > 0 {
				_ = json.Unmarshal(b, &rec.writeBody)
			}
			if rec.failDrafts {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Location", "/messages/777")
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	sdk := hey.NewClient(&hey.Config{BaseURL: srv.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.rootSDK = sdk
	vc.sdk = sdk
	vc.ctx = context.Background()
	v := newMailView(vc)
	v.boxes = orderBoxes(testBoxes())
	v.Update(currentPostingsLoaded(v, testPostings()))
	v.Resize(100, 30)
	return v, rec
}

// settle runs the command a composer opened with and hands the view the list it
// read. The command is a batch whose last part is the read; the rest only focus a
// field, and running a cursor's blink would wait on its timer.
func settle(t *testing.T, v *mailView, cmd tea.Cmd) {
	t.Helper()
	msg := runCmd(cmd)
	if batch, ok := msg.(tea.BatchMsg); ok && len(batch) > 0 {
		msg = runCmd(batch[len(batch)-1])
	}
	loaded, ok := msg.(recipientsLoadedMsg)
	if !ok {
		t.Fatalf("a composer should read the recipient list as it opens, got %T", msg)
	}
	v.Update(loaded)
}

// openThreadComposer opens a reply or a forward from what HEY answered for it.
func openThreadComposer(t *testing.T, v *mailView, load tea.Cmd) *composeForm {
	t.Helper()
	cmd, _ := v.Update(runCmd(load))
	settle(t, v, cmd)
	return composeModal(v)
}

// openComposer opens a new message and lets the recipient list arrive.
func openComposer(t *testing.T, v *mailView) *composeForm {
	t.Helper()
	settle(t, v, v.HandleContentKey(keyPress("c")))
	form := composeModal(v)
	if form == nil {
		t.Fatal("c should open a new message")
	}
	if len(form.recipients) == 0 {
		t.Fatal("the recipient list should have reached the form")
	}
	return form
}

func suggestedLabels(f *composeForm) []string {
	if f.suggest == nil {
		return nil
	}
	var labels []string
	for _, match := range f.suggest.matches {
		labels = append(labels, match.label)
	}
	return labels
}

func TestComposerReadsTheRecipientListOnceWithItself(t *testing.T) {
	v, rec := recipientsTestView(t)
	openComposer(t, v)
	if rec.reads() != 1 {
		t.Fatalf("list reads = %d, want 1", rec.reads())
	}
	if rec.seen().includeSelf != "true" {
		t.Errorf("include_self = %q, want true, as the web composer asks", rec.seen().includeSelf)
	}

	// A second read while the first is on its way is the same read.
	v.modal = nil
	v.recipientsLoading = true
	if cmd := v.loadRecipients(); cmd != nil {
		t.Error("a composer opening while the list is loading should wait for that read")
	}
}

func TestTypingANameSuggestsAndTabAddsThePerson(t *testing.T) {
	v, rec := recipientsTestView(t)
	form := openComposer(t, v)

	typeText(v, "ja")
	if got := suggestedLabels(form); !slices.Equal(got, []string{"Jane Doe"}) {
		t.Fatalf("suggestions for ja = %q", got)
	}
	if view := v.View(); !strings.Contains(view, "Jane Doe") || !strings.Contains(view, "jane@example.com") {
		t.Errorf("the list should be on screen with names and addresses:\n%s", view)
	}

	v.HandleContentKey(keyPress("tab"))
	if got := form.inputs[fieldTo].Value(); got != "Jane Doe <jane@example.com>, " {
		t.Fatalf("To = %q", got)
	}
	if form.suggest != nil || form.focus != int(fieldTo) {
		t.Fatalf("tab on the list should add the person and stay in To, focus %d", form.focus)
	}

	// With the list closed tab moves on, as it always has.
	v.HandleContentKey(keyPress("tab"))
	if form.focus != int(fieldCc) {
		t.Errorf("tab with no list open should move to Cc, got %d", form.focus)
	}

	typeText(v, "bry")
	v.HandleContentKey(keyPress("enter"))
	if got := form.inputs[fieldCc].Value(); got != `"Bryan, Annie" <annie@example.com>, ` {
		t.Fatalf("Cc = %q", got)
	}

	form.inputs[fieldSubject].SetValue("Offsite agenda")
	form.body.SetValue("Here is the plan for Thursday.")
	if msg, ok := runCmd(v.HandleContentKey(ctrlS())).(composeSentMsg); !ok || msg.err != nil {
		t.Fatalf("send = %#v", msg)
	}
	entry, _ := rec.seen().writeBody["entry"].(map[string]any)
	addressed, _ := entry["addressed"].(map[string]any)
	if fmt.Sprint(addressed["directly"]) != "[Jane Doe <jane@example.com>]" ||
		fmt.Sprint(addressed["copied"]) != `["Bryan, Annie" <annie@example.com>]` {
		t.Errorf("sent addressed = %v", addressed)
	}
}

func TestArrowsChooseAndEscClosesOnlyTheList(t *testing.T) {
	v, _ := recipientsTestView(t)
	form := openComposer(t, v)
	typeText(v, "j")
	if form.suggest == nil || form.suggest.cursor != 0 {
		t.Fatal("typing should open the list on its first row")
	}
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("up"))
	v.HandleContentKey(keyPress("down"))
	if form.suggest.cursor != 1 {
		t.Errorf("cursor = %d, want 1", form.suggest.cursor)
	}
	for range 10 {
		v.HandleContentKey(tea.KeyPressMsg(tea.Key{Code: 'n', Mod: tea.ModCtrl}))
	}
	if form.suggest.cursor != len(form.suggest.matches)-1 {
		t.Errorf("ctrl+n should stop on the last row, got %d", form.suggest.cursor)
	}

	v.HandleContentKey(keyPress("esc"))
	if form.suggest != nil {
		t.Fatal("esc should close the list")
	}
	if composeModal(v) == nil || form.confirmLeave {
		t.Fatal("esc on the list must not touch the message")
	}
}

func TestMovingTheCursorClosesTheList(t *testing.T) {
	v, _ := recipientsTestView(t)
	form := openComposer(t, v)
	typeText(v, "ri")
	if form.suggest == nil {
		t.Fatal("typing should open the list")
	}
	v.HandleContentKey(keyPress("left"))
	if form.suggest != nil {
		t.Error("moving the cursor should close the list")
	}
	v.HandleContentKey(keyPress("right"))
	if form.suggest != nil {
		t.Error("moving back should not open it either")
	}
	typeText(v, "c")
	if form.suggest == nil {
		t.Error("typing again should open it again")
	}
}

func TestPickingAGroupAddsEveryAddress(t *testing.T) {
	v, _ := recipientsTestView(t)
	form := openComposer(t, v)
	typeText(v, "book")
	if got := suggestedLabels(form); !slices.Equal(got, []string{"Book club"}) {
		t.Fatalf("suggestions for book = %q", got)
	}
	if view := v.View(); !strings.Contains(view, "Contact group with 2 people") {
		t.Errorf("a group should say what it stands for:\n%s", view)
	}
	v.HandleContentKey(keyPress("tab"))
	if got := form.inputs[fieldTo].Value(); got != "morty@example.com, summer@example.com, " {
		t.Errorf("To = %q", got)
	}
}

func TestSuggestionsReplaceOnlyTheRecipientBeingTyped(t *testing.T) {
	v, _ := recipientsTestView(t)
	form := openComposer(t, v)
	to := &form.inputs[fieldTo]
	to.SetValue("rick@example.com, jo, summer@example.com")
	to.SetCursor(len("rick@example.com, jo"))
	typeText(v, "a")
	if got := suggestedLabels(form); len(got) == 0 || got[0] != "Joanna Lumley" {
		t.Fatalf("suggestions for joa = %q", got)
	}
	v.HandleContentKey(keyPress("tab"))
	if got := to.Value(); got != "rick@example.com, Joanna Lumley <joanna@example.org>, summer@example.com" {
		t.Errorf("To = %q", got)
	}
	if got := to.Position(); got != len([]rune("rick@example.com, Joanna Lumley <joanna@example.org>")) {
		t.Errorf("cursor = %d, want it after the name it added", got)
	}
}

func TestSomeoneAlreadyOnTheLineIsNotSuggestedAgain(t *testing.T) {
	v, _ := recipientsTestView(t)
	form := openComposer(t, v)
	form.inputs[fieldTo].SetValue("Jane Doe <JANE@example.com>, ")
	form.inputs[fieldTo].CursorEnd()
	typeText(v, "j")
	if got := suggestedLabels(form); slices.Contains(got, "Jane Doe") {
		t.Errorf("Jane is already on the line, suggestions = %q", got)
	}
}

func TestAGroupAlreadyOnTheLineIsNotSuggestedAgain(t *testing.T) {
	v, _ := recipientsTestView(t)
	form := openComposer(t, v)
	typeText(v, "book")
	v.HandleContentKey(keyPress("tab"))
	if got := form.inputs[fieldTo].Value(); got != "morty@example.com, summer@example.com, " {
		t.Fatalf("To = %q", got)
	}
	typeText(v, "book")
	if got := suggestedLabels(form); slices.Contains(got, "Book club") {
		t.Errorf("every member of Book club is already on the line, suggestions = %q", got)
	}
}

// A selected row draws "› " before its name, which is four bytes and two cells.
// Measuring it in bytes cut the name short the moment the row was selected.
func TestASelectedRowKeepsItsWholeName(t *testing.T) {
	// A 42-cell form leaves 26 cells inside the list's frame; after the two-cell
	// marker that is room for exactly this 24-cell name.
	name := "Josephine Montgomery-Lee"
	form := newComposeForm(composeNew, newStyles())
	form.width = 42
	form.recipients = newRecipientSuggestions([]hey.AddressableRecipient{{Value: "bart@example.com", Label: name}})
	form.focus = int(fieldTo)
	form.inputs[fieldTo].SetValue("bart")
	form.inputs[fieldTo].CursorEnd()
	form.refreshSuggestions()
	if form.suggest == nil {
		t.Fatal("the list should be open")
	}
	if view := form.suggestionsView(); !strings.Contains(view, name) {
		t.Errorf("the selected row should show the whole name:\n%s", view)
	}
}

func TestTheHelpBarFollowsAListThatOpensWhenTheRecipientsArrive(t *testing.T) {
	m := modelWithBoxes()
	updated, _ := m.Update(keyPress("c"))
	m = updated.(model)
	for _, r := range "jan" {
		updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
		m = updated.(model)
	}
	updated, _ = m.Update(recipientsLoadedMsg{suggestions: newRecipientSuggestions([]hey.AddressableRecipient{
		{Value: "jane@example.com", Label: "Jane Doe"},
	})})
	m = updated.(model)
	if form := composeModal(m.mailView); form == nil || form.suggest == nil {
		t.Fatal("the list should open when the recipients arrive mid-word")
	}
	if len(m.help.bindings) == 0 || m.help.bindings[0].desc != "choose" {
		t.Errorf("the help bar should describe the list, got %v", m.help.bindings)
	}
}

func TestCcAndBccSuggestToo(t *testing.T) {
	for _, field := range []composeField{fieldCc, fieldBcc} {
		v, _ := recipientsTestView(t)
		form := openComposer(t, v)
		form.focus = int(field)
		_ = form.focusCurrent()
		typeText(v, "rick")
		v.HandleContentKey(keyPress("tab"))
		if got := form.inputs[field].Value(); got != "Rick Sanchez <rick@example.com>, " {
			t.Errorf("field %d = %q", field, got)
		}
	}
}

func TestReplyAndForwardSuggestRecipients(t *testing.T) {
	v, rec := recipientsTestView(t)
	form := openThreadComposer(t, v, v.loadReplyContext(100, "Quarterly planning"))
	if form == nil || form.mode != composeReply {
		t.Fatal("the reply form should be open")
	}
	form.focus = int(fieldCc)
	_ = form.focusCurrent()
	typeText(v, "jan")
	v.HandleContentKey(keyPress("tab"))
	if got := form.inputs[fieldCc].Value(); got != "Jane Doe <jane@example.com>, " {
		t.Errorf("reply Cc = %q", got)
	}
	if got := form.inputs[fieldTo].Value(); got != "rick@example.com" {
		t.Errorf("the prefilled To should be left alone, got %q", got)
	}

	v.modal = nil
	form = openThreadComposer(t, v, v.loadForwardContext(100, "Quarterly planning"))
	if form == nil || form.mode != composeForward {
		t.Fatal("the forward form should be open")
	}
	typeText(v, "mor")
	v.HandleContentKey(keyPress("tab"))
	if got := form.inputs[fieldTo].Value(); got != "morty@example.com, " {
		t.Errorf("forward To = %q (a contact with no name goes in as the address)", got)
	}
	if rec.reads() != 2 {
		t.Errorf("each composer should read the list again, reads = %d", rec.reads())
	}
}

func TestTypingBeforeTheListArrivesSuggestsWhenItDoes(t *testing.T) {
	v, _ := recipientsTestView(t)
	cmd := v.HandleContentKey(keyPress("c"))
	form := composeModal(v)
	typeText(v, "jan")
	if form.suggest != nil {
		t.Fatal("there is nothing to suggest before the list arrives")
	}
	settle(t, v, cmd)
	if got := suggestedLabels(form); !slices.Equal(got, []string{"Jane Doe"}) {
		t.Errorf("suggestions once the list arrived = %q", got)
	}
}

func TestAFailedListReadLeavesTypingAlone(t *testing.T) {
	v, rec := recipientsTestView(t)
	rec.configure(func(r *recipientsRecorder) { r.failList = true })
	settle(t, v, v.HandleContentKey(keyPress("c")))
	form := composeModal(v)
	typeText(v, "jane@example.com")
	if form.suggest != nil || form.inputs[fieldTo].Value() != "jane@example.com" {
		t.Errorf("typing should work without the list, To = %q", form.inputs[fieldTo].Value())
	}
	if v.recipientsLoading {
		t.Error("a failed read should not stop the next composer from reading again")
	}
}

func TestAFailedReadKeepsTheListFromBefore(t *testing.T) {
	v, rec := recipientsTestView(t)
	openComposer(t, v)
	v.modal = nil
	rec.configure(func(r *recipientsRecorder) { r.failList = true })
	form := openComposer(t, v)
	typeText(v, "rick")
	if got := suggestedLabels(form); !slices.Equal(got, []string{"Rick Sanchez"}) {
		t.Errorf("suggestions = %q", got)
	}
}

func TestSuggestionsAreSanitized(t *testing.T) {
	v, rec := recipientsTestView(t)
	rec.configure(func(r *recipientsRecorder) {
		r.addressables = `[["mallory@example.com","Mallory \u001b[31mRed\u001b]8;;https://evil.example.com\u0007 Evil","\u001b[2Jwipe"]]`
	})
	form := openComposer(t, v)
	typeText(v, "mal")
	view := v.View()
	if strings.Contains(view, "evil.example.com") || strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\x1b]8") {
		t.Errorf("escape sequences from a contact's name reached the screen: %q", view)
	}
	if got := suggestedLabels(form); len(got) != 1 || got[0] != "Mallory Red Evil" {
		t.Errorf("label = %q", got)
	}
}

func TestTheListReachesMailFromAnotherSection(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.recipientsLoading = true
	updated, _ := m.switchSection(sectionCalendar)
	m = updated.(model)
	updated, _ = m.Update(recipientsLoadedMsg{suggestions: newRecipientSuggestions([]hey.AddressableRecipient{
		{Value: "jane@example.com", Label: "Jane Doe"},
	})})
	m = updated.(model)
	if m.mailView.recipientsLoading || len(m.mailView.recipients) != 1 {
		t.Errorf("a list that arrives in the calendar is still Mail's: loading %v, %d rows",
			m.mailView.recipientsLoading, len(m.mailView.recipients))
	}
}

func TestDeleteRecipientBeforeTakesANamedRecipientWhole(t *testing.T) {
	for _, tc := range []struct {
		name       string
		value      string
		pos        int
		want       string
		wantCursor int
		ok         bool
	}{
		{name: "the only one, after its comma", value: "Jane Doe <jane@example.com>, ", pos: 29, want: "", wantCursor: 0, ok: true},
		{name: "the only one, right after it", value: "Jane Doe <jane@example.com>", pos: 27, want: "", wantCursor: 0, ok: true},
		{name: "the last of two", value: "Jane Doe <jane@example.com>, Rick Sanchez <rick@example.com>, ", pos: 62,
			want: "Jane Doe <jane@example.com>, ", wantCursor: 29, ok: true},
		{name: "one in the middle", value: "Jane Doe <jane@example.com>, Rick Sanchez <rick@example.com>, summer@example.com", pos: 60,
			want: "Jane Doe <jane@example.com>, summer@example.com", wantCursor: 29, ok: true},
		{name: "the first of two", value: "Jane Doe <jane@example.com>, rick@example.com", pos: 27,
			want: "rick@example.com", wantCursor: 0, ok: true},
		{name: "a quoted name with a comma", value: `"Bryan, Annie" <annie@example.com>, `, pos: 36, want: "", wantCursor: 0, ok: true},
		{name: "a bare address deletes a letter at a time", value: "jane@example.com, ", pos: 18, ok: false},
		{name: "an address in brackets with no name", value: "<jane@example.com>, ", pos: 20, ok: false},
		{name: "typing the next one", value: "Jane Doe <jane@example.com>, ri", pos: 31, ok: false},
		{name: "inside the address", value: "Jane Doe <jane@example.com>", pos: 20, ok: false},
	} {
		got, cursor, ok := deleteRecipientBefore(tc.value, tc.pos)
		if ok != tc.ok || got != tc.want || cursor != tc.wantCursor {
			t.Errorf("%s: deleteRecipientBefore = %q, %d, %v; want %q, %d, %v", tc.name, got, cursor, ok, tc.want, tc.wantCursor, tc.ok)
		}
	}
}

func TestBackspaceTakesAPickedRecipientInOnePress(t *testing.T) {
	v, _ := recipientsTestView(t)
	form := openComposer(t, v)
	typeText(v, "jan")
	v.HandleContentKey(keyPress("tab"))
	typeText(v, "rick")
	v.HandleContentKey(keyPress("tab"))
	to := &form.inputs[fieldTo]
	if to.Value() != "Jane Doe <jane@example.com>, Rick Sanchez <rick@example.com>, " {
		t.Fatalf("To = %q", to.Value())
	}

	v.HandleContentKey(keyPress("backspace"))
	if got := to.Value(); got != "Jane Doe <jane@example.com>, " {
		t.Fatalf("one backspace should take Rick whole, To = %q", got)
	}
	if to.Position() != len([]rune(to.Value())) {
		t.Errorf("the cursor should be ready for the next recipient, at %d", to.Position())
	}
	v.HandleContentKey(keyPress("backspace"))
	if got := to.Value(); got != "" {
		t.Errorf("a second backspace should take Jane, To = %q", got)
	}

	typeText(v, "sam@example.com")
	v.HandleContentKey(keyPress("backspace"))
	if got := to.Value(); got != "sam@example.co" {
		t.Errorf("a typed address deletes a letter at a time, To = %q", got)
	}
}

func TestMatchRecipientsPutsWordStartsFirstAndKeepsHEYsOrder(t *testing.T) {
	all := newRecipientSuggestions([]hey.AddressableRecipient{
		{Value: "joanna@example.org", Label: "Joanna Lumley"},
		{Value: "anna@example.com", Label: "Anna Karenina"},
		{Value: "susan@example.com", Label: "Susan Annaberg"},
		{Value: "hannah@example.com", Label: "Hannah Arendt"},
		{Value: "annual-report@example.com", Label: ""},
	})
	got := matchRecipients(all, "Ann", nil)
	labels := make([]string, 0, len(got))
	for _, match := range got {
		labels = append(labels, match.label)
	}
	want := []string{"Anna Karenina", "Susan Annaberg", "annual-report@example.com", "Joanna Lumley", "Hannah Arendt"}
	if !slices.Equal(labels, want) {
		t.Errorf("matches for Ann = %q, want %q", labels, want)
	}

	if matches := matchRecipients(all, "  ", nil); matches != nil {
		t.Errorf("a blank query should suggest nothing, got %d", len(matches))
	}
	for _, match := range matchRecipients(all, "anna", map[string]bool{"anna@example.com": true}) {
		if match.value == "anna@example.com" {
			t.Errorf("a chosen address should be left out, got %v", match)
		}
	}
}

func TestMatchRecipientsStopsAtTheLimit(t *testing.T) {
	rows := make([]hey.AddressableRecipient, 0, 50)
	for i := range 50 {
		rows = append(rows, hey.AddressableRecipient{Value: fmt.Sprintf("person%d@example.com", i), Label: fmt.Sprintf("Person %d", i)})
	}
	matches := matchRecipients(newRecipientSuggestions(rows), "person", nil)
	if len(matches) != maxRecipientSuggestions || matches[0].label != "Person 0" {
		t.Errorf("matches = %d, first %q", len(matches), matches[0].label)
	}
}

func TestNewRecipientSuggestionsSkipsRowsWithNoAddress(t *testing.T) {
	all := newRecipientSuggestions([]hey.AddressableRecipient{
		{Value: " ", Label: "Nobody"},
		{Value: "jane@example.com", Label: " "},
	})
	if len(all) != 1 || all[0].label != "jane@example.com" {
		t.Errorf("suggestions = %+v", all)
	}
}

// BenchmarkMatchRecipients is a keystroke against a large address book: the
// scan HEY's whole list costs when the query matches almost nobody, which is the
// worst case, since a common one stops early.
func BenchmarkMatchRecipients(b *testing.B) {
	rows := make([]hey.AddressableRecipient, 0, 20000)
	for i := range 20000 {
		rows = append(rows, hey.AddressableRecipient{
			Value: fmt.Sprintf("colleague.%d@example.com", i),
			Label: fmt.Sprintf("Colleague Number %d", i),
		})
	}
	all := newRecipientSuggestions(rows)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = matchRecipients(all, "zelda", nil)
	}
}
