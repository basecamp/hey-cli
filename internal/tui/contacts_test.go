package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/models"
)

type recordedTUIContacts struct {
	mu       sync.Mutex
	requests []string
	bodies   [][]byte
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
		recorded.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/contacts.json":
			if req.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":7,"name":"Jane Doe","email_address":"jane@example.com"}]`))
		case req.Method == http.MethodPost && req.URL.Path == "/contacts.json":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":8,"name":"Sam Rivera","email_address":"sam@example.org"}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/7.json":
			_, _ = w.Write([]byte(`{"id":7,"name":"Jane Doe","email_address":"jane@example.com","aliases":[{"id":17,"name":"Jane Doe","email_address":"jane.doe@example.org"}]}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/8.json":
			_, _ = w.Write([]byte(`{"id":8,"name":"Sam Rivera","email_address":"sam@example.org","aliases":[]}`))
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
	view.Update(msg)
}

func openTUIContact(t *testing.T, view *contactsView) {
	t.Helper()
	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil {
		t.Fatal("opening selected contact returned no command")
	}
	view.Update(cmd())
}

func TestContactsViewLoadsListsAndPages(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	if view.loading || !view.loaded || len(view.list.contacts) != 1 || view.list.contacts[0].ID != 7 {
		t.Errorf("list state = loading:%v loaded:%v contacts:%+v", view.loading, view.loaded, view.list.contacts)
	}

	next := view.HandleContentKey(keyPress("n"))
	if next == nil {
		t.Fatal("next page returned no command")
	}
	view.Update(next())
	if view.page != 1 || len(view.list.contacts) != 1 || view.notice != "No more contacts" {
		t.Errorf("empty next page changed results: page=%d contacts=%+v notice=%q", view.page, view.list.contacts, view.notice)
	}
	requests, _ := recorded.snapshot()
	if !strings.Contains(strings.Join(requests, "\n"), "page=2") {
		t.Errorf("requests = %v", requests)
	}
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
	view.activeRequestID = 2
	view.Update(contactsLoadedMsg{requestID: 1, page: 1, contacts: []models.Contact{{ID: 99}}})
	if len(view.list.contacts) != 0 {
		t.Error("stale contacts changed the list")
	}
	view.Update(contactDetailLoadedMsg{requestID: 1, contact: models.Contact{ID: 99}})
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
	detail, _ := view.Update(save())
	if detail == nil {
		t.Fatal("successful create should load contact detail")
	}
	view.Update(detail())
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
	view.list.setContacts([]models.Contact{{ID: 7, Name: "Jane Doe"}})
	view.activeRequestID = 3
	view.loading = true

	cmd, _ := view.Update(contactSavedMsg{
		requestID:  3,
		originalID: 7,
		contact:    models.Contact{ID: 17, Name: "Jane Doe"},
	})
	if cmd == nil {
		t.Fatal("saved contact should load returned contact detail")
	}
	if len(view.list.contacts) != 1 || view.list.contacts[0].ID != 17 {
		t.Errorf("promoted alias left stale contact in list: %+v", view.list.contacts)
	}
}

func TestContactsViewValidatesFormBeforeSaving(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	view.HandleContentKey(keyPress("a"))
	if cmd := view.HandleContentKey(keyPress("ctrl+s")); cmd != nil {
		t.Fatal("invalid form should not save")
	}
	if view.contactForm == nil || view.contactForm.status != "Name is required" {
		t.Errorf("form status = %q", view.contactForm.status)
	}
	requests, _ := recorded.snapshot()
	if len(requests) != 1 {
		t.Errorf("validation made requests: %v", requests)
	}
}

func TestContactsViewHidesAndShowsAgain(t *testing.T) {
	view, recorded := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	openTUIContact(t, view)
	hide := view.HandleContentKey(keyPress("h"))
	view.Update(hide())
	if view.inDetail || len(view.list.contacts) != 0 || view.lastHiddenID != 7 || view.notice != "Contact hidden" {
		t.Errorf("hide state = detail:%v contacts:%+v hidden:%d notice:%q", view.inDetail, view.list.contacts, view.lastHiddenID, view.notice)
	}
	reveal := view.HandleContentKey(keyPress("u"))
	refresh, _ := view.Update(reveal())
	if refresh == nil {
		t.Fatal("successful reveal should refresh contacts")
	}
	view.Update(refresh())
	if view.lastHiddenID != 0 || len(view.list.contacts) != 1 || view.notice != "Contact shown again" {
		t.Errorf("reveal state = contacts:%+v hidden:%d notice:%q", view.list.contacts, view.lastHiddenID, view.notice)
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
	view.Update(save())
	if view.noteForm != nil || view.note != "Prefers a call" || view.notice != "Private note saved" {
		t.Errorf("note save state = form:%v note:%q notice:%q", view.noteForm, view.note, view.notice)
	}
	if deleteCmd := view.HandleContentKey(keyPress("x")); deleteCmd != nil || !view.confirmNoteDelete || !strings.Contains(view.notice, "permanently delete") {
		t.Fatal("first x should request note deletion confirmation")
	}
	deleteCmd := view.HandleContentKey(keyPress("x"))
	if deleteCmd == nil {
		t.Fatal("second x should delete the note")
	}
	view.Update(deleteCmd())
	if view.note != "" || view.notice != "Private note deleted" {
		t.Errorf("note delete state = note:%q notice:%q", view.note, view.notice)
	}
	requests, _ := recorded.snapshot()
	joined := strings.Join(requests, "\n")
	if !strings.Contains(joined, "PATCH /contacts/7/note.json") || !strings.Contains(joined, "DELETE /contacts/7/note.json") {
		t.Errorf("requests = %v", requests)
	}
}

func TestContactsViewCancelsPendingDetail(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	loadTUIContacts(t, view)
	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil || !view.loading || view.activeRequestKind != contactRequestDetail {
		t.Fatal("opening should start detail request")
	}
	if !view.CancelPendingDetail() || view.loading || view.activeRequestKind != contactRequestNone {
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
	list.setContacts([]models.Contact{
		{ID: 1, Name: "Jane Doe", EmailAddress: "jane@example.com"},
		{ID: 2, Name: "Sam Rivera", EmailAddress: "sam@example.org"},
	})
	list.moveDown()
	if selected := list.selected(); selected == nil || selected.ID != 2 {
		t.Errorf("selected = %+v", selected)
	}
	view := list.view()
	for _, want := range []string{"Jane Doe", "jane@example.com", "Sam Rivera", "#2"} {
		if !strings.Contains(view, want) {
			t.Errorf("list omitted %q: %q", want, view)
		}
	}
	list.remove(2)
	if len(list.contacts) != 1 || list.cursor != 0 {
		t.Errorf("remove state = contacts:%+v cursor:%d", list.contacts, list.cursor)
	}
}

func TestContactsViewHelpBindings(t *testing.T) {
	view, _ := contactsWithTestServer(t)
	view.loaded = true
	view.list.setContacts([]models.Contact{{ID: 7}})
	keys := map[string]bool{}
	for _, binding := range view.HelpBindings() {
		keys[binding.key] = true
	}
	for _, want := range []string{"a", "r", "n/p"} {
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
