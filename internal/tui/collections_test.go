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

	"github.com/basecamp/hey-cli/internal/mail"
)

func TestCollectionNavPickerConstrainsAndSanitizesNames(t *testing.T) {
	picker := newCollectionNavPicker([]mail.Source{{
		ID:   12,
		Kind: mail.KindCollection,
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
	picker := newCollectionMembershipPicker(mail.Posting{
		ID:          100,
		Summary:     "Cabinet estimate",
		Collections: []mail.Collection{{ID: 12, Name: "Kitchen remodel"}},
	}, []mail.Source{
		{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"},
		{ID: 34, Kind: mail.KindCollection, Name: "Project Apollo"},
	})
	picker.resize(80, 20)
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
	if len(v.boxes) != 2 || v.boxes[1].Kind != mail.KindCollection {
		t.Fatalf("sources = %+v", v.boxes)
	}
	if cmd := v.SubnavRight(); cmd != nil || collectionsModal(v) == nil {
		t.Fatal("right from the last box should open Collections")
	}
	first := runCmd(v.HandleContentKey(keyPress("enter"))).(postingsLoadedMsg)
	more, _ := v.Update(first)
	if v.currentSourceKind() != mail.KindCollection || len(v.postingList.postings) != 1 {
		t.Fatalf("collection source = %q postings = %+v", v.currentSourceKind(), v.postingList.postings)
	}
	posting := v.postingList.postings[0]
	if posting.TopicID != 501 || len(posting.Collections) != 1 || posting.Collections[0].ID != 12 {
		t.Errorf("posting = %+v", posting)
	}
	if v.postingPaging.nextPage != "next-cursor" {
		t.Errorf("next page = %q, want next-cursor", v.postingPaging.nextPage)
	}

	// The first page does not fill the window, so the collection reads on without being
	// asked and the second page lands under the first.
	second := runCmd(more).(postingsAppendedMsg)
	if cmd, _ := v.Update(second); cmd != nil {
		t.Error("a page with no next cursor should end the collection")
	}
	if len(v.postingList.postings) != 2 || v.postingList.postings[1].Summary != "Second page" {
		t.Errorf("grown list = %+v", v.postingList.postings)
	}
	if v.postingPaging.hasMore() {
		t.Errorf("next page = %q, want none", v.postingPaging.nextPage)
	}
	if fmt.Sprint(collectionQueries) != "[ next-cursor]" {
		t.Errorf("collection page queries = %q", collectionQueries)
	}
}

func TestMailViewCollectionMembershipAddsAndRemoves(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		v, recorded := mailWithTestServer(t, http.StatusNoContent)
		v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
		v.postingList.postings[0].TopicID = 501

		v.HandleContentKey(keyPress("n"))
		if collectionModal(v) == nil || !v.CapturingInput() {
			t.Fatal("collection picker should capture input")
		}
		done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
		if recorded.method != http.MethodPost || recorded.path != "/topics/501/collecting" || recorded.rawQueries[len(recorded.rawQueries)-1] != "collection_id=12" {
			t.Errorf("request = %s %s?%v", recorded.method, recorded.path, recorded.rawQueries)
		}
		answer, _ := v.Update(done)
		if toast := deliverToView(v, answer); v.pendingMutations != 0 || toast != "Added to collection Kitchen remodel" {
			t.Errorf("mutation state = pending:%d toast:%q", v.pendingMutations, toast)
		}
		if memberships := v.postingList.postings[0].Collections; len(memberships) != 1 || memberships[0].ID != 12 {
			t.Errorf("memberships = %+v", memberships)
		}
	})

	t.Run("remove", func(t *testing.T) {
		v, recorded := mailWithTestServer(t, http.StatusNoContent)
		collection := mail.Collection{ID: 12, Name: "Kitchen remodel"}
		v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: collection.Name})
		v.postingList.postings[0].TopicID = 501
		v.postingList.postings[0].Collections = []mail.Collection{collection}

		v.HandleContentKey(keyPress("n"))
		done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
		if recorded.method != http.MethodDelete || recorded.path != "/topics/501/collecting" || recorded.rawQueries[len(recorded.rawQueries)-1] != "collection_id=12" {
			t.Errorf("request = %s %s?%v", recorded.method, recorded.path, recorded.rawQueries)
		}
		answer, _ := v.Update(done)
		if toast := deliverToView(v, answer); len(v.postingList.postings[0].Collections) != 0 || toast != "Removed from collection Kitchen remodel" {
			t.Errorf("posting = %+v toast = %q", v.postingList.postings[0], toast)
		}
	})
}

func TestMailViewCollectionMembershipFailureKeepsState(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusBadRequest)
	collection := mail.Collection{ID: 12, Name: "Kitchen remodel"}
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: collection.Name})
	v.postingList.postings[0].TopicID = 501
	v.postingList.postings[0].Collections = []mail.Collection{collection}

	v.HandleContentKey(keyPress("n"))
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
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.postingList.postings[0].TopicID = 0

	if cmd := v.HandleContentKey(keyPress("n")); cmd != nil {
		t.Fatal("unresolved posting should not start a mutation")
	}
	if collectionModal(v) != nil || v.notice != "This item does not identify an email thread" || len(recorded.requests) != 0 {
		t.Errorf("picker = %v notice = %q requests = %v", collectionModal(v) != nil, v.notice, recorded.requests)
	}
}

func TestMailViewCollectionDiscoveryFailurePreservesKnownCollections(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.sourceRequestID = 1

	v.Update(mailSourcesLoadedMsg{
		requestID:     1,
		sources:       testBoxes(),
		collectionErr: fmt.Errorf("collections unavailable"),
	})
	if sourceIndex(v.boxes, 12, mail.KindCollection) == 0 || v.collectionDiscoveryErr == "" {
		t.Errorf("sources = %+v error = %q", v.boxes, v.collectionDiscoveryErr)
	}
	if !strings.Contains(v.notice, "press n to retry") {
		t.Errorf("notice = %q", v.notice)
	}
}

func TestMailViewCollectionMutationIgnoresStaleSource(t *testing.T) {
	v := mailWithPostings()
	v.pendingMutations = 1
	v.Update(collectionActionDoneMsg{
		action:     "Added to collection Kitchen remodel",
		sourceID:   99,
		sourceKind: mail.KindCollection,
		postingID:  100,
		collection: mail.Collection{ID: 12, Name: "Kitchen remodel"},
		added:      true,
	})
	if v.pendingMutations != 0 || len(v.postingList.postings[0].Collections) != 0 || v.notice != "" {
		t.Errorf("stale completion changed current source: pending:%d posting:%+v notice:%q", v.pendingMutations, v.postingList.postings[0], v.notice)
	}
}

func TestMailViewCollectionIgnoresBoxLiveRefreshes(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	if cmd := v.boxChanged(AnyBoxChanged); cmd != nil || v.liveRefreshDue {
		t.Errorf("collection should not follow box live updates: command:%v due:%v", cmd != nil, v.liveRefreshDue)
	}
}

func TestMailViewCollectionNamedImboxIsNotAnImbox(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Imbox"})
	v.boxIndex = len(v.boxes) - 1
	if v.showsImbox() {
		t.Fatal("a collection named Imbox should remain a flat collection view")
	}
}

func TestMailViewCollectionNavigationShortcut(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	if cmd := v.handleBoxShortcut("K"); cmd == nil || collectionsModal(v) == nil {
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
	collection := mail.Collection{ID: 12, Name: "Kitchen remodel"}
	v.boxes = []mail.Source{{ID: 12, Kind: mail.KindCollection, Name: collection.Name}}
	v.boxIndex = 0
	v.postingList.postings[0].TopicID = 501
	v.postingList.postings[0].Collections = []mail.Collection{collection}

	v.HandleContentKey(keyPress("n"))
	done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
	refresh, consumed := v.Update(done)
	if !consumed || refresh == nil || v.requests.kind != mailRequestPostings || v.postingIndex(100) >= 0 {
		t.Errorf("current collection removal should update then refresh: consumed:%v refresh:%v kind:%v postings:%+v", consumed, refresh != nil, v.requests.kind, v.postingList.postings)
	}
}

func TestMailViewCollectionPendingMutationBlocksAccountSwitch(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.postingList.postings[0].TopicID = 501
	v.HandleContentKey(keyPress("n"))
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
	cmd := v.HandleContentKey(keyPress("n"))
	if cmd == nil || !v.requests.loading || v.notice != "Retrying collections…" {
		t.Errorf("retry = cmd:%v loading:%v notice:%q", cmd != nil, v.requests.loading, v.notice)
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
	v.boxes = []mail.Source{{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"}, {ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"}}
	v.postingList.setPostings([]mail.Posting{{ID: 100, TopicID: 501}})

	v.HandleContentKey(keyPress("n"))
	done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
	if path != "/topics/501/collecting" || done.postingID != 100 {
		t.Errorf("path = %q posting ID = %d", path, done.postingID)
	}
}

// A collection scrolls rather than paging, so p is free to mean what it means everywhere
// else in the mail list.
func TestMailViewCollectionHelpOffersPaperTrail(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	v.postingPaging.nextPage = "next-cursor"

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
	if previous != 0 || paperTrail != 1 {
		t.Errorf("bindings = %v", v.HelpBindings())
	}
}

func TestMailViewCollectionMoveKeepsCollectionMembership(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	v.postingList.setPostings([]mail.Posting{{ID: 100, Collections: []mail.Collection{{ID: 12, Name: "Kitchen remodel"}}}})

	v.startMove()
	if moveModal(v) == nil {
		t.Fatal("move picker should offer mailbox destinations")
	}
	done := runCmd(v.movePostingToBox(100, mail.Source{Kind: mail.KindBox, ID: 2, BoxKind: hey.BoxKindFeed, Name: "The Feed"})).(postingActionDoneMsg)
	if done.effect != postingActionNone {
		t.Errorf("collection move effect = %v, want no row removal", done.effect)
	}
}

func TestCollectionSourceIdentityDistinguishesCollidingIDs(t *testing.T) {
	sources := []mail.Source{
		{Kind: mail.KindBox, ID: 12, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
		{ID: 12, Kind: mail.KindFolder, Name: "Receipts"},
		{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"},
	}
	if got := sourceIndex(sources, 12, mail.KindCollection); got != 2 {
		t.Errorf("collection source index = %d, want 2", got)
	}
}

func TestMailViewCollectionPickerEscapeMakesNoRequest(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.postingList.postings[0].TopicID = 501
	v.HandleContentKey(keyPress("n"))
	if cmd := v.HandleContentKey(keyPress("esc")); cmd != nil {
		t.Fatal("escape should not return a command")
	}
	if collectionModal(v) != nil || len(recorded.requests) != 0 {
		t.Errorf("picker = %v requests = %v", collectionModal(v) != nil, recorded.requests)
	}
}

func TestMailViewCollectionDiscoveryStaleResultIgnored(t *testing.T) {
	v := mailWithPostings()
	v.sourceRequestID = 2
	v.Update(mailSourcesLoadedMsg{requestID: 1, sources: []mail.Source{{ID: 12, Kind: mail.KindCollection, Name: "Stale"}}})
	if len(v.boxes) != len(testBoxes()) {
		t.Errorf("stale discovery replaced sources: %+v", v.boxes)
	}
}

func TestMailViewCollectionMembershipListScrolls(t *testing.T) {
	collections := make([]mail.Source, 0, 20)
	for id := int64(1); id <= 20; id++ {
		collections = append(collections, mail.Source{ID: id, Kind: mail.KindCollection, Name: fmt.Sprintf("Collection %02d", id)})
	}
	picker := newCollectionMembershipPicker(mail.Posting{ID: 100}, collections)
	picker.resize(40, 8)
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
		sourceKind: mail.KindCollection,
		postingID:  100,
		collection: mail.Collection{ID: 12, Name: "Kitchen remodel"},
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
		sources:       append(testBoxes(), mail.Source{ID: 7, Kind: mail.KindFolder, Name: "Receipts"}),
		collectionErr: fmt.Errorf("collections unavailable"),
	})
	if !v.hasLabels() || v.hasCollections() {
		t.Errorf("sources = %+v", v.boxes)
	}
}

func TestMailViewCollectionSourceDoesNotAppearAsMoveDestination(t *testing.T) {
	posting := mail.Posting{ID: 100}
	boxes := []mail.Source{
		{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
		{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"},
	}
	picker := newMovePicker(posting, boxes, boxes[0])
	if len(picker.destinations) != 0 {
		t.Errorf("move destinations = %+v", picker.destinations)
	}
}

func TestMailViewCollectionNoticeSanitizesName(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen\x1b]2;owned\a\nremodel"})
	v.postingList.postings[0].TopicID = 501
	v.HandleContentKey(keyPress("n"))
	done := runCmd(v.HandleContentKey(keyPress("enter"))).(collectionActionDoneMsg)
	v.Update(done)
	if strings.Contains(v.notice, "\x1b") || strings.Contains(v.notice, "\nremodel") {
		t.Errorf("unsafe collection notice = %q", v.notice)
	}
}

func TestMailViewCollectionPageMessageRejectsWrongSource(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	v.requests.id = 3
	_, consumed := v.Update(postingsLoadedMsg{
		requestID:  3,
		boxID:      12,
		sourceKind: mail.KindFolder,
		postings:   []mail.Posting{{ID: 999}},
	})
	if !consumed || len(v.postingList.postings) != 2 {
		t.Errorf("wrong-kind page replaced postings: %+v", v.postingList.postings)
	}
}

func TestMailViewCollectionEmptySourceHasNothingMoreToRead(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	v.requests.id = 1

	cmd, _ := v.Update(postingsLoadedMsg{requestID: 1, boxID: 12, sourceKind: mail.KindCollection})
	if cmd != nil || len(v.postingList.postings) != 0 || v.postingPaging.hasMore() {
		t.Errorf("empty collection state = postings:%+v next:%q", v.postingList.postings, v.postingPaging.nextPage)
	}
}

func TestMailViewCollectionPickerNoCollections(t *testing.T) {
	v := mailWithPostings()
	v.postingList.postings[0].TopicID = 501
	v.HandleContentKey(keyPress("n"))
	if collectionModal(v) != nil || v.notice != "No collections available" {
		t.Errorf("picker = %v notice = %q", collectionModal(v) != nil, v.notice)
	}
}

func TestMailViewCollectionNavigationFromLabels(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes,
		mail.Source{ID: 7, Kind: mail.KindFolder, Name: "Receipts"},
		mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"},
	)
	v.boxIndex = len(v.boxes) - 2
	if cmd := v.SubnavRight(); cmd != nil || collectionsModal(v) == nil {
		t.Fatal("right from Labels should open Collections")
	}
	v.modal = nil
	v.boxIndex = len(v.boxes) - 1
	if cmd := v.SubnavLeft(); cmd != nil || labelsModal(v) == nil {
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
		collection: mail.Collection{ID: 12, Name: "Kitchen remodel"},
		err:        fmt.Errorf("unavailable"),
	})
	if v.pendingMutations != 0 || !strings.Contains(v.notice, "unavailable") {
		t.Errorf("pending = %d notice = %q", v.pendingMutations, v.notice)
	}
}

func TestMailViewCollectionMembershipToggleUsesKnownState(t *testing.T) {
	posting := mail.Posting{Collections: []mail.Collection{{ID: 12, Name: "Kitchen remodel"}}}
	picker := newCollectionMembershipPicker(posting, []mail.Source{{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"}})
	selected := picker.selected()
	if selected == nil || !picker.postingHasCollection(selected.ID) {
		t.Fatal("known membership should select removal behavior")
	}
}

func TestMailViewCollectionNavigationSelection(t *testing.T) {
	sources := []mail.Source{
		{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
		{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"},
		{ID: 34, Kind: mail.KindCollection, Name: "Project Apollo"},
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
	collection := mail.Collection{ID: 12, Name: "Kitchen remodel"}
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
	collection := mail.Collection{ID: 12, Name: "Kitchen remodel"}
	v.postingList.postings[0].Collections = []mail.Collection{collection}
	v.updatePostingCollection(0, collection, true)
	if len(v.postingList.postings[0].Collections) != 1 {
		t.Errorf("memberships = %+v", v.postingList.postings[0].Collections)
	}
}

func TestMailViewCollectionMembershipRemoveUnknownIsStable(t *testing.T) {
	v := mailWithPostings()
	v.updatePostingCollection(0, mail.Collection{ID: 12, Name: "Kitchen remodel"}, false)
	if len(v.postingList.postings[0].Collections) != 0 {
		t.Errorf("memberships = %+v", v.postingList.postings[0].Collections)
	}
}

func TestMailViewCollectionNavigationHelp(t *testing.T) {
	picker := newCollectionNavPicker([]mail.Source{{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"}}, 0)
	bindings := picker.helpBindings()
	if len(bindings) != 3 || bindings[1].desc != "open" {
		t.Errorf("bindings = %+v", bindings)
	}
}

func TestMailViewCollectionMembershipHelp(t *testing.T) {
	picker := newCollectionMembershipPicker(mail.Posting{}, nil)
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
		if binding.key == "n" && binding.desc == "retry collections" {
			found = true
		}
	}
	if !found {
		t.Errorf("bindings = %+v", bindings)
	}
}

func TestMailViewCollectionTabSelectedWhenOpen(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.boxIndex = len(v.boxes) - 1
	items, selected, label, _ := v.SubnavItems()
	if selected != len(items)-1 || items[selected].label != "Collections" || !strings.Contains(label, "Kitchen remodel") {
		t.Errorf("items = %+v selected = %d label = %q", items, selected, label)
	}
}

func TestMailViewCollectionPaginationNoticeSanitizesNameInHeader(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen\nremodel"})
	v.boxIndex = len(v.boxes) - 1
	_, _, label, _ := v.SubnavItems()
	if strings.Contains(label, "\n") {
		t.Errorf("unsafe collection header = %q", label)
	}
}

func TestMailViewCollectionsAndLabelsStaySeparate(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes,
		mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"},
		mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"},
	)
	labels := newLabelPicker(v.boxes, 0)
	collections := newCollectionNavPicker(v.boxes, 0)
	if len(labels.names) != 1 || labels.names[0] != "Receipts" || len(collections.names) != 1 || collections.names[0] != "Kitchen remodel" {
		t.Errorf("labels = %v collections = %v", labels.names, collections.names)
	}
}

func TestMailViewCollectionActionCompletionMessageCarriesIdentity(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
	v.postingList.postings[0].TopicID = 501
	v.HandleContentKey(keyPress("n"))
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
	v.boxes = []mail.Source{{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"}, {ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"}}
	v.postingList.setPostings([]mail.Posting{{ID: 100, TopicID: 501}})
	v.HandleContentKey(keyPress("n"))
	runCmd(v.HandleContentKey(keyPress("enter")))
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}
}
