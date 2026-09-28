package tui

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// selectTwoSeenThreads selects both test postings after marking the unseen one seen, so
// u has two rows it can act on.
func selectTwoSeenThreads(v *mailView) {
	v.postingList.postings[0].Seen = true
	selectTwoThreads(v)
}

func TestMailViewMarksSelectedThreadsSeenInOneRequest(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	selectTwoThreads(v)

	done, ok := runCmd(v.HandleContentKey(keyPress("e"))).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("bulk seen returned %#v", done)
	}
	if recorded.path != "/postings/seen.json" || !slices.Equal(recorded.body.PostingIDs, []int64{100, 101}) {
		t.Fatalf("request = %s %v, want one POST /postings/seen.json with [100 101]", recorded.path, recorded.body.PostingIDs)
	}

	answer, _ := v.Update(done)
	if toast := deliverToView(v, answer); toast != "2 threads marked as seen" {
		t.Errorf("toast = %q", toast)
	}
	for _, id := range []int64{100, 101} {
		if !v.postingList.postings[v.postingIndex(id)].Seen {
			t.Errorf("posting %d is not seen", id)
		}
	}
	if ids := v.postingList.selectedIDs(); len(ids) != 0 {
		t.Errorf("selection left = %v, want it let go once HEY answered", ids)
	}
	if v.AccountSwitchBlocked() {
		t.Error("completed bulk seen still blocks account switching")
	}
}

func TestMailViewMarksSelectedThreadsUnseenInOneRequest(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	selectTwoSeenThreads(v)

	done, ok := runCmd(v.HandleContentKey(keyPress("u"))).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("bulk unseen returned %#v", done)
	}
	if recorded.path != "/postings/unseen.json" || !slices.Equal(recorded.body.PostingIDs, []int64{100, 101}) {
		t.Fatalf("request = %s %v, want one POST /postings/unseen.json with [100 101]", recorded.path, recorded.body.PostingIDs)
	}

	answer, _ := v.Update(done)
	if toast := deliverToView(v, answer); toast != "2 threads marked as unseen" {
		t.Errorf("toast = %q", toast)
	}
	for _, id := range []int64{100, 101} {
		if v.postingList.postings[v.postingIndex(id)].Seen {
			t.Errorf("posting %d is still seen", id)
		}
	}
	if ids := v.postingList.selectedIDs(); len(ids) != 0 {
		t.Errorf("selection left = %v, want it let go once HEY answered", ids)
	}
}

// The selection wins even at one row, as it does for t: the cursor has moved off what
// was selected, and marking the row under it instead would change the wrong thread.
func TestMailViewMarksTheSelectionSeenRatherThanTheCursor(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.HandleContentKey(keyPress(" "))
	v.HandleContentKey(keyPress("down"))

	done, ok := runCmd(v.HandleContentKey(keyPress("e"))).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("seen returned %#v", done)
	}
	if !slices.Equal(recorded.body.PostingIDs, []int64{100}) {
		t.Errorf("marked %v seen, want the selected row alone", recorded.body.PostingIDs)
	}
	answer, _ := v.Update(done)
	if toast := deliverToView(v, answer); toast != "Thread marked as seen" {
		t.Errorf("toast = %q", toast)
	}
}

// u holds each selected row to the rules it holds the cursor's row to. A thread already
// unseen is left out silently, because it already is what was asked for; an ignored one
// is left out and counted, because for that one the key did nothing.
func TestMailViewUnseenSkipsWhatItCannotChangeInASelection(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.postingList.postings[0].Seen = true
	v.postingList.postings[0].Muted = true
	selectTwoThreads(v)

	done, ok := runCmd(v.HandleContentKey(keyPress("u"))).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("bulk unseen returned %#v", done)
	}
	if !slices.Equal(recorded.body.PostingIDs, []int64{101}) {
		t.Errorf("marked %v unseen, want the ignored thread left out", recorded.body.PostingIDs)
	}
	answer, _ := v.Update(done)
	if toast := deliverToView(v, answer); toast != "Thread marked as unseen — 1 ignored thread skipped" {
		t.Errorf("toast = %q", toast)
	}
	if !v.postingList.postings[v.postingIndex(100)].Seen {
		t.Error("the ignored thread was marked unseen in the list")
	}

	quiet, recordedQuiet := mailWithTestServer(t, http.StatusNoContent)
	selectTwoSeenThreads(quiet)
	quiet.postingList.postings[quiet.postingIndex(100)].Seen = false
	done, ok = runCmd(quiet.HandleContentKey(keyPress("u"))).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("bulk unseen returned %#v", done)
	}
	if !slices.Equal(recordedQuiet.body.PostingIDs, []int64{101}) {
		t.Errorf("marked %v unseen, want the already unseen thread left out", recordedQuiet.body.PostingIDs)
	}
	answer, _ = quiet.Update(done)
	if toast := deliverToView(quiet, answer); toast != "Thread marked as unseen" {
		t.Errorf("toast = %q, want no mention of a thread that was already unseen", toast)
	}
}

// With no row u could change there is no request at all, and the selection stays for
// whatever the reader does next.
func TestMailViewUnseenRefusesASelectionItCannotChange(t *testing.T) {
	cases := []struct {
		name   string
		prime  func(*mailView)
		notice string
	}{
		{
			name: "one already unseen",
			prime: func(v *mailView) {
				v.HandleContentKey(keyPress(" "))
			},
			notice: "Thread is already unseen",
		},
		{
			name: "all already unseen",
			prime: func(v *mailView) {
				v.postingList.postings[1].Seen = false
				selectTwoThreads(v)
			},
			notice: "Selected threads are already unseen",
		},
		{
			name: "one ignored",
			prime: func(v *mailView) {
				v.postingList.postings[0].Seen = true
				v.postingList.postings[0].Muted = true
				v.HandleContentKey(keyPress(" "))
			},
			notice: "Stop ignoring this thread to mark it unseen",
		},
		{
			name: "all ignored",
			prime: func(v *mailView) {
				for i := range v.postingList.postings {
					v.postingList.postings[i].Seen = true
					v.postingList.postings[i].Muted = true
				}
				selectTwoThreads(v)
			},
			notice: "Stop ignoring these threads to mark them unseen",
		},
		{
			name: "ignored and already unseen",
			prime: func(v *mailView) {
				v.postingList.postings[1].Muted = true
				selectTwoThreads(v)
			},
			notice: "Selected threads are already unseen or ignored",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)
			tc.prime(v)
			selected := v.postingList.selectedIDs()

			if cmd := v.HandleContentKey(keyPress("u")); cmd != nil {
				t.Fatalf("u returned %#v, want no request", runCmd(cmd))
			}
			if v.notice != tc.notice {
				t.Errorf("notice = %q, want %q", v.notice, tc.notice)
			}
			if len(recorded.requests) != 0 {
				t.Errorf("requests = %v, want none", recorded.requests)
			}
			if ids := v.postingList.selectedIDs(); !slices.Equal(ids, selected) {
				t.Errorf("selection = %v, want %v kept", ids, selected)
			}
		})
	}
}

func TestMailViewSeenFailureKeepsSelection(t *testing.T) {
	for _, key := range []string{"e", "u"} {
		t.Run(key, func(t *testing.T) {
			v, _ := mailWithTestServer(t, http.StatusInternalServerError)
			selectTwoSeenThreads(v)

			done := runCmd(v.HandleContentKey(keyPress(key))).(postingActionDoneMsg)
			if done.err == nil {
				t.Fatal("a failed bulk action should carry its error")
			}
			v.Update(done)
			if ids := v.postingList.selectedIDs(); !slices.Equal(ids, []int64{100, 101}) {
				t.Errorf("selection = %v, want both rows still selected for another try", ids)
			}
		})
	}
}

// Marking a thread unseen takes it off Previously Seen, so the selected rows leave the
// screen and the selection goes with them.
func TestMailViewMarksSelectedThreadsUnseenFromPreviouslySeen(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.seenActive = true
	seen := testPostings()
	seen[0].Seen = true
	v.seenList.setPostings(seen)
	selectTwoThreads(v)

	done, ok := runCmd(v.HandleContentKey(keyPress("u"))).(postingActionDoneMsg)
	if !ok || done.err != nil || !done.seen {
		t.Fatalf("Previously Seen bulk unseen returned %#v", done)
	}
	if recorded.path != "/postings/unseen.json" || !slices.Equal(recorded.body.PostingIDs, []int64{100, 101}) {
		t.Fatalf("request = %s %v, want one POST /postings/unseen.json with [100 101]", recorded.path, recorded.body.PostingIDs)
	}

	v.Update(done)
	if len(v.seenList.postings) != 0 {
		t.Errorf("Previously Seen postings left = %d, want every selected row gone", len(v.seenList.postings))
	}
	if ids := v.seenList.selectedIDs(); len(ids) != 0 {
		t.Errorf("Previously Seen selection left = %v", ids)
	}
	if len(v.postingList.postings) != 2 {
		t.Errorf("bulk unseen changed the box list: %+v", v.postingList.postings)
	}
}

// Only t, e, u and Ctrl+B act on a selection. Every other thread action would act on the
// row under the cursor, which is not what the reader selected, so it is refused.
func TestMailViewRefusesOneThreadActionsWhileASelectionStands(t *testing.T) {
	for _, key := range []string{"r", "f", "v", "b", "n", "i", "l", "a", "d", "p", "!", "-"} {
		t.Run(key, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)
			selectTwoThreads(v)

			if cmd := v.HandleContentKey(keyPress(key)); cmd != nil {
				t.Fatalf("%s with a selection returned %#v", key, runCmd(cmd))
			}
			if v.notice != "2 threads selected — choose a bulk action or press Esc to clear" {
				t.Errorf("notice = %q", v.notice)
			}
			if v.modal != nil {
				t.Errorf("%s opened %T over a selection", key, v.modal)
			}
			if len(recorded.requests) != 0 {
				t.Errorf("requests = %v, want none", recorded.requests)
			}
			if ids := v.postingList.selectedIDs(); !slices.Equal(ids, []int64{100, 101}) {
				t.Errorf("selection = %v, want it kept", ids)
			}
		})
	}
}

func TestMailViewRefusesOneThreadActionsOnPreviouslySeenSelection(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.seenActive = true
	v.seenList.setPostings(testPostings())
	v.HandleContentKey(keyPress(" "))

	if cmd := v.HandleContentKey(keyPress("l")); cmd != nil {
		t.Fatalf("l with a selection returned %#v", runCmd(cmd))
	}
	if v.notice != "1 thread selected — choose a bulk action or press Esc to clear" {
		t.Errorf("notice = %q", v.notice)
	}
	if len(recorded.requests) != 0 {
		t.Errorf("requests = %v, want none", recorded.requests)
	}
}

func TestMailViewHelpBarOffersOnlyTheSelectionActions(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	selectTwoThreads(v)

	bindings := v.HelpBindings()
	if !strings.Contains(bindings[0].key, "2 selected") || bindings[0].desc != "" {
		t.Errorf("first binding = %+v, want the selected count", bindings[0])
	}
	for _, key := range []string{"e", "u", "t", "space", "esc", "ctrl+b"} {
		if !hasHelpBinding(bindings, key) {
			t.Errorf("help bar is missing %q: %v", key, bindings)
		}
	}
	for _, key := range []string{"r", "v", "l", "!", "-"} {
		if hasHelpBinding(bindings, key) {
			t.Errorf("help bar offers %q, which a selection refuses: %v", key, bindings)
		}
	}

	v.seenActive = true
	v.seenList.setPostings(testPostings())
	v.HandleContentKey(keyPress(" "))
	if hasHelpBinding(v.HelpBindings(), "e") {
		t.Error("Previously Seen offers e for a selection of threads that are seen already")
	}
}

func TestEscapeClearsTheMailSelection(t *testing.T) {
	m := modelWithBoxes()
	selectTwoThreads(m.mailView)

	updated, _ := m.Update(keyPress("esc"))
	m = updated.(model)
	if ids := m.mailView.postingList.selectedIDs(); len(ids) != 0 {
		t.Errorf("selection after esc = %v, want it cleared", ids)
	}
	if hasHelpBinding(m.help.bindings, "esc") {
		t.Errorf("help bar still offers esc to clear: %v", m.help.bindings)
	}
}

// q leaves things; it has never cleared anything, and a selection is no exception.
func TestQuitKeyLeavesTheMailSelectionAlone(t *testing.T) {
	m := modelWithBoxes()
	selectTwoThreads(m.mailView)

	updated, _ := m.Update(keyPress("q"))
	m = updated.(model)
	if ids := m.mailView.postingList.selectedIDs(); !slices.Equal(ids, []int64{100, 101}) {
		t.Errorf("selection after q = %v, want it kept", ids)
	}
}

// Escape's existing priorities stay ahead of the selection: an open modal closes first.
func TestEscapeClosesAModalBeforeClearingTheSelection(t *testing.T) {
	m := modelWithBoxes()
	selectTwoThreads(m.mailView)
	m.mailView.HandleContentKey(keyPress("/"))
	if m.mailView.modal == nil {
		t.Fatal("/ did not open the search form")
	}

	updated, _ := m.Update(keyPress("esc"))
	m = updated.(model)
	if m.mailView.modal != nil {
		t.Fatalf("esc left %T open", m.mailView.modal)
	}
	if ids := m.mailView.postingList.selectedIDs(); !slices.Equal(ids, []int64{100, 101}) {
		t.Errorf("selection after closing the form = %v, want it kept", ids)
	}

	updated, _ = m.Update(keyPress("esc"))
	m = updated.(model)
	if ids := m.mailView.postingList.selectedIDs(); len(ids) != 0 {
		t.Errorf("selection after the second esc = %v, want it cleared", ids)
	}
}

// A thread the reader is waiting on is cancelled before the selection is touched.
func TestEscapeCancelsAPendingThreadBeforeClearingTheSelection(t *testing.T) {
	m := modelWithBoxes()
	selectTwoThreads(m.mailView)
	m.mailView.requests.begin(context.Background(), mailRequestTopic)

	updated, _ := m.Update(keyPress("esc"))
	m = updated.(model)
	if m.mailView.requests.loading {
		t.Fatal("esc did not cancel the pending thread")
	}
	if ids := m.mailView.postingList.selectedIDs(); !slices.Equal(ids, []int64{100, 101}) {
		t.Errorf("selection after cancelling = %v, want it kept", ids)
	}
}

// Previously Seen is a screen of its own, so the first Escape clears its selection and
// the next one goes back to the box.
func TestEscapeClearsAPreviouslySeenSelectionBeforeLeaving(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.seenActive = true
	m.mailView.seenList.setPostings(testPostings())
	m.mailView.HandleContentKey(keyPress(" "))

	updated, _ := m.Update(keyPress("esc"))
	m = updated.(model)
	if !m.mailView.seenActive {
		t.Fatal("the first esc left Previously Seen instead of clearing its selection")
	}
	if ids := m.mailView.seenList.selectedIDs(); len(ids) != 0 {
		t.Errorf("selection after esc = %v, want it cleared", ids)
	}

	updated, _ = m.Update(keyPress("esc"))
	m = updated.(model)
	if m.mailView.seenActive {
		t.Error("the second esc did not leave Previously Seen")
	}
}
