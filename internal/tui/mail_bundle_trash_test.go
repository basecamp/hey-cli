package tui

import (
	"net/http"
	"slices"
	"testing"
)

// mailWithABundle is the box list HEY serves for an account with a grouped sender: a
// thread, and a bundle row standing in for that sender's whole stream. A bundle carries
// no topic, which is why it cannot be trashed.
func mailWithABundle(t *testing.T) (*mailView, *recordedMailRequest) {
	t.Helper()
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.postingList.postings[1] = bundleRow()
	return v, recorded
}

// HEY trashes through the thread, so a bundle row — which has none — is silently skipped
// by the server while the list reports it gone. The web app hides Trash as soon as a
// bundle is in the selection; t refuses for the same reason, and says what to do instead.
func TestMailViewRefusesToTrashASelectionHoldingABundle(t *testing.T) {
	v, recorded := mailWithABundle(t)
	selectTwoThreads(v)

	if cmd := v.HandleContentKey(keyPress("t")); cmd != nil {
		t.Fatalf("trashing a selection holding a bundle returned %#v", runCmd(cmd))
	}
	if v.notice != "A bundle cannot be trashed — deselect the bundle to trash the rest" {
		t.Errorf("notice = %q", v.notice)
	}
	if len(recorded.requests) != 0 {
		t.Errorf("requests = %v, want none", recorded.requests)
	}
	if len(v.postingList.postings) != 2 {
		t.Errorf("postings = %d, want both rows standing", len(v.postingList.postings))
	}
	if ids := v.postingList.selectedIDs(); !slices.Equal(ids, []int64{100, 311}) {
		t.Errorf("selection = %v, want both rows still selected", ids)
	}
}

// Nothing else is in reach, so there is no rest to trash and the notice does not offer one.
func TestMailViewRefusesToTrashABundleOnItsOwn(t *testing.T) {
	for _, reach := range []string{"cursor", "selection"} {
		t.Run(reach, func(t *testing.T) {
			v, recorded := mailWithABundle(t)
			v.HandleContentKey(keyPress("down"))
			if reach == "selection" {
				v.HandleContentKey(keyPress(" "))
			}

			if cmd := v.HandleContentKey(keyPress("t")); cmd != nil {
				t.Fatalf("trashing a bundle returned %#v", runCmd(cmd))
			}
			if v.notice != "A bundle cannot be trashed" {
				t.Errorf("notice = %q", v.notice)
			}
			if len(recorded.requests) != 0 {
				t.Errorf("requests = %v, want none", recorded.requests)
			}
			if len(v.postingList.postings) != 2 {
				t.Errorf("postings = %d, want the bundle standing", len(v.postingList.postings))
			}
		})
	}
}

// The help bar is the TUI's version of the web app's button disappearing: t is dropped
// whenever what it would act on holds a bundle, so the key is never offered for an action
// that would do nothing.
func TestMailViewHelpBarDropsTrashWhenABundleIsInReach(t *testing.T) {
	if v, _ := mailWithABundle(t); !hasHelpBinding(v.HelpBindings(), "t") {
		t.Error("a thread under the cursor should still offer t")
	}

	onBundle, _ := mailWithABundle(t)
	onBundle.HandleContentKey(keyPress("down"))
	if hasHelpBinding(onBundle.HelpBindings(), "t") {
		t.Errorf("a bundle under the cursor still offers t: %v", onBundle.HelpBindings())
	}

	mixed, _ := mailWithABundle(t)
	selectTwoThreads(mixed)
	if hasHelpBinding(mixed.HelpBindings(), "t") {
		t.Errorf("a selection holding a bundle still offers t: %v", mixed.HelpBindings())
	}

	selectedThread, _ := mailWithABundle(t)
	selectedThread.HandleContentKey(keyPress(" "))
	selectedThread.HandleContentKey(keyPress("down"))
	if !hasHelpBinding(selectedThread.HelpBindings(), "t") {
		t.Error("a selected thread should still offer t when the cursor is on a bundle")
	}

	threads, _ := mailWithTestServer(t, http.StatusNoContent)
	selectTwoThreads(threads)
	if !hasHelpBinding(threads.HelpBindings(), "t") {
		t.Error("a selection of threads should still offer t")
	}
}

// The selection wins even at one row, because the cursor moves away from what was
// selected — trashing what is under the cursor instead would take the wrong thread.
func TestMailViewTrashesTheSelectionRatherThanTheCursor(t *testing.T) {
	v, recorded := mailWithABundle(t)
	v.HandleContentKey(keyPress(" "))
	v.HandleContentKey(keyPress("down"))

	done, ok := runCmd(v.HandleContentKey(keyPress("t"))).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("trash returned %#v", done)
	}
	if !slices.Equal(recorded.body.PostingIDs, []int64{100}) {
		t.Errorf("trashed %v, want the selected row alone", recorded.body.PostingIDs)
	}
}
