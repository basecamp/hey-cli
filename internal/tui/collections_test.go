package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"charm.land/lipgloss/v2"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/models"
)

func TestCollectionNavPickerConstrainsAndSanitizesNames(t *testing.T) {
	picker := newCollectionNavPicker([]models.Box{{
		ID:   12,
		Kind: mailSourceKindCollection,
		Name: "Kitchen remodel\x1b]2;owned\a\nwith every contractor decision and invoice",
	}}, 0)

	const width = 32
	view := stripANSI(picker.overlay("", width, 10))
	if !strings.Contains(view, "Collections") || strings.Contains(view, "\x1b]2;owned") {
		t.Errorf("picker should identify collections and sanitize controls: %q", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > width {
			t.Errorf("picker line width = %d, want at most %d: %q", lipgloss.Width(line), width, line)
		}
	}
}

func TestCollectionMembershipPickerShowsCurrentMembership(t *testing.T) {
	picker := newCollectionMembershipPicker(models.Posting{
		ID:          100,
		Summary:     "Cabinet estimate",
		Collections: []models.Collection{{ID: 12, Name: "Kitchen remodel"}},
	}, []models.Box{
		{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"},
		{ID: 34, Kind: mailSourceKindCollection, Name: "Project Apollo"},
	})
	picker.resize(20)
	view := stripANSI(picker.view(newStyles(), 80))
	for _, want := range []string{"Thread collections", "Cabinet estimate", "[x] Kitchen remodel", "[ ] Project Apollo"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker view %q does not contain %q", view, want)
		}
	}
}

func TestMailViewLoadsAndPagesCollections(t *testing.T) {
	var collectionQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/boxes.json":
			_, _ = w.Write([]byte(`[{"id":1,"kind":"imbox","name":"Imbox"}]`))
		case "/my/navigation.json":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/collections.json":
			_, _ = w.Write([]byte(`[{"id":12,"name":"Kitchen remodel","app_url":"/collections/12"}]`))
		case "/collections/12.json":
			collectionQueries = append(collectionQueries, r.URL.Query().Get("page"))
			w.Header().Set("X-Total-Count", "2")
			if r.URL.Query().Get("page") == "next-cursor" {
				_, _ = w.Write([]byte(`{"id":12,"name":"Kitchen remodel","postings":[{"id":101,"kind":"topic","summary":"Second page","app_url":"/topics/502","collections":[{"id":12,"name":"Kitchen remodel"}]}]}`))
				return
			}
			w.Header().Set("Link", "<http://"+r.Host+"/collections/12.json?page=next-cursor>; rel=\"next\"")
			_, _ = w.Write([]byte(`{"id":12,"name":"Kitchen remodel","postings":[{"id":100,"kind":"topic","summary":"First page","app_url":"/topics/501","collections":[{"id":12,"name":"Kitchen remodel"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	v := newMailView(vc)

	loaded := runCmd(v.Init()).(mailSourcesLoadedMsg)
	v.Update(loaded)
	if len(v.boxes) != 2 || v.boxes[1].Kind != mailSourceKindCollection {
		t.Fatalf("sources = %+v", v.boxes)
	}
	if cmd := v.SubnavRight(); cmd != nil || v.collections == nil {
		t.Fatal("right from the last box should open Collections")
	}
	first := runCmd(v.HandleContentKey(keyPress("enter"))).(postingsLoadedMsg)
	v.Update(first)
	if v.currentSourceKind() != mailSourceKindCollection || len(v.postingList.postings) != 1 {
		t.Fatalf("collection source = %q postings = %+v", v.currentSourceKind(), v.postingList.postings)
	}
	posting := v.postingList.postings[0]
	if posting.ResolveTopicID() != 501 || len(posting.Collections) != 1 || posting.Collections[0].ID != 12 {
		t.Errorf("posting = %+v", posting)
	}
	if v.notice != "Collection page 1 — 2 threads total" || v.folderNextPage != "next-cursor" {
		t.Errorf("page state = notice:%q next:%q", v.notice, v.folderNextPage)
	}

	second := runCmd(v.HandleContentKey(keyPress("n"))).(postingsLoadedMsg)
	v.Update(second)
	if len(v.folderPageHistory) != 1 || v.postingList.postings[0].Summary != "Second page" {
		t.Errorf("second page = history:%v postings:%+v", v.folderPageHistory, v.postingList.postings)
	}
	previous := runCmd(v.HandleContentKey(keyPress("p"))).(postingsLoadedMsg)
	v.Update(previous)
	if len(v.folderPageHistory) != 0 || v.postingList.postings[0].Summary != "First page" {
		t.Errorf("previous page = history:%v postings:%+v", v.folderPageHistory, v.postingList.postings)
	}
	if fmt.Sprint(collectionQueries) != "[ next-cursor ]" {
		t.Errorf("collection page queries = %q", collectionQueries)
	}
}

func TestMailViewCollectionMembershipAddsAndRemoves(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		v, recorded := mailWithTestServer(t, http.StatusNoContent)
		v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
		v.postingList.postings[0].AppURL = "/topics/501"

		v.HandleContentKey(keyPress("k"))
		if v.collectionPicker == nil || !v.CapturingInput() {
			t.Fatal("collection picker should capture input")
		}
		done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
		if recorded.method != http.MethodPost || recorded.path != "/topics/501/collecting" || recorded.rawQueries[len(recorded.rawQueries)-1] != "collection_id=12" {
			t.Errorf("request = %s %s?%v", recorded.method, recorded.path, recorded.rawQueries)
		}
		v.Update(done)
		if v.pendingMutations != 0 || v.notice != "Added to collection Kitchen remodel" {
			t.Errorf("mutation state = pending:%d notice:%q", v.pendingMutations, v.notice)
		}
		if memberships := v.postingList.postings[0].Collections; len(memberships) != 1 || memberships[0].ID != 12 {
			t.Errorf("memberships = %+v", memberships)
		}
	})

	t.Run("remove", func(t *testing.T) {
		v, recorded := mailWithTestServer(t, http.StatusNoContent)
		collection := models.Collection{ID: 12, Name: "Kitchen remodel"}
		v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: collection.Name})
		v.postingList.postings[0].AppURL = "/topics/501"
		v.postingList.postings[0].Collections = []models.Collection{collection}

		v.HandleContentKey(keyPress("k"))
		done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
		if recorded.method != http.MethodDelete || recorded.path != "/topics/501/collecting" || recorded.rawQueries[len(recorded.rawQueries)-1] != "collection_id=12" {
			t.Errorf("request = %s %s?%v", recorded.method, recorded.path, recorded.rawQueries)
		}
		v.Update(done)
		if len(v.postingList.postings[0].Collections) != 0 || v.notice != "Removed from collection Kitchen remodel" {
			t.Errorf("posting = %+v notice = %q", v.postingList.postings[0], v.notice)
		}
	})
}

func TestMailViewCollectionMembershipFailureKeepsState(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusBadRequest)
	collection := models.Collection{ID: 12, Name: "Kitchen remodel"}
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: collection.Name})
	v.postingList.postings[0].AppURL = "/topics/501"
	v.postingList.postings[0].Collections = []models.Collection{collection}

	v.HandleContentKey(keyPress("k"))
	done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
	v.Update(done)
	if v.pendingMutations != 0 || len(v.postingList.postings[0].Collections) != 1 {
		t.Errorf("failed mutation changed state: pending:%d posting:%+v", v.pendingMutations, v.postingList.postings[0])
	}
	if !strings.Contains(v.notice, "Could not update collections") {
		t.Errorf("notice = %q", v.notice)
	}
}

func TestMailViewCollectionMembershipRequiresTopicID(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})

	if cmd := v.HandleContentKey(keyPress("k")); cmd != nil {
		t.Fatal("unresolved posting should not start a mutation")
	}
	if v.collectionPicker != nil || v.notice != "This item does not identify an email thread" || len(recorded.requests) != 0 {
		t.Errorf("picker = %v notice = %q requests = %v", v.collectionPicker != nil, v.notice, recorded.requests)
	}
}

func TestMailViewCollectionDiscoveryFailurePreservesKnownCollections(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.sourceRequestID = 1

	v.Update(mailSourcesLoadedMsg{
		requestID:     1,
		sources:       testBoxes(),
		collectionErr: fmt.Errorf("collections unavailable"),
	})
	if sourceIndex(v.boxes, 12, mailSourceKindCollection) == 0 || v.collectionDiscoveryErr == "" {
		t.Errorf("sources = %+v error = %q", v.boxes, v.collectionDiscoveryErr)
	}
	if !strings.Contains(v.notice, "press k to retry") {
		t.Errorf("notice = %q", v.notice)
	}
}

func TestMailViewCollectionMutationIgnoresStaleSource(t *testing.T) {
	v := mailWithPostings()
	v.pendingMutations = 1
	v.Update(collectionActionDoneMsg{
		action:     "Added to collection Kitchen remodel",
		sourceID:   99,
		sourceKind: mailSourceKindCollection,
		postingID:  100,
		collection: models.Collection{ID: 12, Name: "Kitchen remodel"},
		added:      true,
	})
	if v.pendingMutations != 0 || len(v.postingList.postings[0].Collections) != 0 || v.notice != "" {
		t.Errorf("stale completion changed current source: pending:%d posting:%+v notice:%q", v.pendingMutations, v.postingList.postings[0], v.notice)
	}
}

func TestMailViewCollectionIgnoresBoxLiveRefreshes(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	if cmd := v.boxChanged(AnyBoxChanged); cmd != nil || v.liveRefreshDue {
		t.Errorf("collection should not follow box live updates: command:%v due:%v", cmd != nil, v.liveRefreshDue)
	}
}

func TestMailViewCollectionNamedImboxIsNotAnImbox(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Imbox"})
	v.boxIndex = len(v.boxes) - 1
	if v.currentBoxIsImbox() {
		t.Fatal("a collection named Imbox should remain a flat collection view")
	}
}

func TestMailViewCollectionNavigationShortcut(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	if cmd := v.handleBoxShortcut("K"); cmd == nil || v.collections == nil {
		t.Fatal("K should open the Collections picker")
	}
	items, _, _, _ := v.SubnavItems()
	last := items[len(items)-1]
	if last.label != "Collections" || last.shortcut != "K" {
		t.Errorf("collections tab = %+v", last)
	}
}

func TestMailViewRemovingFromCurrentCollectionRefreshesIt(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	collection := models.Collection{ID: 12, Name: "Kitchen remodel"}
	v.boxes = []models.Box{{ID: 12, Kind: mailSourceKindCollection, Name: collection.Name}}
	v.boxIndex = 0
	v.postingList.postings[0].AppURL = "/topics/501"
	v.postingList.postings[0].Collections = []models.Collection{collection}

	v.HandleContentKey(keyPress("k"))
	done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
	refresh, consumed := v.Update(done)
	if !consumed || refresh == nil || v.activeRequestKind != mailRequestPostings || v.postingIndex(100) >= 0 {
		t.Errorf("current collection removal should update then refresh: consumed:%v refresh:%v kind:%v postings:%+v", consumed, refresh != nil, v.activeRequestKind, v.postingList.postings)
	}
}

func TestMailViewCollectionPendingMutationBlocksAccountSwitch(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.postingList.postings[0].AppURL = "/topics/501"
	v.HandleContentKey(keyPress("k"))
	cmd := v.HandleContentKey(keyPress("enter"))
	if cmd == nil || !v.AccountSwitchBlocked() {
		t.Fatal("collection write should block account switching until completion")
	}
	v.Update(runCmd(cmd))
	if v.AccountSwitchBlocked() {
		t.Fatal("collection write completion should release account switching")
	}
}

func TestMailViewCollectionDiscoveryRetryKey(t *testing.T) {
	v := mailWithPostings()
	v.collectionDiscoveryErr = "unavailable"
	cmd := v.HandleContentKey(keyPress("k"))
	if cmd == nil || !v.loading || v.notice != "Retrying collections…" {
		t.Errorf("retry = cmd:%v loading:%v notice:%q", cmd != nil, v.loading, v.notice)
	}
}

func TestMailViewCollectionActionUsesTopicNotPostingID(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	v := newMailView(vc)
	v.boxes = []models.Box{{ID: 1, Kind: "imbox", Name: "Imbox"}, {ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"}}
	v.postingList.setPostings([]models.Posting{{ID: 100, AppURL: "/topics/501"}})

	v.HandleContentKey(keyPress("k"))
	done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
	if path != "/topics/501/collecting" || done.postingID != 100 {
		t.Errorf("path = %q posting ID = %d", path, done.postingID)
	}
}

func TestMailViewCollectionPageHelpUsesPreviousInsteadOfPaperTrail(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	v.folderPageHistory = []string{""}
	previous, paperTrail := 0, 0
	for _, binding := range v.HelpBindings() {
		if binding.key != "p" {
			continue
		}
		if binding.desc == "previous page" {
			previous++
		}
		if binding.desc == "paper trail" {
			paperTrail++
		}
	}
	if previous != 1 || paperTrail != 0 {
		t.Errorf("bindings = %v", v.HelpBindings())
	}
}

func TestMailViewCollectionMoveKeepsCollectionMembership(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	v.postingList.setPostings([]models.Posting{{ID: 100, Collections: []models.Collection{{ID: 12, Name: "Kitchen remodel"}}}})

	v.startMove()
	if v.movePicker == nil {
		t.Fatal("move picker should offer mailbox destinations")
	}
	done := runCmd(v.movePostingToBox(100, models.Box{ID: 2, Kind: "feedbox", Name: "The Feed"})).(postingActionDoneMsg)
	if done.effect != postingActionNone {
		t.Errorf("collection move effect = %v, want no row removal", done.effect)
	}
}

func TestCollectionSourceIdentityDistinguishesCollidingIDs(t *testing.T) {
	sources := []models.Box{
		{ID: 12, Kind: "imbox", Name: "Imbox"},
		{ID: 12, Kind: mailSourceKindFolder, Name: "Receipts"},
		{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"},
	}
	if got := sourceIndex(sources, 12, mailSourceKindCollection); got != 2 {
		t.Errorf("collection source index = %d, want 2", got)
	}
}

func TestMailViewCollectionPickerEscapeMakesNoRequest(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.postingList.postings[0].AppURL = "/topics/501"
	v.HandleContentKey(keyPress("k"))
	if cmd := v.HandleContentKey(keyPress("esc")); cmd != nil {
		t.Fatal("escape should not return a command")
	}
	if v.collectionPicker != nil || len(recorded.requests) != 0 {
		t.Errorf("picker = %v requests = %v", v.collectionPicker != nil, recorded.requests)
	}
}

func TestMailViewCollectionDiscoveryStaleResultIgnored(t *testing.T) {
	v := mailWithPostings()
	v.sourceRequestID = 2
	v.Update(mailSourcesLoadedMsg{requestID: 1, sources: []models.Box{{ID: 12, Kind: mailSourceKindCollection, Name: "Stale"}}})
	if len(v.boxes) != len(testBoxes()) {
		t.Errorf("stale discovery replaced sources: %+v", v.boxes)
	}
}

func TestMailViewCollectionMembershipListScrolls(t *testing.T) {
	collections := make([]models.Box, 0, 20)
	for id := int64(1); id <= 20; id++ {
		collections = append(collections, models.Box{ID: id, Kind: mailSourceKindCollection, Name: fmt.Sprintf("Collection %02d", id)})
	}
	picker := newCollectionMembershipPicker(models.Posting{ID: 100}, collections)
	picker.resize(8)
	for range 19 {
		picker.update(keyPress("down"))
	}
	view := stripANSI(picker.view(newStyles(), 40))
	if strings.Contains(view, "Collection 01") || !strings.Contains(view, "Collection 20") {
		t.Errorf("scrolled picker = %q", view)
	}
}

func TestMailViewCollectionCompletionFromWrongKindIgnored(t *testing.T) {
	v := mailWithPostings()
	v.pendingMutations = 1
	v.Update(collectionActionDoneMsg{
		sourceID:   v.currentBoxID(),
		sourceKind: mailSourceKindCollection,
		postingID:  100,
		collection: models.Collection{ID: 12, Name: "Kitchen remodel"},
		added:      true,
	})
	if len(v.postingList.postings[0].Collections) != 0 {
		t.Errorf("wrong-kind completion changed current posting: %+v", v.postingList.postings[0])
	}
}

func TestMailViewCollectionDiscoveryErrorDoesNotHideLabels(t *testing.T) {
	v := mailWithPostings()
	v.sourceRequestID = 1
	v.Update(mailSourcesLoadedMsg{
		requestID:     1,
		sources:       append(testBoxes(), models.Box{ID: 7, Kind: mailSourceKindFolder, Name: "Receipts"}),
		collectionErr: fmt.Errorf("collections unavailable"),
	})
	if !v.hasLabels() || v.hasCollections() {
		t.Errorf("sources = %+v", v.boxes)
	}
}

func TestMailViewCollectionSourceDoesNotAppearAsMoveDestination(t *testing.T) {
	posting := models.Posting{ID: 100}
	boxes := []models.Box{
		{ID: 1, Kind: "imbox", Name: "Imbox"},
		{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"},
	}
	picker := newMovePicker(posting, boxes, boxes[0])
	if len(picker.destinations) != 0 {
		t.Errorf("move destinations = %+v", picker.destinations)
	}
}

func TestMailViewCollectionNoticeSanitizesName(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen\x1b]2;owned\a\nremodel"})
	v.postingList.postings[0].AppURL = "/topics/501"
	v.HandleContentKey(keyPress("k"))
	done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
	v.Update(done)
	if strings.Contains(v.notice, "\x1b") || strings.Contains(v.notice, "\nremodel") {
		t.Errorf("unsafe collection notice = %q", v.notice)
	}
}

func TestMailViewCollectionPageMessageRejectsWrongSource(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	v.activeRequestID = 3
	_, consumed := v.Update(postingsLoadedMsg{
		requestID:  3,
		boxID:      12,
		sourceKind: mailSourceKindFolder,
		postings:   []models.Posting{{ID: 999}},
	})
	if !consumed || len(v.postingList.postings) != 2 {
		t.Errorf("wrong-kind page replaced postings: %+v", v.postingList.postings)
	}
}

func TestMailViewCollectionEmptySourceStillPages(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	v.activeRequestID = 1
	v.Update(postingsLoadedMsg{requestID: 1, boxID: 12, sourceKind: mailSourceKindCollection, totalCount: 0})
	if v.notice != "Collection page 1" || len(v.postingList.postings) != 0 {
		t.Errorf("empty collection state = notice:%q postings:%+v", v.notice, v.postingList.postings)
	}
}

func TestMailViewCollectionPickerNoCollections(t *testing.T) {
	v := mailWithPostings()
	v.postingList.postings[0].AppURL = "/topics/501"
	v.HandleContentKey(keyPress("k"))
	if v.collectionPicker != nil || v.notice != "No collections available" {
		t.Errorf("picker = %v notice = %q", v.collectionPicker != nil, v.notice)
	}
}

func TestMailViewCollectionNavigationFromLabels(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes,
		models.Box{ID: 7, Kind: mailSourceKindFolder, Name: "Receipts"},
		models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"},
	)
	v.boxIndex = len(v.boxes) - 2
	if cmd := v.SubnavRight(); cmd != nil || v.collections == nil {
		t.Fatal("right from Labels should open Collections")
	}
	v.collections = nil
	v.boxIndex = len(v.boxes) - 1
	if cmd := v.SubnavLeft(); cmd != nil || v.labels == nil {
		t.Fatal("left from Collections should open Labels")
	}
}

func TestMailViewCollectionActionErrorReleasesPendingMutation(t *testing.T) {
	v := mailWithPostings()
	v.pendingMutations = 1
	v.Update(collectionActionDoneMsg{
		sourceID:   v.currentBoxID(),
		sourceKind: v.currentSourceKind(),
		postingID:  100,
		collection: models.Collection{ID: 12, Name: "Kitchen remodel"},
		err:        fmt.Errorf("unavailable"),
	})
	if v.pendingMutations != 0 || !strings.Contains(v.notice, "unavailable") {
		t.Errorf("pending = %d notice = %q", v.pendingMutations, v.notice)
	}
}

func TestMailViewCollectionMembershipToggleUsesKnownState(t *testing.T) {
	posting := models.Posting{Collections: []models.Collection{{ID: 12, Name: "Kitchen remodel"}}}
	picker := newCollectionMembershipPicker(posting, []models.Box{{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"}})
	selected := picker.selected()
	if selected == nil || !picker.postingHasCollection(selected.ID) {
		t.Fatal("known membership should select removal behavior")
	}
}

func TestMailViewCollectionNavigationSelection(t *testing.T) {
	sources := []models.Box{
		{ID: 1, Kind: "imbox", Name: "Imbox"},
		{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"},
		{ID: 34, Kind: mailSourceKindCollection, Name: "Project Apollo"},
	}
	picker := newCollectionNavPicker(sources, 2)
	if picker.selectedSourceIndex() != 2 {
		t.Errorf("selected source = %d, want 2", picker.selectedSourceIndex())
	}
	picker.update(keyPress("up"))
	if picker.selectedSourceIndex() != 1 {
		t.Errorf("selected source after up = %d, want 1", picker.selectedSourceIndex())
	}
}

func TestMailViewCollectionMembershipCompletionUpdatesExactPosting(t *testing.T) {
	v := mailWithPostings()
	v.pendingMutations = 1
	collection := models.Collection{ID: 12, Name: "Kitchen remodel"}
	v.Update(collectionActionDoneMsg{
		sourceID:   v.currentBoxID(),
		sourceKind: v.currentSourceKind(),
		postingID:  101,
		collection: collection,
		added:      true,
	})
	if len(v.postingList.postings[0].Collections) != 0 || len(v.postingList.postings[1].Collections) != 1 {
		t.Errorf("postings = %+v", v.postingList.postings)
	}
}

func TestMailViewCollectionMembershipDoesNotDuplicate(t *testing.T) {
	v := mailWithPostings()
	collection := models.Collection{ID: 12, Name: "Kitchen remodel"}
	v.postingList.postings[0].Collections = []models.Collection{collection}
	v.updatePostingCollection(0, collection, true)
	if len(v.postingList.postings[0].Collections) != 1 {
		t.Errorf("memberships = %+v", v.postingList.postings[0].Collections)
	}
}

func TestMailViewCollectionMembershipRemoveUnknownIsStable(t *testing.T) {
	v := mailWithPostings()
	v.updatePostingCollection(0, models.Collection{ID: 12, Name: "Kitchen remodel"}, false)
	if len(v.postingList.postings[0].Collections) != 0 {
		t.Errorf("memberships = %+v", v.postingList.postings[0].Collections)
	}
}

func TestMailViewCollectionNavigationHelp(t *testing.T) {
	picker := newCollectionNavPicker([]models.Box{{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"}}, 0)
	bindings := picker.helpBindings()
	if len(bindings) != 3 || bindings[1].desc != "open" {
		t.Errorf("bindings = %+v", bindings)
	}
}

func TestMailViewCollectionMembershipHelp(t *testing.T) {
	picker := newCollectionMembershipPicker(models.Posting{}, nil)
	bindings := picker.helpBindings()
	if len(bindings) != 3 || bindings[1].desc != "toggle" {
		t.Errorf("bindings = %+v", bindings)
	}
}

func TestMailViewCollectionListErrorNoticeIsRecoverable(t *testing.T) {
	v := mailWithPostings()
	v.collectionDiscoveryErr = "unavailable"
	bindings := v.HelpBindings()
	found := false
	for _, binding := range bindings {
		if binding.key == "k" && binding.desc == "retry collections" {
			found = true
		}
	}
	if !found {
		t.Errorf("bindings = %+v", bindings)
	}
}

func TestMailViewCollectionTabSelectedWhenOpen(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	items, selected, label, _ := v.SubnavItems()
	if selected != len(items)-1 || items[selected].label != "Collections" || !strings.Contains(label, "Kitchen remodel") {
		t.Errorf("items = %+v selected = %d label = %q", items, selected, label)
	}
}

func TestMailViewCollectionPaginationNoticeSanitizesNameInHeader(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen\nremodel"})
	v.boxIndex = len(v.boxes) - 1
	_, _, label, _ := v.SubnavItems()
	if strings.Contains(label, "\n") {
		t.Errorf("unsafe collection header = %q", label)
	}
}

func TestMailViewCollectionsAndLabelsStaySeparate(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes,
		models.Box{ID: 12, Kind: mailSourceKindFolder, Name: "Receipts"},
		models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"},
	)
	labels := newLabelPicker(v.boxes, 0)
	collections := newCollectionNavPicker(v.boxes, 0)
	if len(labels.names) != 1 || labels.names[0] != "Receipts" || len(collections.names) != 1 || collections.names[0] != "Kitchen remodel" {
		t.Errorf("labels = %v collections = %v", labels.names, collections.names)
	}
}

func TestMailViewCollectionActionCompletionMessageCarriesIdentity(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, models.Box{ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"})
	v.postingList.postings[0].AppURL = "/topics/501"
	v.HandleContentKey(keyPress("k"))
	done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
	if done.sourceID != v.currentBoxID() || done.sourceKind != v.currentSourceKind() || done.collection.ID != 12 || !done.added {
		t.Errorf("completion = %+v", done)
	}
}

func TestMailViewCollectionMutationUsesOneRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	v := newMailView(vc)
	v.boxes = []models.Box{{ID: 1, Kind: "imbox", Name: "Imbox"}, {ID: 12, Kind: mailSourceKindCollection, Name: "Kitchen remodel"}}
	v.postingList.setPostings([]models.Posting{{ID: 100, AppURL: "/topics/501"}})
	v.HandleContentKey(keyPress("k"))
	runCmd(v.HandleContentKey(keyPress("enter")))
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}
}
