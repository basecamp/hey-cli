package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
)

// --- The changes stream ---

func TestWaitForMailWatchEventReportsTheChangedBox(t *testing.T) {
	events := make(chan MailWatchEvent, 1)
	events <- MailWatchEvent{BoxID: 7}

	msg := waitForMailWatchEventCmd(events)()
	changed, ok := msg.(mailWatchEventMsg)
	if !ok {
		t.Fatalf("msg = %#v, want mailWatchEventMsg", msg)
	}
	if changed.event.BoxID != 7 || changed.closed {
		t.Errorf("changed = %+v, want box 7 and an open stream", changed)
	}
}

func TestWaitForMailWatchEventReportsAClosedStream(t *testing.T) {
	events := make(chan MailWatchEvent)
	close(events)

	if changed := waitForMailWatchEventCmd(events)().(mailWatchEventMsg); !changed.closed {
		t.Errorf("changed = %+v, want a closed stream", changed)
	}
	if cmd := waitForMailWatchEventCmd(nil); cmd != nil {
		t.Error("nothing to wait on should be no command")
	}
}

func TestStartMailWatchCarriesTheStreamAttemptOrReason(t *testing.T) {
	events := make(chan MailWatchEvent)
	opened := startMailWatchCmd(context.Background(), func(context.Context) (<-chan MailWatchEvent, error) {
		return events, nil
	}, 4)().(mailWatchStartedMsg)
	if opened.events == nil || opened.err != nil || opened.attempt != 4 {
		t.Errorf("opened = %+v, want attempt 4 and its stream", opened)
	}

	refused := startMailWatchCmd(context.Background(), func(context.Context) (<-chan MailWatchEvent, error) {
		return nil, errors.New("cable server said no")
	}, 5)().(mailWatchStartedMsg)
	if refused.err == nil || refused.attempt != 5 {
		t.Errorf("refused = %+v, want attempt 5 and the reason it failed", refused)
	}

	if cmd := startMailWatchCmd(context.Background(), nil, 1); cmd != nil {
		t.Error("no watcher should be no command")
	}
}

// --- The model side ---

func TestModelListensForMailChanges(t *testing.T) {
	m := newModel()
	m.mailView.boxes = orderBoxes(testBoxes())
	events := make(chan MailWatchEvent, 1)

	updated, cmd := m.Update(mailWatchStartedMsg{attempt: m.mailWatchAttempt, events: events})
	m = updated.(model)
	if m.mailWatchEvents == nil {
		t.Fatal("the model should hold on to the stream")
	}
	if cmd == nil {
		t.Fatal("the model should wait for the first event")
	}

	updated, _ = m.Update(mailWatchEventMsg{event: MailWatchEvent{BoxID: m.mailView.currentBoxID()}})
	m = updated.(model)
	if !m.mailView.liveRefreshDue {
		t.Error("a change to the box on screen should arm a re-read")
	}
}

func TestModelShowsAStandingOfflineNoticeAndRetriesAClosedWatch(t *testing.T) {
	m := newModel()
	m.width, m.height = 80, 30
	m.vc.width = 80
	m.watchMail = func(context.Context) (<-chan MailWatchEvent, error) { return nil, nil }
	m.mailWatchEvents = make(chan MailWatchEvent)

	updated, cmd := m.Update(mailWatchEventMsg{closed: true})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("a closed stream should schedule a new connection")
	}
	if m.mailWatchEvents != nil {
		t.Error("the closed stream should be let go of")
	}
	if !strings.Contains(m.View().Content, "reconnecting to HEY") {
		t.Errorf("the whole app should say live updates are reconnecting:\n%s", m.View().Content)
	}
}

func TestModelShowsAndClearsATemporaryDisconnectAcrossSections(t *testing.T) {
	m := newModel()
	m.width, m.height = 80, 30
	m.vc.width, m.vc.height = 80, 20
	m.help.setHidden(true)
	m.activeView = m.calendarView
	m.mailWatchEvents = make(chan MailWatchEvent)
	fullHeight := m.contentHeight()

	updated, cmd := m.Update(mailWatchEventMsg{event: MailWatchEvent{Connection: MailConnectionDisconnected, WillReconnect: true}})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("the model should keep waiting while Action Cable reconnects")
	}
	if !strings.Contains(m.View().Content, "Live updates disconnected") {
		t.Errorf("the Calendar should carry the global connection notice:\n%s", m.View().Content)
	}
	if m.contentHeight() != fullHeight-1 {
		t.Errorf("content height = %d, want one row held for status from %d", m.contentHeight(), fullHeight)
	}

	updated, cmd = m.Update(mailWatchEventMsg{event: MailWatchEvent{Connection: MailConnectionReconnected}})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("a reconnect should wait for more events and catch mail up")
	}
	if strings.Contains(m.View().Content, "Live updates disconnected") {
		t.Errorf("the reconnect should clear the notice:\n%s", m.View().Content)
	}
	if m.contentHeight() != fullHeight {
		t.Errorf("content height = %d, want the status row returned", m.contentHeight())
	}
}

func TestModelReportsAWatcherThatNeverStarted(t *testing.T) {
	m := newModel()
	m.width, m.height = 80, 30
	m.vc.width = 80
	m.mailView.boxes = orderBoxes(testBoxes())

	updated, cmd := m.Update(mailWatchStartedMsg{attempt: m.mailWatchAttempt, err: errors.New("\x1b[31mcable server said no")})
	m = updated.(model)
	if cmd != nil {
		t.Error("an unclassified refusal should not enter a reconnect loop")
	}
	view := m.View().Content
	if !strings.Contains(view, "cable server said no") {
		t.Errorf("view = %q, want the reason there are no live updates", view)
	}
	if strings.Contains(m.mailWatchNotice(), "\x1b") {
		t.Errorf("notice = %q, want the cable error sanitized", m.mailWatchNotice())
	}
}

func TestModelRetriesANetworkFailureButNotAStaleTimer(t *testing.T) {
	m := newModel()
	m.watchMail = func(context.Context) (<-chan MailWatchEvent, error) { return nil, nil }
	network := apierr.ErrNetwork(errors.New("no route to host"))

	updated, cmd := m.Update(mailWatchStartedMsg{attempt: m.mailWatchAttempt, err: network})
	m = updated.(model)
	if cmd == nil || m.mailWatchFailures != 1 {
		t.Fatal("a network failure should schedule its first retry")
	}
	if notice := m.mailWatchNotice(); !strings.Contains(notice, "Offline") {
		t.Errorf("notice = %q, want the network failure identified as offline", notice)
	}
	if delay := mailWatchRetryDelay(10); delay != mailWatchMaximumRetry {
		t.Errorf("retry delay = %s, want it capped at %s", delay, mailWatchMaximumRetry)
	}

	m.mailWatchEvents = make(chan MailWatchEvent)
	_, cmd = m.Update(mailWatchRetryMsg{attempt: m.mailWatchAttempt})
	if cmd != nil {
		t.Error("a timer left behind after connecting must not replace the live stream")
	}

	m.mailWatchEvents = nil
	attempt := m.mailWatchAttempt
	updated, cmd = m.Update(mailWatchRetryMsg{attempt: attempt})
	m = updated.(model)
	if cmd == nil || m.mailWatchAttempt != attempt+1 {
		t.Fatal("the current retry timer should open the next watch attempt")
	}
	started := cmd().(mailWatchStartedMsg)
	if started.attempt != attempt+1 {
		t.Errorf("started attempt = %d, want %d", started.attempt, attempt+1)
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

	v.boxes = append(v.boxes, mail.Source{Kind: mail.KindFolder, ID: 900, Name: "Receipts"})
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

	v.modal = nil
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
	if paths := recorded.paths; len(paths) != 1 || !strings.HasSuffix(paths[0], "/imbox.json") {
		t.Errorf("read = %v, want the Imbox's own route", paths)
	}
	if v.requests.loading {
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

// A re-read reads the top page of the box, so it may only replace that much: the pages the
// reader scrolled down to stay as they were read, and a thread that has left the top page
// leaves the list with it rather than sinking below the fresh rows.
func TestMailViewReReadReplacesOnlyTheTopPage(t *testing.T) {
	v := mailWithPostings()
	v.postingPaging.read(postingIDs(testPostings()), "cursor-2")
	v.Update(postingsAppendedMsg{
		requestID:  v.moreRequestID,
		boxID:      v.currentBoxID(),
		sourceKind: v.currentSourceKind(),
		nextPage:   "cursor-3",
		postings:   []mail.Posting{{ID: 102, Summary: "Scrolled to", Seen: true}},
	})

	v.Update(postingsRefreshedMsg{
		requestID:  v.liveRequestID,
		boxID:      v.currentBoxID(),
		sourceKind: v.currentSourceKind(),
		nextPage:   "cursor-2",
		postings:   []mail.Posting{{ID: 103, Summary: "Just arrived"}, testPostings()[1]},
	})

	ids := make([]int64, 0, len(v.postingList.postings))
	for _, posting := range v.postingList.postings {
		ids = append(ids, posting.ID)
	}
	if fmt.Sprint(ids) != "[103 101 102]" {
		t.Errorf("list = %v, want the fresh top page above the page below it", ids)
	}
	if v.postingPaging.nextPage != "cursor-3" {
		t.Errorf("next page = %q, want the cursor the deepest page answered with", v.postingPaging.nextPage)
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
	if !v.requests.loading {
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

	list.refreshHead([]mail.Posting{testPostings()[0]}, postingIDs(testPostings()))
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
	changed := waitForScreenerChangeCmd("stream-one", changes)().(screenerChangedMsg)
	if changed.closed || changed.stream != "stream-one" {
		t.Errorf("changed = %+v, want a ring from the stream it was waiting on", changed)
	}

	close(changes)
	if changed := waitForScreenerChangeCmd("stream-one", changes)().(screenerChangedMsg); !changed.closed {
		t.Error("a closed stream should say so")
	}
	if cmd := waitForScreenerChangeCmd("stream-one", nil); cmd != nil {
		t.Error("nothing to wait on should be no command")
	}
}

func TestModelOpensTheScreenerStreamOnlyWhenItsNameChanges(t *testing.T) {
	m := newModel()
	m.watchScreener = func(context.Context, context.Context, string) (<-chan struct{}, error) {
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

func TestModelGivesUpTheOldScreenerStreamWhenItOpensANewOne(t *testing.T) {
	m := newModel()
	var opened, owners []context.Context
	m.watchScreener = func(ctx, owner context.Context, _ string) (<-chan struct{}, error) {
		opened = append(opened, ctx)
		owners = append(owners, owner)
		return make(chan struct{}), nil
	}

	runCmd(m.startScreenerWatch("stream-one"))
	runCmd(m.startScreenerWatch("stream-two"))

	if len(opened) != 2 {
		t.Fatalf("opened %d streams, want 2", len(opened))
	}
	if opened[0].Err() == nil {
		t.Error("the stream behind the old name should be given up, not left ringing")
	}
	if opened[1].Err() != nil {
		t.Error("the stream just opened should still be open")
	}
	if len(owners) != 2 || owners[0] != m.watchCtx || owners[1] != m.watchCtx || m.watchCtx.Err() != nil {
		t.Error("replacing a Screener stream must leave the TUI-wide connection context alive")
	}
}

func TestModelReopensTheScreenerStreamFromACountRead(t *testing.T) {
	m := newModel()
	opened := make(chan string, 2)
	m.watchScreener = func(_, _ context.Context, signedStreamName string) (<-chan struct{}, error) {
		opened <- signedStreamName
		return make(chan struct{}), nil
	}

	updated, cmd := m.Update(screenerCountLoadedMsg{count: 2, screenerStream: "stream-one"})
	m = updated.(model)
	runCmd(cmd)

	if m.screenerStream != "stream-one" {
		t.Errorf("stream = %q, want the one the count read named", m.screenerStream)
	}
	if name := <-opened; name != "stream-one" {
		t.Errorf("opened %q, want the stream the count read named", name)
	}
	if m.mailView.screenerCount != 2 {
		t.Errorf("count = %d, want the 2 the read answered", m.mailView.screenerCount)
	}
}

func TestMailViewReadsTheScreenerCountWithItsStreamName(t *testing.T) {
	v := mailWithScreenerSummaryServer(t, "stream-one")

	msg, ok := runCmd(v.refreshScreenerCount()).(screenerCountLoadedMsg)
	if !ok {
		t.Fatalf("msg = %#v, want screenerCountLoadedMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("err = %v", msg.err)
	}
	if msg.count != 2 || msg.screenerStream != "stream-one" {
		t.Errorf("msg = %+v, want the count and the stream name HEY serves with it", msg)
	}
}

func TestModelGivesUpTheScreenerStreamOnAMailAccountSwitch(t *testing.T) {
	m := newModel()
	var opened []context.Context
	m.watchScreener = func(ctx, _ context.Context, _ string) (<-chan struct{}, error) {
		opened = append(opened, ctx)
		return make(chan struct{}), nil
	}
	runCmd(m.startScreenerWatch("stream-one"))

	updated, _ := m.applyMailAccount(mailAccountChoice{id: 42, label: "Work"}, nil)
	m = updated.(model)

	if m.screenerStream != "" {
		t.Errorf("stream = %q, want the old account's stream let go of", m.screenerStream)
	}
	if len(opened) != 1 || opened[0].Err() == nil {
		t.Error("the old account's stream should stop ringing when the account changes")
	}
}

func TestModelSaysWhenTheScreenerStreamCloses(t *testing.T) {
	m := newModel()
	m.screenerStream = "stream-one"
	m.screenerChanges = make(chan struct{})

	updated, cmd := m.Update(screenerChangedMsg{stream: "stream-one", closed: true})
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

func TestModelIgnoresTheCloseOfAScreenerStreamItGaveUp(t *testing.T) {
	m := newModel()
	m.watchScreener = func(context.Context, context.Context, string) (<-chan struct{}, error) {
		return make(chan struct{}), nil
	}
	runCmd(m.startScreenerWatch("stream-one"))
	runCmd(m.startScreenerWatch("stream-two"))

	// Giving the first one up closes it, and that close arrives after the second one
	// has been opened. It is not news about the stream now being followed.
	updated, _ := m.Update(screenerChangedMsg{stream: "stream-one", closed: true})
	m = updated.(model)

	if m.screenerStream != "stream-two" {
		t.Errorf("stream = %q, want the one being followed left alone", m.screenerStream)
	}
	if m.mailView.notice != "" {
		t.Errorf("notice = %q, want nothing said about a live stream", m.mailView.notice)
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

// mailWithScreenerSummaryServer answers what HEY serves about The Screener without its
// queue: the pending count and the signed name of the stream that says it changed.
func mailWithScreenerSummaryServer(t *testing.T, signedStreamName string) *mailView {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pending_clearances_count":2,"signed_stream_name":"` + signedStreamName + `"}`))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	return newMailView(vc)
}

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
