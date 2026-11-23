package mcpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// failingHEY is HEY answering every request with status, its request ID, and
// a body that must never reach the person.
func failingHEY(t *testing.T, status int) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req-hey-1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error": "secret stack trace at app/models/topic.rb:42"}`))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

// closedHEY is an address nothing listens on.
func closedHEY(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

// heyAt is the hey-sdk client hey mcp hands New, against baseURL.
func heyAt(baseURL string) *hey.Client {
	return hey.NewClient(&hey.Config{BaseURL: baseURL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
}

// assertNoLeak fails when text carries HEY's body, the request's URL or
// path, or the client's or Go's error text.
func assertNoLeak(t *testing.T, text string) {
	t.Helper()
	for _, leak := range []string{
		"secret", "topic.rb", // the body
		"127.0.0.1", "http://", ".json", // the URL and path
		"dial tcp", "connection refused", "Network error", "Gateway error", "Server error", "Resource not found", // the client's text
	} {
		if strings.Contains(text, leak) {
			t.Errorf("%q carries %q", text, leak)
		}
	}
}

func callDomain(t *testing.T, api API, key, action string, params map[string]any) string {
	t.Helper()
	cat := loadForTest(t)
	if err := holdToDrafts(cat); err != nil {
		t.Fatal(err)
	}
	domain := domainByKey(t, cat, key)
	result, err := (dispatcher{api: api}).handle(context.Background(), domain, mustFind(t, domain, action), params)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("a failed call reported success: %s", textContent(t, result))
	}
	return textContent(t, result)
}

func TestUpstreamFailureIsAnsweredInOwnWords(t *testing.T) {
	cases := []struct {
		name, baseURL, key, action string
		params                     map[string]any
		want                       string
	}{
		{"read 500", failingHEY(t, 500), "boxes", "list_boxes", map[string]any{},
			"Error: HEY hit an error on its side (HTTP 500) and didn't complete this request\nNext: Try again later."},
		{"read 404", failingHEY(t, 404), "boxes", "list_boxes", map[string]any{},
			"Error: HEY couldn't find what this request names (HTTP 404)\nNext: Check the IDs in the request: what it names may have been deleted, or this account can't see it."},
		{"read unreachable", closedHEY(t), "boxes", "list_boxes", map[string]any{},
			"Error: HEY couldn't be reached, so this request wasn't completed\nNext: Try again later."},
		{"write 503", failingHEY(t, 503), "todos", "create_calendar_todo", map[string]any{"calendar_todo": map[string]any{"title": "Ship it"}},
			"Error: HEY hit an error on its side (HTTP 503) before confirming this change, so it may or may not have been made\n" +
				"Next: Check in HEY whether the change went through before trying again, so it isn't made twice."},
		{"draft save 502", failingHEY(t, 502), "threads", "create_message", paramsFor("create_message", nil),
			"Error: HEY hit an error on its side (HTTP 502) before confirming this change, so it may or may not have been made\n" +
				"Next: Check the thread and Drafts in HEY before trying again; a retry could save a second copy."},
		{"draft save unreachable", closedHEY(t), "threads", "create_message", paramsFor("create_message", nil),
			"Error: The connection to HEY failed before it answered, so this change may or may not have been made\n" +
				"Next: Check the thread and Drafts in HEY before trying again; a retry could save a second copy."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := callDomain(t, heyAt(tc.baseURL), tc.key, tc.action, tc.params)
			if text != tc.want {
				t.Errorf("got  %q\nwant %q", text, tc.want)
			}
			assertNoLeak(t, text)
		})
	}
}

// HEY's own reasons for refusing what was sent are kept.
func TestValidationRefusalKeepsHEYsReasons(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors": ["Title can't be blank"]}`))
	}))
	t.Cleanup(ts.Close)
	text := callDomain(t, heyAt(ts.URL), "todos", "create_calendar_todo", map[string]any{"calendar_todo": map[string]any{"title": ""}})
	mustContain(t, text, "Title can't be blank")
}

// A read made before a change names HEY's failure by status alone.
func TestSenderReadNamesTheStatusOnly(t *testing.T) {
	params := paramsFor("update_message", nil)
	delete(params, "acting_sender_id")
	text := callDomain(t, heyAt(failingHEY(t, 500)), "threads", "update_message", params)
	mustContain(t, text, "couldn't read draft 5's: HEY answered HTTP 500. Nothing was changed")
	assertNoLeak(t, text)
}

// A send HEY failed may have gone out; it says so in hey mcp's words.
func TestASendHEYFailedIsAnsweredInOwnWords(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.sendErr = &hey.Error{Code: hey.CodeAPI, Message: "Gateway error (502)", HTTPStatus: http.StatusBadGateway, RequestID: "req-send-1"}
	session := rig.connect(t, approve, "", false)
	text := resultText(t, sendDraft(t, session))
	want := "Error: HEY hit an error on its side (HTTP 502) before confirming the send, so it may or may not have been sent (request_id: req-send-1)\n" +
		"Next: Check Sent in HEY before doing anything else; hey mcp won't send this draft again."
	if text != want {
		t.Errorf("got  %q\nwant %q", text, want)
	}
	assertNoLeak(t, text)
}

// A draft that can't be read before a send names HEY's failure by status.
func TestADraftHEYFailedToReadIsNotSent(t *testing.T) {
	rig := newSendRig(t)
	rig.drafts.editErr = &hey.Error{Code: hey.CodeAPI, Message: "Server error (500)", HTTPStatus: http.StatusInternalServerError}
	session := rig.connect(t, approve, "", false)
	text := resultText(t, sendDraft(t, session))
	if text != "not sent: couldn't read draft 5: HEY answered HTTP 500" {
		t.Errorf("got %q", text)
	}
	if n := len(rig.drafts.sent()); n != 0 {
		t.Errorf("sent %d times after a failed read", n)
	}
}

// The client may redact a transport failure's cause to a stand-in the toolkit
// can't name; its network code alone makes it the connection failing, worded
// without the client's text.
func TestRedactedNetworkFailureIsAnsweredInOwnWords(t *testing.T) {
	redacted := &hey.Error{Code: hey.CodeNetwork, Message: "Network error", Hint: "transport failure at https://app.hey.com/topics/9.json", Retryable: true,
		Cause: errors.New("transport failure")}
	text := callDomain(t, &fakeAPI{err: redacted}, "boxes", "list_boxes", map[string]any{})
	if want := "Error: The connection to HEY failed, so this request wasn't completed\nNext: Try again later."; text != want {
		t.Errorf("got  %q\nwant %q", text, want)
	}
	assertNoLeak(t, text)
	if strings.Contains(text, "transport failure") {
		t.Errorf("%q carries the client's text", text)
	}
}
