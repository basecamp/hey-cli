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
	account      string
	body         map[string]any
}) {
	t.Helper()
	rec := &struct {
		method, path string
		account      string
		body         map[string]any
	}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/identity.json":
			_, _ = w.Write([]byte(`{"id":1,"accounts":[{"id":9,"status":"active"}],"senders":[{"id":42,"account_id":9,"default":true}]}`))
		case "/topics/100.json":
			_, _ = w.Write([]byte(`{"id":100,"account_id":9,"name":"Quarterly planning","entries":[{"id":500},{"id":501}]}`))
		case "/topics/100":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<span class="entry__full-recipients"><a title="jane@example.com">Jane</a></span>`))
		case "/entries/501/forwards/new.json":
			_, _ = w.Write([]byte(`{"subject":"Fwd: Quarterly planning","content":"<div>Quoted message</div>"}`))
		default:
			rec.method, rec.path = r.Method, r.URL.Path
			rec.account = r.URL.Query().Get("filtered_account_id")
			if b, _ := io.ReadAll(r.Body); len(b) > 0 {
				_ = json.Unmarshal(b, &rec.body)
			}
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{}`))
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
		boxID: 1, topicID: 7, topicName: "Kitchen", entryID: 99,
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

func TestReplyLoadsAndSendsThroughThreadAccount(t *testing.T) {
	v, rec := composeTestServer(t)
	v.Resize(80, 30)

	loaded := runCmd(v.loadReplyContext(100, "Quarterly planning"))
	ctxMsg, ok := loaded.(replyContextLoadedMsg)
	if !ok || ctxMsg.err != nil {
		t.Fatalf("reply command returned %#v", loaded)
	}
	if accountID, ok := ctxMsg.sdk.AccountID(); !ok || accountID != 9 {
		t.Fatalf("reply SDK account = %d, %v", accountID, ok)
	}
	v.Update(ctxMsg)
	typeText(v, "Thanks!")
	msg := runCmd(v.HandleContentKey(ctrlS()))
	if sent, ok := msg.(composeSentMsg); !ok || sent.err != nil {
		t.Fatalf("reply send returned %#v", msg)
	}
	if rec.path != "/entries/501/replies.json" || rec.account != "9" {
		t.Fatalf("reply path/account = %s/%q, want /entries/501/replies.json/9", rec.path, rec.account)
	}
}

func TestForwardFormLoadsLatestEntryAndSends(t *testing.T) {
	v, rec := composeTestServer(t)
	v.Resize(80, 30)

	loaded := runCmd(v.HandleContentKey(keyPress("f")))
	ctxMsg, ok := loaded.(forwardContextLoadedMsg)
	if !ok || ctxMsg.err != nil {
		t.Fatalf("forward command returned %#v", loaded)
	}
	if ctxMsg.subject != "Fwd: Quarterly planning" || ctxMsg.content != "<div>Quoted message</div>" {
		t.Errorf("forward draft = subject %q content %q", ctxMsg.subject, ctxMsg.content)
	}
	v.Update(ctxMsg)

	f := v.compose
	if f == nil || f.mode != composeForward {
		t.Fatal("forward context should open a forward form")
	}
	if f.inputs[fieldSubject].Value() != "Fwd: Quarterly planning" {
		t.Errorf("subject = %q", f.inputs[fieldSubject].Value())
	}
	if !strings.Contains(v.View(), "Forward: Hello world") || !strings.Contains(v.View(), "original message will be included") {
		t.Errorf("forward form view = %q", v.View())
	}

	typeText(v, "alice@example.com")
	for range 4 {
		v.HandleContentKey(keyPress("tab"))
	}
	typeText(v, "For your review")

	msg := runCmd(v.HandleContentKey(ctrlS()))
	sent, ok := msg.(composeSentMsg)
	if !ok || sent.err != nil || sent.label != "Message forwarded" {
		t.Fatalf("expected Message forwarded, got %#v", msg)
	}
	if rec.method != http.MethodPost || rec.path != "/messages.json" {
		t.Errorf("sent %s %s, want POST /messages.json", rec.method, rec.path)
	}
	if rec.account != "9" {
		t.Errorf("forward account = %q, want 9", rec.account)
	}
	message := rec.body["message"].(map[string]any)
	if message["subject"] != "Fwd: Quarterly planning" {
		t.Errorf("message subject = %v", message["subject"])
	}
	wantContent := `<div>For your review</div><br><div>Quoted message</div>`
	if message["content"] != wantContent {
		t.Errorf("message content = %q, want %q", message["content"], wantContent)
	}
	addressed := rec.body["entry"].(map[string]any)["addressed"].(map[string]any)
	if got := addressed["directly"].([]any); len(got) != 1 || got[0] != "alice@example.com" {
		t.Errorf("directly = %v", got)
	}

	v.Update(sent)
	if v.compose != nil || v.notice != "Message forwarded" {
		t.Errorf("forward completion = compose %v notice %q", v.compose, v.notice)
	}
}

func TestForwardCompletionNoticeIsVisibleInThread(t *testing.T) {
	v := mailWithPostings()
	v.Resize(80, 30)
	v.inThread = true
	v.topicViewport.SetContent("Original thread")
	v.compose = newForwardForm(forwardContextLoadedMsg{
		topicName: "Quarterly planning",
		subject:   "Fwd: Quarterly planning",
		content:   "<div>Quoted message</div>",
	}, v.vc.styles)

	v.Update(composeSentMsg{label: "Message forwarded"})

	if v.compose != nil {
		t.Error("forward form should close after sending")
	}
	if view := v.View(); !strings.Contains(view, "Message forwarded") || !strings.Contains(view, "Original thread") {
		t.Errorf("thread view should show the forwarding notice, got %q", view)
	}
}

func TestForwardKeyInThreadLoadsContext(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	v.topicID = 123
	cmd := v.HandleContentKey(keyPress("f"))
	if cmd == nil || !v.loading || v.activeRequestKind != mailRequestForward {
		t.Fatal("'f' in a thread should start loading the forward context")
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
