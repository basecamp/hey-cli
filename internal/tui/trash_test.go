package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/mail"
)

func trashView(t *testing.T, handler http.HandlerFunc) *mailView {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	v := mailWithPostings()
	v.vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v.boxes = append(v.boxes, mail.Source{Kind: mail.KindTrash, Name: "Trash"})
	v.boxIndex = v.trashIndex()
	v.postingList.setPostings([]mail.Posting{{ID: 701, TopicID: 701, Name: "Conference travel plans", Seen: true}})
	return v
}

func TestTrashTabAndNumberShortcut(t *testing.T) {
	v := mailWithPostings()
	v.applySources(v.boxes)
	v.requests.loading = false
	if v.trashIndex() < 0 {
		t.Fatal("Trash source missing")
	}
	items, _, _, _ := v.SubnavItems()
	last := items[len(items)-1]
	if last.shortcut != "0" || last.label != "Trash" {
		t.Fatalf("last tab = %+v", last)
	}
	cmd := v.handleBoxShortcut("0")
	if cmd == nil || v.currentSourceKind() != mail.KindTrash {
		t.Fatal("0 did not open Trash")
	}
	items, selected, label, _ := v.SubnavItems()
	if items[selected].shortcut != "0" || label != "Trash" {
		t.Fatal("Trash tab was not selected")
	}
	v.requests.loading = false
	v.SubnavLeft()
	if !v.seenActive && v.modal == nil {
		t.Fatal("left arrow did not leave Trash")
	}
}

func TestTrashRestoresTopicAndOnlyRemovesRowOnSuccess(t *testing.T) {
	for _, inThread := range []bool{false, true} {
		status := http.StatusForbidden
		calls := 0
		v := trashView(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method != http.MethodPut || r.URL.Path != "/topics/701/status/active.json" {
				t.Errorf("restore request = %s %s", r.Method, r.URL)
			}
			w.WriteHeader(status)
		})
		if inThread {
			v.inThread = true
			v.threadFromTrash = true
			v.topicID = 701
			v.threadPosting = v.postingList.postings[0]
		}
		cmd := v.HandleContentKey(keyPress("i"))
		if cmd == nil || v.pendingMutations != 1 {
			t.Fatal("restore not dispatched")
		}
		if v.HandleContentKey(keyPress("i")) != nil {
			t.Fatal("duplicate restore dispatch")
		}
		v.Update(cmd())
		if len(v.postingList.postings) != 1 || v.inThread != inThread || v.pendingMutations != 0 {
			t.Fatal("failure discarded thread")
		}
		status = http.StatusOK
		cmd = v.HandleContentKey(keyPress("I"))
		v.Update(cmd())
		if calls != 2 || len(v.postingList.postings) != 0 || v.inThread || v.pendingMutations != 0 {
			t.Fatal("success did not restore and remove the row")
		}
	}
}

func TestTrashBlocksPostingMutations(t *testing.T) {
	for _, inThread := range []bool{false, true} {
		v := trashView(t, func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusOK)
		})
		if inThread {
			v.inThread = true
			v.threadFromTrash = true
			v.topicID = 701
			v.threadPosting = v.postingList.postings[0]
		}
		for _, key := range []string{"t", "T", "v", "V", "b", "n", "w", "r", "f", "a", "l", "d", "p", "u", "e", "!", "-", "+", "ctrl+b", "ctrl+u", "space", "x"} {
			if cmd := v.HandleContentKey(keyPress(key)); cmd != nil || v.modal != nil || v.pendingMutations != 0 {
				t.Fatalf("%q dispatched a posting action from Trash", key)
			}
		}
		if v.trashOffered() {
			t.Fatal("Trash offered t")
		}
		restored := false
		for _, binding := range v.HelpBindings() {
			if binding.key == "t" || binding.key == "v" || binding.key == "r" {
				t.Fatalf("unsafe help binding %+v", binding)
			}
			if binding.key == "i" && binding.desc == "restore" {
				restored = true
			}
		}
		if !restored {
			t.Fatal("missing restore help")
		}
	}
}

func TestTrashRowsReadTopicsAndPageByOpaqueCursor(t *testing.T) {
	var pages []string
	v := trashView(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/topics/trash.json" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		w.Header().Set("Content-Type", "application/json")
		if page == "" {
			w.Header().Set("Link", `<https://app.hey.com/topics/trash.json?page=opaque-next>; rel="next"`)
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "Trash", "topics": []map[string]any{{"id": 701, "name": "Conference travel plans", "creator": map[string]any{"name": "Alex Morgan"}, "latest_entry": map[string]any{"summary": "Flight details"}}}})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"topics": []any{}})
		}
	})
	source := *v.currentSource()
	rows, next, err := v.readPostingsPage(v.vc.ctx, source, "")
	if err != nil || len(rows) != 1 || rows[0].TopicID != 701 || rows[0].ID != 701 || !rows[0].Seen || next != "opaque-next" {
		t.Fatalf("first page %+v %q %v", rows, next, err)
	}
	_, next, err = v.readPostingsPage(v.vc.ctx, source, next)
	if err != nil || next != "" || len(pages) != 2 || pages[1] != "opaque-next" {
		t.Fatalf("paging %v %q %v", pages, next, err)
	}
	if !strings.Contains(rows[0].Summary, "Flight details") {
		t.Fatal("missing summary")
	}
}

func TestRestoreReplyCannotRemoveSameIDFromAnotherSource(t *testing.T) {
	v := trashView(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	cmd := v.HandleContentKey(keyPress("i"))
	v.boxIndex = 0
	v.postingList.setPostings([]mail.Posting{{ID: 701, TopicID: 900}})
	v.Update(cmd())
	if len(v.postingList.postings) != 1 {
		t.Fatal("late restore removed unrelated row")
	}
}

func TestOpeningTrashThreadDoesNotObservePosting(t *testing.T) {
	v := trashView(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/topics/701.json":
			_, _ = w.Write([]byte(`{"id":701,"name":"Conference travel plans","status":"trashed","screened_in":true,"entries":[{"id":801,"kind":"message","summary":"Flight details"}]}`))
		case "/topics/701/entries.json":
			_, _ = w.Write([]byte(`[{"id":801,"kind":"message","summary":"Flight details"}]`))
		case "/messages/801.json":
			_, _ = w.Write([]byte(`{"id":801,"content":"<p>Flight details</p>"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	cmd := v.openSelected()
	if cmd == nil {
		t.Fatal("Enter did not start a thread read")
	}
	loaded := cmd()
	v.Update(loaded)
	if !v.inThread || !v.threadFromTrash || v.topicID != 701 || v.pendingMutations != 0 || v.fileablePosting() != nil {
		t.Fatalf("Trash reader state: inThread=%v trash=%v topic=%d pending=%d", v.inThread, v.threadFromTrash, v.topicID, v.pendingMutations)
	}
}

func TestRestoreInvalidatesEarlierTrashPageReads(t *testing.T) {
	v := trashView(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	cmd := v.HandleContentKey(keyPress("i"))
	oldMore, oldLive := v.moreRequestID, v.liveRequestID
	v.Update(cmd())
	v.Update(postingsAppendedMsg{requestID: oldMore, boxID: 0, sourceKind: mail.KindTrash, postings: []mail.Posting{{ID: 701, TopicID: 701}}})
	v.Update(postingsRefreshedMsg{requestID: oldLive, boxID: 0, sourceKind: mail.KindTrash, postings: []mail.Posting{{ID: 701, TopicID: 701}}})
	if len(v.postingList.postings) != 0 {
		t.Fatal("an earlier read resurrected the restored topic")
	}
}

func TestTrashCanBeReachedFromEndOfTabNavigation(t *testing.T) {
	for _, kind := range []mail.Kind{mail.KindBox, mail.KindFolder, mail.KindCollection} {
		v := mailWithPostings()
		v.boxes = append(v.boxes, mail.Source{Kind: mail.KindTrash, Name: "Trash"})
		switch kind {
		case mail.KindBundle, mail.KindContact, mail.KindTrash:
			t.Fatalf("unexpected source kind %s", kind)
		case mail.KindBox:
			v.seenActive = true
		case mail.KindFolder, mail.KindCollection:
			v.boxes = append(v.boxes, mail.Source{Kind: kind, ID: 12, Name: "Travel"})
			v.boxIndex = len(v.boxes) - 1
		}
		if cmd := v.SubnavRight(); cmd == nil || v.currentSourceKind() != mail.KindTrash || v.seenActive {
			t.Fatalf("right from %s did not reach Trash", kind)
		}
	}
}

func TestMovePickerNeverOffersTrashAsDestination(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{Kind: mail.KindTrash, Name: "Trash"})
	v.startMove()
	picker := modalOf[*movePicker](v)
	if picker == nil {
		t.Fatal("move picker missing")
	}
	for _, source := range picker.destinations {
		if source.Kind == mail.KindTrash {
			t.Fatal("Trash offered as a box move")
		}
	}
}

func TestRestoreInvalidatesEarlierTrashReload(t *testing.T) {
	v := trashView(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	restore := v.HandleContentKey(keyPress("i"))
	_ = v.reloadPostings()
	oldRequest := v.requests.id
	v.Update(restore())
	v.Update(postingsLoadedMsg{requestID: oldRequest, boxID: 0, sourceKind: mail.KindTrash, postings: []mail.Posting{{ID: 701, TopicID: 701}}})
	if len(v.postingList.postings) != 0 {
		t.Fatal("an earlier reload resurrected a restored topic")
	}
}

func TestPostingActionCannotRemoveTrashTopicWithSameNumericID(t *testing.T) {
	v := trashView(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("unexpected request") })
	v.searchActive = true
	v.searchList.setPostings([]mail.Posting{{ID: 701, TopicID: 900}})
	v.Update(postingActionDoneMsg{boxID: 0, sourceKind: mail.KindTrash, postingID: 701, effect: postingActionRemove})
	if len(v.postingList.postings) != 1 || len(v.searchList.postings) != 0 {
		t.Fatal("posting completion affected the Trash topic instead of only the search result")
	}
}

func TestRestoreAwayFromTrashInvalidatesEarlierLiveRefresh(t *testing.T) {
	var restored atomic.Bool
	v := trashView(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/topics/701/status/active.json":
			restored.Store(true)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/topics/trash.json":
			w.Header().Set("Content-Type", "application/json")
			topics := []map[string]any{{"id": 702, "name": "Hotel receipt"}}
			if !restored.Load() {
				topics = append(topics, map[string]any{"id": 701, "name": "Conference travel plans"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"topics": topics})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	// Hold the old response until after a restore and a round trip through the Imbox.
	refresh := v.refreshBox(AnyBoxChanged)
	if refresh == nil {
		t.Fatal("Trash refresh did not start")
	}
	stale := refresh()
	restore := v.HandleContentKey(keyPress("i"))
	if restore == nil {
		t.Fatal("restore did not start")
	}
	v.switchBox(0)
	v.Update(currentPostingsLoaded(v, []mail.Posting{{ID: 701, TopicID: 900, Name: "Dinner reservation"}}))
	moreRequest := v.moreRequestID
	v.Update(restore())
	if len(v.postingList.postings) != 1 || v.postingList.postings[0].TopicID != 900 {
		t.Fatal("restore changed the Imbox row with the same numeric ID")
	}
	if v.moreRequestID != moreRequest {
		t.Fatal("restore invalidated paging for the other source")
	}
	read := v.switchBox(v.trashIndex())
	v.Update(read())
	if got := postingIDs(v.postingList.postings); len(got) != 1 || postingIndexIn(v.postingList.postings, 702) < 0 {
		t.Fatalf("fresh Trash read = %v, want only the remaining topic", got)
	}
	v.Update(stale)
	if got := postingIDs(v.postingList.postings); len(got) != 1 || postingIndexIn(v.postingList.postings, 702) < 0 {
		t.Fatalf("stale refresh reinserted the restored topic: %v", got)
	}
}

func TestFailedRestoreAwayFromTrashKeepsLiveRefreshValid(t *testing.T) {
	v := trashView(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	restore := v.HandleContentKey(keyPress("i"))
	oldLive := v.liveRequestID
	v.switchBox(0)
	v.Update(restore())
	if v.liveRequestID != oldLive || v.pendingMutations != 0 || v.trashRestoreID != 0 {
		t.Fatal("failed restore invalidated the refresh or left the mutation pending")
	}
	v.switchBox(v.trashIndex())
	v.Update(currentPostingsLoaded(v, []mail.Posting{{ID: 701, TopicID: 701}}))
	v.Update(postingsRefreshedMsg{
		requestID: oldLive, sourceKind: mail.KindTrash,
		postings: []mail.Posting{{ID: 701, TopicID: 701}, {ID: 702, TopicID: 702}},
	})
	if len(v.postingList.postings) != 2 {
		t.Fatal("failed restore prevented an otherwise valid refresh")
	}
}
