package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func typeText(v *mailView, s string) {
	for _, r := range s {
		v.HandleContentKey(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
}

func ctrlS() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl})
}

// runCmd executes a tea.Cmd synchronously and returns its message.
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// composeTestServer serves /identity.json (for DefaultSenderID) and records
// the send request. It returns the mailView wired to it and the recorder.
func composeTestServer(t *testing.T) (*mailView, *struct {
	method, path string
	body         map[string]any
}) {
	t.Helper()
	rec := &struct {
		method, path string
		body         map[string]any
	}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1,"email_address":"me@hey.com","senders":[{"id":42,"default":true}]}`))
			return
		}
		rec.method, rec.path = r.Method, r.URL.Path
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &rec.body)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	sdk := hey.NewClient(&hey.Config{BaseURL: srv.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = sdk
	vc.ctx = context.Background()
	v := newMailView(vc)
	v.boxes = orderBoxes(testBoxes())
	v.Update(postingsLoadedMsg{postings: testPostings()})
	return v, rec
}

func TestComposeOpensAndCancels(t *testing.T) {
	v := mailWithPostings()
	if v.CapturingInput() {
		t.Fatal("no form should be open initially")
	}
	v.HandleContentKey(keyPress("c"))
	if !v.CapturingInput() || v.compose == nil || v.compose.mode != composeNew {
		t.Fatal("'c' should open a new-message form")
	}
	if !strings.Contains(v.View(), "New message") {
		t.Errorf("view should render the form, got %q", v.View())
	}
	if b := v.HelpBindings(); len(b) == 0 || b[0].key != "tab" {
		t.Errorf("help should describe the form, got %v", b)
	}
	v.HandleContentKey(keyPress("esc"))
	if v.CapturingInput() {
		t.Error("esc should close the form")
	}
}

func TestComposeTabCyclesFields(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("c"))
	f := v.compose
	if f.focus != int(fieldTo) {
		t.Fatalf("focus should start on To, got %d", f.focus)
	}
	for range 4 {
		v.HandleContentKey(keyPress("tab"))
	}
	if f.focus != f.bodyIndex() {
		t.Errorf("four tabs from To should land on the body, got %d", f.focus)
	}
	v.HandleContentKey(keyPress("tab"))
	if f.focus != int(fieldTo) {
		t.Errorf("tab from body should wrap to To, got %d", f.focus)
	}
	v.HandleContentKey(keyPress("shift+tab"))
	if f.focus != f.bodyIndex() {
		t.Errorf("shift+tab from To should wrap to body, got %d", f.focus)
	}
}

func TestComposeValidatesBeforeSending(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("c"))
	cmd := v.HandleContentKey(ctrlS())
	if cmd != nil {
		t.Fatal("ctrl+s on an empty form must not send")
	}
	if !v.compose.isError || !strings.Contains(v.compose.status, "recipient") {
		t.Errorf("expected a recipient error, got %q", v.compose.status)
	}
	typeText(v, "a@b.com")
	v.HandleContentKey(keyPress("tab")) // cc
	v.HandleContentKey(keyPress("tab")) // bcc
	v.HandleContentKey(keyPress("tab")) // subject
	if cmd := v.HandleContentKey(ctrlS()); cmd != nil {
		t.Fatal("missing subject must not send")
	}
	if !strings.Contains(v.compose.status, "Subject") {
		t.Errorf("expected a subject error, got %q", v.compose.status)
	}
}

func TestComposeSendsMessage(t *testing.T) {
	v, rec := composeTestServer(t)
	v.Resize(80, 30)
	v.HandleContentKey(keyPress("c"))
	typeText(v, "a@b.com, c@d.com")
	v.HandleContentKey(keyPress("tab"))
	typeText(v, "cc@d.com")
	v.HandleContentKey(keyPress("tab")) // bcc
	v.HandleContentKey(keyPress("tab")) // subject
	typeText(v, "Hello")
	v.HandleContentKey(keyPress("tab")) // body
	typeText(v, "Body text")

	cmd := v.HandleContentKey(ctrlS())
	if cmd == nil {
		t.Fatal("expected a send command")
	}
	if !v.compose.sending {
		t.Error("form should be marked sending")
	}
	msg := runCmd(cmd)
	sent, ok := msg.(composeSentMsg)
	if !ok || sent.err != nil {
		t.Fatalf("expected successful composeSentMsg, got %#v", msg)
	}
	if rec.method != "POST" || rec.path != "/messages.json" {
		t.Errorf("sent %s %s, want POST /messages.json", rec.method, rec.path)
	}
	m := rec.body["message"].(map[string]any)
	if m["subject"] != "Hello" || m["content"] != "Body text" {
		t.Errorf("message payload = %v", m)
	}
	addressed := rec.body["entry"].(map[string]any)["addressed"].(map[string]any)
	if got := addressed["directly"].([]any); len(got) != 2 || got[0] != "a@b.com" {
		t.Errorf("directly = %v", got)
	}
	if got := addressed["copied"].([]any); len(got) != 1 || got[0] != "cc@d.com" {
		t.Errorf("copied = %v", got)
	}

	v.Update(sent)
	if v.compose != nil {
		t.Error("form should close after a successful send")
	}
	if v.notice != "Message sent" || !strings.Contains(v.View(), "Message sent") {
		t.Errorf("expected sent notice, got %q", v.notice)
	}
	v.HandleContentKey(keyPress("down"))
	if v.notice != "" {
		t.Error("notice should clear on the next key")
	}
}

func TestComposeSendFailureKeepsForm(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("c"))
	v.compose.sending = true
	v.Update(composeSentMsg{label: "Message sent", err: io.ErrUnexpectedEOF})
	if v.compose == nil {
		t.Fatal("form should stay open so the user can retry")
	}
	if v.compose.sending || !v.compose.isError || !strings.Contains(v.compose.status, "Send failed") {
		t.Errorf("expected an inline error, got sending=%v status=%q", v.compose.sending, v.compose.status)
	}
}

func TestReplyFormPrefillsAndSends(t *testing.T) {
	v, rec := composeTestServer(t)
	v.Resize(80, 30)
	v.Update(replyContextLoadedMsg{
		topicID: 7, topicName: "Kitchen", entryID: 99,
		to: []string{"jane@x.com"}, cc: []string{"bob@x.com"},
	})
	f := v.compose
	if f == nil || f.mode != composeReply {
		t.Fatal("reply context should open a reply form")
	}
	if f.inputs[fieldTo].Value() != "jane@x.com" || f.inputs[fieldCc].Value() != "bob@x.com" {
		t.Errorf("recipients not prefilled: to=%q cc=%q", f.inputs[fieldTo].Value(), f.inputs[fieldCc].Value())
	}
	if f.focus != f.bodyIndex() {
		t.Error("reply should start in the body")
	}
	if !strings.Contains(v.View(), "Reply: Kitchen") {
		t.Errorf("view should show the reply title, got %q", v.View())
	}
	typeText(v, "Thanks!")
	cmd := v.HandleContentKey(ctrlS())
	msg := runCmd(cmd)
	if sent, ok := msg.(composeSentMsg); !ok || sent.err != nil || sent.label != "Reply sent" {
		t.Fatalf("expected Reply sent, got %#v", msg)
	}
	if rec.method != "POST" || rec.path != "/entries/99/replies.json" {
		t.Errorf("sent %s %s, want POST /entries/99/replies.json", rec.method, rec.path)
	}
	if rec.body["message"].(map[string]any)["content"] != "Thanks!" {
		t.Errorf("body = %v", rec.body)
	}
	addressed := rec.body["entry"].(map[string]any)["addressed"].(map[string]any)
	if got := addressed["directly"].([]any); len(got) != 1 || got[0] != "jane@x.com" {
		t.Errorf("directly = %v", got)
	}
}

func TestReplyKeyInThreadLoadsContext(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	v.topicID = 123
	cmd := v.HandleContentKey(keyPress("r"))
	if cmd == nil || !v.loading {
		t.Fatal("'r' in a thread should start loading the reply context")
	}
}

func TestModelRoutesAllKeysToOpenForm(t *testing.T) {
	m := modelWithBoxes()
	updated, _ := m.Update(keyPress("c"))
	m = updated.(model)
	if !m.mailView.CapturingInput() {
		t.Fatal("'c' should open the compose form through the model")
	}
	// 'q' normally does nothing/back; tab normally moves focus rows. Both must
	// reach the form instead.
	updated, _ = m.Update(keyPress("q"))
	m = updated.(model)
	if m.mailView.compose == nil || m.mailView.compose.inputs[fieldTo].Value() != "q" {
		t.Errorf("'q' should be typed into the form, got %q", m.mailView.compose.inputs[fieldTo].Value())
	}
	focusBefore := m.focus
	updated, _ = m.Update(keyPress("tab"))
	m = updated.(model)
	if m.focus != focusBefore {
		t.Error("tab must not change the focus row while a form is open")
	}
	if m.mailView.compose.focus != int(fieldCc) {
		t.Errorf("tab should move to Cc, got %d", m.mailView.compose.focus)
	}
	updated, _ = m.Update(keyPress("esc"))
	m = updated.(model)
	if m.mailView.CapturingInput() {
		t.Error("esc should close the form through the model")
	}
}
