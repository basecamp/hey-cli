package tui

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/mail"
)

// selectionMail is the mail view the selection tests work in: the Imbox, with every row
// filed there as HEY serves it, since e and u act on a selection only in the Imbox.
func selectionMail(t *testing.T, status int) (*mailView, *recordedMailRequest) {
	t.Helper()
	v, recorded := mailWithTestServer(t, status)
	for i := range v.postingList.postings {
		v.postingList.postings[i].BoxID = 1
	}
	return v, recorded
}

// imboxPostings are the test postings filed in the Imbox, the box mailWithTestServer lists
// first.
func imboxPostings() []mail.Posting {
	postings := testPostings()
	for i := range postings {
		postings[i].BoxID = 1
	}
	return postings
}

// selectTwoSeenThreads selects both test postings after marking the unseen one seen, so
// u has two rows it can act on.
func selectTwoSeenThreads(v *mailView) {
	v.postingList.postings[0].Seen = true
	selectTwoThreads(v)
}

func TestMailViewMarksSelectedThreadsSeenInOneRequest(t *testing.T) {
	v, recorded := selectionMail(t, http.StatusNoContent)
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
	v, recorded := selectionMail(t, http.StatusNoContent)
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
	v, recorded := selectionMail(t, http.StatusNoContent)
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

// Like the web app, u sends every selected thread — the ones already unseen included —
// once the selection has any seen thread in it, rather than picking some out.
func TestMailViewUnseenSendsEverySelectedThread(t *testing.T) {
	v, recorded := selectionMail(t, http.StatusNoContent)
	selectTwoThreads(v)

	done, ok := runCmd(v.HandleContentKey(keyPress("u"))).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("bulk unseen returned %#v", done)
	}
	if !slices.Equal(recorded.body.PostingIDs, []int64{100, 101}) {
		t.Errorf("marked %v unseen, want every selected thread", recorded.body.PostingIDs)
	}
	answer, _ := v.Update(done)
	if toast := deliverToView(v, answer); toast != "2 threads marked as unseen" {
		t.Errorf("toast = %q", toast)
	}
}

// e and u act on a selection when HEY's web app enables its bulk Seen and Unseen buttons
// (bulk_actions_controller.js) and are refused, with no request, when it disables them:
// outside the Imbox, by each row's own box; with an ignored thread selected; and for e
// when every thread is seen already, for u when none is. A bubbled-up thread is not a
// seen one. A refusal keeps the selection for whatever the reader does next.
func TestMailViewSelectionSeenFollowsTheWebToolbar(t *testing.T) {
	selectSecond := func(v *mailView) {
		v.HandleContentKey(keyPress("down"))
		v.HandleContentKey(keyPress(" "))
	}
	cases := []struct {
		name   string
		key    string
		prime  func(*mailView)
		notice string
	}{
		{
			name:   "e on one thread already seen",
			key:    "e",
			prime:  selectSecond,
			notice: "Thread is already seen",
		},
		{
			name:   "e on threads all seen",
			key:    "e",
			prime:  selectTwoSeenThreads,
			notice: "Selected threads are already seen",
		},
		{
			name: "u on one thread already unseen",
			key:  "u",
			prime: func(v *mailView) {
				v.HandleContentKey(keyPress(" "))
			},
			notice: "Thread is already unseen",
		},
		{
			name: "u on threads none of them seen",
			key:  "u",
			prime: func(v *mailView) {
				v.postingList.postings[1].Seen = false
				selectTwoThreads(v)
			},
			notice: "Selected threads are already unseen",
		},
		{
			name: "u counts a bubbled-up thread as not seen",
			key:  "u",
			prime: func(v *mailView) {
				v.postingList.postings[1].Seen = false
				v.postingList.postings[1].BubbledUp = true
				selectTwoThreads(v)
			},
			notice: "Selected threads are already unseen",
		},
		{
			name: "e outside the Imbox",
			key:  "e",
			prime: func(v *mailView) {
				for i := range v.postingList.postings {
					v.postingList.postings[i].BoxID = 2
				}
				selectTwoThreads(v)
			},
			notice: "Seen and unseen work on a selection only in the Imbox",
		},
		{
			name: "u with threads from two boxes",
			key:  "u",
			prime: func(v *mailView) {
				v.postingList.postings[1].BoxID = 3
				selectTwoThreads(v)
			},
			notice: "Seen and unseen work on a selection only in the Imbox",
		},
		{
			name: "e on a thread whose box is not known",
			key:  "e",
			prime: func(v *mailView) {
				v.postingList.postings[0].BoxID = 0
				v.HandleContentKey(keyPress(" "))
			},
			notice: "Seen and unseen work on a selection only in the Imbox",
		},
		{
			name: "e on one ignored thread",
			key:  "e",
			prime: func(v *mailView) {
				v.postingList.postings[0].Muted = true
				v.HandleContentKey(keyPress(" "))
			},
			notice: "Stop ignoring this thread to mark it seen",
		},
		{
			name: "u on threads all ignored",
			key:  "u",
			prime: func(v *mailView) {
				for i := range v.postingList.postings {
					v.postingList.postings[i].Muted = true
				}
				selectTwoThreads(v)
			},
			notice: "Stop ignoring these threads to mark them unseen",
		},
		{
			name: "u with one ignored thread among others",
			key:  "u",
			prime: func(v *mailView) {
				v.postingList.postings[0].Muted = true
				selectTwoSeenThreads(v)
			},
			notice: "A selected thread is ignored — stop ignoring it to mark threads unseen",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, recorded := selectionMail(t, http.StatusNoContent)
			tc.prime(v)
			selected := v.postingList.selectedIDs()

			if cmd := v.HandleContentKey(keyPress(tc.key)); cmd != nil {
				t.Fatalf("%s returned %#v, want no request", tc.key, runCmd(cmd))
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
			if hasHelpBinding(v.HelpBindings(), tc.key) {
				t.Errorf("help bar offers %q, which the selection refuses", tc.key)
			}
		})
	}
}

// A refusal of e or u describes the selection, so it goes with it on Escape and is asked
// again when a re-read changes it.
func TestMailViewSeenRefusalFollowsTheSelection(t *testing.T) {
	v, _ := selectionMail(t, http.StatusNoContent)
	selectTwoSeenThreads(v)
	v.HandleContentKey(keyPress("e"))
	if v.notice != "Selected threads are already seen" {
		t.Fatalf("notice = %q", v.notice)
	}
	v.ClearSelection()
	if v.notice != "" {
		t.Errorf("notice after esc = %q, want the refusal gone with the selection", v.notice)
	}

	selectTwoSeenThreads(v)
	v.HandleContentKey(keyPress("e"))
	v.postingPaging.read(postingIDs(imboxPostings()), "")
	unseen := imboxPostings()[1]
	unseen.Seen = false
	v.Update(postingsRefreshedMsg{
		requestID:  v.liveRequestID,
		boxID:      v.currentBoxID(),
		sourceKind: v.currentSourceKind(),
		postings:   []mail.Posting{imboxPostings()[0], unseen},
	})
	if v.notice != "" {
		t.Errorf("notice = %q, want it gone once a thread in the selection is unseen again", v.notice)
	}
}

func TestMailViewSeenFailureKeepsSelection(t *testing.T) {
	for _, key := range []string{"e", "u"} {
		t.Run(key, func(t *testing.T) {
			v, _ := selectionMail(t, http.StatusInternalServerError)
			selectTwoThreads(v)

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
	v, recorded := selectionMail(t, http.StatusNoContent)
	v.seenActive = true
	seen := imboxPostings()
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
			v, recorded := selectionMail(t, http.StatusNoContent)
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
	v, recorded := selectionMail(t, http.StatusNoContent)
	v.seenActive = true
	v.seenList.setPostings(imboxPostings())
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
	v, _ := selectionMail(t, http.StatusNoContent)
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
	seen := imboxPostings()
	seen[0].Seen = true
	v.seenList.setPostings(seen)
	v.HandleContentKey(keyPress(" "))
	bindings = v.HelpBindings()
	if hasHelpBinding(bindings, "e") {
		t.Error("Previously Seen offers e for a selection of threads that are seen already")
	}
	if !hasHelpBinding(bindings, "u") {
		t.Error("Previously Seen does not offer u for a selection of seen threads")
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
	m.mailView.seenList.setPostings(imboxPostings())
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

// refreshWithoutTheTestThreads is a live re-read whose top page no longer holds either
// test thread, as when both were filed away from another device.
func refreshWithoutTheTestThreads(v *mailView) {
	v.postingPaging.read(postingIDs(imboxPostings()), "")
	v.Update(postingsRefreshedMsg{
		requestID:  v.liveRequestID,
		boxID:      v.currentBoxID(),
		sourceKind: v.currentSourceKind(),
		postings:   []mail.Posting{{ID: 103, Summary: "Quarterly planning agenda"}, {ID: 104, Summary: "Lunch on Friday?"}},
	})
}

// A re-read that takes every selected thread out of the list leaves the cursor on a row
// the reader never chose. The next e is refused rather than marking that row, once: by
// the key after it the reader has seen why and means the cursor.
func TestMailViewRefusesTheCursorWhenARefreshTookTheSelection(t *testing.T) {
	v, recorded := selectionMail(t, http.StatusNoContent)
	selectTwoThreads(v)
	refreshWithoutTheTestThreads(v)

	if ids := v.postingList.selectedIDs(); len(ids) != 0 {
		t.Fatalf("selection = %v, want it gone with its threads", ids)
	}
	if cmd := v.HandleContentKey(keyPress("z")); cmd != nil {
		t.Fatalf("z returned %#v", runCmd(cmd))
	}
	if v.notice != "" {
		t.Errorf("notice after a key that means nothing = %q, want none", v.notice)
	}
	for _, key := range []string{"e", "u", "t", "v"} {
		t.Run(key, func(t *testing.T) {
			v, recorded := selectionMail(t, http.StatusNoContent)
			selectTwoThreads(v)
			refreshWithoutTheTestThreads(v)
			if cmd := v.HandleContentKey(keyPress(key)); cmd != nil {
				t.Fatalf("%s after the selection left returned %#v", key, runCmd(cmd))
			}
			if v.notice != "The selected threads left the list — nothing was changed" {
				t.Errorf("notice = %q", v.notice)
			}
			if v.modal != nil {
				t.Errorf("%s opened %T", key, v.modal)
			}
			if len(recorded.requests) != 0 {
				t.Errorf("requests = %v, want none", recorded.requests)
			}
		})
	}

	cursorID := v.postingList.selectedPosting().ID
	v.HandleContentKey(keyPress("e"))
	done, ok := runCmd(v.HandleContentKey(keyPress("e"))).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("the second e returned %#v, want it to mark the cursor's thread", done)
	}
	if !slices.Equal(recorded.body.PostingIDs, []int64{cursorID}) {
		t.Errorf("marked %v seen, want the cursor's thread", recorded.body.PostingIDs)
	}
}

// Moving the cursor is aiming again, so the key after it acts on the row it reached.
func TestMailViewActsOnTheCursorOnceTheReaderMovesOnFromALostSelection(t *testing.T) {
	v, recorded := selectionMail(t, http.StatusNoContent)
	selectTwoThreads(v)
	refreshWithoutTheTestThreads(v)

	v.HandleContentKey(keyPress("up"))
	if _, ok := runCmd(v.HandleContentKey(keyPress("e"))).(postingActionDoneMsg); !ok {
		t.Fatal("e after moving the cursor did nothing")
	}
	if !slices.Equal(recorded.body.PostingIDs, []int64{103}) {
		t.Errorf("marked %v seen, want the row the cursor moved to", recorded.body.PostingIDs)
	}
}

// Escape lets go of a selection that went out from under the reader, as it lets go of
// one that is still standing.
func TestEscapeLetsGoOfALostSelection(t *testing.T) {
	v, recorded := selectionMail(t, http.StatusNoContent)
	selectTwoThreads(v)
	refreshWithoutTheTestThreads(v)

	if !v.ClearSelection() {
		t.Fatal("esc did not claim a lost selection")
	}
	cursorID := v.postingList.selectedPosting().ID
	if _, ok := runCmd(v.HandleContentKey(keyPress("e"))).(postingActionDoneMsg); !ok {
		t.Fatal("e after esc did nothing")
	}
	if !slices.Equal(recorded.body.PostingIDs, []int64{cursorID}) {
		t.Errorf("marked %v seen, want the cursor's thread", recorded.body.PostingIDs)
	}
}

// A thread the reader selects while e is on its way to HEY is a new selection. HEY's
// answer lets go of the threads it answered for and leaves that one selected.
func TestMailViewLetsGoOfOnlyTheThreadsAnActionCarried(t *testing.T) {
	v, recorded := selectionMail(t, http.StatusNoContent)
	v.HandleContentKey(keyPress(" "))

	cmd := v.HandleContentKey(keyPress("e"))
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress(" "))

	done, ok := runCmd(cmd).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("seen returned %#v", done)
	}
	if !slices.Equal(recorded.body.PostingIDs, []int64{100}) {
		t.Fatalf("marked %v seen, want the thread selected when e was pressed", recorded.body.PostingIDs)
	}
	v.Update(done)
	if ids := v.postingList.selectedIDs(); !slices.Equal(ids, []int64{101}) {
		t.Errorf("selection = %v, want the thread selected after e still selected", ids)
	}
}

// t on the row under the cursor is not a selection, so its answer lets go of nothing the
// reader selected in the meantime.
func TestMailViewTrashingTheCursorLeavesALaterSelectionAlone(t *testing.T) {
	v, _ := selectionMail(t, http.StatusNoContent)

	cmd := v.HandleContentKey(keyPress("t"))
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress(" "))

	done, ok := runCmd(cmd).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("trash returned %#v", done)
	}
	if done.fromSelection {
		t.Error("trashing the cursor's row was reported as a selection")
	}
	v.Update(done)
	if ids := v.postingList.selectedIDs(); !slices.Equal(ids, []int64{101}) {
		t.Errorf("selection = %v, want the thread selected after t still selected", ids)
	}
}

// Escape reaches the list without going through HandleContentKey, which clears a notice
// on every key, so the refusal counting the selection has to go with the selection. A
// notice about something else is left where it is.
func TestMailViewSelectionRefusalFollowsTheSelection(t *testing.T) {
	v, _ := selectionMail(t, http.StatusNoContent)
	selectTwoThreads(v)

	v.HandleContentKey(keyPress("b"))
	if v.notice != "2 threads selected — choose a bulk action or press Esc to clear" {
		t.Fatalf("notice = %q", v.notice)
	}
	if !v.ClearSelection() {
		t.Fatal("esc did not clear the selection")
	}
	if v.notice != "" {
		t.Errorf("notice after esc = %q, want the refusal gone with the selection", v.notice)
	}

	selectTwoThreads(v)
	v.HandleContentKey(keyPress("b"))
	v.notice = "Could not load labels — press b to retry"
	v.ClearSelection()
	if v.notice != "Could not load labels — press b to retry" {
		t.Errorf("notice = %q, want an unrelated notice left alone", v.notice)
	}
}

// A live re-read is not a key either: the refusal follows the selection it leaves, and
// goes when the re-read takes all of it.
func TestMailViewSelectionRefusalFollowsARefresh(t *testing.T) {
	v, _ := selectionMail(t, http.StatusNoContent)
	selectTwoThreads(v)
	v.HandleContentKey(keyPress("b"))

	v.postingPaging.read(postingIDs(imboxPostings()), "")
	v.Update(postingsRefreshedMsg{
		requestID:  v.liveRequestID,
		boxID:      v.currentBoxID(),
		sourceKind: v.currentSourceKind(),
		postings:   []mail.Posting{{ID: 103, Summary: "Quarterly planning agenda"}, imboxPostings()[1]},
	})
	if v.notice != "1 thread selected — choose a bulk action or press Esc to clear" {
		t.Errorf("notice = %q, want the count the re-read left", v.notice)
	}

	refreshWithoutTheTestThreads(v)
	if v.notice != "" {
		t.Errorf("notice = %q, want the refusal gone with the selection", v.notice)
	}
}
