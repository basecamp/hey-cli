package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/mail"
)

// The nine modals used to be nine fields, and every test reached for the one it had
// opened. These say the same thing through the single field.
func composeModal(v *mailView) *composeForm     { return modalOf[*composeForm](v) }
func bulkReplyModal(v *mailView) *bulkReplyForm { return modalOf[*bulkReplyForm](v) }
func searchModal(v *mailView) *mailSearchForm   { return modalOf[*mailSearchForm](v) }
func moveModal(v *mailView) *movePicker         { return modalOf[*movePicker](v) }
func coverModal(v *mailView) *coverPicker       { return modalOf[*coverPicker](v) }
func folderModal(v *mailView) *folderPicker     { return modalOf[*folderPicker](v) }
func collectionModal(v *mailView) *collectionMembershipPicker {
	return modalOf[*collectionMembershipPicker](v)
}
func labelsModal(v *mailView) *labelPicker              { return modalOf[*labelPicker](v) }
func collectionsModal(v *mailView) *collectionNavPicker { return modalOf[*collectionNavPicker](v) }

// modalViewWithPostings is a mail view with one box of threads, ready for a modal to be
// opened over.
func modalViewWithPostings(t *testing.T) *mailView {
	t.Helper()
	view := newMailView(testVC())
	view.boxes = []mail.Source{
		{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
		{Kind: mail.KindBox, ID: 2, BoxKind: hey.BoxKindLater, Name: "Reply Later"},
		{Kind: mail.KindFolder, ID: 3, Name: "Travel Plans"},
		{Kind: mail.KindCollection, ID: 4, Name: "Receipts"},
	}
	view.boxIndex = 0
	view.postingList.setPostings([]mail.Posting{
		{ID: 10, Summary: "Quarterly planning notes", TopicID: 100},
	})
	view.Resize(80, 24)
	return view
}

// Opening a modal is what makes the section capture input, and every modal answers the
// same five questions, so one table can hold all nine.
func TestEveryModalCapturesInputAndDrawsItself(t *testing.T) {
	cases := []struct {
		name  string
		open  func(*mailView)
		title string
	}{
		{"compose", func(v *mailView) { v.startCompose() }, "New message"},
		{"search", func(v *mailView) { v.startSearch() }, "Search email"},
		{"move", func(v *mailView) { v.startMove() }, "Move thread"},
		{"cover", func(v *mailView) { v.startCoverPicker() }, "Cover the Imbox"},
		{"labels", func(v *mailView) { v.openLabels() }, "Labels"},
		{"collections", func(v *mailView) { v.openCollections() }, "Collections"},
		{"folders", func(v *mailView) { v.startFolderPicker() }, "Label thread"},
		{"memberships", func(v *mailView) { v.startCollectionPicker() }, "Thread collections"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			view := modalViewWithPostings(t)
			testCase.open(view)
			if view.modal == nil || !view.CapturingInput() {
				t.Fatalf("%s did not open a modal", testCase.name)
			}
			if rendered := stripANSI(view.View()); !strings.Contains(rendered, testCase.title) {
				t.Errorf("%s view = %q, want it to contain %q", testCase.name, rendered, testCase.title)
			}
			if len(view.HelpBindings()) == 0 {
				t.Errorf("%s help bar is empty", testCase.name)
			}
			view.Resize(60, 18)
			view.Restyle()
			if view.modal == nil {
				t.Errorf("%s closed on a resize", testCase.name)
			}
		})
	}
}

// Escape closes the modal on screen, and closing is mailView's to do, so a modal that
// only says "I am finished" cannot leave itself half-open.
func TestEscapeClosesEveryModal(t *testing.T) {
	opens := map[string]func(*mailView){
		"compose":     func(v *mailView) { v.startCompose() },
		"search":      func(v *mailView) { v.startSearch() },
		"move":        func(v *mailView) { v.startMove() },
		"cover":       func(v *mailView) { v.startCoverPicker() },
		"labels":      func(v *mailView) { v.openLabels() },
		"collections": func(v *mailView) { v.openCollections() },
		"folders":     func(v *mailView) { v.startFolderPicker() },
		"memberships": func(v *mailView) { v.startCollectionPicker() },
	}

	for name, open := range opens {
		t.Run(name, func(t *testing.T) {
			view := modalViewWithPostings(t)
			open(view)
			view.HandleContentKey(keyPress("esc"))
			if view.modal != nil || view.CapturingInput() {
				t.Errorf("%s stayed open on escape", name)
			}
		})
	}
}

// A picker takes key presses and nothing else, so the cursor blinks and the other
// component messages go on reaching whatever is behind it. A form takes them all.
func TestOnlyFormsTakeNonKeyMessages(t *testing.T) {
	cases := []struct {
		name  string
		open  func(*mailView)
		takes bool
	}{
		{"compose", func(v *mailView) { v.startCompose() }, true},
		{"search", func(v *mailView) { v.startSearch() }, true},
		{"move", func(v *mailView) { v.startMove() }, false},
		{"cover", func(v *mailView) { v.startCoverPicker() }, false},
		{"labels", func(v *mailView) { v.openLabels() }, false},
		{"folders", func(v *mailView) { v.startFolderPicker() }, false},
		{"memberships", func(v *mailView) { v.startCollectionPicker() }, false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			view := modalViewWithPostings(t)
			testCase.open(view)
			_, taken := view.modal.handleMsg(tea.WindowSizeMsg{Width: 80, Height: 24})
			if taken != testCase.takes {
				t.Errorf("%s took a non-key message = %v, want %v", testCase.name, taken, testCase.takes)
			}
		})
	}
}

// The folder picker's name field is the one place a non-key message reaches a picker,
// because the field has a cursor to blink.
func TestFolderPickerTakesMessagesOnlyWhileNaming(t *testing.T) {
	view := modalViewWithPostings(t)
	view.startFolderPicker()
	picker := folderModal(view)
	if picker == nil {
		t.Fatal("the folder picker did not open")
	}

	picker.cursor = len(picker.folders)
	view.HandleContentKey(keyPress("enter"))
	if !picker.creating {
		t.Fatal("enter on Create did not start naming a label")
	}
	if _, taken := view.modal.handleMsg(tea.WindowSizeMsg{Width: 80, Height: 24}); !taken {
		t.Error("the name field did not take a non-key message")
	}
}

// Opening a thread, then leaving it, leaves nothing over the list.
func TestLeavingAThreadClosesTheModal(t *testing.T) {
	view := modalViewWithPostings(t)
	view.inThread = true
	view.startCompose()
	view.ExitThread()
	if view.modal != nil || view.inThread {
		t.Errorf("thread exit left modal %#v inThread %v", view.modal, view.inThread)
	}
}
