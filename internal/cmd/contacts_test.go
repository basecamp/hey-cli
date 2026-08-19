package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type recordedContactRequest struct {
	Method string
	Path   string
	Query  string
	Body   []byte
}

type recordedContacts struct {
	mu       sync.Mutex
	requests []recordedContactRequest
	statuses map[string]int
}

func (r *recordedContacts) snapshot() []recordedContactRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedContactRequest(nil), r.requests...)
}

func contactsServer(t *testing.T) (*httptest.Server, *recordedContacts) {
	t.Helper()
	recorded := &recordedContacts{statuses: make(map[string]int)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(req.Body)
		recorded.mu.Lock()
		recorded.requests = append(recorded.requests, recordedContactRequest{Method: req.Method, Path: req.URL.Path, Query: req.URL.RawQuery, Body: body.Bytes()})
		status := recorded.statuses[req.Method+" "+req.URL.Path]
		recorded.mu.Unlock()
		if status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if status == http.StatusConflict {
				_, _ = w.Write([]byte(`{"errors":["Some email addresses are already in use for other contacts"],"contact_id":9,"conflicting_contact_ids":[4,5]}`))
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/contacts.json":
			if req.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{
				"id":7,"account_id":1,"name":"Jane Doe","email_address":"jane@example.com","updated_at":"2026-08-18T12:00:00Z"
			}]`))
		case req.Method == http.MethodPost && req.URL.Path == "/contacts.json":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":8,"account_id":1,"name":"Jane Dawson","email_address":"jane.dawson@example.com"}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/7.json":
			_, _ = w.Write([]byte(`{
				"id":7,"account_id":1,"name":"Jane Doe","email_address":"jane@example.com",
				"aliases":[{"id":17,"name":"Jane Doe","email_address":"jane.doe@example.org"}],
				"clearance":{"id":3,"status":"approved"}
			}`))
		case req.Method == http.MethodPatch && req.URL.Path == "/contacts/7.json":
			_, _ = w.Write([]byte(`{"id":7,"account_id":1,"name":"Jane Dawson","email_address":"jane@example.com"}`))
		case req.Method == http.MethodDelete && req.URL.Path == "/contacts/7.json":
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodPost && req.URL.Path == "/contacts/7/reveal.json":
			_, _ = w.Write([]byte(`{"id":7,"account_id":1,"name":"Jane Doe","email_address":"jane@example.com"}`))
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/7/note.json":
			_, _ = w.Write([]byte(`{"contact_id":7,"note":"Prefers email","note_html":"<p>Prefers email</p>"}`))
		case req.Method == http.MethodPatch && req.URL.Path == "/contacts/7/note.json":
			_, _ = w.Write([]byte(`{"contact_id":7,"note":"Prefers a call","note_html":"<p>Prefers a call</p>"}`))
		case req.Method == http.MethodDelete && req.URL.Path == "/contacts/7/note.json":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	return server, recorded
}

func runContacts(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs(append([]string{"contacts", "--json", "--base-url", server.URL}, args...))
	err := root.Execute()
	var resp output.Response
	if stdout.Len() > 0 {
		_ = json.Unmarshal(stdout.Bytes(), &resp)
	}
	return resp, err
}

func decodeContactData[T any](t *testing.T, data any) T {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var result T
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestContactsListAndPagination(t *testing.T) {
	server, recorded := contactsServer(t)
	resp, err := runContacts(t, server, "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	contacts := decodeContactData[[]generated.Contact](t, resp.Data)
	if len(contacts) != 1 || contacts[0].Id != 7 {
		t.Errorf("contacts = %+v", contacts)
	}
	requests := recorded.snapshot()
	if len(requests) != 2 || !strings.Contains(requests[0].Query, "page=1") || !strings.Contains(requests[1].Query, "page=2") {
		t.Errorf("requests = %+v", requests)
	}
	if resp.Summary != "1 contact" {
		t.Errorf("summary = %q", resp.Summary)
	}
}

func TestContactsShowIncludesAliasesAndPrivateNote(t *testing.T) {
	server, recorded := contactsServer(t)
	resp, err := runContacts(t, server, "show", "7")
	if err != nil {
		t.Fatalf("show failed: %v; requests=%+v", err, recorded.snapshot())
	}
	result := decodeContactData[contactShowResult](t, resp.Data)
	if result.Id != 7 || len(result.Aliases) != 1 || result.Note != "Prefers email" || result.Clearance.Status != "approved" {
		t.Errorf("result = %+v", result)
	}
	requests := recorded.snapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want contact and note", len(requests))
	}
}

func TestContactsAddSendsFieldsAndAccount(t *testing.T) {
	server, recorded := contactsServer(t)
	resp, err := runContacts(t, server, "add", "--name", "Jane Dawson", "--email", "jane.dawson@example.com", "--alias", "jd@example.org", "--account-user-id", "42")
	if err != nil {
		t.Fatal(err)
	}
	contact := decodeContactData[generated.Contact](t, resp.Data)
	if contact.Id != 8 || resp.Summary != "Contact added" {
		t.Errorf("contact = %+v, summary = %q", contact, resp.Summary)
	}
	requests := recorded.snapshot()
	if len(requests) != 1 || requests[0].Method != http.MethodPost {
		t.Fatalf("requests = %+v", requests)
	}
	var body generated.CreateContactRequestContent
	if err := json.Unmarshal(requests[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.ActingUserId != 42 || body.Contact.Name != "Jane Dawson" || body.Contact.EmailAddress != "jane.dawson@example.com" || len(body.Contact.AliasEmailAddresses) != 1 {
		t.Errorf("body = %+v", body)
	}
}

func TestContactsUpdatePreservesOmittedFields(t *testing.T) {
	server, recorded := contactsServer(t)
	resp, err := runContacts(t, server, "update", "7", "--name", "Jane Dawson")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Summary != "Contact updated" {
		t.Errorf("summary = %q", resp.Summary)
	}
	requests := recorded.snapshot()
	if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodPatch {
		t.Fatalf("requests = %+v", requests)
	}
	var body generated.ContactRequestContent
	if err := json.Unmarshal(requests[1].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Contact.Name != "Jane Dawson" || body.Contact.EmailAddress != "jane@example.com" || len(body.Contact.AliasEmailAddresses) != 1 || body.Contact.AliasEmailAddresses[0] != "jane.doe@example.org" {
		t.Errorf("merged update body = %+v", body)
	}
}

func TestContactsUpdateClearsAliases(t *testing.T) {
	server, recorded := contactsServer(t)
	if _, err := runContacts(t, server, "update", "7", "--alias="); err != nil {
		t.Fatal(err)
	}
	requests := recorded.snapshot()
	var body generated.ContactRequestContent
	if err := json.Unmarshal(requests[1].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Contact.AliasEmailAddresses != nil {
		t.Errorf("aliases = %#v, want the empty replacement omitted so the server applies its empty-list default", body.Contact.AliasEmailAddresses)
	}
	if body.Contact.Name != "Jane Doe" || body.Contact.EmailAddress != "jane@example.com" {
		t.Errorf("clear-alias update did not preserve contact fields: %+v", body.Contact)
	}
}

func TestContactsHideAndReveal(t *testing.T) {
	server, recorded := contactsServer(t)
	if resp, err := runContacts(t, server, "hide", "7"); err != nil || resp.Summary != "Contact hidden" {
		t.Fatalf("hide: response=%+v error=%v", resp, err)
	}
	if resp, err := runContacts(t, server, "show-again", "7"); err != nil || resp.Summary != "Contact shown again" {
		t.Fatalf("reveal: response=%+v error=%v", resp, err)
	}
	requests := recorded.snapshot()
	if len(requests) != 2 || requests[0].Method != http.MethodDelete || requests[1].Method != http.MethodPost {
		t.Errorf("requests = %+v", requests)
	}
}

func TestContactNotesShowSetAndDelete(t *testing.T) {
	server, recorded := contactsServer(t)
	show, err := runContacts(t, server, "note", "show", "7")
	if err != nil {
		t.Fatal(err)
	}
	if note := decodeContactData[generated.ContactNote](t, show.Data); note.Note != "Prefers email" {
		t.Errorf("show note = %+v", note)
	}
	set, err := runContacts(t, server, "note", "set", "7", "Prefers a call")
	if err != nil {
		t.Fatal(err)
	}
	if note := decodeContactData[generated.ContactNote](t, set.Data); note.Note != "Prefers a call" {
		t.Errorf("set note = %+v", note)
	}
	if deleted, err := runContacts(t, server, "note", "delete", "7"); err != nil || deleted.Summary != "Private contact note deleted" {
		t.Fatalf("delete: response=%+v error=%v", deleted, err)
	}
	requests := recorded.snapshot()
	if len(requests) != 3 {
		t.Fatalf("requests = %+v", requests)
	}
	var body generated.ContactNoteRequestContent
	if err := json.Unmarshal(requests[1].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Contact.Note != "Prefers a call" {
		t.Errorf("note body = %+v", body)
	}
}

func TestContactNoteSetPreservesMultilineContent(t *testing.T) {
	server, recorded := contactsServer(t)
	content := "First line\nSecond line"
	if _, err := runContacts(t, server, "note", "set", "7", content); err != nil {
		t.Fatal(err)
	}
	requests := recorded.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %+v", requests)
	}
	var body generated.ContactNoteRequestContent
	if err := json.Unmarshal(requests[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Contact.Note != content {
		t.Errorf("note = %q, want %q", body.Contact.Note, content)
	}
}

func TestContactNoteForEditorReturnsReadErrors(t *testing.T) {
	want := errors.New("note read failed")
	if _, err := contactNoteForEditor(t.Context(), 7, func(context.Context, int64) (*generated.ContactNote, error) {
		return nil, want
	}); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	content, err := contactNoteForEditor(t.Context(), 7, func(context.Context, int64) (*generated.ContactNote, error) {
		return &generated.ContactNote{ContactId: 7, Note: "Existing note"}, nil
	})
	if err != nil || content != "Existing note" {
		t.Errorf("content = %q, error = %v", content, err)
	}
}

func TestContactCommandsValidateBeforeRequests(t *testing.T) {
	tests := [][]string{
		{"add", "--name", "Jane"},
		{"add", "--email", "jane@example.com"},
		{"add", "--name", "Jane", "--email", "jane@example.com", "--alias", "jane@example.com"},
		{"update", "7"},
		{"show", "not-an-id"},
		{"note", "set", "7", ""},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			server, recorded := contactsServer(t)
			_, err := runContacts(t, server, args...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
				t.Fatalf("error = %v, want usage", err)
			}
			if len(recorded.snapshot()) != 0 {
				t.Errorf("validation made requests: %+v", recorded.snapshot())
			}
		})
	}
}

func TestContactConflictIncludesMergeIDs(t *testing.T) {
	server, recorded := contactsServer(t)
	recorded.mu.Lock()
	recorded.statuses[http.MethodPost+" /contacts.json"] = http.StatusConflict
	recorded.mu.Unlock()
	_, err := runContacts(t, server, "add", "--name", "Jane", "--email", "jane@example.com")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "conflict" || !strings.Contains(cliErr.Hint, "4, 5") || !strings.Contains(cliErr.Hint, "9") {
		t.Fatalf("conflict error = %#v", err)
	}
}

func TestCollectContactsReportsPageCap(t *testing.T) {
	calls := 0
	lastPage := 0
	contacts, pages, truncated, err := collectContacts(t.Context(), 7, true,
		func(_ context.Context, params *generated.ListContactsParams) (*generated.ListContactsResponseContent, error) {
			calls++
			lastPage, _ = strconv.Atoi(*params.Page)
			result := generated.ListContactsResponseContent{{Id: int64(calls)}}
			return &result, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls != maxContactPages || pages != maxContactPages || len(contacts) != maxContactPages || !truncated || lastPage != 106 {
		t.Errorf("calls=%d pages=%d contacts=%d truncated=%v lastPage=%d", calls, pages, len(contacts), truncated, lastPage)
	}
	want := "Contact listing stopped after 100 pages. Continue with --page 107."
	if got := contactTruncationNotice(7, pages, truncated); got != want {
		t.Errorf("notice = %q, want %q", got, want)
	}
}

func TestParseContactIDAndNoun(t *testing.T) {
	if id, err := parseContactID("123"); err != nil || id != 123 {
		t.Errorf("parseContactID = %d, %v", id, err)
	}
	for _, value := range []string{"0", "-1", "nope"} {
		if _, err := parseContactID(value); err == nil {
			t.Errorf("parseContactID(%q) should fail", value)
		}
	}
	if contactNoun(1) != "contact" || contactNoun(2) != "contacts" {
		t.Errorf("contact nouns are wrong: %q %q", contactNoun(1), contactNoun(2))
	}
}

func TestCollectContactsRejectsNilFetcher(t *testing.T) {
	if _, _, _, err := collectContacts(t.Context(), 1, false, nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("error = %v", err)
	}
}

func TestContactListPageErrorIsReturned(t *testing.T) {
	want := fmt.Errorf("list failed")
	_, _, _, err := collectContacts(t.Context(), 1, false, func(context.Context, *generated.ListContactsParams) (*generated.ListContactsResponseContent, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
