package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/models"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	pixels := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	pixels.Set(0, 0, color.NRGBA{R: 0x22, G: 0x66, B: 0xAA, A: 0xFF})
	if err := png.Encode(&encoded, pixels); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func testVC() *viewContext {
	return &viewContext{
		ctx:           context.Background(),
		styles:        newStyles(),
		imageRenderer: textImageRenderer{},
		width:         80,
		height:        30,
	}
}

func mailWithPostings() *mailView {
	v := newMailView(testVC())
	v.boxes = orderBoxes(testBoxes())
	v.boxIndex = 0
	v.Update(currentPostingsLoaded(v, testPostings()))
	return v
}

func currentPostingsLoaded(v *mailView, postings []models.Posting) postingsLoadedMsg {
	return postingsLoadedMsg{
		requestID: v.activeRequestID,
		boxID:     v.currentBoxID(),
		postings:  postings,
	}
}

type recordedMailRequest struct {
	method     string
	path       string
	requests   []string
	rawQueries []string
	body       struct {
		PostingIDs []int64 `json:"posting_ids"`
		BoxID      *int64  `json:"box_id"`
	}
}

func mailWithTestServer(t *testing.T, status int) (*mailView, *recordedMailRequest) {
	t.Helper()

	recorded := &recordedMailRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boxes.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"id":1,"kind":"imbox","name":"Imbox"},
				{"id":2,"kind":"feedbox","name":"The Feed"},
				{"id":3,"kind":"asidebox","name":"Set Aside"},
				{"id":4,"kind":"laterbox","name":"Reply Later"},
				{"id":5,"kind":"trailbox","name":"Paper Trail"}
			]`))
			return
		}

		recorded.method = r.Method
		recorded.path = r.URL.Path
		recorded.requests = append(recorded.requests, r.Method+" "+r.URL.Path)
		recorded.rawQueries = append(recorded.rawQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/advanced_search.json":
			_, _ = w.Write([]byte(`{"matches":[{"topic":{"id":100,"name":"Hello world","app_url":"https://app.hey.com/topics/100","updated_at":"2026-08-19T09:00:00Z"},"posting_id":10,"entries":[{"id":501,"kind":"message","summary":"Matching message summary","created_at":"2026-08-19T09:00:00Z","creator":{"id":10,"name":"Alice"}}]}]}`))
		case "/topics/100.json":
			_, _ = w.Write([]byte(`{"id":100,"name":"Hello world","entries":[{"id":501,"kind":"message","summary":"Hello world","created_at":"2026-08-19T09:00:00Z","creator":{"id":10,"name":"Alice"}}]}`))
		case "/messages/501.json":
			_, _ = w.Write([]byte(`{"id":501,"subject":"Hello world","content":"<p>Message body</p>","created_at":"2026-08-19T09:00:00Z","creator":{"id":10,"name":"Alice"}}`))
		default:
			if r.Method == http.MethodDelete {
				for _, value := range strings.Split(r.URL.Query().Get("posting_ids"), ",") {
					if id, err := strconv.ParseInt(value, 10, 64); err == nil {
						recorded.body.PostingIDs = append(recorded.body.PostingIDs, id)
					}
				}
			} else {
				_ = json.NewDecoder(r.Body).Decode(&recorded.body)
			}
			w.WriteHeader(status)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(
		&hey.Config{BaseURL: server.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	vc := testVC()
	vc.sdk = client
	vc.ctx = context.Background()
	v := newMailView(vc)
	v.Resize(vc.width, vc.height)
	v.boxes = orderBoxes(testBoxes())
	v.Update(currentPostingsLoaded(v, testPostings()))
	return v, recorded
}

// --- Init ---

func TestMailViewInitFetchesBoxes(t *testing.T) {
	v := newMailView(testVC())
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init with no boxes should return a fetch command")
	}
	if !v.loading {
		t.Error("Init should set loading = true")
	}
}

func TestMailViewInitRefetchesWhenBoxesLoaded(t *testing.T) {
	v := newMailView(testVC())
	v.boxes = testBoxes()
	v.boxIndex = 0
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init with boxes should return a fetch command for current box")
	}
	if !v.loading {
		t.Error("Init should set loading = true")
	}
}

// --- Update: message routing ---

func TestMailViewHandlesBoxesLoaded(t *testing.T) {
	v := newMailView(testVC())
	_, consumed := v.Update(boxesLoadedMsg(testBoxes()))
	if !consumed {
		t.Error("boxesLoadedMsg should be consumed")
	}
	if len(v.boxes) != 3 {
		t.Errorf("expected 3 boxes, got %d", len(v.boxes))
	}
}

func TestMailViewHandlesPostingsLoaded(t *testing.T) {
	v := newMailView(testVC())
	v.boxes = testBoxes()
	v.loading = true

	_, consumed := v.Update(currentPostingsLoaded(v, testPostings()))
	if !consumed {
		t.Error("postingsLoadedMsg should be consumed")
	}
	if v.loading {
		t.Error("loading should be false after postings loaded")
	}
	if len(v.postingList.postings) != 2 {
		t.Errorf("expected 2 postings, got %d", len(v.postingList.postings))
	}
}

func TestMailViewIgnoresPostingLoadFromEarlierBoxVisit(t *testing.T) {
	v := mailWithPostings()

	v.SubnavRight()
	stale := currentPostingsLoaded(v, []models.Posting{{ID: 200, Summary: "The Feed message"}})
	v.SubnavLeft()
	current := currentPostingsLoaded(v, []models.Posting{{ID: 300, Summary: "Current Imbox message"}})
	v.Update(current)
	v.Update(stale)

	if len(v.postingList.postings) != 1 || v.postingList.postings[0].ID != 300 {
		t.Errorf("an earlier box visit overwrote the current postings: %v", v.postingList.postings)
	}
}

func TestMailViewReportsCurrentPostingLoadFailure(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusBadRequest)

	failed, ok := runCmd(v.SubnavRight()).(postingsLoadedMsg)
	if !ok || failed.err == nil {
		t.Fatalf("posting load returned %#v, want an error", failed)
	}
	cmd, consumed := v.Update(failed)

	if !consumed || cmd == nil {
		t.Fatal("current posting load error should be reported")
	}
	if _, ok := runCmd(cmd).(errMsg); !ok {
		t.Error("current posting load error should produce errMsg")
	}
	if v.loading {
		t.Error("failed current posting load should stop loading")
	}
}

func TestMailViewIgnoresPostingErrorFromEarlierBoxVisit(t *testing.T) {
	v := mailWithPostings()

	v.SubnavRight()
	stale := currentPostingsLoaded(v, nil)
	stale.err = fmt.Errorf("old box failed")
	v.SubnavLeft()
	cmd, consumed := v.Update(stale)

	if !consumed || cmd != nil {
		t.Error("an earlier box error should be ignored")
	}
	if !v.loading {
		t.Error("an earlier box error should not stop the current box load")
	}
}

func TestMailViewHandlesTopicLoaded(t *testing.T) {
	v := mailWithPostings()
	v.loading = true

	_, consumed := v.Update(topicLoadedMsg{
		boxID:   1,
		topicID: 100,
		title:   "Test topic",
		entries: []models.Entry{{Creator: models.Contact{Name: "Alice"}, Body: "hello"}},
	})
	if !consumed {
		t.Error("topicLoadedMsg should be consumed")
	}
	if !v.inThread || v.topicID != 100 || v.topicName != "Test topic" {
		t.Errorf("thread state = open:%v id:%d name:%q", v.inThread, v.topicID, v.topicName)
	}
	if v.loading {
		t.Error("loading should be false")
	}
}

func TestMailViewIgnoresUnrelatedMessages(t *testing.T) {
	v := newMailView(testVC())
	_, consumed := v.Update(calendarsLoadedMsg{})
	if consumed {
		t.Error("calendarsLoadedMsg should not be consumed by mailView")
	}
}

// --- Posting actions ---

func TestMailViewPostingKeysCallExpectedEndpoints(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		path   string
		boxID  int64
		effect postingActionEffect
		notice string
	}{
		{"reply later", "l", "/postings/moves.json", 4, postingActionRemove, "Thread moved to Reply Later"},
		{"set aside", "a", "/postings/moves.json", 3, postingActionRemove, "Thread moved to Set Aside"},
		{"seen", "e", "/postings/seen.json", 0, postingActionSeen, "Thread marked as seen"},
		{"feed", "d", "/postings/moves.json", 2, postingActionRemove, "Thread moved to The Feed"},
		{"paper trail", "p", "/postings/moves.json", 5, postingActionRemove, "Thread moved to Paper Trail"},
		{"trash", "t", "/postings/trash.json", 0, postingActionRemove, "Thread moved to Trash"},
		{"spam", "s", "/postings/spam.json", 0, postingActionRemove, "Thread marked as spam"},
		{"ignore", "-", "/postings/mutings.json", 0, postingActionIgnore, "Thread ignored"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)

			msg := runCmd(v.HandleContentKey(keyPress(tt.key)))
			done, ok := msg.(postingActionDoneMsg)
			if !ok || done.err != nil {
				t.Fatalf("posting command returned %#v", msg)
			}
			if done.boxID != 1 || done.postingID != 100 {
				t.Errorf("action origin = box %d posting %d, want box 1 posting 100", done.boxID, done.postingID)
			}
			if done.effect != tt.effect {
				t.Errorf("action effect = %v, want %v", done.effect, tt.effect)
			}
			v.Update(done)

			if recorded.method != http.MethodPost || recorded.path != tt.path {
				t.Errorf("request = %s %s, want POST %s", recorded.method, recorded.path, tt.path)
			}
			if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
				t.Errorf("posting_ids = %v, want [100]", recorded.body.PostingIDs)
			}
			if tt.boxID == 0 {
				if recorded.body.BoxID != nil {
					t.Errorf("box_id = %d, want field omitted", *recorded.body.BoxID)
				}
			} else if recorded.body.BoxID == nil {
				t.Errorf("box_id is omitted, want %d", tt.boxID)
			} else if *recorded.body.BoxID != tt.boxID {
				t.Errorf("box_id = %d, want %d", *recorded.body.BoxID, tt.boxID)
			}

			wantPostings := 2
			if tt.effect == postingActionRemove {
				wantPostings = 1
			}
			if len(v.postingList.postings) != wantPostings {
				t.Errorf("posting count = %d, want %d", len(v.postingList.postings), wantPostings)
			}
			if tt.effect == postingActionSeen && !v.postingList.postings[0].Seen {
				t.Error("selected posting should be marked seen")
			}
			if tt.effect == postingActionIgnore && !v.postingList.postings[0].Muted {
				t.Error("selected posting should be ignored")
			}
			if v.notice != tt.notice || !strings.Contains(v.View(), tt.notice) {
				t.Errorf("notice = %q, want visible %q", v.notice, tt.notice)
			}
		})
	}
}

func TestMailViewStopIgnoringCallsDeleteAndKeepsThreadVisible(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.postingList.postings[0].Muted = true

	msg := runCmd(v.HandleContentKey(keyPress("+")))
	done, ok := msg.(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("stop-ignoring command returned %#v", msg)
	}
	if recorded.method != http.MethodDelete || recorded.path != "/postings/mutings.json" {
		t.Errorf("request = %s %s, want DELETE /postings/mutings.json", recorded.method, recorded.path)
	}
	if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
		t.Errorf("posting_ids = %v, want [100]", recorded.body.PostingIDs)
	}

	v.Update(done)
	if len(v.postingList.postings) != 2 {
		t.Errorf("posting count = %d, want ignored thread to remain visible", len(v.postingList.postings))
	}
	if v.postingList.postings[0].Muted {
		t.Error("selected posting should no longer be ignored")
	}
	if v.notice != "Stopped ignoring thread" {
		t.Errorf("notice = %q, want %q", v.notice, "Stopped ignoring thread")
	}
}

func TestMailViewIgnoreActionsSkipThreadsAlreadyInRequestedState(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		muted  bool
		notice string
	}{
		{"already ignored", "-", true, "Already ignoring thread"},
		{"not ignored", "+", false, "Thread is not ignored"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)
			v.postingList.postings[0].Muted = tt.muted
			if cmd := v.HandleContentKey(keyPress(tt.key)); cmd != nil {
				t.Fatal("redundant action should not start a request")
			}
			if len(recorded.requests) != 0 {
				t.Errorf("redundant action made requests: %v", recorded.requests)
			}
			if v.notice != tt.notice {
				t.Errorf("notice = %q, want %q", v.notice, tt.notice)
			}
		})
	}
}

func TestMailViewMovePickerMovesToSelectedBox(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = []models.Box{
		{ID: 1, Kind: hey.BoxKindImbox, Name: "Imbox"},
		{ID: 2, Kind: hey.BoxKindFeed, Name: "The Feed"},
		{ID: 3, Kind: hey.BoxKindSetAside, Name: "Set Aside"},
		{ID: 4, Kind: hey.BoxKindLater, Name: "Reply Later"},
		{ID: 5, Kind: hey.BoxKindTrail, Name: "Paper Trail"},
		{ID: 6, Kind: hey.BoxKindBubbleUp, Name: "Bubble Up"},
	}

	if cmd := v.HandleContentKey(keyPress("m")); cmd != nil {
		t.Fatal("opening the move picker should not start a request")
	}
	if !v.CapturingInput() || v.movePicker == nil {
		t.Fatal("move picker should capture input")
	}
	if len(v.movePicker.destinations) != 4 {
		t.Fatalf("destinations = %v, want four boxes", v.movePicker.destinations)
	}
	if view := v.View(); !strings.Contains(view, "Move thread") || !strings.Contains(view, "marks the thread as seen") {
		t.Errorf("move picker does not use thread terminology: %q", view)
	} else if strings.Contains(view, "Bubble Up") || strings.Contains(view, "\n  Imbox") {
		t.Errorf("move picker offered an ineligible destination: %q", view)
	}

	msg := runCmd(v.HandleContentKey(keyPress("enter")))
	done, ok := msg.(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("move command returned %#v", msg)
	}
	if v.movePicker != nil || v.CapturingInput() {
		t.Error("move picker should close after choosing a destination")
	}
	if recorded.body.BoxID == nil || *recorded.body.BoxID != 2 {
		t.Errorf("box_id = %v, want 2", recorded.body.BoxID)
	}
	if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
		t.Errorf("posting_ids = %v, want [100]", recorded.body.PostingIDs)
	}

	v.Update(done)
	if v.postingIndex(100) >= 0 {
		t.Error("moved posting should leave the current box")
	}
	if v.notice != "Thread moved to The Feed" {
		t.Errorf("notice = %q", v.notice)
	}
}

func TestMailViewMovePickerSelectsWithArrowKeys(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = []models.Box{
		{ID: 1, Kind: hey.BoxKindImbox, Name: "Imbox"},
		{ID: 2, Kind: hey.BoxKindFeed, Name: "The Feed"},
		{ID: 3, Kind: hey.BoxKindSetAside, Name: "Set Aside"},
	}

	v.HandleContentKey(keyPress("m"))
	v.HandleContentKey(keyPress("down"))
	msg := runCmd(v.HandleContentKey(keyPress("enter")))
	if done, ok := msg.(postingActionDoneMsg); !ok || done.err != nil {
		t.Fatalf("move command returned %#v", msg)
	}
	if recorded.body.BoxID == nil || *recorded.body.BoxID != 3 {
		t.Errorf("box_id = %v, want 3", recorded.body.BoxID)
	}
}

func TestMailViewMovePickerCancelsWithoutRequest(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = []models.Box{
		{ID: 1, Kind: hey.BoxKindImbox, Name: "Imbox"},
		{ID: 2, Kind: hey.BoxKindFeed, Name: "The Feed"},
	}

	v.HandleContentKey(keyPress("m"))
	if cmd := v.HandleContentKey(keyPress("esc")); cmd != nil {
		t.Fatal("canceling the move picker should not return a command")
	}
	if v.movePicker != nil || v.CapturingInput() {
		t.Error("escape should close the move picker")
	}
	if len(recorded.requests) != 0 {
		t.Errorf("canceling made requests: %v", recorded.requests)
	}
}

func TestMailViewMoveWithinCurrentBoxSkipsRequest(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		kind    string
		boxID   int64
		display string
	}{
		{"reply later", "l", hey.BoxKindLater, 4, "Reply Later"},
		{"set aside", "a", hey.BoxKindSetAside, 3, "Set Aside"},
		{"feed", "d", hey.BoxKindFeed, 2, "The Feed"},
		{"paper trail", "p", hey.BoxKindTrail, 5, "Paper Trail"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)
			v.boxes = []models.Box{{ID: tt.boxID, Kind: tt.kind, Name: tt.name}}
			v.boxIndex = 0

			if cmd := v.HandleContentKey(keyPress(tt.key)); cmd != nil {
				t.Fatal("move within the current box should not start a request")
			}
			if len(v.postingList.postings) != len(testPostings()) || v.postingList.postings[0].ID != 100 {
				t.Errorf("move within current box changed postings: %v", v.postingList.postings)
			}
			if len(recorded.requests) != 0 {
				t.Errorf("move within current box made requests: %v", recorded.requests)
			}
			if v.notice != "Already in "+tt.display {
				t.Errorf("notice = %q, want %q", v.notice, "Already in "+tt.display)
			}
		})
	}
}

func TestMailViewPostingKeyFailureKeepsPosting(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusInternalServerError)

	msg := runCmd(v.HandleContentKey(keyPress("t")))
	done, ok := msg.(postingActionDoneMsg)
	if !ok || done.err == nil {
		t.Fatalf("posting command returned %#v, want an action error", msg)
	}
	errCmd, consumed := v.Update(done)

	if !consumed || errCmd == nil {
		t.Fatal("failed action should return an error command")
	}
	errResult := runCmd(errCmd)
	if _, ok := errResult.(errMsg); !ok {
		t.Errorf("error command returned %T, want errMsg", errResult)
	}
	if len(v.postingList.postings) != 2 {
		t.Error("failed action should keep the selected posting")
	}
	if v.notice != "" {
		t.Errorf("failed action notice = %q, want empty", v.notice)
	}
}

func TestMailViewPostingActionCopiesSelectedPostingBeforeAsyncRequest(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)

	cmd := v.HandleContentKey(keyPress("t"))
	v.Update(postingActionDoneMsg{
		action: "Thread moved to Trash", boxID: 1, postingID: 100, effect: postingActionRemove,
	})
	done, ok := runCmd(cmd).(postingActionDoneMsg)
	if !ok {
		t.Fatalf("posting command returned %T, want postingActionDoneMsg", done)
	}

	if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
		t.Errorf("request posting_ids = %v, want the originally selected posting [100]", recorded.body.PostingIDs)
	}
	if done.postingID != 100 {
		t.Errorf("completion posting ID = %d, want 100", done.postingID)
	}
}

func TestMailViewPostingCompletionInvalidatesConcurrentReload(t *testing.T) {
	v := mailWithPostings()

	v.SubnavRight()
	v.SubnavLeft()
	staleRequestID := v.activeRequestID
	stale := currentPostingsLoaded(v, testPostings())

	refresh, consumed := v.Update(postingActionDoneMsg{
		action: "Thread moved to Trash", boxID: 1, postingID: 100, effect: postingActionRemove,
	})
	if !consumed || refresh == nil {
		t.Fatal("action completion should replace a concurrent reload with a fresh reload")
	}
	if v.activeRequestID == staleRequestID {
		t.Fatal("action completion should invalidate the concurrent reload")
	}

	v.Update(stale)
	if v.postingIndex(100) >= 0 {
		t.Error("stale reload restored the posting removed by the completed action")
	}
	if !v.loading {
		t.Error("stale reload should not stop the post-action refresh")
	}
}

func TestMailViewPostingCompletionUpdatesOriginatingPosting(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.Resize(80, 2)

	msg := runCmd(v.HandleContentKey(keyPress("t")))
	done, ok := msg.(postingActionDoneMsg)
	if !ok {
		t.Fatalf("posting command returned %T, want postingActionDoneMsg", msg)
	}
	v.postingList.moveDown()
	if v.postingList.scrollOff != 1 {
		t.Fatalf("scroll offset before completion = %d, want 1", v.postingList.scrollOff)
	}
	v.Update(done)

	if len(v.postingList.postings) != 1 || v.postingList.postings[0].ID != 101 {
		t.Errorf("postings after completion = %v, want only posting 101", v.postingList.postings)
	}
	if v.postingList.scrollOff != 0 || !strings.Contains(v.View(), "│") {
		t.Errorf("selected posting is not visible: cursor=%d scroll=%d view=%q", v.postingList.cursor, v.postingList.scrollOff, v.View())
	}
}

func TestMailViewIgnoresPostingCompletionAfterBoxSwitch(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)

	msg := runCmd(v.HandleContentKey(keyPress("t")))
	done, ok := msg.(postingActionDoneMsg)
	if !ok {
		t.Fatalf("posting command returned %T, want postingActionDoneMsg", msg)
	}
	v.SubnavRight()
	v.Update(currentPostingsLoaded(v, []models.Posting{{ID: 200, Summary: "Other box"}}))
	v.Update(done)

	if len(v.postingList.postings) != 1 || v.postingList.postings[0].ID != 200 {
		t.Errorf("stale completion changed the new box: %v", v.postingList.postings)
	}
	if v.notice != "" {
		t.Errorf("stale completion notice = %q, want empty", v.notice)
	}
}

func TestMailViewReportsPostingFailureAfterBoxSwitch(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusInternalServerError)

	msg := runCmd(v.HandleContentKey(keyPress("t")))
	done, ok := msg.(postingActionDoneMsg)
	if !ok || done.err == nil {
		t.Fatalf("posting command returned %#v, want an action error", msg)
	}
	v.SubnavRight()
	v.Update(currentPostingsLoaded(v, []models.Posting{{ID: 200, Summary: "Other box"}}))
	errCmd, consumed := v.Update(done)

	if !consumed || errCmd == nil {
		t.Fatal("failed action should still return an error after a box switch")
	}
	if _, ok := runCmd(errCmd).(errMsg); !ok {
		t.Error("failed action should produce errMsg")
	}
	if len(v.postingList.postings) != 1 || v.postingList.postings[0].ID != 200 {
		t.Errorf("failed action changed the new box: %v", v.postingList.postings)
	}
}

func TestMailViewPostingActionRemoves(t *testing.T) {
	v := mailWithPostings()
	if len(v.postingList.postings) != 2 {
		t.Fatalf("expected 2 postings, got %d", len(v.postingList.postings))
	}

	v.Update(postingActionDoneMsg{action: "Thread moved to Trash", boxID: 1, postingID: 100, effect: postingActionRemove})
	if len(v.postingList.postings) != 1 {
		t.Errorf("expected 1 posting after remove, got %d", len(v.postingList.postings))
	}
}

func TestMailViewPostingActionMarksSeen(t *testing.T) {
	v := mailWithPostings()
	if v.postingList.postings[0].Seen {
		t.Fatal("first posting should be unseen")
	}

	v.Update(postingActionDoneMsg{action: "Thread marked as seen", boxID: 1, postingID: 100, effect: postingActionSeen})
	if !v.postingList.postings[0].Seen {
		t.Error("first posting should be seen after action")
	}
}

func TestMailViewPostingActionError(t *testing.T) {
	v := mailWithPostings()
	cmd, consumed := v.Update(postingActionDoneMsg{boxID: 1, postingID: 100, err: fmt.Errorf("network error")})

	if !consumed {
		t.Error("postingActionDoneMsg with error should be consumed")
	}
	if cmd == nil {
		t.Fatal("should return a command that produces errMsg")
	}
	msg := cmd()
	if _, ok := msg.(errMsg); !ok {
		t.Errorf("command produced %T, want errMsg", msg)
	}
}

// --- Content key handling ---

func TestMailViewEnterOpensSelectedThread(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusOK)

	msg := runCmd(v.HandleContentKey(keyPress("enter")))
	loaded, ok := msg.(topicLoadedMsg)
	if !ok {
		t.Fatalf("open command returned %T, want topicLoadedMsg", msg)
	}
	v.Update(loaded)

	wantRequests := "GET /topics/100.json,GET /messages/501.json"
	if got := strings.Join(recorded.requests, ","); got != wantRequests {
		t.Errorf("requests = %q, want %q", got, wantRequests)
	}
	if !v.inThread || v.topicID != 100 || v.topicName != "Hello world" {
		t.Errorf("thread state = open:%v id:%d name:%q", v.inThread, v.topicID, v.topicName)
	}
	if !strings.Contains(v.View(), "Alice") || !strings.Contains(v.View(), "Message body") {
		t.Errorf("thread view does not contain the fetched entry: %q", v.View())
	}
}

func TestMailViewDownloadsImageDataOnlyForKittyRenderer(t *testing.T) {
	var imageRequests atomic.Int64
	imageData := testPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/topics/100.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":100,"name":"Latest image","entries":[{"id":501,"kind":"message"}]}`))
		case "/messages/501.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":501,"content":"<action-text-attachment url=\"/rails/blobs/chart.png\" filename=\"chart.png\" content-type=\"image/png\"></action-text-attachment>"}`))
		case "/rails/blobs/chart.png":
			imageRequests.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageData)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(
		&hey.Config{BaseURL: server.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	vc := testVC()
	vc.sdk = client
	vc.imageFetcher = newTrustedImageFetcher(client)
	v := newMailView(vc)
	loaded := v.fetchTopic(context.Background(), 1, 1, 100, "Latest image")().(topicLoadedMsg)

	if loaded.err != nil || len(loaded.attachments) != 1 {
		t.Fatalf("text fallback topic = attachments:%d error:%v", len(loaded.attachments), loaded.err)
	}
	if len(loaded.images) != 0 || imageRequests.Load() != 0 {
		t.Errorf("text fallback downloaded %d discarded images and returned %d", imageRequests.Load(), len(loaded.images))
	}

	vc.imageRenderer = kittyImageRenderer{}
	v = newMailView(vc)
	loaded = v.fetchTopic(context.Background(), 2, 1, 100, "Latest image")().(topicLoadedMsg)
	if loaded.err != nil || len(loaded.images) != 1 || imageRequests.Load() != 1 {
		t.Errorf("Kitty topic = images:%d requests:%d error:%v", len(loaded.images), imageRequests.Load(), loaded.err)
	}
}

func TestMailViewDoesNotFetchImagesOutsideHEYOrGopher(t *testing.T) {
	var untrustedRequests atomic.Int64
	untrusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		untrustedRequests.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("untrusted image"))
	}))
	t.Cleanup(untrusted.Close)

	heyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/topics/100.json":
			_, _ = w.Write([]byte(`{"id":100,"name":"Untrusted image","entries":[{"id":501,"kind":"message"}]}`))
		case "/messages/501.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      501,
				"content": fmt.Sprintf(`<img src=%q>`, untrusted.URL+"/tracking.png"),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(heyServer.Close)

	client := hey.NewClient(
		&hey.Config{BaseURL: heyServer.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	vc := testVC()
	vc.sdk = client
	vc.imageRenderer = kittyImageRenderer{}
	vc.imageFetcher = newTrustedImageFetcher(client)
	v := newMailView(vc)

	loaded := v.fetchTopic(context.Background(), 1, 1, 100, "Untrusted image")().(topicLoadedMsg)

	if loaded.err != nil {
		t.Fatalf("fetch topic: %v", loaded.err)
	}
	if got := untrustedRequests.Load(); got != 0 {
		t.Fatalf("opening a message fetched an untrusted image %d time(s)", got)
	}
	if len(loaded.images) != 0 {
		t.Fatalf("topic returned %d untrusted images", len(loaded.images))
	}
}

func TestMailViewFetchesThreadMessagesConcurrentlyInOrder(t *testing.T) {
	const entryCount = 12

	var active atomic.Int64
	var maximum atomic.Int64
	started := make(chan struct{}, entryCount)
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/topics/100.json" {
			entries := make([]map[string]any, entryCount)
			for i := range entries {
				entries[i] = map[string]any{"id": 501 + i, "kind": "message"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 100, "name": "Thread", "entries": entries})
			return
		}

		idText := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/messages/"), ".json")
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil {
			http.Error(w, "bad message ID", http.StatusBadRequest)
			return
		}

		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "subject": fmt.Sprintf("Message %d", id), "content": fmt.Sprintf("body %d", id),
		})
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(
		&hey.Config{BaseURL: server.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	vc := testVC()
	vc.sdk = client
	vc.ctx = context.Background()
	v := newMailView(vc)
	v.boxes = orderBoxes(testBoxes())
	v.Update(currentPostingsLoaded(v, testPostings()))

	result := make(chan tea.Msg, 1)
	cmd := v.HandleContentKey(keyPress("enter"))
	go func() { result <- cmd() }()

	startedCount := 0
	deadline := time.NewTimer(2 * time.Second)
waitForLimit:
	for startedCount < maxConcurrentMessageFetches {
		select {
		case <-started:
			startedCount++
		case <-deadline.C:
			break waitForLimit
		}
	}
	if !deadline.Stop() {
		select {
		case <-deadline.C:
		default:
		}
	}

	if startedCount == maxConcurrentMessageFetches {
		select {
		case <-started:
			startedCount++
		case <-time.After(100 * time.Millisecond):
		}
	}
	observedConcurrency := maximum.Load()
	close(release)
	loaded, ok := (<-result).(topicLoadedMsg)
	if !ok {
		t.Fatalf("thread command did not return topicLoadedMsg")
	}

	if startedCount < maxConcurrentMessageFetches {
		t.Errorf("started %d concurrent message requests, want %d", startedCount, maxConcurrentMessageFetches)
	}
	if observedConcurrency <= 1 {
		t.Errorf("message requests were sequential, maximum concurrency = %d", observedConcurrency)
	}
	if observedConcurrency > maxConcurrentMessageFetches {
		t.Errorf("message request concurrency = %d, limit = %d", observedConcurrency, maxConcurrentMessageFetches)
	}
	if len(loaded.entries) != entryCount {
		t.Fatalf("loaded %d entries, want %d", len(loaded.entries), entryCount)
	}
	for i, entry := range loaded.entries {
		wantID := int64(501 + i)
		if entry.ID != wantID {
			t.Errorf("entry %d ID = %d, want %d", i, entry.ID, wantID)
		}
	}
}

func TestMailViewCancelPendingDetailStopsMessageRequests(t *testing.T) {
	messageStarted := make(chan struct{})
	messageCanceled := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/topics/100.json":
			_, _ = w.Write([]byte(`{"id":100,"name":"Hello world","entries":[{"id":501,"kind":"message"}]}`))
		case "/messages/501.json":
			close(messageStarted)
			<-r.Context().Done()
			close(messageCanceled)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(
		&hey.Config{BaseURL: server.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	vc := testVC()
	vc.sdk = client
	v := newMailView(vc)
	v.boxes = orderBoxes(testBoxes())
	v.Update(currentPostingsLoaded(v, testPostings()))

	result := make(chan tea.Msg, 1)
	cmd := v.HandleContentKey(keyPress("enter"))
	go func() { result <- cmd() }()

	select {
	case <-messageStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("message request did not start")
	}

	if !v.CancelPendingDetail() {
		t.Fatal("pending thread load should be cancellable")
	}
	select {
	case <-messageCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("canceling the detail load did not cancel its HTTP request")
	}

	select {
	case msg := <-result:
		loaded, ok := msg.(topicLoadedMsg)
		if !ok || loaded.err == nil {
			t.Fatalf("canceled command returned %#v, want topicLoadedMsg with an error", msg)
		}
		if cmd, consumed := v.Update(loaded); !consumed || cmd != nil {
			t.Error("canceled detail result should be ignored without reporting an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled detail command did not return")
	}
}

func TestMailViewIgnoresThreadLoadAfterBoxSwitch(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusOK)

	loaded := runCmd(v.HandleContentKey(keyPress("enter")))
	v.SubnavRight()
	v.Update(loaded)

	if v.inThread {
		t.Error("stale thread load should not reopen a thread after switching boxes")
	}
}

func TestMailViewIgnoresThreadLoadAfterReturningToOriginBox(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusOK)

	loaded := runCmd(v.HandleContentKey(keyPress("enter")))
	v.SubnavRight()
	v.SubnavLeft()
	v.Update(loaded)

	if v.inThread {
		t.Error("a stale thread load should not reopen after returning to its source box")
	}
	if !v.loading {
		t.Error("a stale thread load should not stop the current box load")
	}
}

func TestMailViewIgnoresEarlierThreadLoadInSameBox(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusOK)

	earlier := runCmd(v.HandleContentKey(keyPress("enter")))
	v.postingList.moveDown()
	_ = v.openSelected()
	v.Update(earlier)

	if v.inThread {
		t.Error("an earlier thread load should not replace a newer open request")
	}
	if !v.loading {
		t.Error("an earlier thread load should not stop the newer open request")
	}
}

func TestMailViewIgnoresReplyLoadAfterBoxSwitch(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	_ = v.loadReplyContext(100, "Hello world")
	loaded := replyContextLoadedMsg{
		requestID: v.activeRequestID,
		boxID:     1,
		topicID:   100,
		topicName: "Hello world",
		entryID:   501,
		to:        []string{"jane@example.com"},
	}

	v.SubnavRight()
	cmd, consumed := v.Update(loaded)

	if !consumed {
		t.Error("stale reply context should be consumed")
	}
	if cmd != nil || v.compose != nil {
		t.Error("stale reply context should not open the reply form")
	}
}

func TestMailViewIgnoresForwardLoadAfterBoxSwitch(t *testing.T) {
	v := mailWithPostings()
	_ = v.loadForwardContext(100, "Hello world")
	loaded := forwardContextLoadedMsg{
		requestID: v.activeRequestID,
		boxID:     1,
		topicID:   100,
		topicName: "Hello world",
		subject:   "Fwd: Hello world",
		content:   "<div>Hello world</div>",
	}

	v.SubnavRight()
	cmd, consumed := v.Update(loaded)

	if !consumed || cmd != nil {
		t.Error("stale forward context should be ignored")
	}
	if v.compose != nil {
		t.Error("stale forward context should not open the forward form")
	}
}

func TestMailViewIgnoresReplyLoadAfterThreadExit(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	_ = v.loadReplyContext(100, "Hello world")
	loaded := replyContextLoadedMsg{
		requestID: v.activeRequestID,
		boxID:     1,
		topicID:   100,
		topicName: "Hello world",
		entryID:   501,
		to:        []string{"jane@example.com"},
	}

	v.ExitThread()
	cmd, consumed := v.Update(loaded)

	if !consumed || cmd != nil {
		t.Error("a canceled reply load should be ignored")
	}
	if v.compose != nil {
		t.Error("a canceled reply load should not open the reply form")
	}
	if v.loading {
		t.Error("a canceled reply load should not keep loading")
	}
}

func TestMailViewContentKeyUpDown(t *testing.T) {
	v := mailWithPostings()

	if v.postingList.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", v.postingList.cursor)
	}

	v.HandleContentKey(keyPress("down"))
	if v.postingList.cursor != 1 {
		t.Errorf("after down: cursor = %d, want 1", v.postingList.cursor)
	}

	v.HandleContentKey(keyPress("up"))
	if v.postingList.cursor != 0 {
		t.Errorf("after up: cursor = %d, want 0", v.postingList.cursor)
	}
}

func TestMailViewContentKeyInThread(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true

	// In thread mode, content keys go to viewport (no crash)
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("up"))
}

// --- Subnav ---

func TestMailViewSubnavItems(t *testing.T) {
	v := mailWithPostings()
	items, selected, label, centered := v.SubnavItems()

	if len(items) != 3 {
		t.Errorf("expected 3 subnav items, got %d", len(items))
	}
	if selected != 0 {
		t.Errorf("selected = %d, want 0", selected)
	}
	if label != "Imbox" {
		t.Errorf("label = %q, want Imbox", label)
	}
	if !centered {
		t.Error("mail subnav should be centered")
	}
}

func TestMailViewSubnavLeftRight(t *testing.T) {
	v := mailWithPostings()

	// Can't go left from first box
	v.SubnavLeft()
	if v.boxIndex != 0 {
		t.Errorf("SubnavLeft at 0: boxIndex = %d, want 0", v.boxIndex)
	}

	// Go right
	v.inThread = true
	v.notice = "previous action"
	v.SubnavRight()
	if v.boxIndex != 1 {
		t.Errorf("after SubnavRight: boxIndex = %d, want 1", v.boxIndex)
	}
	if !v.loading {
		t.Error("SubnavRight should set loading")
	}
	if v.inThread {
		t.Error("SubnavRight should close the open thread")
	}
	if v.notice != "" {
		t.Errorf("SubnavRight should clear the previous notice, got %q", v.notice)
	}

	v.loading = false
	v.SubnavRight()
	if v.boxIndex != 2 {
		t.Errorf("after second SubnavRight: boxIndex = %d, want 2", v.boxIndex)
	}

	// Can't go right past last box
	v.loading = false
	v.SubnavRight()
	if v.boxIndex != 2 {
		t.Errorf("SubnavRight at end: boxIndex = %d, want 2", v.boxIndex)
	}
}

func TestMailViewBoxSwitchClearsPostingsDuringLoad(t *testing.T) {
	v := mailWithPostings()

	if cmd := v.SubnavRight(); cmd == nil {
		t.Fatal("box switch should start loading the new box")
	}
	if len(v.postingList.postings) != 0 {
		t.Fatalf("postings during box load = %v, want none", v.postingList.postings)
	}

	for _, key := range []string{"enter", "r", "t"} {
		if cmd := v.HandleContentKey(keyPress(key)); cmd != nil {
			t.Errorf("key %q acted on a posting from the previous box", key)
		}
	}
}

// --- Thread state ---

func TestMailViewInThread(t *testing.T) {
	v := newMailView(testVC())
	if v.InThread() {
		t.Error("should not be in thread initially")
	}
	v.inThread = true
	if !v.InThread() {
		t.Error("InThread should return true")
	}
	v.ExitThread()
	if v.InThread() {
		t.Error("ExitThread should clear thread state")
	}
}

// --- View rendering ---

func TestMailViewRendersPostings(t *testing.T) {
	v := mailWithPostings()
	v.Resize(80, 30)
	view := v.View()
	if !strings.Contains(view, "Hello world") {
		t.Error("view should contain posting summary")
	}
}

func TestMailViewAttachmentNavigationUpdatesSelectionAndHelp(t *testing.T) {
	v := mailWithPostings()
	v.Resize(80, 30)
	v.Update(topicLoadedMsg{
		boxID:   1,
		topicID: 100,
		title:   "Quarterly planning",
		entries: []models.Entry{{ID: 501, Creator: models.Contact{Name: "Alice"}}, {ID: 502, Creator: models.Contact{Name: "Bob"}}},
		attachments: []messageAttachment{
			{ID: "501:1", MessageID: 501, Filename: "agenda.pdf"},
			{ID: "502:1", MessageID: 502, Filename: "chart.png"},
		},
	})

	if !strings.Contains(v.View(), "› 1. agenda.pdf") {
		t.Fatalf("first attachment is not selected: %q", v.View())
	}
	if cmd := v.HandleContentKey(keyPress("]")); cmd != nil {
		t.Fatal("attachment navigation should not start a command")
	}
	if v.attachmentCursor != 1 || !strings.Contains(v.View(), "› 1. chart.png") || strings.Contains(v.View(), "› 1. agenda.pdf") {
		t.Errorf("next attachment state = cursor:%d view:%q", v.attachmentCursor, v.View())
	}
	v.HandleContentKey(keyPress("["))
	if v.attachmentCursor != 0 || !strings.Contains(v.View(), "› 1. agenda.pdf") {
		t.Errorf("previous attachment state = cursor:%d view:%q", v.attachmentCursor, v.View())
	}

	bindings := fmt.Sprint(v.HelpBindings())
	for _, want := range []string{"previous attachment", "next attachment", "save attachment", "open attachment"} {
		if !strings.Contains(bindings, want) {
			t.Errorf("thread help does not contain %q: %s", want, bindings)
		}
	}
}

func TestMailViewSavesSelectedAttachmentOnlyAfterExplicitAction(t *testing.T) {
	v := mailWithPostings()
	var savedDestination, savedURL string
	var savedForce bool
	var saveCalls int
	v.vc.saveAttachment = func(_ context.Context, destination, sourceURL string, force bool) (int64, error) {
		saveCalls++
		savedDestination = destination
		savedURL = sourceURL
		savedForce = force
		return 128, nil
	}
	v.Update(topicLoadedMsg{
		boxID:   1,
		topicID: 100,
		title:   "Quarterly planning",
		entries: []models.Entry{{ID: 501, Creator: models.Contact{Name: "Alice"}}},
		attachments: []messageAttachment{
			{ID: "501:1", MessageID: 501, Filename: "agenda.pdf", URL: "/rails/blobs/agenda.pdf"},
			{ID: "501:2", MessageID: 501, Filename: "chart.png", URL: "/rails/blobs/chart.png"},
		},
	})
	if saveCalls != 0 {
		t.Fatal("loading a thread must not save its attachment")
	}
	v.HandleContentKey(keyPress("]"))

	save := v.HandleContentKey(keyPress("s"))
	if save == nil {
		t.Fatal("s should save the selected attachment")
	}
	if saveCalls != 0 {
		t.Fatal("handling the key must defer file access to the command")
	}
	if _, consumed := v.Update(save()); !consumed {
		t.Fatal("attachment save result was not consumed")
	}
	if saveCalls != 1 || savedDestination != "chart.png" || savedURL != "/rails/blobs/chart.png" || savedForce {
		t.Errorf("save call = count:%d destination:%q URL:%q force:%v", saveCalls, savedDestination, savedURL, savedForce)
	}
	if v.notice != "Saved attachment to chart.png" {
		t.Errorf("save notice = %q", v.notice)
	}
}

func TestMailViewExplainsThatSaveWillNotReplaceExistingAttachment(t *testing.T) {
	v := mailWithPostings()
	v.vc.saveAttachment = func(context.Context, string, string, bool) (int64, error) {
		return 0, apierr.ErrUsage("destination already exists: agenda.pdf (use --force to replace it)")
	}
	v.Update(topicLoadedMsg{
		boxID:       1,
		topicID:     100,
		title:       "Quarterly planning",
		entries:     []models.Entry{{ID: 501, Creator: models.Contact{Name: "Alice"}}},
		attachments: []messageAttachment{{ID: "501:1", MessageID: 501, Filename: "agenda.pdf", URL: "/rails/blobs/agenda.pdf"}},
	})

	save := v.HandleContentKey(keyPress("s"))
	v.Update(save())
	if v.notice != "Attachment already exists: agenda.pdf" {
		t.Errorf("existing attachment notice = %q", v.notice)
	}
}

func TestMailViewDownloadsBeforeExplicitExternalOpen(t *testing.T) {
	v := mailWithPostings()
	temporaryDirectory := t.TempDir()
	var events []string
	var openedPath string
	v.vc.newAttachmentTempDir = func() (string, error) { return temporaryDirectory, nil }
	v.vc.saveAttachment = func(_ context.Context, destination, sourceURL string, force bool) (int64, error) {
		events = append(events, "save")
		if sourceURL != "/rails/blobs/chart.png" || force {
			t.Errorf("save arguments = URL:%q force:%v", sourceURL, force)
		}
		return 256, nil
	}
	v.vc.openAttachment = func(path string) error {
		events = append(events, "open")
		openedPath = path
		return nil
	}
	v.Update(topicLoadedMsg{
		boxID:       1,
		topicID:     100,
		title:       "Quarterly planning",
		entries:     []models.Entry{{ID: 501, Creator: models.Contact{Name: "Alice"}}},
		attachments: []messageAttachment{{ID: "501:1", MessageID: 501, Filename: "chart.png", URL: "/rails/blobs/chart.png"}},
	})
	if len(events) != 0 {
		t.Fatalf("loading a thread opened an attachment: %v", events)
	}

	open := v.HandleContentKey(keyPress("o"))
	if open == nil {
		t.Fatal("o should open the selected attachment")
	}
	if len(events) != 0 {
		t.Fatalf("handling the key opened an attachment before the command ran: %v", events)
	}
	if _, consumed := v.Update(open()); !consumed {
		t.Fatal("attachment open result was not consumed")
	}
	if fmt.Sprint(events) != "[save open]" {
		t.Errorf("open events = %v", events)
	}
	if openedPath != filepath.Join(temporaryDirectory, "chart.png") {
		t.Errorf("opened path = %q", openedPath)
	}
	if v.notice != "Opened attachment chart.png" {
		t.Errorf("open notice = %q", v.notice)
	}
}

func TestMailViewDoesNotOpenAttachmentWhenDownloadFails(t *testing.T) {
	v := mailWithPostings()
	v.vc.newAttachmentTempDir = func() (string, error) { return t.TempDir(), nil }
	v.vc.saveAttachment = func(context.Context, string, string, bool) (int64, error) {
		return 0, fmt.Errorf("download failed")
	}
	openCalls := 0
	v.vc.openAttachment = func(string) error {
		openCalls++
		return nil
	}
	v.Update(topicLoadedMsg{
		boxID:       1,
		topicID:     100,
		title:       "Quarterly planning",
		entries:     []models.Entry{{ID: 501, Creator: models.Contact{Name: "Alice"}}},
		attachments: []messageAttachment{{ID: "501:1", MessageID: 501, Filename: "chart.png", URL: "/rails/blobs/chart.png"}},
	})

	open := v.HandleContentKey(keyPress("o"))
	v.Update(open())
	if openCalls != 0 {
		t.Errorf("opener was called %d times after download failure", openCalls)
	}
	if !strings.Contains(v.notice, "Could not open attachment: download failed") {
		t.Errorf("open failure notice = %q", v.notice)
	}
}

func TestMailViewAlwaysRendersAttachmentPanelAndTextMarker(t *testing.T) {
	size := int64(1536)
	v := mailWithPostings()
	v.Resize(80, 30)
	v.Update(topicLoadedMsg{
		boxID:   1,
		topicID: 100,
		title:   "Quarterly planning",
		entries: []models.Entry{{
			ID:      501,
			Creator: models.Contact{Name: "Alice"},
			Body:    `<p>Review this image:</p><action-text-attachment url="/rails/blobs/chart.png" filename="chart.png" content-type="image/png" filesize="1536"></action-text-attachment><p>Thank you.</p>`,
		}},
		attachments: []messageAttachment{{
			ID:          "501:1",
			MessageID:   501,
			Filename:    "chart.png",
			ContentType: "image/png",
			ByteSize:    &size,
			URL:         "/rails/blobs/chart.png",
		}},
	})

	view := v.View()
	for _, want := range []string{"[chart.png]", "Attachments", "chart.png", "image/png", "1.5 KB"} {
		if !strings.Contains(view, want) {
			t.Errorf("thread view does not contain %q: %q", want, view)
		}
	}
	if strings.ContainsRune(view, placeholder) {
		t.Errorf("text fallback contains an invisible Kitty placeholder: %q", view)
	}
}

func TestMailViewRendersIgnoredThread(t *testing.T) {
	v := mailWithPostings()
	v.Resize(80, 30)
	v.postingList.postings[0].Muted = true
	if view := v.View(); !strings.Contains(view, "[Ignored] Hello world") {
		t.Errorf("ignored thread state is not visible: %q", view)
	}
}

func TestMailViewRendersEmptyList(t *testing.T) {
	v := newMailView(testVC())
	v.Resize(80, 30)
	view := v.View()
	if !strings.Contains(view, "(empty)") {
		t.Error("view should show (empty) with no postings")
	}
}

// --- Search ---

func TestMailViewSearchWaitsForMailboxLoad(t *testing.T) {
	v := newMailView(testVC())
	v.loading = true
	if cmd := v.HandleContentKey(keyPress("/")); cmd != nil || v.searchForm != nil {
		t.Error("search should not start before boxes load")
	}

	v.Update(boxesLoadedMsg(testBoxes()))
	if cmd := v.HandleContentKey(keyPress("/")); cmd != nil || v.searchForm != nil {
		t.Error("search should not start while the first box loads")
	}

	v.Update(currentPostingsLoaded(v, testPostings()))
	if cmd := v.HandleContentKey(keyPress("/")); cmd == nil || v.searchForm == nil {
		t.Error("search should start after the mailbox finishes loading")
	}
}

func TestMailViewSearchFormSubmitsQueryAndRendersResults(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	if cmd := v.HandleContentKey(keyPress("/")); cmd == nil {
		t.Fatal("opening search should focus the input")
	}
	if v.searchForm == nil || !v.CapturingInput() {
		t.Fatal("search form should capture input")
	}
	v.searchForm.input.SetValue("quarterly planning")

	cmd := v.HandleContentKey(keyPress("enter"))
	if cmd == nil || !v.loading {
		t.Fatal("submitting search should start a request")
	}
	msg := cmd()
	loaded, ok := msg.(searchResultsLoadedMsg)
	if !ok {
		t.Fatalf("search command returned %T", msg)
	}
	v.Update(loaded)

	if recorded.path != "/advanced_search.json" || len(recorded.rawQueries) == 0 || !strings.Contains(recorded.rawQueries[len(recorded.rawQueries)-1], "q=quarterly+planning") {
		t.Errorf("search request = %s?%v", recorded.path, recorded.rawQueries)
	}
	if !v.searchActive || v.searchQuery != "quarterly planning" || v.searchPage != 1 {
		t.Errorf("search state = active:%v query:%q page:%d", v.searchActive, v.searchQuery, v.searchPage)
	}
	if len(v.searchList.postings) != 1 || v.searchList.postings[0].ResolveTopicID() != 100 {
		t.Errorf("search postings = %+v", v.searchList.postings)
	}
	view := v.View()
	if !strings.Contains(view, "Hello world") || !strings.Contains(view, "Matching message summary") {
		t.Errorf("search result does not show thread and matching-message context: %q", view)
	}
	if strings.Contains(view, "●") {
		t.Errorf("search result shows an unread marker without a read state: %q", view)
	}
	_, _, label, _ := v.SubnavItems()
	if !strings.Contains(label, "quarterly planning") || !strings.Contains(label, "page 1") {
		t.Errorf("search label = %q", label)
	}
}

func TestMailViewSearchRequiresWords(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("/"))
	cmd := v.HandleContentKey(keyPress("enter"))
	if cmd != nil {
		t.Fatal("empty search should not submit")
	}
	if v.searchForm == nil || !strings.Contains(v.searchForm.view(), "Enter words to search for") {
		t.Error("empty search should keep the form open with guidance")
	}
}

func TestMailViewSearchEscapeClosesForm(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("/"))
	v.HandleContentKey(keyPress("esc"))
	if v.searchForm != nil || v.CapturingInput() {
		t.Error("escape should close the search form")
	}
}

func TestMailViewSearchFormConsumesComponentMessages(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("/"))
	if _, consumed := v.Update(struct{}{}); !consumed {
		t.Error("open search form should consume component messages")
	}
}

func TestMailViewIgnoresStaleSearchResults(t *testing.T) {
	v := mailWithPostings()
	v.activeRequestID = 2
	v.Update(searchResultsLoadedMsg{
		requestID: 1,
		query:     "stale",
		page:      1,
		postings:  []models.Posting{{ID: 99}},
	})
	if v.searchActive || len(v.searchList.postings) != 0 {
		t.Error("stale search results changed the view")
	}
}

func TestMailViewSearchPagination(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.searchActive = true
	v.searchQuery = "quarterly planning"
	v.searchPage = 1
	v.searchList.setPostings([]models.Posting{{ID: 10, TopicID: 100, Name: "Hello world"}})

	next := v.HandleContentKey(keyPress("n"))
	if next == nil {
		t.Fatal("next page should start a request")
	}
	nextMsg := next().(searchResultsLoadedMsg)
	v.Update(nextMsg)
	if v.searchPage != 2 || !strings.Contains(recorded.rawQueries[len(recorded.rawQueries)-1], "page=2") {
		t.Errorf("next page state = %d, queries = %v", v.searchPage, recorded.rawQueries)
	}

	previous := v.HandleContentKey(keyPress("p"))
	if previous == nil {
		t.Fatal("previous page should start a request")
	}
	previousMsg := previous().(searchResultsLoadedMsg)
	v.Update(previousMsg)
	if v.searchPage != 1 {
		t.Errorf("previous page = %d, want 1", v.searchPage)
	}
}

func TestMailViewEmptyNextSearchPagePreservesResults(t *testing.T) {
	v := mailWithPostings()
	v.searchActive = true
	v.searchQuery = "quarterly planning"
	v.searchPage = 1
	v.searchList.setPostings([]models.Posting{{ID: 10, TopicID: 100}})
	v.activeRequestID = 3

	v.Update(searchResultsLoadedMsg{requestID: 3, query: v.searchQuery, page: 2})
	if v.searchPage != 1 || len(v.searchList.postings) != 1 {
		t.Errorf("empty next page replaced results: page=%d postings=%v", v.searchPage, v.searchList.postings)
	}
	if v.notice != "No more search results" {
		t.Errorf("notice = %q", v.notice)
	}
}

func TestMailViewCancelPendingSearchResultPreservesResults(t *testing.T) {
	v := mailWithPostings()
	v.searchActive = true
	v.searchQuery = "quarterly planning"
	v.searchPage = 1
	v.searchList.setPostings([]models.Posting{{ID: 10, TopicID: 100, Name: "Hello world"}})

	if cmd := v.HandleContentKey(keyPress("enter")); cmd == nil {
		t.Fatal("enter should start loading the selected thread")
	}
	if v.activeRequestKind != mailRequestTopic || !v.loading {
		t.Fatalf("request state = kind:%d loading:%v", v.activeRequestKind, v.loading)
	}
	v.ExitThread()
	if !v.searchActive || v.searchQuery != "quarterly planning" || v.searchPage != 1 || len(v.searchList.postings) != 1 {
		t.Error("canceling a pending searched thread should preserve search results")
	}
	if v.loading || v.activeRequestKind != mailRequestNone {
		t.Errorf("pending request was not canceled: kind=%d loading=%v", v.activeRequestKind, v.loading)
	}
}

func TestMailViewSearchResultOpensThreadAndReturnsToResults(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.searchActive = true
	v.searchQuery = "quarterly planning"
	v.searchPage = 1
	v.searchList.setPostings([]models.Posting{{ID: 10, TopicID: 100, Name: "Hello world"}})

	open := v.HandleContentKey(keyPress("enter"))
	if open == nil {
		t.Fatal("enter should open the selected search result")
	}
	v.Update(open())
	if !v.inThread || !v.searchActive || v.topicID != 100 {
		t.Errorf("thread state = open:%v search:%v id:%d", v.inThread, v.searchActive, v.topicID)
	}

	v.ExitThread()
	if v.inThread || !v.searchActive || len(v.searchList.postings) != 1 {
		t.Error("closing a searched thread should return to search results")
	}
	v.ExitThread()
	if v.searchActive {
		t.Error("closing search results should return to the box")
	}
}

func TestSDKSearchMatchToModelPreservesActionAndThreadIDs(t *testing.T) {
	match := generated.SearchMatch{
		PostingId: 10,
		Topic: generated.Topic{
			Id:     100,
			Name:   "Hello world",
			AppUrl: "https://app.hey.com/topics/100",
		},
		Entries: []generated.Entry{{
			Id:      501,
			Summary: "Matching message summary",
			Creator: generated.Contact{Name: "Alice"},
		}},
	}
	posting := sdkSearchMatchToModel(match)
	if posting.ID != 10 || posting.ResolveTopicID() != 100 || posting.Name != "Hello world" || posting.Summary != "Matching message summary" || posting.Creator.Name != "Alice" {
		t.Errorf("posting = %+v", posting)
	}
}

// --- Help bindings ---

func TestMailViewHelpBindings(t *testing.T) {
	v := mailWithPostings()
	bindings := v.HelpBindings()
	if len(bindings) == 0 {
		t.Fatal("mail should have help bindings for posting actions")
	}

	keys := make(map[string]bool)
	for _, b := range bindings {
		keys[b.key] = true
	}
	for _, expected := range []string{"/", "r", "f", "m", "e", "l", "a", "t", "s", "-"} {
		if !keys[expected] {
			t.Errorf("missing help binding for key %q", expected)
		}
	}
}

func TestMailViewHelpBindingsStopIgnoringForIgnoredThread(t *testing.T) {
	v := mailWithPostings()
	v.postingList.postings[0].Muted = true

	bindings := v.HelpBindings()
	for _, binding := range bindings {
		if binding.key == "+" && binding.desc == "stop ignoring" {
			return
		}
	}
	t.Errorf("ignored thread help does not offer stop ignoring: %v", bindings)
}

func TestMailViewHelpBindingsInMovePicker(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("m"))

	bindings := v.HelpBindings()
	if len(bindings) != 3 || bindings[0].key != "↑↓" || bindings[1].key != "enter" || bindings[2].key != "esc" {
		t.Errorf("move picker help = %v", bindings)
	}
}

func TestMailViewHelpBindingsInThread(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	bindings := v.HelpBindings()
	if len(bindings) != 2 || bindings[0].key != "r" || bindings[1].key != "f" {
		t.Errorf("thread mode should offer reply and forward, got %v", bindings)
	}
}

func TestMailViewHelpBindingsInSearchResults(t *testing.T) {
	v := mailWithPostings()
	v.searchActive = true
	bindings := v.HelpBindings()
	keys := make(map[string]bool)
	for _, binding := range bindings {
		keys[binding.key] = true
	}
	for _, expected := range []string{"enter", "/", "n", "p"} {
		if !keys[expected] {
			t.Errorf("search results missing help binding %q: %v", expected, bindings)
		}
	}
}

// --- Box shortcuts ---

func TestMailViewBoxShortcut(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	v.notice = "previous action"
	cmd := v.handleBoxShortcut("F") // The Feed
	if cmd == nil {
		t.Fatal("box shortcut 'F' should return a command")
	}
	if v.boxIndex == 0 {
		t.Error("boxIndex should have changed")
	}
	if !v.loading {
		t.Error("should be loading after box switch")
	}
	if v.notice != "" {
		t.Errorf("box switch should clear the previous notice, got %q", v.notice)
	}
	if v.inThread {
		t.Error("box switch should close the open thread")
	}
}

func TestMailViewBoxShortcutNoOp(t *testing.T) {
	v := mailWithPostings()
	cmd := v.handleBoxShortcut("I") // Imbox — already selected
	if cmd != nil {
		t.Error("shortcut for current box should be no-op")
	}
}
