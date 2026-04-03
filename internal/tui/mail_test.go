package tui

import (
	"fmt"
	"strings"
	"testing"

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
	if len(bindings) != 0 {
		t.Error("mail should have no extra bindings in thread mode")
	}
}

// --- Box shortcuts ---

func TestMailViewBoxShortcut(t *testing.T) {
	v := mailWithPostings()
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
}

func TestMailViewBoxShortcutNoOp(t *testing.T) {
	v := mailWithPostings()
	cmd := v.handleBoxShortcut("I") // Imbox — already selected
	if cmd != nil {
		t.Error("shortcut for current box should be no-op")
	}
}
