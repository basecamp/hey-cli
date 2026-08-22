package tui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func ctrlT() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl})
}

func snippetPickerTestView(t *testing.T, handler http.Handler) *mailView {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	sdk := hey.NewClient(&hey.Config{BaseURL: srv.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.rootSDK = sdk
	vc.sdk = sdk
	vc.ctx = context.Background()
	view := newMailView(vc)
	view.boxes = orderBoxes(testBoxes())
	view.Update(currentPostingsLoaded(view, testPostings()))
	view.Resize(80, 24)
	return view
}

func snippetListHandler(t *testing.T, body string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/snippets.json" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
}

func settleSnippetPickerLoad(t *testing.T, view *mailView, cmd tea.Cmd) {
	t.Helper()
	batch, ok := runCmd(cmd).(tea.BatchMsg)
	if !ok || len(batch) < 2 {
		t.Fatalf("snippet picker command = %#v, want a focus/load batch", batch)
	}
	msg := runCmd(batch[len(batch)-1])
	if _, ok := msg.(snippetsLoadedMsg); !ok {
		t.Fatalf("snippet load returned %#v", msg)
	}
	view.Update(msg)
}

func openLoadedSnippetPicker(t *testing.T, view *mailView) *composeForm {
	t.Helper()
	view.HandleContentKey(keyPress("c"))
	form := composeModal(view)
	form.focus = form.bodyIndex()
	_ = form.focusCurrent()
	settleSnippetPickerLoad(t, view, view.HandleContentKey(ctrlT()))
	if form.snippetPicker == nil || form.snippetPicker.loading {
		t.Fatal("snippet picker did not finish loading")
	}
	return form
}

func TestSnippetPickerLoadsFiltersAndInsertsAtCursor(t *testing.T) {
	view := snippetPickerTestView(t, snippetListHandler(t, `[
		{"id":1,"name":"Office hours","content":"Monday through Thursday"},
		{"id":2,"name":"Scheduling reply","content":"Tuesday works"}
	]`))
	view.HandleContentKey(keyPress("c"))
	form := composeModal(view)
	form.focus = form.bodyIndex()
	_ = form.focusCurrent()
	form.body.SetValue("Hello world")
	for range 5 {
		form.body, _ = form.body.Update(keyPress("left"))
	}

	cmd := view.HandleContentKey(ctrlT())
	if form.snippetPicker == nil || !strings.Contains(view.View(), "Loading snippets") {
		t.Fatalf("picker loading view = %q", view.View())
	}
	settleSnippetPickerLoad(t, view, cmd)
	if !strings.Contains(view.View(), "Office hours") || !strings.Contains(view.View(), "Scheduling reply") {
		t.Fatalf("picker view = %q", view.View())
	}

	typeText(view, "sched")
	if got := len(form.snippetPicker.filtered); got != 1 || form.snippetPicker.filtered[0].Name != "Scheduling reply" {
		t.Fatalf("filtered snippets = %#v", form.snippetPicker.filtered)
	}
	view.HandleContentKey(keyPress("enter"))
	if form.snippetPicker != nil {
		t.Fatal("picker should close after insertion")
	}
	if got := form.body.Value(); got != "Hello Tuesday worksworld" {
		t.Errorf("body = %q", got)
	}
	if form.focus != form.bodyIndex() {
		t.Errorf("focus = %d, want body", form.focus)
	}
}

func TestReplySnippetPickerUsesTheThreadAccount(t *testing.T) {
	view, recorder := composeTestServer(t)
	view.Resize(80, 24)
	loaded := runCmd(view.loadReplyContext(100, "Quarterly planning"))
	contextMsg, ok := loaded.(replyContextLoadedMsg)
	if !ok || contextMsg.err != nil {
		t.Fatalf("reply context = %#v", loaded)
	}
	view.Update(contextMsg)
	form := composeModal(view)
	settleSnippetPickerLoad(t, view, view.HandleContentKey(ctrlT()))
	if recorder.path != "/snippets.json" || recorder.account != "9" {
		t.Fatalf("snippet path/account = %s/%q", recorder.path, recorder.account)
	}
	view.HandleContentKey(keyPress("enter"))
	if form.body.Value() != "Tuesday works" {
		t.Errorf("reply body = %q", form.body.Value())
	}
}

func TestForwardFormCanInsertASnippet(t *testing.T) {
	view, _ := composeTestServer(t)
	view.Resize(80, 24)
	loaded := runCmd(view.HandleContentKey(keyPress("f")))
	contextMsg, ok := loaded.(forwardContextLoadedMsg)
	if !ok || contextMsg.err != nil {
		t.Fatalf("forward context = %#v", loaded)
	}
	view.Update(contextMsg)
	form := composeModal(view)
	settleSnippetPickerLoad(t, view, view.HandleContentKey(ctrlT()))
	view.HandleContentKey(keyPress("enter"))
	if form.body.Value() != "Tuesday works" {
		t.Errorf("forward note = %q", form.body.Value())
	}
}

func TestSnippetPickerCancelPreservesDraftAndFocus(t *testing.T) {
	view := snippetPickerTestView(t, snippetListHandler(t, `[{"id":1,"name":"Greeting","content":"Hello"}]`))
	view.HandleContentKey(keyPress("c"))
	form := composeModal(view)
	form.inputs[fieldTo].SetValue("sam@example.com")
	form.focus = int(fieldSubject)
	settleSnippetPickerLoad(t, view, view.HandleContentKey(ctrlT()))
	typeText(view, "greet")
	view.HandleContentKey(keyPress("esc"))

	if composeModal(view) != form || form.snippetPicker != nil {
		t.Fatal("escape should return to the same compose form")
	}
	if form.inputs[fieldTo].Value() != "sam@example.com" || form.body.Value() != "" {
		t.Error("escape changed the draft")
	}
	if form.focus != int(fieldSubject) {
		t.Errorf("focus = %d, want subject", form.focus)
	}
}

func TestSnippetPickerCanInsertMoreThanOnceWithoutReloading(t *testing.T) {
	requests := 0
	view := snippetPickerTestView(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":1,"name":"Greeting","content":"Hello"}]`)
	}))
	form := openLoadedSnippetPicker(t, view)
	view.HandleContentKey(keyPress("enter"))
	if got := form.body.Value(); got != "Hello" {
		t.Fatalf("first insertion = %q", got)
	}

	cmd := view.HandleContentKey(ctrlT())
	if cmd == nil || form.snippetPicker == nil || form.snippetPicker.loading {
		t.Fatal("cached snippets should open immediately")
	}
	view.HandleContentKey(keyPress("enter"))
	if got := form.body.Value(); got != "HelloHello" {
		t.Errorf("repeated insertion = %q", got)
	}
	if requests != 1 {
		t.Errorf("snippet requests = %d, want 1", requests)
	}
}

func TestSnippetPickerFailureKeepsDraft(t *testing.T) {
	view := snippetPickerTestView(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	view.HandleContentKey(keyPress("c"))
	form := composeModal(view)
	form.focus = form.bodyIndex()
	form.body.SetValue("Keep this draft")
	settleSnippetPickerLoad(t, view, view.HandleContentKey(ctrlT()))

	if form.snippetPicker.err == nil || !strings.Contains(view.View(), "Could not load snippets") {
		t.Fatalf("picker error view = %q", view.View())
	}
	view.HandleContentKey(keyPress("esc"))
	if form.body.Value() != "Keep this draft" || composeModal(view) != form {
		t.Error("failed snippet read lost the draft")
	}
}

func TestSnippetPickerEmptyAndNoMatchesStates(t *testing.T) {
	empty := snippetPickerTestView(t, snippetListHandler(t, `[]`))
	openLoadedSnippetPicker(t, empty)
	if !strings.Contains(empty.View(), "No snippets yet") {
		t.Errorf("empty view = %q", empty.View())
	}

	filtered := snippetPickerTestView(t, snippetListHandler(t, `[{"id":1,"name":"Greeting","content":"Hello"}]`))
	openLoadedSnippetPicker(t, filtered)
	typeText(filtered, "missing")
	if !strings.Contains(filtered.View(), "No matching snippets") {
		t.Errorf("filtered view = %q", filtered.View())
	}
}

func TestSnippetPickerSanitizesNames(t *testing.T) {
	view := snippetPickerTestView(t, snippetListHandler(t, `[{"id":1,"name":"Safe\u001b[31mRed","content":"Hello"}]`))
	openLoadedSnippetPicker(t, view)
	if output := view.View(); strings.Contains(output, "\x1b[31mRed") || !strings.Contains(output, "SafeRed") {
		t.Errorf("picker output = %q", output)
	}
}

func TestStaleSnippetResponseCannotReachAnotherComposeForm(t *testing.T) {
	view := snippetPickerTestView(t, snippetListHandler(t, `[{"id":1,"name":"Greeting","content":"Hello"}]`))
	view.HandleContentKey(keyPress("c"))
	first := composeModal(view)
	batch := runCmd(view.HandleContentKey(ctrlT())).(tea.BatchMsg)
	view.HandleContentKey(keyPress("esc")) // back to compose
	view.HandleContentKey(keyPress("esc")) // close compose
	view.HandleContentKey(keyPress("c"))
	second := composeModal(view)

	view.Update(runCmd(batch[len(batch)-1]))
	if first == second || second.snippetsLoaded || second.snippetPicker != nil {
		t.Fatal("stale snippet response changed the new compose form")
	}
}

func TestSnippetLoadedMessageKeepsErrorsOnTheMatchingForm(t *testing.T) {
	view := mailWithPostings()
	view.HandleContentKey(keyPress("c"))
	form := composeModal(view)
	form.snippetRequestID = 2
	form.snippetPicker = newSnippetPicker(form.focus)
	view.Update(snippetsLoadedMsg{form: form, requestID: 1, err: errors.New("stale")})
	if form.snippetPicker.err != nil {
		t.Fatal("stale error reached the picker")
	}
}
