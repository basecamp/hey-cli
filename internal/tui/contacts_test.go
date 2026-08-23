package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

type recordedTUIContacts struct {
	mu       sync.Mutex
	requests []string
	bodies   [][]byte
	conflict bool
}

func (r *recordedTUIContacts) snapshot() ([]string, [][]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...), append([][]byte(nil), r.bodies...)
}

func contactsWithTestServer(t *testing.T) (*contactsView, *recordedTUIContacts) {
	t.Helper()
	recorded := &recordedTUIContacts{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var raw json.RawMessage
		if req.Body != nil {
			_ = json.NewDecoder(req.Body).Decode(&raw)
		}
		recorded.mu.Lock()
		recorded.requests = append(recorded.requests, req.Method+" "+req.URL.RequestURI())
		recorded.bodies = append(recorded.bodies, append([]byte(nil), raw...))
		conflict := recorded.conflict
		recorded.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/contacts.json":
			switch req.URL.Query().Get("page") {
			case "2":
				_, _ = w.Write([]byte(`[{"id":21,"name":"Alex Nakamura","email_address":"alex@example.com"},{"id":22,"name":"Priya Raman","email_address":"priya@example.org"}]`))
			case "3":
				_, _ = w.Write([]byte(`[]`))
			default:
				_, _ = w.Write([]byte(`[{"id":7,"name":"Jane Doe","email_address":"jane@example.com"}]`))
			}
		case req.Method == http.MethodPost && req.URL.Path == "/contacts.json":
			if conflict {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"errors":["Some email addresses are already in use for other contacts"],"contact_id":9,"conflicting_contact_ids":[4,5]}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":8,"name":"Sam Rivera","email_address":"sam@example.org"}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/7.json":
			_, _ = w.Write([]byte(`{"id":7,"name":"Jane Doe","email_address":"jane@example.com","aliases":[{"id":17,"name":"Jane Doe","email_address":"jane.doe@example.org"}]}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/8.json":
			_, _ = w.Write([]byte(`{"id":8,"name":"Sam Rivera","email_address":"sam@example.org","aliases":[]}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/9.json":
			_, _ = w.Write([]byte(`{"id":9,"name":"Sam Rivera","email_address":"sam@example.org","aliases":[]}`))
		case req.Method == http.MethodPatch && req.URL.Path == "/contacts/7.json":
			_, _ = w.Write([]byte(`{"id":7,"name":"Jane Dawson","email_address":"jane@example.com"}`))
		case req.Method == http.MethodDelete && req.URL.Path == "/contacts/7.json":
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodPost && req.URL.Path == "/contacts/7/reveal.json":
			_, _ = w.Write([]byte(`{"id":7,"name":"Jane Doe","email_address":"jane@example.com"}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/7/note.json":
			_, _ = w.Write([]byte(`{"contact_id":7,"note":"Prefers email","note_html":"<p>Prefers email</p>"}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/8/note.json":
			_, _ = w.Write([]byte(`{"contact_id":8,"note":"","note_html":""}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/9/note.json":
			_, _ = w.Write([]byte(`{"contact_id":9,"note":"","note_html":""}`))
		case req.Method == http.MethodPatch && req.URL.Path == "/contacts/7/note.json":
			_, _ = w.Write([]byte(`{"contact_id":7,"note":"Prefers a call","note_html":"<p>Prefers a call</p>"}`))
		case req.Method == http.MethodDelete && req.URL.Path == "/contacts/7/note.json":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	view := newContactsView(vc)
	view.Resize(vc.width, vc.height)
	return view, recorded
}

func loadTUIContacts(t *testing.T, view *contactsView) {
	t.Helper()
	cmd := view.Init()
	if cmd == nil {
		t.Fatal("contacts init returned no command")
	}
	msg := cmd()
	if _, ok := msg.(contactsLoadedMsg); !ok {
		t.Fatalf("contacts init returned %T", msg)
	}
	drainContactPages(t, view, msg)
}

// drainContactPages applies a contacts response and every page it goes on to read below
// it, the way the runtime does, so a test sees the list at the depth a reader would.
func drainContactPages(t *testing.T, view *contactsView, msg tea.Msg) {
	t.Helper()
	for range 20 {
		cmd, _ := view.Update(msg)
		if cmd == nil {
			return
		}
		msg = cmd()
	}
	t.Fatal("contacts never stopped reading pages")
}

func openTUIContact(t *testing.T, view *contactsView) {
	t.Helper()
	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil {
		t.Fatal("opening selected contact returned no command")
	}
	view.Update(cmd())
}

func TestContactsViewGrowsUntilTheListRunsOut(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	if view.requests.loading || !view.loaded || view.loadingMore || view.nextPage != 0 {
		t.Errorf("list state = loading:%v loaded:%v growing:%v nextPage:%d", view.requests.loading, view.loaded, view.loadingMore, view.nextPage)
	}
	if ids := contactIDs(view.list.contacts); !slices.Equal(ids, []int64{7, 21, 22}) {
		t.Errorf("contacts = %v, want the first page and the one below it", ids)
	}
	requests, _ := recorded.snapshot()
	if !slices.Equal(requests, []string{
		"GET /contacts.json?page=1",
		"GET /contacts.json?page=2",
		"GET /contacts.json?page=3",
	}) {
		t.Errorf("requests = %v", requests)
	}

	// The reader can see the end of the list, and the empty page said that is all there is.
	if cmd := view.HandleContentKey(keyPress("down")); cmd != nil {
		t.Error("scrolling a finished list read another page")
	}
}

func TestContactsViewReadsThePageBelowTheCursorOnce(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	view.loaded = true
	view.nextPage = 2
	view.list.setSize(80, 4)
	view.list.setContacts(testContactRows(40))

	view.list.cursor = 10
	if cmd := view.HandleContentKey(keyPress("down")); cmd != nil {
		t.Error("a list with plenty left below it read the page below")
	}

	view.list.cursor = len(view.list.contacts) - 2
	cmd := view.HandleContentKey(keyPress("down"))
	if cmd == nil || !view.loadingMore {
		t.Fatal("scrolling to the bottom should read the page below")
	}
	if again := view.HandleContentKey(keyPress("down")); again != nil {
		t.Error("the page below was asked for twice")
	}

	msg, ok := cmd().(contactsAppendedMsg)
	if !ok {
		t.Fatalf("page below answered %T", cmd())
	}
	view.Update(msg)
	if view.nextPage != 3 {
		t.Errorf("nextPage = %d, want the page after the one that arrived", view.nextPage)
	}
	if ids := contactIDs(view.list.contacts[40:]); !slices.Equal(ids, []int64{21, 22}) {
		t.Errorf("appended contacts = %v", ids)
	}
}

func TestContactsViewIgnoresASupersededPageBelow(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	view.loaded = true
	view.nextPage = 2
	view.loadingMore = true
	view.moreRequestID = 3
	view.list.setContacts([]Contact{{ID: 7}})

	view.Update(contactsAppendedMsg{requestID: 2, contacts: []Contact{{ID: 99}}, nextPage: 3})
	if len(view.list.contacts) != 1 || !view.loadingMore {
		t.Errorf("stale page grew the list: contacts=%+v growing:%v", view.list.contacts, view.loadingMore)
	}

	view.Update(contactsAppendedMsg{requestID: 3, err: errors.New("read failed")})
	if view.loadingMore || !strings.Contains(view.notice, "Could not load more contacts") {
		t.Errorf("failed page state = growing:%v notice:%q", view.loadingMore, view.notice)
	}
}

func TestContactsViewRefreshReadsFromTheTop(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	view.list.cursor = 2

	cmd := view.HandleContentKey(keyPress("r"))
	if cmd == nil || view.nextPage != 0 {
		t.Fatalf("refresh state = cmd:%v nextPage:%d", cmd != nil, view.nextPage)
	}
	drainContactPages(t, view, cmd())
	if ids := contactIDs(view.list.contacts); !slices.Equal(ids, []int64{7, 21, 22}) {
		t.Errorf("refreshed contacts = %v", ids)
	}
	if view.list.cursor != 0 {
		t.Errorf("refresh left the cursor at %d", view.list.cursor)
	}
	requests, _ := recorded.snapshot()
	if requests[len(requests)-3] != "GET /contacts.json?page=1" {
		t.Errorf("refresh did not start over: %v", requests)
	}
}

func contactIDs(contacts []Contact) []int64 {
	ids := make([]int64, 0, len(contacts))
	for _, contact := range contacts {
		ids = append(ids, contact.ID)
	}
	return ids
}

func testContactRows(count int) []Contact {
	contacts := make([]Contact, 0, count)
	for i := range count {
		contacts = append(contacts, Contact{
			ID:           int64(100 + i),
			Name:         fmt.Sprintf("Contact %d", i),
			EmailAddress: fmt.Sprintf("contact%d@example.com", i),
		})
	}
	return contacts
}

func TestContactsViewOpensDetailWithAliasesAndNote(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	openTUIContact(t, view)
	if !view.inDetail || view.detail.ID != 7 || len(view.detail.Aliases) != 1 || view.note != "Prefers email" {
		t.Errorf("detail = open:%v contact:%+v note:%q", view.inDetail, view.detail, view.note)
	}
	rendered := view.View()
	for _, want := range []string{"Jane Doe", "jane@example.com", "jane.doe@example.org", "Prefers email"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("detail omitted %q: %q", want, rendered)
		}
	}
}

func TestContactsViewIgnoresStaleResponses(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	view.requests.id = 2
	view.Update(contactsLoadedMsg{requestResult: requestResult{requestID: 1}, contacts: []Contact{{ID: 99}}})
	if len(view.list.contacts) != 0 {
		t.Error("stale contacts changed the list")
	}
	view.Update(contactDetailLoadedMsg{requestResult: requestResult{requestID: 1}, contact: Contact{ID: 99}})
	if view.inDetail {
		t.Error("stale detail opened")
	}
}

func TestContactsViewAddsContact(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	if cmd := view.HandleContentKey(keyPress("a")); cmd == nil || view.contactForm == nil || !view.CapturingInput() {
		t.Fatal("a should open and focus the contact form")
	}
	view.contactForm.inputs[contactFieldName].SetValue("Sam Rivera")
	view.contactForm.inputs[contactFieldEmail].SetValue("sam@example.org")
	view.contactForm.inputs[contactFieldAliases].SetValue("sam.rivera@example.com")

	save := view.HandleContentKey(keyPress("ctrl+s"))
	if save == nil {
		t.Fatal("ctrl+s should save the contact")
	}
	answer, _ := view.Update(save())
	if answer == nil {
		t.Fatal("successful create should load contact detail")
	}
	if toast := deliverToView(view, answer); toast != "Contact added" {
		t.Errorf("toast = %q", toast)
	}
	if view.contactForm != nil || !view.inDetail || view.detail.ID != 8 {
		t.Errorf("create state = form:%v detail:%+v", view.contactForm, view.detail)
	}
	requests, bodies := recorded.snapshot()
	var found bool
	for i, request := range requests {
		if request == "POST /contacts.json" {
			found = true
			if !strings.Contains(string(bodies[i]), "sam.rivera@example.com") {
				t.Errorf("create body = %s", bodies[i])
			}
		}
	}
	if !found {
		t.Errorf("requests = %v", requests)
	}
}

func TestContactsViewCreateConflictLoadsWrittenContact(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	view.HandleContentKey(keyPress("a"))
	view.contactForm.inputs[contactFieldName].SetValue("Sam Rivera")
	view.contactForm.inputs[contactFieldEmail].SetValue("sam@example.org")
	recorded.mu.Lock()
	recorded.conflict = true
	recorded.mu.Unlock()

	save := view.HandleContentKey(keyPress("ctrl+s"))
	detail, _ := view.Update(save())
	if detail == nil || view.contactForm != nil || !strings.Contains(view.notice, "Contact 9 was saved") {
		t.Fatalf("conflict state = detail:%v form:%v notice:%q", detail != nil, view.contactForm, view.notice)
	}
	view.Update(detail())
	if !view.inDetail || view.detail.ID != 9 {
		t.Errorf("written conflict contact was not loaded: %+v", view.detail)
	}
	if len(view.list.contacts) != 4 || view.list.contacts[0].ID != 9 {
		t.Errorf("written conflict contact was not added to the list: %+v", view.list.contacts)
	}
}

func TestContactsViewEditsContact(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	openTUIContact(t, view)
	view.HandleContentKey(keyPress("e"))
	if view.contactForm == nil || view.contactForm.inputs[contactFieldAliases].Value() != "jane.doe@example.org" {
		t.Fatal("edit form should include current aliases")
	}
	view.contactForm.inputs[contactFieldName].SetValue("Jane Dawson")
	save := view.HandleContentKey(keyPress("ctrl+s"))
	detail, _ := view.Update(save())
	if detail == nil {
		t.Fatal("successful update should reload contact detail")
	}
	view.Update(detail())
	if view.detail.Name != "Jane Doe" {
		// The detail fixture is intentionally stable; the PATCH response itself verifies the edit.
		t.Errorf("detail fixture = %+v", view.detail)
	}
	requests, bodies := recorded.snapshot()
	var patchBody string
	for i, request := range requests {
		if request == "PATCH /contacts/7.json" {
			patchBody = string(bodies[i])
		}
	}
	if !strings.Contains(patchBody, "Jane Dawson") || !strings.Contains(patchBody, "jane.doe@example.org") {
		t.Errorf("patch body = %s", patchBody)
	}
}

func TestContactsViewReplacesPromotedAliasContactID(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	view.list.setContacts([]Contact{{ID: 7, Name: "Jane Doe"}})
	view.requests.id = 3
	view.requests.loading = true

	cmd, _ := view.Update(contactSavedMsg{
		requestResult: requestResult{requestID: 3},
		originalID:    7,
		contact:       Contact{ID: 17, Name: "Jane Doe"},
	})
	if cmd == nil {
		t.Fatal("saved contact should load returned contact detail")
	}
	if len(view.list.contacts) != 1 || view.list.contacts[0].ID != 17 {
		t.Errorf("promoted alias left stale contact in list: %+v", view.list.contacts)
	}
}

func TestContactFormBlankAliasesCreatesExplicitEmptyReplacement(t *testing.T) {
	form := newContactForm(contactFormEdit, Contact{
		ID:           7,
		Name:         "Jane Doe",
		EmailAddress: "jane@example.com",
		Aliases:      []Contact{{EmailAddress: "jane.doe@example.org"}},
	}, newStyles())
	form.inputs[contactFieldAliases].SetValue("")
	_, _, aliases := form.values()
	if aliases == nil || len(aliases) != 0 {
		t.Errorf("aliases = %#v, want non-nil empty replacement", aliases)
	}
}

func TestContactsViewValidatesFormBeforeSaving(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	loadRequests, _ := recorded.snapshot()
	view.HandleContentKey(keyPress("a"))
	if cmd := view.HandleContentKey(keyPress("ctrl+s")); cmd != nil {
		t.Fatal("invalid form should not save")
	}
	if view.contactForm == nil || view.contactForm.status != "Name is required" {
		t.Errorf("form status = %q", view.contactForm.status)
	}
	requests, _ := recorded.snapshot()
	if len(requests) != len(loadRequests) {
		t.Errorf("validation made requests: %v", requests[len(loadRequests):])
	}
}

func TestContactsViewHidesAndShowsAgain(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	openTUIContact(t, view)
	hide := view.HandleContentKey(keyPress("h"))
	hidden, _ := view.Update(hide())
	if toast := deliverToView(view, hidden); view.inDetail || len(view.list.contacts) != 2 || view.lastHiddenID != 7 || toast != "Contact hidden" {
		t.Errorf("hide state = detail:%v contacts:%+v hidden:%d toast:%q", view.inDetail, view.list.contacts, view.lastHiddenID, toast)
	}
	reveal := view.HandleContentKey(keyPress("u"))
	revealed, _ := view.Update(reveal())
	if revealed == nil {
		t.Fatal("successful reveal should refresh contacts")
	}
	toast, refresh := toastAndRest(t, revealed)
	drainContactPages(t, view, refresh())
	if view.lastHiddenID != 0 || len(view.list.contacts) != 3 || toast != "Contact shown again" {
		t.Errorf("reveal state = contacts:%+v hidden:%d toast:%q", view.list.contacts, view.lastHiddenID, toast)
	}
	requests, _ := recorded.snapshot()
	joined := strings.Join(requests, "\n")
	if !strings.Contains(joined, "DELETE /contacts/7.json") || !strings.Contains(joined, "POST /contacts/7/reveal.json") {
		t.Errorf("requests = %v", requests)
	}
}

func TestContactsViewEditsAndDeletesNote(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	openTUIContact(t, view)
	view.HandleContentKey(keyPress("n"))
	if view.noteForm == nil || view.noteForm.input.Value() != "Prefers email" {
		t.Fatal("note form should contain existing note")
	}
	view.noteForm.input.SetValue("Prefers a call")
	save := view.HandleContentKey(keyPress("ctrl+s"))
	saved, _ := view.Update(save())
	if toast := deliverToView(view, saved); view.noteForm != nil || view.note != "Prefers a call" || toast != "Private note saved" {
		t.Errorf("note save state = form:%v note:%q toast:%q", view.noteForm, view.note, toast)
	}
	if deleteCmd := view.HandleContentKey(keyPress("x")); deleteCmd != nil || !view.confirmNoteDelete || !strings.Contains(view.notice, "permanently delete") {
		t.Fatal("first x should request note deletion confirmation")
	}
	deleteCmd := view.HandleContentKey(keyPress("x"))
	if deleteCmd == nil {
		t.Fatal("second x should delete the note")
	}
	deleted, _ := view.Update(deleteCmd())
	if toast := deliverToView(view, deleted); view.note != "" || toast != "Private note deleted" {
		t.Errorf("note delete state = note:%q toast:%q", view.note, toast)
	}
	requests, _ := recorded.snapshot()
	joined := strings.Join(requests, "\n")
	if !strings.Contains(joined, "PATCH /contacts/7/note.json") || !strings.Contains(joined, "DELETE /contacts/7/note.json") {
		t.Errorf("requests = %v", requests)
	}
}

func TestContactsViewCannotExitDuringMutation(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	view.inDetail = true
	view.requests.loading = true
	view.requests.kind = contactRequestMutation
	view.ExitDetail("q")
	if !view.inDetail || !view.requests.loading || view.requests.kind != contactRequestMutation {
		t.Error("detail exited while a contact mutation was pending")
	}
}

func TestContactsViewCancelsPendingDetail(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil || !view.requests.loading || view.requests.kind != contactRequestDetail {
		t.Fatal("opening should start detail request")
	}
	if !view.CancelPendingDetail() || view.requests.loading || view.requests.kind != contactRequestNone {
		t.Error("pending detail was not canceled")
	}
	view.Update(cmd())
	if view.inDetail {
		t.Error("canceled detail response opened the contact")
	}
}

func TestContactsListNavigationAndRendering(t *testing.T) {
	list := contactList{}
	list.setSize(80, 10)
	list.setContacts([]Contact{
		{ID: 1, Name: "Jane Doe", EmailAddress: "jane@example.com"},
		{ID: 2, Name: "Sam Rivera", EmailAddress: "sam@example.org"},
	})
	list.moveDown()
	if selected := list.selected(); selected == nil || selected.ID != 2 {
		t.Errorf("selected = %+v", selected)
	}
	view := list.view()
	for _, want := range []string{"Jane Doe", "jane@example.com", "Sam Rivera", "sam@example.org"} {
		if !strings.Contains(view, want) {
			t.Errorf("list omitted %q: %q", want, view)
		}
	}
	if strings.Contains(view, "#1") || strings.Contains(view, "#2") {
		t.Errorf("list should not show contact ids: %q", view)
	}
	if got := strings.Count(strings.TrimRight(view, "\n"), "\n") + 1; got != 2 {
		t.Errorf("two contacts should render on %d lines, want 2: %q", got, view)
	}
	// The name is bold; the address next to it stays regular weight.
	if !strings.Contains(view, "\x1b[1;97mJane Doe\x1b[m\x1b[97m <jane@example.com>") {
		t.Errorf("the address should not share the name's bold: %q", view)
	}
	list.remove(2)
	if len(list.contacts) != 1 || list.cursor != 0 {
		t.Errorf("remove state = contacts:%+v cursor:%d", list.contacts, list.cursor)
	}
}

func TestContactsViewHelpBindings(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	view.loaded = true
	view.list.setContacts([]Contact{{ID: 7}})
	keys := map[string]bool{}
	for _, binding := range view.HelpBindings() {
		keys[binding.key] = true
	}
	for _, want := range []string{"a", "r"} {
		if !keys[want] {
			t.Errorf("list help missing %q: %v", want, view.HelpBindings())
		}
	}
	view.inDetail = true
	view.note = "Private"
	view.confirmNoteDelete = true
	keys = map[string]bool{}
	for _, binding := range view.HelpBindings() {
		keys[binding.key] = true
	}
	for _, want := range []string{"e", "n", "h", "x"} {
		if !keys[want] {
			t.Errorf("detail help missing %q: %v", want, view.HelpBindings())
		}
	}
	for _, binding := range view.HelpBindings() {
		if binding.key == "x" && binding.desc != "confirm delete" {
			t.Errorf("delete confirmation help = %v", view.HelpBindings())
		}
	}
}

func TestContactsViewConverterPreservesAliases(t *testing.T) {
	contact := sdkContactDetailToModel(generatedContactDetailFixture())
	if contact.ID != 7 || len(contact.Aliases) != 1 || contact.Aliases[0].EmailAddress != "jane.doe@example.org" {
		t.Errorf("contact = %+v", contact)
	}
}

func generatedContactDetailFixture() generated.ContactDetail {
	return generated.ContactDetail{
		Id:           7,
		Name:         "Jane Doe",
		EmailAddress: "jane@example.com",
		Aliases: []generated.Contact{{
			Id:           17,
			Name:         "Jane Doe",
			EmailAddress: "jane.doe@example.org",
		}},
	}
}
