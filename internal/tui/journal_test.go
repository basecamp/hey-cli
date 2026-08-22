package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

func currentJournalResult(v *journalView) requestResult {
	return requestResult{requestID: v.requests.id}
}

func journalEntries(count int) []journalSummary {
	entries := make([]journalSummary, count)
	for index := range count {
		starts := time.Now().AddDate(0, 0, -index)
		entries[index] = journalSummary{
			ID:      int64(index + 1),
			Date:    starts.Format("2006-01-02"),
			Starts:  starts,
			Preview: "Reflection from the day",
		}
	}
	return entries
}

func loadedJournalView(entries []journalSummary) *journalView {
	v := newJournalView(testVC())
	v.Resize(80, 20)
	v.Init()
	v.Update(journalPageLoadedMsg{requestResult: currentJournalResult(v), entries: entries})
	return v
}

func TestJournalInitRequestsFeed(t *testing.T) {
	v := newJournalView(testVC())
	cmd := v.Init()
	if cmd == nil || !v.requests.loading || v.requests.kind != journalRequestFeed {
		t.Fatalf("feed request = cmd:%v loading:%v kind:%v", cmd != nil, v.requests.loading, v.requests.kind)
	}
}

func TestJournalSummaryKeepsHEYsUTCCalendarDate(t *testing.T) {
	recording := generated.Recording{
		Id:       1,
		Type:     "Calendar::JournalEntry",
		StartsAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Content:  "A day worth remembering",
	}

	entries := journalSummaries([]generated.Recording{recording})
	if len(entries) != 1 || entries[0].Date != "2026-07-01" {
		t.Fatalf("journal date = %#v, want 2026-07-01", entries)
	}
	if got := entries[0].Starts.Format("2006-01-02"); got != "2026-07-01" {
		t.Fatalf("display date = %q, want 2026-07-01", got)
	}
}

func TestJournalFeedShowsEntriesInsteadOfDateTabs(t *testing.T) {
	v := loadedJournalView(journalEntries(2))
	content := stripANSI(v.View())
	if !strings.Contains(content, "Journal · newest first") || !strings.Contains(content, "Reflection from the day") {
		t.Fatalf("feed = %q", content)
	}
	items, _, label, _ := v.SubnavItems()
	if len(items) != 0 || label != "Journal" {
		t.Fatalf("subnav = items:%d label:%q", len(items), label)
	}
}

func TestJournalFeedEmptyStateInvitesAddingToday(t *testing.T) {
	v := loadedJournalView(nil)
	if got := stripANSI(v.View()); !strings.Contains(got, "press a to write about today") {
		t.Fatalf("empty feed = %q", got)
	}
}

func TestJournalFeedAppendsOlderPage(t *testing.T) {
	v := newJournalView(testVC())
	v.Resize(80, 20)
	v.Init()
	loadMore, _ := v.Update(journalPageLoadedMsg{
		requestResult: currentJournalResult(v),
		entries:       journalEntries(2),
		nextPage:      "older-cursor",
	})
	if loadMore == nil || !v.loadingMore {
		t.Fatal("a short first page should load the page below it")
	}
	_, consumed := v.Update(journalPageAppendedMsg{
		requestID: v.moreID,
		entries: []journalSummary{{
			ID: 9, Date: "2025-01-01", Starts: time.Date(2025, 1, 1, 12, 0, 0, 0, time.Local), Preview: "Older entry",
		}},
	})
	if !consumed || len(v.list.entries) != 3 || v.list.entries[2].Preview != "Older entry" {
		t.Fatalf("grown feed = consumed:%v entries:%#v", consumed, v.list.entries)
	}
}

func TestJournalFeedIgnoresStalePage(t *testing.T) {
	v := newJournalView(testVC())
	v.Init()
	v.requestFeed("planning")
	v.Update(journalPageLoadedMsg{
		requestResult: requestResult{requestID: v.requests.id - 1},
		entries:       journalEntries(1),
	})
	if len(v.list.entries) != 0 || !v.requests.loading {
		t.Fatalf("stale page changed feed: entries:%d loading:%v", len(v.list.entries), v.requests.loading)
	}
}

func TestJournalScrollNearBottomLoadsMore(t *testing.T) {
	v := loadedJournalView(journalEntries(10))
	v.nextPage = "next"
	v.Resize(80, 6)
	for range 6 {
		v.HandleContentKey(keyPress("down"))
	}
	if !v.loadingMore {
		t.Fatal("scrolling near the bottom should load another page")
	}
}

func TestJournalSearchAndClear(t *testing.T) {
	v := loadedJournalView(journalEntries(1))
	if cmd := v.HandleContentKey(keyPress("/")); cmd == nil || v.prompt == nil {
		t.Fatal("/ should open and focus search")
	}
	v.prompt.input.SetValue("quarterly planning")
	cmd := v.HandleContentKey(keyPress("enter"))
	if cmd == nil || v.prompt != nil || v.query != "quarterly planning" || v.requests.kind != journalRequestFeed {
		t.Fatalf("search state = cmd:%v prompt:%v query:%q kind:%v", cmd != nil, v.prompt != nil, v.query, v.requests.kind)
	}
	v.Update(journalPageLoadedMsg{requestResult: currentJournalResult(v)})
	if got := stripANSI(v.View()); !strings.Contains(got, "Search: quarterly planning · 0 results") {
		t.Fatalf("search empty state = %q", got)
	}
	if cmd := v.HandleContentKey(keyPress("c")); cmd == nil || v.query != "" {
		t.Fatalf("clear search = cmd:%v query:%q", cmd != nil, v.query)
	}
}

func TestJournalSearchEscapeKeepsFeed(t *testing.T) {
	v := loadedJournalView(journalEntries(1))
	v.HandleContentKey(keyPress("/"))
	v.prompt.input.SetValue("discard me")
	v.HandleContentKey(keyPress("esc"))
	if v.prompt != nil || v.query != "" || len(v.list.entries) != 1 {
		t.Fatalf("cancelled search = prompt:%v query:%q entries:%d", v.prompt != nil, v.query, len(v.list.entries))
	}
}

func TestJournalDateJumpValidatesAndLoads(t *testing.T) {
	v := loadedJournalView(nil)
	v.HandleContentKey(keyPress("g"))
	v.prompt.input.SetValue("August 19")
	if cmd := v.HandleContentKey(keyPress("enter")); cmd != nil || v.prompt.status == "" {
		t.Fatalf("invalid date = cmd:%v status:%q", cmd != nil, v.prompt.status)
	}
	v.prompt.input.SetValue("2026-08-19")
	cmd := v.HandleContentKey(keyPress("enter"))
	if cmd == nil || v.prompt != nil || v.requests.kind != journalRequestDetail {
		t.Fatalf("date jump = cmd:%v prompt:%v kind:%v", cmd != nil, v.prompt != nil, v.requests.kind)
	}
}

func TestJournalOpensSelectedEntryAndBackReturnsToFeed(t *testing.T) {
	v := loadedJournalView(journalEntries(1))
	if cmd := v.HandleContentKey(keyPress("enter")); cmd == nil || v.requests.kind != journalRequestDetail {
		t.Fatal("enter should request the selected day")
	}
	v.Update(journalDetailMsg{
		requestResult: currentJournalResult(v),
		date:          v.list.entries[0].Date,
		content:       "Today was productive",
		body:          htmlutil.ToMarkdown("<p>Today was productive</p>"),
	})
	if !v.InThread() || !strings.Contains(stripANSI(v.View()), "Today was productive") {
		t.Fatalf("detail = inDetail:%v view:%q", v.InThread(), stripANSI(v.View()))
	}
	v.ExitThread()
	if v.InThread() || len(v.list.entries) != 1 {
		t.Fatalf("back = inDetail:%v entries:%d", v.InThread(), len(v.list.entries))
	}
}

func TestJournalAddTodayOpensEditorAfterSafeRead(t *testing.T) {
	v := loadedJournalView(nil)
	if cmd := v.HandleContentKey(keyPress("a")); cmd == nil {
		t.Fatal("add should read today before editing")
	}
	edit, consumed := v.Update(journalDetailMsg{
		requestResult: currentJournalResult(v),
		date:          todayJournalDate(),
		edit:          true,
	})
	if !consumed || edit == nil || v.form == nil || v.form.date != todayJournalDate() {
		t.Fatalf("add state = consumed:%v focus:%v form:%v", consumed, edit != nil, v.form != nil)
	}
}

func TestJournalDirtyEditorRequiresSecondEscape(t *testing.T) {
	v := loadedJournalView(nil)
	v.detailDate = "2026-08-19"
	v.detailContent = "Original"
	v.inDetail = true
	v.startEditor()
	v.form.input.SetValue("Changed")
	v.HandleContentKey(keyPress("esc"))
	if v.form == nil || !v.form.confirmDiscard {
		t.Fatal("first escape should warn and keep the editor")
	}
	v.HandleContentKey(keyPress("esc"))
	if v.form != nil {
		t.Fatal("second escape should discard the draft")
	}
}

func TestJournalEmptySaveRequiresExplicitConfirmedRemoval(t *testing.T) {
	v := loadedJournalView(nil)
	v.detailDate = "2026-08-19"
	v.detailContent = "Original"
	v.inDetail = true
	v.startEditor()
	v.form.input.SetValue("  ")
	if cmd := v.HandleContentKey(keyPress("ctrl+s")); cmd != nil || v.form.status == "" {
		t.Fatalf("empty save = cmd:%v status:%q", cmd != nil, v.form.status)
	}
	if cmd := v.HandleContentKey(keyPress("ctrl+d")); cmd != nil || !v.form.confirmRemove {
		t.Fatalf("first removal = cmd:%v confirmed:%v", cmd != nil, v.form.confirmRemove)
	}
	if cmd := v.HandleContentKey(keyPress("ctrl+d")); cmd == nil || v.requests.kind != journalRequestMutation {
		t.Fatalf("confirmed removal = cmd:%v kind:%v", cmd != nil, v.requests.kind)
	}
}

func TestJournalNewEmptyEntryDoesNotSave(t *testing.T) {
	v := loadedJournalView(nil)
	v.detailDate = "2026-08-19"
	v.inDetail = true
	v.startEditor()
	if cmd := v.HandleContentKey(keyPress("ctrl+s")); cmd != nil || !v.form.isError {
		t.Fatalf("new empty save = cmd:%v error:%v", cmd != nil, v.form.isError)
	}
}

func TestJournalDetailRemovalRequiresConfirmation(t *testing.T) {
	v := loadedJournalView(nil)
	v.detailDate = "2026-08-19"
	v.detailContent = "Original"
	v.inDetail = true
	if cmd := v.HandleContentKey(keyPress("x")); cmd != nil || !v.confirmRemove {
		t.Fatalf("first x = cmd:%v confirmed:%v", cmd != nil, v.confirmRemove)
	}
	if cmd := v.HandleContentKey(keyPress("x")); cmd == nil || v.requests.kind != journalRequestMutation {
		t.Fatalf("second x = cmd:%v kind:%v", cmd != nil, v.requests.kind)
	}
}

func TestJournalFailedReadDoesNotOpenEditor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newJournalView(vc)
	v.requestDate("2026-08-19", true)
	msg := v.fetchJournalEntry(vc.ctx, v.requests.id, "2026-08-19", true)()
	cmd, consumed := v.Update(msg)
	if !consumed || cmd == nil || v.form != nil || v.inDetail {
		t.Fatalf("failed read = consumed:%v error:%v form:%v detail:%v", consumed, cmd != nil, v.form != nil, v.inDetail)
	}
}

func TestJournalFetchKeepsRichContentForEditing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 1, "content": "Today was great",
			"content_html": `<div class="trix-content"><p><strong>Today</strong> was great</p></div>`,
			"type":         "Calendar::JournalEntry",
		})
	}))
	t.Cleanup(server.Close)
	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newJournalView(vc)
	loaded := v.fetchJournalEntry(vc.ctx, 1, "2026-08-19", false)().(journalDetailMsg)
	if loaded.content != "<p><strong>Today</strong> was great</p>" || loaded.body.IsEmpty() {
		t.Fatalf("loaded rich entry = content:%q empty:%v", loaded.content, loaded.body.IsEmpty())
	}
}

func TestJournalEditorContentPreservesAttachmentsWithoutTrixWrapper(t *testing.T) {
	content := `<div class="trix-content"><figure data-trix-attachment="{&quot;sgid&quot;:&quot;abc&quot;}"></figure></div>`
	want := `<figure data-trix-attachment="{&quot;sgid&quot;:&quot;abc&quot;}"></figure>`
	if got := journalEditorContent(content); got != want {
		t.Fatalf("journalEditorContent() = %q, want %q", got, want)
	}
}

func TestJournalHelpBindingsDescribeFeedSearchDateAndAdd(t *testing.T) {
	v := loadedJournalView(journalEntries(1))
	got := v.HelpBindings()
	for _, want := range []helpBinding{{"enter", "open"}, {"a", "add today"}, {"/", "search"}, {"g", "go to date"}} {
		found := false
		for _, binding := range got {
			found = found || binding == want
		}
		if !found {
			t.Errorf("help bindings %#v do not contain %#v", got, want)
		}
	}
}
