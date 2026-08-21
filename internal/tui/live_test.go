package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/models"
)

// --- The changes stream ---

func TestWaitForMailChangeReportsTheChangedBox(t *testing.T) {
	changes := make(chan int64, 1)
	changes <- 7

	msg := waitForMailChangeCmd(changes)()
	changed, ok := msg.(mailChangedMsg)
	if !ok {
		t.Fatalf("msg = %#v, want mailChangedMsg", msg)
	}
	if changed.boxID != 7 || changed.closed {
		t.Errorf("changed = %+v, want box 7 and an open stream", changed)
	}
}

func TestWaitForMailChangeReportsAClosedStream(t *testing.T) {
	changes := make(chan int64)
	close(changes)

	if changed := waitForMailChangeCmd(changes)().(mailChangedMsg); !changed.closed {
		t.Errorf("changed = %+v, want a closed stream", changed)
	}
	if cmd := waitForMailChangeCmd(nil); cmd != nil {
		t.Error("nothing to wait on should be no command")
	}
}

func TestStartMailWatchCarriesTheStreamOrTheReason(t *testing.T) {
	changes := make(chan int64)
	opened := startMailWatchCmd(context.Background(), func(context.Context) (<-chan int64, error) {
		return changes, nil
	})().(mailWatchStartedMsg)
	if opened.changes == nil || opened.err != nil {
		t.Errorf("opened = %+v, want the stream", opened)
	}

	refused := startMailWatchCmd(context.Background(), func(context.Context) (<-chan int64, error) {
		return nil, errors.New("cable server said no")
	})().(mailWatchStartedMsg)
	if refused.err == nil {
		t.Error("a watcher that fails should say why")
	}

	if cmd := startMailWatchCmd(context.Background(), nil); cmd != nil {
		t.Error("no watcher should be no command")
	}
}

// --- The model side ---

func TestModelListensForMailChanges(t *testing.T) {
	m := newModel()
	m.mailView.boxes = orderBoxes(testBoxes())
	changes := make(chan int64, 1)

	updated, cmd := m.Update(mailWatchStartedMsg{changes: changes})
	m = updated.(model)
	if m.mailChanges == nil {
		t.Fatal("the model should hold on to the stream")
	}
	if cmd == nil {
		t.Fatal("the model should wait for the first change")
	}

	updated, _ = m.Update(mailChangedMsg{boxID: m.mailView.currentBoxID()})
	m = updated.(model)
	if !m.mailView.liveRefreshDue {
		t.Error("a change to the box on screen should arm a re-read")
	}
}

func TestModelSaysWhenLiveUpdatesStopped(t *testing.T) {
	m := newModel()
	m.mailView.boxes = orderBoxes(testBoxes())
	m.mailView.Update(currentPostingsLoaded(m.mailView, testPostings()))
	m.mailChanges = make(chan int64)

	updated, cmd := m.Update(mailChangedMsg{closed: true})
	m = updated.(model)
	if cmd != nil {
		t.Error("a closed stream should not be waited on again")
	}
	if m.mailChanges != nil {
		t.Error("the closed stream should be let go of")
	}
	if !strings.Contains(m.mailView.View(), "ctrl+r") {
		t.Errorf("the list should say it stopped following the server:\n%s", m.mailView.View())
	}

	if cmd := m.mailView.reloadPostings(); cmd == nil {
		t.Fatal("ctrl+r should read the box again")
	}
	if strings.Contains(m.mailView.View(), "Not live") {
		t.Error("a reload should take the standing word back down")
	}
}

func TestModelReportsAWatcherThatNeverStarted(t *testing.T) {
	m := newModel()
	m.vc.width = 80
	m.mailView.boxes = orderBoxes(testBoxes())

	updated, _ := m.Update(mailWatchStartedMsg{err: errors.New("cable server said no")})
	m = updated.(model)
	if !strings.Contains(m.mailView.notice, "cable server said no") {
		t.Errorf("notice = %q, want the reason there are no live updates", m.mailView.notice)
	}
}

// --- The doorbell ---

func TestMailViewArmsOneReReadPerRing(t *testing.T) {
	v := mailWithPostings()

	if cmd := v.boxChanged(v.currentBoxID()); cmd == nil {
		t.Fatal("a change to the box on screen should arm a re-read")
	}
	if cmd := v.boxChanged(v.currentBoxID()); cmd != nil {
		t.Error("a second ring should join the re-read already on its way")
	}
	if cmd := v.boxChanged(AnyBoxChanged); cmd != nil {
		t.Error("a reconnect should join it too")
	}
}

func TestMailViewIgnoresChangesItIsNotShowing(t *testing.T) {
	v := mailWithPostings()

	if cmd := v.boxChanged(v.currentBoxID() + 1000); cmd != nil {
		t.Error("another box's change should not read the box on screen")
	}
	if !v.showsBox(AnyBoxChanged) {
		t.Error("a reconnect stands for the box on screen")
	}

	v.boxes = append(v.boxes, models.Box{ID: 900, Kind: mailSourceKindFolder, Name: "Receipts"})
	v.boxIndex = len(v.boxes) - 1
	if cmd := v.boxChanged(900); cmd != nil {
		t.Error("a label pages through its own feed, not a box's")
	}
}

func TestMailViewHoldsAReReadWhileAFormIsOpen(t *testing.T) {
	v, recorded := mailWithBoxServer(t, `[{"id":100,"summary":"Hello world","created_at":"2025-03-01T10:00:00Z"}]`)
	v.startMove()

	cmd := v.refreshBox(v.currentBoxID())
	if cmd == nil {
		t.Fatal("the change should be held onto, not dropped")
	}
	if !v.liveRefreshDue {
		t.Error("a held change should still be due")
	}
	if len(recorded.paths) != 0 {
		t.Errorf("nothing should have been read yet, got %v", recorded.paths)
	}

	v.movePicker = nil
	if cmd := v.refreshBox(v.currentBoxID()); cmd == nil {
		t.Fatal("closing the picker should let the re-read through")
	}
}

func TestMailViewReReadKeepsTheReadersPlace(t *testing.T) {
	v, recorded := mailWithBoxServer(t, `[
		{"id":102,"summary":"Just arrived","created_at":"2025-03-01T11:00:00Z","creator":{"id":12,"name":"Carol Danvers"}},
		{"id":100,"summary":"Hello world","created_at":"2025-03-01T10:00:00Z","creator":{"id":10,"name":"Alice Chen"}},
		{"id":101,"summary":"Meeting notes","created_at":"2025-03-01T09:00:00Z","seen":true,"creator":{"id":11,"name":"Bob Lee"}}
	]`)
	v.postingList.cursor = 1 // Meeting notes, the seen one at the bottom
	v.postingList.toggleSelected()

	cmd := v.refreshBox(v.currentBoxID())
	if cmd == nil {
		t.Fatal("the box on screen should be re-read")
	}
	msg := cmd()
	if _, consumed := v.Update(msg); !consumed {
		t.Fatalf("msg = %#v, want a re-read the mail view takes", msg)
	}

	if len(v.postingList.postings) != 3 {
		t.Fatalf("postings = %d, want the three the server answered", len(v.postingList.postings))
	}
	if v.postingList.postings[0].ID != 102 {
		t.Errorf("first posting = %d, want the one that just arrived", v.postingList.postings[0].ID)
	}
	if selected := v.postingList.selectedPosting(); selected == nil || selected.ID != 101 {
		t.Errorf("cursor = %+v, want it still on Meeting notes", selected)
	}
	if ids := v.postingList.selectedIDs(); len(ids) != 1 || ids[0] != 101 {
		t.Errorf("selection = %v, want Meeting notes still selected", ids)
	}
	if paths := recorded.paths; len(paths) != 1 || !strings.HasSuffix(paths[0], "/boxes/1.json") {
		t.Errorf("read = %v, want the one box", paths)
	}
	if v.loading {
		t.Error("a re-read is not the user waiting on something")
	}
}

func TestMailViewReadsTheBoxWhenTheDelayIsUp(t *testing.T) {
	v, _ := mailWithBoxServer(t, `[{"id":100,"summary":"Hello world","created_at":"2025-03-01T10:00:00Z"}]`)
	v.boxChanged(v.currentBoxID())

	cmd, consumed := v.Update(mailRefreshDueMsg{boxID: v.currentBoxID()})
	if !consumed {
		t.Fatal("the mail view should take the re-read it asked for")
	}
	if cmd == nil {
		t.Fatal("the delay being up should read the box")
	}
	if v.liveRefreshDue {
		t.Error("the re-read is no longer due once it is on its way")
	}
}

func TestMailViewIgnoresAReReadOfAnotherBox(t *testing.T) {
	v := mailWithPostings()
	v.liveRequestID = 3

	v.Update(postingsRefreshedMsg{requestID: 3, boxID: v.currentBoxID() + 1, sourceKind: v.currentSourceKind(), postings: nil})
	if len(v.postingList.postings) != 2 {
		t.Error("a re-read of a box that is no longer on screen should be dropped")
	}

	v.Update(postingsRefreshedMsg{requestID: 2, boxID: v.currentBoxID(), sourceKind: v.currentSourceKind(), postings: nil})
	if len(v.postingList.postings) != 2 {
		t.Error("a re-read the view has moved on from should be dropped")
	}
}

func TestMailViewSaysWhenAReReadFails(t *testing.T) {
	v := mailWithPostings()

	v.Update(postingsRefreshedMsg{requestID: v.liveRequestID, boxID: v.currentBoxID(), sourceKind: v.currentSourceKind(), err: errors.New("box unreachable")})
	if !strings.Contains(v.notice, "box unreachable") {
		t.Errorf("notice = %q, want the reason the re-read failed", v.notice)
	}
	if len(v.postingList.postings) != 2 {
		t.Error("a failed re-read should leave the list as it was")
	}
}

func TestMailViewReloadsOnCtrlR(t *testing.T) {
	v := mailWithPostings()

	if cmd := v.HandleContentKey(keyPress("ctrl+r")); cmd == nil {
		t.Fatal("ctrl+r should read the box again")
	}
	if !v.loading {
		t.Error("a reload the user asked for should show as loading")
	}
	if !hasHelpBinding(v.HelpBindings(), "ctrl+r") {
		t.Error("the help should offer the reload")
	}
}

// --- Keeping the reader's place ---

func TestContentListRefreshDropsWhatLeftTheBox(t *testing.T) {
	list := contentList{hideSeenState: true, width: 80, height: 20}
	list.setPostings(testPostings())
	list.cursor = 1
	if !list.toggleSelected() {
		t.Fatal("the posting under the cursor should end up selected")
	}

	list.refreshPostings([]models.Posting{testPostings()[0]})
	if list.cursor != 0 {
		t.Errorf("cursor = %d, want the top once its posting left", list.cursor)
	}
	if ids := list.selectedIDs(); len(ids) != 0 {
		t.Errorf("selection = %v, want the posting that left dropped", ids)
	}
}

// --- The Screener's stream ---

func TestWaitForScreenerChangeReportsTheRingAndTheClose(t *testing.T) {
	changes := make(chan struct{}, 1)
	changes <- struct{}{}
	if changed := waitForScreenerChangeCmd(changes)().(screenerChangedMsg); changed.closed {
		t.Error("a ring is not a closed stream")
	}

	close(changes)
	if changed := waitForScreenerChangeCmd(changes)().(screenerChangedMsg); !changed.closed {
		t.Error("a closed stream should say so")
	}
	if cmd := waitForScreenerChangeCmd(nil); cmd != nil {
		t.Error("nothing to wait on should be no command")
	}
}

func TestModelOpensTheScreenerStreamOnlyWhenItsNameChanges(t *testing.T) {
	m := newModel()
	m.watchScreener = func(context.Context, string) (<-chan struct{}, error) {
		return make(chan struct{}), nil
	}

	if cmd := m.startScreenerWatch(""); cmd != nil {
		t.Error("there is nothing to subscribe to without a name")
	}
	if cmd := m.startScreenerWatch("stream-one"); cmd == nil {
		t.Fatal("the first name HEY serves should open the stream")
	}
	if cmd := m.startScreenerWatch("stream-one"); cmd != nil {
		t.Error("every count read carries the name; the same one should not reopen it")
	}
	if cmd := m.startScreenerWatch("stream-two"); cmd == nil {
		t.Error("a new name should open the stream again")
	}
}

func TestModelSaysWhenTheScreenerStreamCloses(t *testing.T) {
	m := newModel()
	m.screenerStream = "stream-one"
	m.screenerChanges = make(chan struct{})

	updated, cmd := m.Update(screenerChangedMsg{closed: true})
	m = updated.(model)
	if cmd != nil {
		t.Error("a closed stream should not be waited on again")
	}
	if m.screenerChanges != nil || m.screenerStream != "" {
		t.Error("the closed stream and its name should be let go of, so the next count read reopens it")
	}
	if !strings.Contains(m.mailView.notice, "stopped updating live") {
		t.Errorf("notice = %q, want the Screener's own word", m.mailView.notice)
	}
}

func TestModelRingsTheScreenerOnce(t *testing.T) {
	m := newModel()

	if cmd := m.screenerChanged(); cmd == nil {
		t.Fatal("a Screener change should arm a re-read")
	}
	if cmd := m.screenerChanged(); cmd != nil {
		t.Error("screening a queue in one go should still cost one re-read")
	}
}

func TestModelReadsTheScreenerCountWhereverTheUserIs(t *testing.T) {
	m := newModel()
	m.mailView.boxes = orderBoxes(testBoxes())
	m.screenerRefreshDue = true

	if cmd := m.refreshScreener(); cmd == nil {
		t.Fatal("the count should be read again even with The Screener closed")
	}
	if m.screenerRefreshDue {
		t.Error("the re-read is no longer due once it is on its way")
	}
}

func TestModelHoldsTheScreenerReadWhileADecisionIsInFlight(t *testing.T) {
	view, _ := loadedScreener(t)
	m := newModel()
	m.screenerView = view
	m.activeView = view
	view.mutations = 1

	if cmd := m.refreshScreener(); cmd == nil {
		t.Fatal("the change should be held onto, not dropped")
	}
	if !m.screenerRefreshDue {
		t.Error("a held change should still be due")
	}
}

func TestScreenerRefreshKeepsTheCursorOnItsSender(t *testing.T) {
	view, state := loadedScreener(t)
	view.pending.cursor = 1 // Bob Smith

	state.mu.Lock()
	state.pending = `{"pending_clearances_count":3,"clearances":[
		{"id":93,"status":"pending","petitioner":{"id":15,"name":"Nora Vance","email_address":"nora@example.com"},
		 "most_recent_entry":{"id":503,"subject":"Just arrived","summary":"Are you around?","topic_id":702}},
		{"id":91,"status":"pending","petitioner":{"id":11,"name":"Jane Doe","email_address":"jane@example.com"},
		 "most_recent_entry":{"id":501,"subject":"Quarterly planning","summary":"Can we meet Thursday?","topic_id":700}},
		{"id":92,"status":"pending","petitioner":{"id":12,"name":"Bob Smith","email_address":"bob@example.org"},
		 "most_recent_entry":{"id":502,"subject":"Invoice 4102","summary":"Attached for your records","topic_id":701}}
	]}`
	state.mu.Unlock()

	cmd, held := view.refreshPending()
	if held || cmd == nil {
		t.Fatalf("refresh = cmd:%v held:%v, want the queue read again", cmd != nil, held)
	}
	msg := runCmd(cmd)
	if _, consumed := view.Update(msg); !consumed {
		t.Fatalf("msg = %#v, want a re-read The Screener takes", msg)
	}

	if view.pendingCount != 3 || len(view.pending.rows) != 3 {
		t.Fatalf("pending = count:%d rows:%d, want 3 and 3", view.pendingCount, len(view.pending.rows))
	}
	if selected := view.pending.selected(); selected == nil || selected.id != 92 {
		t.Errorf("cursor = %+v, want it still on Bob Smith", selected)
	}
}

func TestScreenerRefreshHoldsWhileDeciding(t *testing.T) {
	view, _ := loadedScreener(t)

	view.mutations = 1
	if cmd, held := view.refreshPending(); !held || cmd != nil {
		t.Errorf("refresh = cmd:%v held:%v, want it held while a decision is in flight", cmd != nil, held)
	}

	view.mutations = 0
	view.confirmingClear = true
	if cmd, held := view.refreshPending(); !held || cmd != nil {
		t.Errorf("refresh = cmd:%v held:%v, want it held while the question is up", cmd != nil, held)
	}
}

func TestScreenerRefreshMarksTheQueueStaleFromHistory(t *testing.T) {
	view, _ := loadedScreener(t)
	view.tab = screenerHistoryTab

	cmd, held := view.refreshPending()
	if cmd != nil || held {
		t.Errorf("refresh = cmd:%v held:%v, want nothing read while history is up", cmd != nil, held)
	}
	if view.pending.loaded {
		t.Error("the queue should be read again when it is next looked at")
	}
}

// --- Helpers ---

type recordedReads struct{ paths []string }

// mailWithBoxServer answers the box on screen with postings, and records what was read.
func mailWithBoxServer(t *testing.T, postingsJSON string) (*mailView, *recordedReads) {
	t.Helper()

	recorded := &recordedReads{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.paths = append(recorded.paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"kind":"imbox","name":"Imbox","postings":` + postingsJSON + `}`))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(
		&hey.Config{BaseURL: server.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	v := newMailView(vc)
	v.Resize(vc.width, vc.height)
	v.boxes = orderBoxes(testBoxes())
	v.Update(currentPostingsLoaded(v, testPostings()))
	return v, recorded
}
