package tui

import (
	"bytes"
	"context"
	"encoding/json"
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

type tuiBulkReplyRequest struct {
	method string
	path   string
	query  string
	body   []byte
}

type tuiBulkReplyServerState struct {
	mu          sync.Mutex
	requests    []tuiBulkReplyRequest
	draft       string
	draftStatus int
	delivery    string
	sendStatus  int
	undoStatus  int
}

func (s *tuiBulkReplyServerState) snapshot() []tuiBulkReplyRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tuiBulkReplyRequest(nil), s.requests...)
}

func tuiBulkReplyServer(t *testing.T) (*mailView, *tuiBulkReplyServerState) {
	t.Helper()
	state := &tuiBulkReplyServerState{
		draft: `{
			"content":"<div>Signing off with a tag!</div>",
			"entries":[
				{"id":501,"topic_id":700,"topic_name":"Quarterly planning","addressed":{"directly":[{"id":31,"name":"Jane Doe","email_address":"jane@example.com"}],"copied":[{"id":32,"name":"Bob Smith","email_address":"bob@example.org"}],"blindcopied":[{"id":33,"email_address":"audit@example.com"}]}},
				{"id":502,"topic_id":701,"topic_name":"Design review","addressed":{"directly":[{"id":34,"name":"Alice Jones","email_address":"alice@example.com"}]}}
			]
		}`,
		delivery: `{"id":900,"entries_count":2,"delayed":true,"undo_send_url":"https://app.hey.com/bulk_replies/900/undo_send"}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		state.mu.Lock()
		state.requests = append(state.requests, tuiBulkReplyRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: body.Bytes()})
		draft := state.draft
		draftStatus := state.draftStatus
		delivery := state.delivery
		sendStatus := state.sendStatus
		undoStatus := state.undoStatus
		state.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/bulk_replies/new.json":
			if draftStatus != 0 {
				w.WriteHeader(draftStatus)
				_, _ = w.Write([]byte(`{"error":"No threads for replying were found"}`))
				return
			}
			_, _ = w.Write([]byte(draft))
		case r.Method == http.MethodPost && r.URL.Path == "/bulk_replies.json":
			if sendStatus != 0 {
				w.WriteHeader(sendStatus)
				_, _ = w.Write([]byte(`{"errors":["Sending is unavailable"]}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(delivery))
		case r.Method == http.MethodPost && r.URL.Path == "/bulk_replies/900/undo_send":
			if undoStatus != 0 {
				w.WriteHeader(undoStatus)
				_, _ = w.Write([]byte(`{"errors":["The undo window expired"]}`))
				return
			}
			w.Header().Set("Location", "/bulk_replies/900")
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodGet && r.URL.Path == "/bulk_replies/900":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	vc.ctx = context.Background()
	view := newMailView(vc)
	view.Resize(vc.width, vc.height)
	view.boxes = orderBoxes(testBoxes())
	view.Update(currentPostingsLoaded(view, testPostings()))
	return view, state
}

func selectTwoThreads(view *mailView) {
	space := tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "})
	view.HandleContentKey(space)
	view.HandleContentKey(keyPress("down"))
	view.HandleContentKey(space)
}

func TestTUIBulkReplyRequiresExplicitSelection(t *testing.T) {
	view, state := tuiBulkReplyServer(t)
	if cmd := view.HandleContentKey(keyPress("ctrl+b")); cmd != nil {
		t.Fatal("bulk reply without a selection should not make a request")
	}
	if view.notice != "Select threads with space before starting a bulk reply" {
		t.Errorf("notice = %q", view.notice)
	}
	if requests := state.snapshot(); len(requests) != 0 {
		t.Errorf("requests = %+v", requests)
	}
}

func TestTUIBulkReplySelectionAndPreviewShowExactRecipients(t *testing.T) {
	view, state := tuiBulkReplyServer(t)
	selectTwoThreads(view)
	if selected := view.postingList.selectedIDs(); len(selected) != 2 || selected[0] != 100 || selected[1] != 101 {
		t.Fatalf("selected IDs = %v", selected)
	}
	if rendered := view.View(); strings.Count(rendered, "✓") != 2 {
		t.Errorf("selected rows are not marked: %q", rendered)
	}

	msg := runCmd(view.HandleContentKey(keyPress("ctrl+b")))
	loaded, ok := msg.(bulkReplyDraftLoadedMsg)
	if !ok || loaded.err != nil {
		t.Fatalf("draft command returned %#v", msg)
	}
	view.Update(loaded)
	if bulkReplyModal(view) == nil || !view.CapturingInput() {
		t.Fatal("draft should open a capturing preview")
	}
	preview := view.View()
	for _, expected := range []string{"Bulk reply preview", "Quarterly planning", "Jane Doe <jane@example.com>", "Bob Smith <bob@example.org>", "audit@example.com", "Signing off with a tag!"} {
		if !strings.Contains(preview, expected) {
			t.Errorf("preview does not contain %q: %q", expected, preview)
		}
	}
	if help := view.HelpBindings(); len(help) < 2 || help[0].key != "↑↓" || help[1].key != "enter" {
		t.Errorf("preview help = %v", help)
	}
	requests := state.snapshot()
	if len(requests) != 1 || requests[0].query != "posting_ids=100%2C101" {
		t.Errorf("draft request = %+v", requests)
	}
}

func TestTUIBulkReplyPreviewScrollsThroughEveryRecipient(t *testing.T) {
	entries := make([]generated.BulkReplyEntry, 12)
	for i := range entries {
		entries[i] = generated.BulkReplyEntry{
			Id:        int64(500 + i),
			TopicId:   int64(700 + i),
			TopicName: fmt.Sprintf("Thread %02d", i+1),
			Addressed: generated.Addressed{Directly: []generated.Contact{{
				Id:           int64(900 + i),
				EmailAddress: fmt.Sprintf("recipient-%02d@example.com", i+1),
			}}},
		}
	}
	form := newBulkReplyForm([]int64{100, 101}, &generated.BulkReplyDraft{Entries: entries}, newStyles())
	form.resize(40, 8)

	if strings.Contains(form.view(), "recipient-12@example.com") {
		t.Fatal("last recipient should begin below the viewport")
	}
	view := newMailView(testVC())
	for range 100 {
		_, _ = form.handleKey(view, keyPress("down"))
	}
	if form.preview.YOffset() == 0 {
		t.Fatal("down should scroll the recipient preview")
	}
	if !strings.Contains(form.view(), "recipient-12@example.com") {
		t.Errorf("last recipient is not reviewable after scrolling: %q", form.view())
	}
}

func TestTUIBulkReplyPreviewWrapsLongRecipientsWithoutDroppingText(t *testing.T) {
	const email = "recipient-with-an-extraordinarily-long-address@example.com"
	form := newBulkReplyForm([]int64{100}, &generated.BulkReplyDraft{Entries: []generated.BulkReplyEntry{{
		Id:        501,
		TopicId:   701,
		TopicName: "A complete safety preview",
		Addressed: generated.Addressed{Directly: []generated.Contact{{Id: 901, EmailAddress: email}}},
	}}}, newStyles())

	content := form.previewContent(24)
	compacted := strings.NewReplacer("\n", "", " ", "").Replace(content)
	if !strings.Contains(compacted, email) {
		t.Errorf("wrapped preview dropped recipient text: %q", content)
	}
}

func TestTUIBulkReplyPreviewReportsSkippedThreads(t *testing.T) {
	view, state := tuiBulkReplyServer(t)
	state.draft = `{"content":"","entries":[{"id":501,"topic_id":700,"topic_name":"Quarterly planning","addressed":{"directly":[{"id":31,"email_address":"jane@example.com"}]}}]}`
	selectTwoThreads(view)
	loaded := runCmd(view.HandleContentKey(keyPress("ctrl+b"))).(bulkReplyDraftLoadedMsg)
	view.Update(loaded)
	if !strings.Contains(view.View(), "1 replyable thread · 1 skipped") {
		t.Errorf("preview = %q", view.View())
	}
}

func TestTUIBulkReplyReviewsThenSendsAndOffersUndo(t *testing.T) {
	view, state := tuiBulkReplyServer(t)
	selectTwoThreads(view)
	view.Update(runCmd(view.HandleContentKey(keyPress("ctrl+b"))))

	if cmd := view.HandleContentKey(keyPress("enter")); cmd == nil {
		t.Error("enter should focus the bulk reply editor")
	}
	if bulkReplyModal(view) == nil || !bulkReplyModal(view).composing {
		t.Fatal("preview should advance to the editor")
	}
	typeText(view, "Thanks everyone")
	cmd := view.HandleContentKey(ctrlS())
	if cmd == nil || !bulkReplyModal(view).sending {
		t.Fatal("ctrl+s should submit the bulk reply")
	}
	sent, ok := runCmd(cmd).(bulkReplySentMsg)
	if !ok || sent.err != nil {
		t.Fatalf("send returned %#v", sent)
	}
	answer, _ := view.Update(sent)
	if bulkReplyModal(view) != nil || len(view.postingList.selectedIDs()) != 0 {
		t.Error("successful send should close the form and clear selection")
	}
	toast := deliverToView(view, answer)
	if view.lastBulkReplyID != 900 || !strings.Contains(toast, "2 bulk replies queued with undo available") || !strings.Contains(toast, "press ctrl+u to undo") {
		t.Errorf("delivery state = id:%d toast:%q", view.lastBulkReplyID, toast)
	}
	if !slices.ContainsFunc(view.HelpBindings(), func(b helpBinding) bool { return b.key == "ctrl+u" }) {
		t.Errorf("help does not offer undo: %v", view.HelpBindings())
	}

	requests := state.snapshot()
	if len(requests) != 2 || requests[1].path != "/bulk_replies.json" {
		t.Fatalf("requests = %+v", requests)
	}
	var request struct {
		EntryIDs []int64 `json:"entry_ids"`
		Message  struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(requests[1].body, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.EntryIDs) != 2 || request.EntryIDs[0] != 501 || request.EntryIDs[1] != 502 {
		t.Errorf("entry IDs = %v", request.EntryIDs)
	}
	want := "<div>Thanks everyone</div><br><div>Signing off with a tag!</div>"
	if request.Message.Content != want {
		t.Errorf("content = %q, want %q", request.Message.Content, want)
	}
}

func TestTUIBulkReplyEmptyDraftNeverSends(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
	}{{"empty", 0}, {"not_found", http.StatusNotFound}} {
		t.Run(test.name, func(t *testing.T) {
			view, state := tuiBulkReplyServer(t)
			state.draft = `{"content":"","entries":[]}`
			state.draftStatus = test.status
			selectTwoThreads(view)
			loaded := runCmd(view.HandleContentKey(keyPress("ctrl+b"))).(bulkReplyDraftLoadedMsg)
			answer, _ := view.Update(loaded)
			if toast := deliverToView(view, answer); bulkReplyModal(view) != nil || !strings.Contains(toast, "nothing was sent") {
				t.Errorf("empty draft state = form:%v toast:%q", bulkReplyModal(view), toast)
			}
			if requests := state.snapshot(); len(requests) != 1 || requests[0].method != http.MethodGet {
				t.Errorf("empty draft made a mutation request: %+v", requests)
			}
		})
	}
}

func TestTUIBulkReplySendFailureKeepsEditor(t *testing.T) {
	view, state := tuiBulkReplyServer(t)
	state.sendStatus = http.StatusUnprocessableEntity
	selectTwoThreads(view)
	view.Update(runCmd(view.HandleContentKey(keyPress("ctrl+b"))))
	view.HandleContentKey(keyPress("enter"))
	typeText(view, "Thanks everyone")
	sent := runCmd(view.HandleContentKey(ctrlS())).(bulkReplySentMsg)
	if sent.err == nil {
		t.Fatal("send should fail")
	}
	view.Update(sent)
	if bulkReplyModal(view) == nil || bulkReplyModal(view).sending || !bulkReplyModal(view).isError || !strings.Contains(bulkReplyModal(view).status, "Send failed") {
		t.Errorf("failed editor state = %#v", bulkReplyModal(view))
	}
}

func TestTUIBulkReplyImmediateDeliveryHasNoUndo(t *testing.T) {
	view, state := tuiBulkReplyServer(t)
	state.delivery = `{"id":901,"entries_count":2,"delayed":false}`
	selectTwoThreads(view)
	view.Update(runCmd(view.HandleContentKey(keyPress("ctrl+b"))))
	view.HandleContentKey(keyPress("enter"))
	typeText(view, "Thanks")
	view.Update(runCmd(view.HandleContentKey(ctrlS())))
	if view.lastBulkReplyID != 0 || strings.Contains(view.notice, "undo") {
		t.Errorf("immediate delivery state = id:%d notice:%q", view.lastBulkReplyID, view.notice)
	}
}

func TestTUIBulkReplyUndoSuccessAndExpiry(t *testing.T) {
	view, _ := tuiBulkReplyServer(t)
	view.lastBulkReplyID = 900
	undone, ok := runCmd(view.HandleContentKey(keyPress("ctrl+u"))).(bulkReplyUndoneMsg)
	if !ok || undone.err != nil {
		t.Fatalf("undo returned %#v", undone)
	}
	answer, _ := view.Update(undone)
	if toast := deliverToView(view, answer); view.lastBulkReplyID != 0 || toast != "Bulk reply recalled" {
		t.Errorf("undo state = id:%d toast:%q", view.lastBulkReplyID, toast)
	}

	view, state := tuiBulkReplyServer(t)
	state.undoStatus = http.StatusUnprocessableEntity
	view.lastBulkReplyID = 900
	undone = runCmd(view.HandleContentKey(keyPress("ctrl+u"))).(bulkReplyUndoneMsg)
	if undone.err == nil {
		t.Fatal("expired undo should fail")
	}
	view.Update(undone)
	if view.lastBulkReplyID != 900 || !strings.Contains(view.notice, "Could not undo") {
		t.Errorf("expired undo state = id:%d notice:%q", view.lastBulkReplyID, view.notice)
	}
}

func TestTUIBulkReplyCanCancelPreviewAndEditor(t *testing.T) {
	for _, advance := range []bool{false, true} {
		view, _ := tuiBulkReplyServer(t)
		selectTwoThreads(view)
		view.Update(runCmd(view.HandleContentKey(keyPress("ctrl+b"))))
		if advance {
			view.HandleContentKey(keyPress("enter"))
		}
		if cmd := view.HandleContentKey(keyPress("esc")); cmd != nil {
			t.Error("cancel should not start a command")
		}
		if bulkReplyModal(view) != nil || view.CapturingInput() {
			t.Errorf("cancel did not close bulk reply (advance=%v)", advance)
		}
	}
}

func TestBulkReplyFormRequiresBodyBeforeSend(t *testing.T) {
	form := newBulkReplyForm([]int64{100}, nil, newStyles())
	form.draft.Entries = append(form.draft.Entries, generated.BulkReplyEntry{Id: 501})
	form.composing = true
	if cmd, _ := form.handleKey(newMailView(testVC()), tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl})); cmd != nil {
		t.Fatal("empty bulk reply should not submit")
	}
	if !form.isError || form.status != "Message is empty" {
		t.Errorf("validation state = error:%v status:%q", form.isError, form.status)
	}
}
