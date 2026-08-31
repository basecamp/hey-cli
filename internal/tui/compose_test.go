package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
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
		case "/messages/501.json":
			_, _ = w.Write([]byte(`{"id":501,"subject":"Quarterly planning","sender":{"id":3,"name":"Rick Sanchez","email_address":"rick@example.com"},
				"addressed":{"directly":[{"id":1,"name":"Jane Doe","email_address":"jane@example.com"}]}}`))
		case "/entries/501/forwards/new.json":
			_, _ = w.Write([]byte(`{"subject":"Fwd: Quarterly planning","content":"<div>Quoted message</div>"}`))
		case "/snippets.json":
			rec.method, rec.path = r.Method, r.URL.Path
			rec.account = r.URL.Query().Get("filtered_account_id")
			_, _ = w.Write([]byte(`[{"id":1,"name":"Scheduling reply","content":"Tuesday works"}]`))
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
	if !v.CapturingInput() || composeModal(v) == nil || composeModal(v).mode != composeNew {
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
	f := composeModal(v)
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
	if !composeModal(v).isError || !strings.Contains(composeModal(v).status, "recipient") {
		t.Errorf("expected a recipient error, got %q", composeModal(v).status)
	}
	typeText(v, "a@b.com")
	v.HandleContentKey(keyPress("tab")) // cc
	v.HandleContentKey(keyPress("tab")) // bcc
	v.HandleContentKey(keyPress("tab")) // subject
	if cmd := v.HandleContentKey(ctrlS()); cmd != nil {
		t.Fatal("missing subject must not send")
	}
	if !strings.Contains(composeModal(v).status, "Subject") {
		t.Errorf("expected a subject error, got %q", composeModal(v).status)
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
	if !composeModal(v).sending {
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
	if m["subject"] != "Hello" || m["content"] != "<p>Body text</p>" {
		t.Errorf("message payload = %v", m)
	}
	addressed := rec.body["entry"].(map[string]any)["addressed"].(map[string]any)
	if got := addressed["directly"].([]any); len(got) != 2 || got[0] != "a@b.com" {
		t.Errorf("directly = %v", got)
	}
	if got := addressed["copied"].([]any); len(got) != 1 || got[0] != "cc@d.com" {
		t.Errorf("copied = %v", got)
	}

	answer, _ := v.Update(sent)
	if composeModal(v) != nil {
		t.Error("form should close after a successful send")
	}
	if toast := deliverToView(v, answer); toast != "Message sent" {
		t.Errorf("expected a sent toast, got %q", toast)
	}
}

func TestComposeSendFailureKeepsForm(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("c"))
	composeModal(v).sending = true
	v.Update(composeSentMsg{label: "Message sent", err: io.ErrUnexpectedEOF})
	if composeModal(v) == nil {
		t.Fatal("form should stay open so the user can retry")
	}
	if composeModal(v).sending || !composeModal(v).isError || !strings.Contains(composeModal(v).status, "Send failed") {
		t.Errorf("expected an inline error, got sending=%v status=%q", composeModal(v).sending, composeModal(v).status)
	}
}

func TestReplyFormPrefillsAndSends(t *testing.T) {
	v, rec := composeTestServer(t)
	v.Resize(80, 30)
	v.Update(replyContextLoadedMsg{
		boxID: 1, topicID: 7, topicName: "Kitchen", entryID: 99, subject: "Re: Kitchen",
		to: []string{"jane@x.com"}, cc: []string{"bob@x.com"},
	})
	f := composeModal(v)
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
	if rec.body["message"].(map[string]any)["content"] != "<p>Thanks!</p>" {
		t.Errorf("body = %v", rec.body)
	}
	// HEY never derives a reply's subject, so the reply carries the prefilled one.
	if rec.body["message"].(map[string]any)["subject"] != "Re: Kitchen" {
		t.Errorf("subject = %v, want Re: Kitchen", rec.body["message"].(map[string]any)["subject"])
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
	// The recipients come from the entry the reply answers, and reach whoever wrote it.
	if want := []string{"jane@example.com", "rick@example.com"}; !slices.Equal(ctxMsg.to, want) {
		t.Errorf("to = %v, want %v", ctxMsg.to, want)
	}
	// So does the subject, prefixed the way HEY derives a reply's.
	if ctxMsg.subject != "Re: Quarterly planning" {
		t.Errorf("subject = %q, want %q", ctxMsg.subject, "Re: Quarterly planning")
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

func TestRecipientsForReplyTo(t *testing.T) {
	contact := func(address string) generated.Contact {
		return generated.Contact{EmailAddress: address}
	}

	for _, testCase := range []struct {
		name           string
		message        generated.Message
		wantTo, wantCC []string
		wantBCC        []string
	}{
		{
			name: "the sender joins the To line",
			message: generated.Message{
				Sender: contact("rick@example.com"),
				Addressed: generated.Addressed{
					Directly:    []generated.Contact{contact("jane@example.com")},
					Copied:      []generated.Contact{contact("cc@example.com")},
					Blindcopied: []generated.Contact{contact("bcc@example.com")},
				},
			},
			wantTo:  []string{"jane@example.com", "rick@example.com"},
			wantCC:  []string{"cc@example.com"},
			wantBCC: []string{"bcc@example.com"},
		},
		{
			name: "the creator stands in for a missing sender",
			message: generated.Message{
				Creator:   contact("rick@example.com"),
				Addressed: generated.Addressed{Directly: []generated.Contact{contact("jane@example.com")}},
			},
			wantTo: []string{"jane@example.com", "rick@example.com"},
		},
		{
			name: "the sender is never addressed twice",
			message: generated.Message{
				Sender: contact("Rick@example.com"),
				Addressed: generated.Addressed{
					Directly: []generated.Contact{contact("rick@example.com")},
					Copied:   []generated.Contact{contact("RICK@example.com")},
				},
			},
			wantTo: []string{"Rick@example.com"},
		},
		{
			name:    "an entry HEY tells us nothing about addresses nobody",
			message: generated.Message{},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			to, cc, bcc := recipientsForReplyTo(testCase.message)
			if !slices.Equal(to, testCase.wantTo) {
				t.Errorf("to = %v, want %v", to, testCase.wantTo)
			}
			if !slices.Equal(cc, testCase.wantCC) {
				t.Errorf("cc = %v, want %v", cc, testCase.wantCC)
			}
			if !slices.Equal(bcc, testCase.wantBCC) {
				t.Errorf("bcc = %v, want %v", bcc, testCase.wantBCC)
			}
		})
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

	f := composeModal(v)
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
	wantContent := `<p>For your review</p><br><div>Quoted message</div>`
	if message["content"] != wantContent {
		t.Errorf("message content = %q, want %q", message["content"], wantContent)
	}
	addressed := rec.body["entry"].(map[string]any)["addressed"].(map[string]any)
	if got := addressed["directly"].([]any); len(got) != 1 || got[0] != "alice@example.com" {
		t.Errorf("directly = %v", got)
	}

	answer, _ := v.Update(sent)
	if toast := deliverToView(v, answer); composeModal(v) != nil || toast != "Message forwarded" {
		t.Errorf("forward completion = compose %v toast %q", composeModal(v), toast)
	}
}

// Sending from inside a thread used to leave its confirmation in the posting list's
// header, which a thread covers — so it was said to nobody. A toast belongs to the
// model and is drawn over whatever the section is showing.
func TestForwardCompletionIsSaidFromInsideAThread(t *testing.T) {
	v := mailWithPostings()
	v.Resize(80, 30)
	v.inThread = true
	v.topicViewport.SetContent("Original thread")
	v.modal = newForwardForm(forwardContextLoadedMsg{
		topicName: "Quarterly planning",
		subject:   "Fwd: Quarterly planning",
		content:   "<div>Quoted message</div>",
	}, v.vc.styles)

	answer, _ := v.Update(composeSentMsg{label: "Message forwarded"})

	if composeModal(v) != nil {
		t.Error("forward form should close after sending")
	}
	if toast := deliverToView(v, answer); toast != "Message forwarded" {
		t.Errorf("toast = %q", toast)
	}
	if view := v.View(); !strings.Contains(view, "Original thread") {
		t.Errorf("the thread should still be on screen, got %q", view)
	}
}

func TestForwardKeysInThreadLoadContext(t *testing.T) {
	for _, key := range []string{"f", "F"} {
		v := mailWithPostings()
		v.inThread = true
		v.topicID = 123
		cmd := v.HandleContentKey(keyPress(key))
		if cmd == nil || !v.requests.loading || v.requests.kind != mailRequestForward {
			t.Fatalf("%q in a thread should start loading the forward context", key)
		}
	}
}

func TestReplyKeysInThreadLoadContext(t *testing.T) {
	for _, key := range []string{"r", "R"} {
		v := mailWithPostings()
		v.inThread = true
		v.topicID = 123
		cmd := v.HandleContentKey(keyPress(key))
		if cmd == nil || !v.requests.loading || v.requests.kind != mailRequestReply {
			t.Fatalf("%q in a thread should start loading the reply context", key)
		}
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
	if composeModal(m.mailView) == nil || composeModal(m.mailView).inputs[fieldTo].Value() != "q" {
		t.Errorf("'q' should be typed into the form, got %q", composeModal(m.mailView).inputs[fieldTo].Value())
	}
	focusBefore := m.focus
	updated, _ = m.Update(keyPress("tab"))
	m = updated.(model)
	if m.focus != focusBefore {
		t.Error("tab must not change the focus row while a form is open")
	}
	if composeModal(m.mailView).focus != int(fieldCc) {
		t.Errorf("tab should move to Cc, got %d", composeModal(m.mailView).focus)
	}
	updated, _ = m.Update(keyPress("esc"))
	m = updated.(model)
	if m.mailView.CapturingInput() {
		t.Error("esc should close the form through the model")
	}
}

func TestComposeMarkdownRendersLiveInTheEditor(t *testing.T) {
	v, _ := composeTestServer(t)
	v.Resize(80, 30)
	v.HandleContentKey(keyPress("c"))
	f := composeModal(v)
	f.focus = f.bodyIndex()
	f.focusCurrent()
	typeText(v, "A **bold** plan")

	// The editor styles the Markdown as it is typed: the word turns bold, the
	// markers stay visible, and no key needs pressing to see it.
	view := v.View()
	if !strings.Contains(view, "\x1b[1mbold") {
		t.Errorf("the editor should render **bold** bold, got %q", view)
	}
	if !strings.Contains(view, "**") {
		t.Errorf("the markers should stay on screen, got %q", view)
	}
	typeText(v, "!")
	if got := f.body.Value(); got != "A **bold** plan!" {
		t.Errorf("typing should keep reaching the body: %q", got)
	}
}

func TestComposeBodyIsMarkdown(t *testing.T) {
	f := newComposeForm(composeNew, styles{})
	f.body.SetValue("**Bold** move\nsee the list:\n\n- budget\n- hiring")

	_, _, _, _, body := f.values()
	want := "<p><strong>Bold</strong> move<br>see the list:</p>\n<ul>\n<li>budget</li>\n<li>hiring</li>\n</ul>"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestForwardNoteIsMarkdown(t *testing.T) {
	f := newComposeForm(composeForward, styles{})
	f.forwardedContent = "<div>Quoted message</div>"
	f.body.SetValue("For **your** review")

	_, _, _, _, body := f.values()
	want := "<p>For <strong>your</strong> review</p><br><div>Quoted message</div>"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}
