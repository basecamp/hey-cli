package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/models"
)

func testVC() *viewContext {
	return &viewContext{styles: newStyles(), width: 80, height: 30}
}

func mailWithPostings() *mailView {
	v := newMailView(testVC())
	v.boxes = orderBoxes(testBoxes())
	v.boxIndex = 0
	v.Update(postingsLoadedMsg{postings: testPostings()})
	return v
}

type recordedMailRequest struct {
	method string
	path   string
	body   struct {
		PostingIDs []int64 `json:"posting_ids"`
		BoxID      int64   `json:"box_id"`
	}
}

func mailWithTestServer(t *testing.T, status int) (*mailView, *recordedMailRequest) {
	t.Helper()

	recorded := &recordedMailRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boxes.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"id":1,"kind":"imbox","name":"Imbox"},
				{"id":2,"kind":"feedbox","name":"The Feed"},
				{"id":3,"kind":"asidebox","name":"Set Aside"},
				{"id":4,"kind":"laterbox","name":"Reply Later"},
				{"id":5,"kind":"trailbox","name":"Paper Trail"}
			]`))
			return
		}

		recorded.method = r.Method
		recorded.path = r.URL.Path
		if r.URL.Path == "/topics/100/entries" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`<article id="entry_501" data-entry-id="501">
				<span id="sender_entry_501">Alice</span>
				<time datetime="2026-08-19T09:00:00Z"></time>
				<iframe srcdoc="&lt;div class=&quot;trix-content&quot;&gt;Message body&lt;/div&gt;"></iframe>
			</article>`))
			return
		}

		_ = json.NewDecoder(r.Body).Decode(&recorded.body)
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(
		&hey.Config{BaseURL: server.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	vc := testVC()
	vc.sdk = client
	vc.ctx = context.Background()
	v := newMailView(vc)
	v.Resize(vc.width, vc.height)
	v.boxes = orderBoxes(testBoxes())
	v.Update(postingsLoadedMsg{postings: testPostings()})
	return v, recorded
}

// --- Init ---

func TestMailViewInitFetchesBoxes(t *testing.T) {
	v := newMailView(testVC())
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init with no boxes should return a fetch command")
	}
	if !v.loading {
		t.Error("Init should set loading = true")
	}
}

func TestMailViewInitRefetchesWhenBoxesLoaded(t *testing.T) {
	v := newMailView(testVC())
	v.boxes = testBoxes()
	v.boxIndex = 0
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init with boxes should return a fetch command for current box")
	}
	if !v.loading {
		t.Error("Init should set loading = true")
	}
}

// --- Update: message routing ---

func TestMailViewHandlesBoxesLoaded(t *testing.T) {
	v := newMailView(testVC())
	_, consumed := v.Update(boxesLoadedMsg(testBoxes()))
	if !consumed {
		t.Error("boxesLoadedMsg should be consumed")
	}
	if len(v.boxes) != 3 {
		t.Errorf("expected 3 boxes, got %d", len(v.boxes))
	}
}

func TestMailViewHandlesPostingsLoaded(t *testing.T) {
	v := newMailView(testVC())
	v.boxes = testBoxes()
	v.loading = true

	_, consumed := v.Update(postingsLoadedMsg{postings: testPostings()})
	if !consumed {
		t.Error("postingsLoadedMsg should be consumed")
	}
	if v.loading {
		t.Error("loading should be false after postings loaded")
	}
	if len(v.postingList.postings) != 2 {
		t.Errorf("expected 2 postings, got %d", len(v.postingList.postings))
	}
}

func TestMailViewHandlesTopicLoaded(t *testing.T) {
	v := mailWithPostings()
	v.loading = true

	_, consumed := v.Update(topicLoadedMsg{
		title:   "Test topic",
		entries: []models.Entry{{Creator: models.Contact{Name: "Alice"}, Body: "hello"}},
	})
	if !consumed {
		t.Error("topicLoadedMsg should be consumed")
	}
	if !v.inThread {
		t.Error("should be in thread after topic loaded")
	}
	if v.loading {
		t.Error("loading should be false")
	}
}

func TestMailViewIgnoresUnrelatedMessages(t *testing.T) {
	v := newMailView(testVC())
	_, consumed := v.Update(calendarsLoadedMsg{})
	if consumed {
		t.Error("calendarsLoadedMsg should not be consumed by mailView")
	}
}

// --- Posting actions ---

func TestMailViewPostingKeysCallExpectedEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		path    string
		boxID   int64
		removes bool
		notice  string
		seen    bool
	}{
		{"reply later", "l", "/postings/moves.json", 4, false, "moved to Reply Later", false},
		{"set aside", "a", "/postings/moves.json", 3, true, "moved to Set Aside", false},
		{"seen", "e", "/postings/seen.json", 0, false, "marked as seen", true},
		{"feed", "d", "/postings/moves.json", 2, true, "moved to The Feed", false},
		{"paper trail", "p", "/postings/moves.json", 5, true, "moved to Paper Trail", false},
		{"trash", "t", "/postings/trash.json", 0, true, "moved to Trash", false},
		{"mute", "-", "/postings/mutings.json", 0, true, "muted", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)

			msg := runCmd(v.HandleContentKey(keyPress(tt.key)))
			done, ok := msg.(postingActionDoneMsg)
			if !ok || done.err != nil {
				t.Fatalf("posting command returned %#v", msg)
			}
			v.Update(done)

			if recorded.method != http.MethodPost || recorded.path != tt.path {
				t.Errorf("request = %s %s, want POST %s", recorded.method, recorded.path, tt.path)
			}
			if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
				t.Errorf("posting_ids = %v, want [100]", recorded.body.PostingIDs)
			}
			if tt.boxID != 0 && recorded.body.BoxID != tt.boxID {
				t.Errorf("box_id = %d, want %d", recorded.body.BoxID, tt.boxID)
			}

			wantPostings := 2
			if tt.removes {
				wantPostings = 1
			}
			if len(v.postingList.postings) != wantPostings {
				t.Errorf("posting count = %d, want %d", len(v.postingList.postings), wantPostings)
			}
			if tt.seen && !v.postingList.postings[0].Seen {
				t.Error("selected posting should be marked seen")
			}
			if v.notice != tt.notice || !strings.Contains(v.View(), tt.notice) {
				t.Errorf("notice = %q, want visible %q", v.notice, tt.notice)
			}
		})
	}
}

func TestMailViewPostingKeyFailureKeepsPosting(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusInternalServerError)

	msg := runCmd(v.HandleContentKey(keyPress("t")))
	done, ok := msg.(postingActionDoneMsg)
	if !ok || done.err == nil {
		t.Fatalf("posting command returned %#v, want an action error", msg)
	}
	errCmd, consumed := v.Update(done)

	if !consumed || errCmd == nil {
		t.Fatal("failed action should return an error command")
	}
	errResult := runCmd(errCmd)
	if _, ok := errResult.(errMsg); !ok {
		t.Errorf("error command returned %T, want errMsg", errResult)
	}
	if len(v.postingList.postings) != 2 {
		t.Error("failed action should keep the selected posting")
	}
	if v.notice != "" {
		t.Errorf("failed action notice = %q, want empty", v.notice)
	}
}

func TestMailViewPostingActionRemoves(t *testing.T) {
	v := mailWithPostings()
	if len(v.postingList.postings) != 2 {
		t.Fatalf("expected 2 postings, got %d", len(v.postingList.postings))
	}

	v.Update(postingActionDoneMsg{action: "moved to Trash", removes: true})
	if len(v.postingList.postings) != 1 {
		t.Errorf("expected 1 posting after remove, got %d", len(v.postingList.postings))
	}
}

func TestMailViewPostingActionMarksSeen(t *testing.T) {
	v := mailWithPostings()
	if v.postingList.postings[0].Seen {
		t.Fatal("first posting should be unseen")
	}

	v.Update(postingActionDoneMsg{action: "marked as seen"})
	if !v.postingList.postings[0].Seen {
		t.Error("first posting should be seen after action")
	}
}

func TestMailViewPostingActionError(t *testing.T) {
	v := mailWithPostings()
	cmd, consumed := v.Update(postingActionDoneMsg{err: fmt.Errorf("network error")})

	if !consumed {
		t.Error("postingActionDoneMsg with error should be consumed")
	}
	if cmd == nil {
		t.Fatal("should return a command that produces errMsg")
	}
	msg := cmd()
	if _, ok := msg.(errMsg); !ok {
		t.Errorf("command produced %T, want errMsg", msg)
	}
}

// --- Content key handling ---

func TestMailViewEnterOpensSelectedThread(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusOK)

	msg := runCmd(v.HandleContentKey(keyPress("enter")))
	loaded, ok := msg.(topicLoadedMsg)
	if !ok {
		t.Fatalf("open command returned %T, want topicLoadedMsg", msg)
	}
	v.Update(loaded)

	if recorded.method != http.MethodGet || recorded.path != "/topics/100/entries" {
		t.Errorf("request = %s %s, want GET /topics/100/entries", recorded.method, recorded.path)
	}
	if !v.inThread || v.topicID != 100 || v.topicName != "Hello world" {
		t.Errorf("thread state = open:%v id:%d name:%q", v.inThread, v.topicID, v.topicName)
	}
	if !strings.Contains(v.View(), "Alice") || !strings.Contains(v.View(), "Message body") {
		t.Errorf("thread view does not contain the fetched entry: %q", v.View())
	}
}

func TestMailViewContentKeyUpDown(t *testing.T) {
	v := mailWithPostings()

	if v.postingList.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", v.postingList.cursor)
	}

	v.HandleContentKey(keyPress("down"))
	if v.postingList.cursor != 1 {
		t.Errorf("after down: cursor = %d, want 1", v.postingList.cursor)
	}

	v.HandleContentKey(keyPress("up"))
	if v.postingList.cursor != 0 {
		t.Errorf("after up: cursor = %d, want 0", v.postingList.cursor)
	}
}

func TestMailViewContentKeyInThread(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true

	// In thread mode, content keys go to viewport (no crash)
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("up"))
}

// --- Subnav ---

func TestMailViewSubnavItems(t *testing.T) {
	v := mailWithPostings()
	items, selected, label, centered := v.SubnavItems()

	if len(items) != 3 {
		t.Errorf("expected 3 subnav items, got %d", len(items))
	}
	if selected != 0 {
		t.Errorf("selected = %d, want 0", selected)
	}
	if label != "Imbox" {
		t.Errorf("label = %q, want Imbox", label)
	}
	if !centered {
		t.Error("mail subnav should be centered")
	}
}

func TestMailViewSubnavLeftRight(t *testing.T) {
	v := mailWithPostings()

	// Can't go left from first box
	v.SubnavLeft()
	if v.boxIndex != 0 {
		t.Errorf("SubnavLeft at 0: boxIndex = %d, want 0", v.boxIndex)
	}

	// Go right
	v.SubnavRight()
	if v.boxIndex != 1 {
		t.Errorf("after SubnavRight: boxIndex = %d, want 1", v.boxIndex)
	}
	if !v.loading {
		t.Error("SubnavRight should set loading")
	}

	v.loading = false
	v.SubnavRight()
	if v.boxIndex != 2 {
		t.Errorf("after second SubnavRight: boxIndex = %d, want 2", v.boxIndex)
	}

	// Can't go right past last box
	v.loading = false
	v.SubnavRight()
	if v.boxIndex != 2 {
		t.Errorf("SubnavRight at end: boxIndex = %d, want 2", v.boxIndex)
	}
}

// --- Thread state ---

func TestMailViewInThread(t *testing.T) {
	v := newMailView(testVC())
	if v.InThread() {
		t.Error("should not be in thread initially")
	}
	v.inThread = true
	if !v.InThread() {
		t.Error("InThread should return true")
	}
	v.ExitThread()
	if v.InThread() {
		t.Error("ExitThread should clear thread state")
	}
}

// --- View rendering ---

func TestMailViewRendersPostings(t *testing.T) {
	v := mailWithPostings()
	v.Resize(80, 30)
	view := v.View()
	if !strings.Contains(view, "Hello world") {
		t.Error("view should contain posting summary")
	}
}

func TestMailViewRendersEmptyList(t *testing.T) {
	v := newMailView(testVC())
	v.Resize(80, 30)
	view := v.View()
	if !strings.Contains(view, "(empty)") {
		t.Error("view should show (empty) with no postings")
	}
}

// --- Help bindings ---

func TestMailViewHelpBindings(t *testing.T) {
	v := mailWithPostings()
	bindings := v.HelpBindings()
	if len(bindings) == 0 {
		t.Fatal("mail should have help bindings for posting actions")
	}

	keys := make(map[string]bool)
	for _, b := range bindings {
		keys[b.key] = true
	}
	for _, expected := range []string{"r", "f", "e", "l", "a", "t"} {
		if !keys[expected] {
			t.Errorf("missing help binding for key %q", expected)
		}
	}
}

func TestMailViewHelpBindingsInThread(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	bindings := v.HelpBindings()
	if len(bindings) != 1 || bindings[0].key != "r" {
		t.Errorf("thread mode should offer only reply, got %v", bindings)
	}
}

// --- Box shortcuts ---

func TestMailViewBoxShortcut(t *testing.T) {
	v := mailWithPostings()
	v.notice = "previous action"
	cmd := v.handleBoxShortcut("F") // The Feed
	if cmd == nil {
		t.Fatal("box shortcut 'F' should return a command")
	}
	if v.boxIndex == 0 {
		t.Error("boxIndex should have changed")
	}
	if !v.loading {
		t.Error("should be loading after box switch")
	}
	if v.notice != "" {
		t.Errorf("box switch should clear the previous notice, got %q", v.notice)
	}
}

func TestMailViewBoxShortcutNoOp(t *testing.T) {
	v := mailWithPostings()
	cmd := v.handleBoxShortcut("I") // Imbox — already selected
	if cmd != nil {
		t.Error("shortcut for current box should be no-op")
	}
}
