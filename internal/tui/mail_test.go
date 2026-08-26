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
	"charm.land/lipgloss/v2"
	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/mail"
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

func currentPostingsLoaded(v *mailView, postings []mail.Posting) postingsLoadedMsg {
	return postingsLoadedMsg{
		requestID: v.requests.id,
		boxID:     v.currentBoxID(),
		postings:  postings,
	}
}

func hasHelpBinding(bindings []helpBinding, key string) bool {
	for _, binding := range bindings {
		if binding.key == key {
			return true
		}
	}
	return false
}

type recordedMailRequest struct {
	method     string
	path       string
	requests   []string
	rawQueries []string
	body       struct {
		PostingIDs []int64 `json:"posting_ids"`
		BoxID      *int64  `json:"box_id"`
		FolderID   *int64  `json:"folder_id"`
		Folder     struct {
			Name string `json:"name"`
		} `json:"folder"`
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
		case "/postings/311/bundles/unseen.json":
			if r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", `<http://`+r.Host+`/postings/311/bundles/unseen.json?page=eyJwYWdlIjoyfQ>; rel="next"`)
				_, _ = w.Write([]byte(`{"contact":{"id":88,"name":"GitHub","email_address":"notifications@example.com"},"postings":[{"id":511,"kind":"topic","name":"Deploy failed on main","app_url":"https://app.hey.com/topics/100","created_at":"2026-08-25T09:00:00Z","creator":{"id":88,"name":"GitHub"}}]}`))
			} else {
				_, _ = w.Write([]byte(`{"contact":{"id":88,"name":"GitHub","email_address":"notifications@example.com"},"postings":[{"id":512,"kind":"topic","name":"Nightly build is green again","app_url":"https://app.hey.com/topics/101","created_at":"2026-08-24T21:00:00Z","creator":{"id":88,"name":"GitHub"}}]}`))
			}
		case "/imbox/seen.json":
			if r.URL.Query().Get("page") == "" {
				_, _ = w.Write([]byte(`{"id":1,"postings":[{"id":611,"kind":"topic","name":"Weekly team sync notes","seen":true,"app_url":"https://app.hey.com/topics/100","created_at":"2026-08-20T09:00:00Z","creator":{"id":21,"name":"Claire Lee"}}],"next_history_url":"/imbox?page=seen-page-2"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":1,"postings":[{"id":612,"kind":"topic","name":"Invoice #2041 from Fastmail","seen":true,"app_url":"https://app.hey.com/topics/101","created_at":"2026-08-18T15:00:00Z","creator":{"id":22,"name":"Fastmail Billing"}}]}`))
			}
		case "/contacts/88.json":
			_, _ = w.Write([]byte(`{"id":88,"name":"GitHub","entries_title":"All threads with GitHub","postings":[{"id":513,"kind":"topic","name":"Deploy failed on main","seen":true,"app_url":"https://app.hey.com/topics/100","created_at":"2026-08-25T09:00:00Z","creator":{"id":88,"name":"GitHub"}},{"id":514,"kind":"topic","name":"Nightly build is green again","seen":true,"app_url":"https://app.hey.com/topics/101","created_at":"2026-08-24T21:00:00Z","creator":{"id":88,"name":"GitHub"}}]}`))
		case "/topics/100/entries.json":
			_, _ = w.Write([]byte(`[{"id":501,"kind":"message","summary":"Hello world","created_at":"2026-08-19T09:00:00Z","creator":{"id":10,"name":"Alice"}}]`))
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
	if !v.requests.loading {
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
	if !v.requests.loading {
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
	v.requests.loading = true

	_, consumed := v.Update(currentPostingsLoaded(v, testPostings()))
	if !consumed {
		t.Error("postingsLoadedMsg should be consumed")
	}
	if v.requests.loading {
		t.Error("loading should be false after postings loaded")
	}
	if len(v.postingList.postings) != 2 {
		t.Errorf("expected 2 postings, got %d", len(v.postingList.postings))
	}
}

func TestMailViewIgnoresPostingLoadFromEarlierBoxVisit(t *testing.T) {
	v := mailWithPostings()

	v.SubnavRight()
	stale := currentPostingsLoaded(v, []mail.Posting{{ID: 200, Summary: "The Feed message"}})
	v.SubnavLeft()
	current := currentPostingsLoaded(v, []mail.Posting{{ID: 300, Summary: "Current Imbox message"}})
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
	if v.requests.loading {
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
	if !v.requests.loading {
		t.Error("an earlier box error should not stop the current box load")
	}
}

func TestMailViewHandlesTopicLoaded(t *testing.T) {
	v := mailWithPostings()
	v.requests.loading = true

	_, consumed := v.Update(topicLoadedMsg{
		boxID:   1,
		topicID: 100,
		title:   "Test topic",
		entries: []mail.Entry{{Creator: mail.Contact{Name: "Alice"}, Body: htmlutil.ToMarkdown("<p>hello</p>")}},
	})
	if !consumed {
		t.Error("topicLoadedMsg should be consumed")
	}
	if !v.inThread || v.topicID != 100 || v.topicName != "Test topic" {
		t.Errorf("thread state = open:%v id:%d name:%q", v.inThread, v.topicID, v.topicName)
	}
	if v.requests.loading {
		t.Error("loading should be false")
	}
}

// A thread heads its messages with the centered subject, and a blank line
// separates each message header from the content under it.
func TestThreadViewShowsSubjectAndSpacesTheHeader(t *testing.T) {
	v := mailWithPostings()
	v.Update(topicLoadedMsg{
		boxID:   1,
		topicID: 100,
		title:   "Quarterly planning",
		entries: []mail.Entry{{
			Creator:   mail.Contact{Name: "Jane Doe"},
			CreatedAt: time.Date(2026, 1, 8, 13, 34, 0, 0, time.UTC),
			Summary:   "Can we meet Thursday?",
			Body:      htmlutil.ToMarkdown("<p>Can we meet Thursday to walk through the numbers?</p>"),
		}},
	})

	lines := strings.Split(ansi.Strip(v.topicContent), "\n")
	if len(lines) < 5 {
		t.Fatalf("thread content = %q", v.topicContent)
	}
	if strings.TrimSpace(lines[0]) != "Quarterly planning" || !strings.HasPrefix(lines[0], "  ") {
		t.Errorf("first line should be the centered subject: %q", lines[0])
	}
	if strings.TrimSpace(lines[1]) != "" {
		t.Errorf("a blank line should follow the subject: %q", lines[1])
	}
	header := -1
	for i, line := range lines {
		if strings.Contains(line, "Jane Doe") {
			header = i
			break
		}
	}
	if header < 0 || header+2 >= len(lines) {
		t.Fatalf("no message header rendered:\n%s", ansi.Strip(v.topicContent))
	}
	if strings.TrimSpace(lines[header+1]) != "" {
		t.Errorf("a blank line should follow the header: %q", lines[header+1])
	}
	if strings.TrimSpace(lines[header+2]) != "Can we meet Thursday?" {
		t.Errorf("the summary should follow the blank line: %q", lines[header+2])
	}
}

func TestMailViewIgnoresUnrelatedMessages(t *testing.T) {
	v := newMailView(testVC())
	_, consumed := v.Update(calendarsLoadedMsg{})
	if consumed {
		t.Error("calendarsLoadedMsg should not be consumed by mailView")
	}
}

func TestMailViewMarksOpenedThreadSeen(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)

	loaded, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(topicLoadedMsg)
	if !ok || loaded.err != nil || loaded.postingID != 100 {
		t.Fatalf("opened topic = posting %d error %v", loaded.postingID, loaded.err)
	}

	cmd, _ := v.Update(loaded)
	if !v.AccountSwitchBlocked() {
		t.Fatal("marking the opened thread seen did not block account switching")
	}
	seen, ok := runCmd(cmd).(postingSeenMsg)
	if !ok || seen.err != nil {
		t.Fatalf("mark seen returned %#v", seen)
	}
	if recorded.method != http.MethodPost || recorded.path != "/postings/seen.json" {
		t.Errorf("request = %s %s, want POST /postings/seen.json", recorded.method, recorded.path)
	}
	if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
		t.Errorf("posting_ids = %v, want [100]", recorded.body.PostingIDs)
	}

	v.Update(seen)
	if v.AccountSwitchBlocked() {
		t.Error("completed mark seen still blocks account switching")
	}
	if !v.postingList.postings[v.postingIndex(100)].Seen {
		t.Error("the opened thread should be seen in the list")
	}
	if v.notice != "" {
		t.Errorf("opening a thread should not announce itself: %q", v.notice)
	}
}

// A thread read only in part keeps saying so for as long as it is open — the notice is
// thread state, not the one-shot kind a key press clears — and opening it does not mark
// it seen: the reader has not had all of it, and seen would put it under the cover.
func TestMailViewKeepsAPartialThreadsNoticeAndLeavesItUnseen(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.requests.loading = true
	v.requests.kind = mailRequestTopic

	cmd, _ := v.Update(topicLoadedMsg{
		requestID: v.requests.id,
		boxID:     1,
		topicID:   100,
		postingID: 100,
		title:     "Hello world",
		entries:   []mail.Entry{{ID: 501, Creator: mail.Contact{Name: "Alice"}, BodyState: "failed"}},
		notice:    "1 of 1 bodies could not be read (failed)",
		complete:  false,
	})
	if msg := runCmd(cmd); msg != nil {
		t.Fatalf("opening a partial thread sent %#v, want no mark seen", msg)
	}
	if recorded.method != "" {
		t.Errorf("request = %s %s, want none", recorded.method, recorded.path)
	}
	if v.postingList.postings[v.postingIndex(100)].Seen {
		t.Error("a partial thread should stay unseen")
	}
	if !strings.Contains(v.View(), "1 of 1 bodies could not be read") {
		t.Errorf("view lacks the notice: %q", v.View())
	}

	v.HandleContentKey(keyPress("j"))
	if !strings.Contains(v.View(), "1 of 1 bodies could not be read") {
		t.Errorf("a key press dismissed the partial-thread notice: %q", v.View())
	}

	// The notice takes a row the viewport gives up, so the section draws the rows it
	// has and no more, whatever the notices do.
	v.Resize(80, 20)
	if rows := strings.Count(v.View(), "\n") + 1; rows != 20 {
		t.Errorf("thread view draws %d rows in 20, notice included", rows)
	}
	v.notice = "Saved attachment to agenda.pdf"
	if rows := strings.Count(v.View(), "\n") + 1; rows != 20 {
		t.Errorf("thread view draws %d rows in 20 with two notices", rows)
	}
	v.notice = ""
	v.threadNotice = ""
	if rows := strings.Count(v.View(), "\n") + 1; rows != 20 {
		t.Errorf("thread view draws %d rows in 20 with no notice", rows)
	}
	v.threadNotice = "1 of 1 bodies could not be read (failed)"
	v.notice = "Saved attachment to agenda.pdf"
	for _, height := range []int{1, 2, 3} {
		v.Resize(80, height)
		if rows := strings.Count(v.View(), "\n") + 1; rows != height {
			t.Errorf("thread view draws %d rows in %d with two notices: a notice gives way, the thread keeps a row", rows, height)
		}
	}
	v.notice = ""

	v.ExitThread()
	if v.inThread || v.threadNotice != "" {
		t.Errorf("leaving the thread left inThread=%v notice=%q", v.inThread, v.threadNotice)
	}
}

func TestMailViewLeavesBubbledUpThreadAloneWhenOpened(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.postingList.postings[0].BubbledUp = true

	loaded := runCmd(v.HandleContentKey(keyPress("enter"))).(topicLoadedMsg)
	cmd, _ := v.Update(loaded)
	if cmd != nil {
		t.Errorf("opening a bubbled up thread should not mark it seen: %#v", runCmd(cmd))
	}
	if recorded.path == "/postings/seen.json" {
		t.Error("opening a bubbled up thread hit the mark seen endpoint")
	}
	if !v.postingList.postings[v.postingIndex(100)].BubbledUp {
		t.Error("reading a bubbled up thread should leave it in its section")
	}
}

func TestMailViewSeenKeyDismissesBubbledUpThread(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.postingList.postings[0].BubbledUp = true

	done := runCmd(v.HandleContentKey(keyPress("e"))).(postingActionDoneMsg)
	if done.err != nil {
		t.Fatalf("marking a bubbled up thread seen failed: %v", done.err)
	}
	if recorded.path != "/postings/seen.json" {
		t.Errorf("request path = %q, want /postings/seen.json", recorded.path)
	}

	v.Update(done)
	posting := v.postingList.postings[v.postingIndex(100)]
	if !posting.Seen || posting.BubbledUp {
		t.Errorf("the seen key should dismiss a bubbled up thread: seen %v bubbled up %v", posting.Seen, posting.BubbledUp)
	}
}

func TestMailViewLeavesSeenThreadAloneWhenOpened(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.postingList.postings[0].Seen = true

	loaded, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(topicLoadedMsg)
	if !ok || loaded.err != nil || loaded.postingID != 100 {
		t.Fatalf("opened topic = posting %d error %v", loaded.postingID, loaded.err)
	}

	cmd, _ := v.Update(loaded)
	if cmd != nil {
		t.Errorf("opening a seen thread should not mark it again: %#v", runCmd(cmd))
	}
	if recorded.path == "/postings/seen.json" {
		t.Error("opening a seen thread hit the mark seen endpoint")
	}
}

func TestMailViewReportsFailureToMarkOpenedThreadSeen(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusInternalServerError)

	loaded := runCmd(v.HandleContentKey(keyPress("enter"))).(topicLoadedMsg)
	cmd, _ := v.Update(loaded)
	seen := runCmd(cmd).(postingSeenMsg)
	if seen.err == nil {
		t.Fatal("a failed mark seen should carry its error")
	}

	v.Update(seen)
	if !strings.HasPrefix(v.notice, "Could not mark thread as seen") {
		t.Errorf("notice = %q, want the mark seen failure", v.notice)
	}
	if v.postingList.postings[v.postingIndex(100)].Seen {
		t.Error("a failed mark seen should leave the thread unseen")
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
		{"set aside uppercase", "A", "/postings/moves.json", 3, postingActionRemove, "Thread moved to Set Aside"},
		{"seen", "e", "/postings/seen.json", 0, postingActionSeen, "Thread marked as seen"},
		{"seen uppercase", "E", "/postings/seen.json", 0, postingActionSeen, "Thread marked as seen"},
		{"feed", "d", "/postings/moves.json", 2, postingActionRemove, "Thread moved to The Feed"},
		{"feed uppercase", "D", "/postings/moves.json", 2, postingActionRemove, "Thread moved to The Feed"},
		{"paper trail", "p", "/postings/moves.json", 5, postingActionRemove, "Thread moved to Paper Trail"},
		{"paper trail uppercase", "P", "/postings/moves.json", 5, postingActionRemove, "Thread moved to Paper Trail"},
		{"trash", "t", "/postings/trash.json", 0, postingActionRemove, "Thread moved to Trash"},
		{"trash uppercase", "T", "/postings/trash.json", 0, postingActionRemove, "Thread moved to Trash"},
		{"spam", "!", "/postings/spam.json", 0, postingActionRemove, "Thread marked as spam"},
		{"ignore", "-", "/postings/mutings.json", 0, postingActionIgnore, "Thread ignored"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)

			cmd := v.HandleContentKey(keyPress(tt.key))
			if !v.AccountSwitchBlocked() {
				t.Fatal("posting mutation did not block account switching")
			}
			msg := runCmd(cmd)
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
			answer, _ := v.Update(done)
			toast := deliverToView(v, answer)
			if v.AccountSwitchBlocked() {
				t.Fatal("completed posting mutation still blocks account switching")
			}

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
			if toast != tt.notice {
				t.Errorf("toast = %q, want %q", toast, tt.notice)
			}
		})
	}
}

func TestMailViewUnseenKeysRestoreSeenAndBubbledUpThreads(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		key       string
		bubbledUp bool
	}{
		{"lowercase", "u", false},
		{"uppercase", "U", false},
		{"bubbled up", "u", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)
			v.postingList.postings[0].Seen = !testCase.bubbledUp
			v.postingList.postings[0].BubbledUp = testCase.bubbledUp
			v.postingList.resort()
			v.postingList.cursor = v.postingIndex(100)

			done, ok := runCmd(v.HandleContentKey(keyPress(testCase.key))).(postingActionDoneMsg)
			if !ok || done.err != nil || done.effect != postingActionUnseen {
				t.Fatalf("unseen command returned %#v", done)
			}
			if recorded.method != http.MethodPost || recorded.path != "/postings/unseen.json" {
				t.Errorf("request = %s %s, want POST /postings/unseen.json", recorded.method, recorded.path)
			}
			if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
				t.Errorf("posting_ids = %v, want [100]", recorded.body.PostingIDs)
			}

			answer, _ := v.Update(done)
			toast := deliverToView(v, answer)
			posting := v.postingList.selectedPosting()
			if posting == nil || posting.ID != 100 || posting.Seen || posting.BubbledUp {
				t.Errorf("selected posting after unseen = %+v", posting)
			}
			if v.postingList.postings[0].ID != 100 {
				t.Errorf("unseen posting did not move to New for You: %+v", v.postingList.postings)
			}
			if toast != "Thread marked as unseen" {
				t.Errorf("toast = %q", toast)
			}
		})
	}
}

func TestMailViewUnseenSkipsThreadsTheServerLeavesAlone(t *testing.T) {
	tests := []struct {
		name    string
		posting mail.Posting
		notice  string
	}{
		{"already unseen", mail.Posting{ID: 100}, "Thread is already unseen"},
		{"ignored", mail.Posting{ID: 100, Seen: true, Muted: true}, "Stop ignoring this thread to mark it unseen"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)
			v.postingList.postings[0] = tt.posting
			if cmd := v.HandleContentKey(keyPress("u")); cmd != nil {
				t.Fatal("unseen should not start a request")
			}
			if len(recorded.requests) != 0 {
				t.Errorf("requests = %v, want none", recorded.requests)
			}
			if v.notice != tt.notice {
				t.Errorf("notice = %q, want %q", v.notice, tt.notice)
			}
		})
	}
}

func TestMailViewUnseenFailureKeepsSeenState(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusInternalServerError)
	v.postingList.postings[0].Seen = true

	done := runCmd(v.HandleContentKey(keyPress("u"))).(postingActionDoneMsg)
	if done.err == nil {
		t.Fatal("unseen failure should carry an error")
	}
	cmd, _ := v.Update(done)
	if _, ok := runCmd(cmd).(errMsg); !ok {
		t.Fatal("unseen failure should report an error")
	}
	posting := v.postingList.postings[v.postingIndex(100)]
	if !posting.Seen || posting.BubbledUp {
		t.Errorf("failed unseen changed posting state: %+v", posting)
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

	answer, _ := v.Update(done)
	toast := deliverToView(v, answer)
	if len(v.postingList.postings) != 2 {
		t.Errorf("posting count = %d, want ignored thread to remain visible", len(v.postingList.postings))
	}
	if v.postingList.postings[0].Muted {
		t.Error("selected posting should no longer be ignored")
	}
	if toast != "Stopped ignoring thread" {
		t.Errorf("toast = %q, want %q", toast, "Stopped ignoring thread")
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

func TestMailViewImboxKeysMoveToImbox(t *testing.T) {
	for _, key := range []string{"i", "I"} {
		t.Run(key, func(t *testing.T) {
			v, recorded := mailWithTestServer(t, http.StatusNoContent)
			v.boxIndex = sourceIndex(v.boxes, 2, mail.KindBox)

			done, ok := runCmd(v.HandleContentKey(keyPress(key))).(postingActionDoneMsg)
			if !ok || done.err != nil || done.effect != postingActionRemove {
				t.Fatalf("move-to-Imbox command returned %#v", done)
			}
			if recorded.path != "/postings/moves.json" || recorded.body.BoxID == nil || *recorded.body.BoxID != 1 {
				t.Errorf("request = %s body=%+v", recorded.path, recorded.body)
			}
			answer, _ := v.Update(done)
			toast := deliverToView(v, answer)
			if v.postingIndex(100) >= 0 || toast != "Thread moved to Imbox" {
				t.Errorf("posting present=%v toast=%q", v.postingIndex(100) >= 0, toast)
			}
		})
	}
}

func TestMailViewImboxKeyDoesNotMoveWithinImbox(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	if cmd := v.HandleContentKey(keyPress("i")); cmd != nil {
		t.Fatal("move within Imbox should not start a request")
	}
	if len(recorded.requests) != 0 || v.notice != "Already in Imbox" {
		t.Errorf("requests=%v notice=%q", recorded.requests, v.notice)
	}
}

func TestMailViewMovePickerMovesToSelectedBox(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = []mail.Source{
		{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
		{Kind: mail.KindBox, ID: 2, BoxKind: hey.BoxKindFeed, Name: "The Feed"},
		{Kind: mail.KindBox, ID: 3, BoxKind: hey.BoxKindSetAside, Name: "Set Aside"},
		{Kind: mail.KindBox, ID: 4, BoxKind: hey.BoxKindLater, Name: "Reply Later"},
		{Kind: mail.KindBox, ID: 5, BoxKind: hey.BoxKindTrail, Name: "Paper Trail"},
		{Kind: mail.KindBox, ID: 6, BoxKind: hey.BoxKindBubbleUp, Name: "Bubble Up"},
	}

	if cmd := v.HandleContentKey(keyPress("v")); cmd != nil {
		t.Fatal("opening the move picker should not start a request")
	}
	if !v.CapturingInput() || moveModal(v) == nil {
		t.Fatal("move picker should capture input")
	}
	if len(moveModal(v).destinations) != 4 {
		t.Fatalf("destinations = %v, want four boxes", moveModal(v).destinations)
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
	if moveModal(v) != nil || v.CapturingInput() {
		t.Error("move picker should close after choosing a destination")
	}
	if recorded.body.BoxID == nil || *recorded.body.BoxID != 2 {
		t.Errorf("box_id = %v, want 2", recorded.body.BoxID)
	}
	if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
		t.Errorf("posting_ids = %v, want [100]", recorded.body.PostingIDs)
	}

	answer, _ := v.Update(done)
	toast := deliverToView(v, answer)
	if v.postingIndex(100) >= 0 {
		t.Error("moved posting should leave the current box")
	}
	if toast != "Thread moved to The Feed" {
		t.Errorf("toast = %q", toast)
	}
}

func TestMailViewMovePickerKeepsBoxWithCollidingLabelID(t *testing.T) {
	v := mailWithPostings()
	v.boxes = []mail.Source{
		{Kind: mail.KindBox, ID: 12, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
		{Kind: mail.KindFolder, ID: 12, Name: "Receipts"},
	}
	v.boxIndex = 1

	v.HandleContentKey(keyPress("v"))
	if moveModal(v) == nil || len(moveModal(v).destinations) != 1 {
		t.Fatalf("move destinations = %+v", moveModal(v))
	}
	destination := moveModal(v).destinations[0]
	if destination.ID != 12 || destination.BoxKind != hey.BoxKindImbox {
		t.Errorf("destination = %+v, want colliding Imbox", destination)
	}
}

func TestMailViewMovePickerSelectsWithArrowKeys(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = []mail.Source{
		{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
		{Kind: mail.KindBox, ID: 2, BoxKind: hey.BoxKindFeed, Name: "The Feed"},
		{Kind: mail.KindBox, ID: 3, BoxKind: hey.BoxKindSetAside, Name: "Set Aside"},
	}

	v.HandleContentKey(keyPress("v"))
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
	v.boxes = []mail.Source{
		{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
		{Kind: mail.KindBox, ID: 2, BoxKind: hey.BoxKindFeed, Name: "The Feed"},
	}

	v.HandleContentKey(keyPress("v"))
	if cmd := v.HandleContentKey(keyPress("esc")); cmd != nil {
		t.Fatal("canceling the move picker should not return a command")
	}
	if moveModal(v) != nil || v.CapturingInput() {
		t.Error("escape should close the move picker")
	}
	if len(recorded.requests) != 0 {
		t.Errorf("canceling made requests: %v", recorded.requests)
	}
}

func TestMailViewLoadsFolderSourcesAndPostings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/boxes.json":
			_, _ = w.Write([]byte(`[{"id":1,"kind":"imbox","name":"Imbox"}]`))
		case "/my/navigation.json":
			_, _ = w.Write([]byte(`{"items":[{"title":"Labels","menu_items":[{"title":"All Labels","app_url":"/folders"},{"title":"Receipts","app_url":"/folders/12"}]}]}`))
		case "/collections.json":
			_, _ = w.Write([]byte(`[]`))
		case "/folders/12.json":
			_, _ = w.Write([]byte(`{"id":12,"name":"Receipts","postings":[{"id":100,"kind":"topic","summary":"Hotel receipt","folders":[{"id":12,"name":"Receipts"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	v := newMailView(vc)

	loaded, ok := runCmd(v.Init()).(mailSourcesLoadedMsg)
	if !ok {
		t.Fatalf("initial command returned %T, want mailSourcesLoadedMsg", loaded)
	}
	postingsCmd, consumed := v.Update(loaded)
	if !consumed || postingsCmd == nil {
		t.Fatal("folder sources should be loaded and start the first mailbox request")
	}
	if len(v.boxes) != 2 || v.boxes[1].Kind != mail.KindFolder || v.boxes[1].Name != "Receipts" {
		t.Fatalf("mail sources = %+v", v.boxes)
	}

	if cmd := v.SubnavRight(); cmd == nil || !v.seenActive {
		t.Fatal("moving right past the last box should land on Previously Seen")
	}
	if cmd := v.SubnavRight(); cmd != nil || labelsModal(v) == nil {
		t.Fatal("moving right past Previously Seen should open the Labels picker")
	}
	folderCmd := v.HandleContentKey(keyPress("enter"))
	folderLoaded, ok := runCmd(folderCmd).(postingsLoadedMsg)
	if !ok || folderLoaded.err != nil {
		t.Fatalf("folder command returned %#v", folderLoaded)
	}
	v.Update(folderLoaded)
	if len(v.postingList.postings) != 1 || v.postingList.postings[0].Summary != "Hotel receipt" {
		t.Errorf("folder postings = %+v", v.postingList.postings)
	}
	if len(v.postingList.postings[0].Folders) != 1 || v.postingList.postings[0].Folders[0].ID != 12 {
		t.Errorf("posting folders = %+v", v.postingList.postings[0].Folders)
	}
	if v.postingPaging.hasMore() {
		t.Errorf("a label with one page should have nothing more to read, got %q", v.postingPaging.nextPage)
	}
}

func TestMailViewFolderGrowsAsTheReaderScrolls(t *testing.T) {
	var folderQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/boxes.json":
			_, _ = w.Write([]byte(`[{"id":1,"kind":"imbox","name":"Imbox"}]`))
		case "/my/navigation.json":
			_, _ = w.Write([]byte(`{"items":[{"title":"Labels","menu_items":[{"title":"Receipts","app_url":"/folders/12"}]}]}`))
		case "/collections.json":
			_, _ = w.Write([]byte(`[]`))
		case "/folders/12.json":
			folderQueries = append(folderQueries, r.URL.Query().Get("page"))
			w.Header().Set("X-Total-Count", "2")
			if r.URL.Query().Get("page") == "next-cursor" {
				_, _ = w.Write([]byte(`{"id":12,"name":"Receipts","postings":[{"id":101,"kind":"topic","summary":"Second page"}]}`))
				return
			}
			w.Header().Set("Link", "<http://"+r.Host+"/folders/12.json?page=next-cursor>; rel=\"next\"")
			_, _ = w.Write([]byte(`{"id":12,"name":"Receipts","postings":[{"id":100,"kind":"topic","summary":"First page"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	v := newMailView(vc)
	v.Update(runCmd(v.Init()))
	v.SubnavRight() // Previously Seen
	v.SubnavRight() // Labels
	first := runCmd(v.HandleContentKey(keyPress("enter"))).(postingsLoadedMsg)
	more, _ := v.Update(first)
	if v.postingPaging.nextPage != "next-cursor" {
		t.Errorf("first page next cursor = %q, want next-cursor", v.postingPaging.nextPage)
	}
	if more == nil {
		t.Fatal("a label the reader can see the end of should read on")
	}

	second := runCmd(more).(postingsAppendedMsg)
	if cmd, _ := v.Update(second); cmd != nil {
		t.Error("a page with no next cursor should end the label")
	}
	if len(v.postingList.postings) != 2 || v.postingList.postings[1].Summary != "Second page" {
		t.Errorf("grown list = %+v", v.postingList.postings)
	}
	if v.postingPaging.hasMore() {
		t.Errorf("next cursor = %q, want none", v.postingPaging.nextPage)
	}
	if _, _, label, _ := v.SubnavItems(); label != "Receipts" {
		t.Errorf("label = %q, want Receipts", label)
	}
	if fmt.Sprint(folderQueries) != "[ next-cursor]" {
		t.Errorf("folder page queries = %q", folderQueries)
	}
}

// --- Growing a list as the reader scrolls ---

// A box grows by following the next_history_url HEY hands back, on the box's own route —
// the cursor the named routes are the only ones to serve, and the only one whose ordering
// matches the page it came from.
func TestMailViewBoxGrowsAsTheReaderScrolls(t *testing.T) {
	var reads []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads = append(reads, r.URL.Path+"?"+r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "cursor-2" {
			_, _ = w.Write([]byte(`{"id":1,"kind":"imbox","name":"Imbox","postings":[
				{"id":102,"summary":"Third","created_at":"2025-03-01T08:00:00Z","seen":true}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"kind":"imbox","name":"Imbox","next_history_url":"/imbox.json?page=cursor-2","postings":[
			{"id":100,"summary":"First","created_at":"2025-03-01T10:00:00Z","seen":true},
			{"id":101,"summary":"Second","created_at":"2025-03-01T09:00:00Z","seen":true}]}`))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newMailView(vc)
	v.Resize(vc.width, vc.height)
	v.boxes = orderBoxes(testBoxes())

	first := runCmd(v.requestPostings(v.boxes[0])).(postingsLoadedMsg)
	more, _ := v.Update(first)
	if more == nil {
		t.Fatal("a box the reader can see the end of should read on")
	}

	appended := runCmd(more).(postingsAppendedMsg)
	if cmd, _ := v.Update(appended); cmd != nil {
		t.Error("a page with no next cursor should end the box")
	}
	if len(v.postingList.postings) != 3 || v.postingList.postings[2].Summary != "Third" {
		t.Errorf("grown list = %+v", v.postingList.postings)
	}
	if strings.Join(reads, ",") != "/imbox.json?,/imbox.json?cursor-2" {
		t.Errorf("reads = %v", reads)
	}
}

// The Feed orders and pages its postings its own way, so it is read on its own route
// rather than through /boxes/{id} — follow one route's cursor into the other's ordering
// and postings repeat or go missing.
func TestMailViewReadsTheFeedOnItsOwnRoute(t *testing.T) {
	var reads []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads = append(reads, r.URL.Path+"?"+r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/feedbox.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"id":2,"kind":"feedbox","name":"The Feed","next_history_url":"https://app.hey.com/feedbox.json?page=feed-cursor-2","postings":[
			{"id":300,"summary":"The Whale Weekly","created_at":"2025-03-01T10:00:00Z","seen":true}]}`))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newMailView(vc)
	v.Resize(vc.width, vc.height)
	v.boxes = orderBoxes(testBoxes())
	v.boxIndex = 1

	loaded := runCmd(v.requestPostings(v.boxes[1])).(postingsLoadedMsg)
	if loaded.err != nil {
		t.Fatalf("reading the Feed failed: %v", loaded.err)
	}
	more, _ := v.Update(loaded)
	if v.postingPaging.nextPage != "https://app.hey.com/feedbox.json?page=feed-cursor-2" {
		t.Errorf("next cursor = %q, want the Feed's own next_history_url", v.postingPaging.nextPage)
	}
	if more == nil {
		t.Fatal("a Feed the reader can see the end of should read on")
	}

	v.Update(runCmd(more))
	if strings.Join(reads, ",") != "/feedbox.json?,/feedbox.json?feed-cursor-2" {
		t.Errorf("reads = %v, want both pages on the Feed's route", reads)
	}
}

func TestMailViewReadsOnOnlyAsTheCursorNearsTheBottom(t *testing.T) {
	v := mailWithPostings()
	v.postingList.hideSeenState = true
	v.postingList.setSize(80, 20)
	postings := make([]mail.Posting, 0, 20)
	for id := range 20 {
		postings = append(postings, mail.Posting{ID: int64(200 + id), Seen: true})
	}
	v.postingList.setPostings(postings)
	v.postingPaging.nextPage = "cursor-2"

	if cmd := v.loadMorePostings(); cmd != nil {
		t.Error("a cursor at the top of a full list should not read on")
	}
	v.postingList.cursor = len(postings) - 2
	if cmd := v.loadMorePostings(); cmd == nil {
		t.Error("a cursor near the bottom should read on")
	}
	if !v.postingPaging.loading {
		t.Error("the page on its way should be marked as such")
	}
	if cmd := v.loadMorePostings(); cmd != nil {
		t.Error("only one page should be asked for at a time")
	}
}

// A thread the list already shows is not shown twice when the page below turns it up
// again, which is what happens when a reply moves it in the ordering between two reads.
func TestMailViewGrowingSkipsPostingsAlreadyShown(t *testing.T) {
	v := mailWithPostings()
	v.postingPaging.nextPage = "cursor-2"

	v.Update(postingsAppendedMsg{
		requestID:  v.moreRequestID,
		boxID:      v.currentBoxID(),
		sourceKind: v.currentSourceKind(),
		postings: []mail.Posting{
			testPostings()[1],
			{ID: 102, Summary: "Third", Seen: true},
		},
	})

	if len(v.postingList.postings) != 3 {
		t.Fatalf("grown list = %+v", v.postingList.postings)
	}
	if v.postingList.postings[2].ID != 102 {
		t.Errorf("last posting = %+v, want the one page two added", v.postingList.postings[2])
	}
}

func TestMailViewFolderDiscoveryFailurePreservesMailAndRetries(t *testing.T) {
	var navigationRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/boxes.json":
			_, _ = w.Write([]byte(`[{"id":1,"kind":"imbox","name":"Imbox"}]`))
		case "/my/navigation.json":
			if navigationRequests.Add(1) == 1 {
				http.Error(w, "navigation unavailable", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"items":[{"title":"Labels","menu_items":[{"title":"Receipts","app_url":"/folders/12"}]}]}`))
		case "/boxes/1.json":
			_, _ = w.Write([]byte(`{"id":1,"kind":"imbox","name":"Imbox","postings":[]}`))
		case "/collections.json":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	v := newMailView(vc)

	failed := runCmd(v.Init()).(mailSourcesLoadedMsg)
	firstPostings, consumed := v.Update(failed)
	if !consumed || firstPostings == nil || len(v.boxes) != 1 || v.folderDiscoveryErr == "" {
		t.Fatalf("folder failure state = consumed:%v command:%v sources:%+v error:%q", consumed, firstPostings != nil, v.boxes, v.folderDiscoveryErr)
	}
	if !strings.Contains(v.notice, "press b to retry") {
		t.Errorf("notice = %q", v.notice)
	}
	v.Update(runCmd(firstPostings))

	retry := v.HandleContentKey(keyPress("b"))
	if retry == nil || !v.requests.loading {
		t.Fatal("b should retry failed folder discovery")
	}
	recovered := runCmd(retry).(mailSourcesLoadedMsg)
	v.Update(recovered)
	if v.folderDiscoveryErr != "" || len(v.boxes) != 2 || v.boxes[1].Name != "Receipts" {
		t.Errorf("recovered sources = %+v error=%q", v.boxes, v.folderDiscoveryErr)
	}
	if v.notice != "" {
		t.Errorf("successful retry left notice %q", v.notice)
	}
}

func TestMailViewFolderDiscoveryFailurePreservesKnownFolders(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"})
	v.sourceRequestID = 1

	v.Update(mailSourcesLoadedMsg{
		requestID: 1,
		sources:   testBoxes(),
		folderErr: fmt.Errorf("navigation unavailable"),
	})
	if sourceIndex(v.boxes, 12, mail.KindFolder) == 0 || v.folderDiscoveryErr == "" {
		t.Errorf("sources = %+v error=%q", v.boxes, v.folderDiscoveryErr)
	}
}

func TestMailViewIgnoresStaleFolderDiscovery(t *testing.T) {
	v := mailWithPostings()
	v.sourceRequestID = 2
	v.Update(mailSourcesLoadedMsg{requestID: 1, sources: []mail.Source{{ID: 99, Kind: mail.KindFolder, Name: "Stale"}}})
	if len(v.boxes) != len(testBoxes()) {
		t.Errorf("stale discovery replaced sources: %+v", v.boxes)
	}
}

func TestMailViewFolderPickerFilesAndUnfilesThread(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		v, recorded := mailWithTestServer(t, http.StatusNoContent)
		v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"})

		v.HandleContentKey(keyPress("b"))
		if folderModal(v) == nil || !v.CapturingInput() {
			t.Fatal("folder picker should capture input")
		}
		if view := v.View(); !strings.Contains(view, "Label thread") || !strings.Contains(view, "[ ] Receipts") || !strings.Contains(view, "Create a new label") {
			t.Errorf("folder picker view = %q", view)
		}

		done, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(folderActionDoneMsg)
		if !ok || done.err != nil {
			t.Fatalf("folder action returned %#v", done)
		}
		if recorded.path != "/postings/filings.json" || recorded.body.FolderID == nil || *recorded.body.FolderID != 12 {
			t.Errorf("folder request = %s body=%+v", recorded.path, recorded.body)
		}
		if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
			t.Errorf("posting_ids = %v", recorded.body.PostingIDs)
		}
		answer, consumed := v.Update(done)
		if toast, _ := toastAndRest(t, answer); !consumed || toast != "Label Receipts added" {
			t.Errorf("completion = consumed:%v toast:%q", consumed, toast)
		}
	})

	t.Run("unfile", func(t *testing.T) {
		v, recorded := mailWithTestServer(t, http.StatusNoContent)
		v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"})
		v.postingList.postings[0].Folders = []mail.Folder{{ID: 12, Name: "Receipts"}}

		v.HandleContentKey(keyPress("b"))
		if view := v.View(); !strings.Contains(view, "[x] Receipts") || !strings.Contains(view, "Remove all labels") {
			t.Errorf("folder picker view = %q", view)
		}
		done, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(folderActionDoneMsg)
		if !ok || done.err != nil {
			t.Fatalf("folder action returned %#v", done)
		}
		if recorded.method != http.MethodDelete || recorded.path != "/postings/filings.json" {
			t.Errorf("request = %s %s", recorded.method, recorded.path)
		}
		if len(recorded.rawQueries) == 0 || !strings.Contains(recorded.rawQueries[len(recorded.rawQueries)-1], "folder_id=12") {
			t.Errorf("queries = %v", recorded.rawQueries)
		}
	})

	t.Run("remove all", func(t *testing.T) {
		v, recorded := mailWithTestServer(t, http.StatusNoContent)
		v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"})
		v.postingList.postings[0].Folders = []mail.Folder{{ID: 12, Name: "Receipts"}}

		v.HandleContentKey(keyPress("b"))
		v.HandleContentKey(keyPress("down"))
		v.HandleContentKey(keyPress("down"))
		done, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(folderActionDoneMsg)
		if !ok || done.err != nil {
			t.Fatalf("folder action returned %#v", done)
		}
		if len(recorded.rawQueries) == 0 || strings.Contains(recorded.rawQueries[len(recorded.rawQueries)-1], "folder_id=") {
			t.Errorf("queries = %v, want no folder_id", recorded.rawQueries)
		}
		answer, _ := v.Update(done)
		if toast := deliverToView(v, answer); toast != "All labels removed" {
			t.Errorf("toast = %q", toast)
		}
	})
}

func TestMailViewFolderPickerCreatesFolder(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)

	v.HandleContentKey(keyPress("b"))
	if cmd := v.HandleContentKey(keyPress("enter")); cmd == nil || folderModal(v) == nil || !folderModal(v).creating {
		t.Fatal("selecting create should focus the folder name input")
	}
	folderModal(v).input.SetValue("Travel receipts")
	done, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(folderActionDoneMsg)
	if !ok || done.err != nil || !done.created {
		t.Fatalf("create command returned %#v", done)
	}
	if recorded.path != "/postings/folders.json" || recorded.body.Folder.Name != "Travel receipts" {
		t.Errorf("create request = %s body=%+v", recorded.path, recorded.body)
	}
	if len(recorded.body.PostingIDs) != 1 || recorded.body.PostingIDs[0] != 100 {
		t.Errorf("posting_ids = %v", recorded.body.PostingIDs)
	}
	answer, consumed := v.Update(done)
	if toast, _ := toastAndRest(t, answer); !consumed || toast != "Label Travel receipts created" {
		t.Errorf("completion = consumed:%v toast:%q", consumed, toast)
	}
}

func TestMailViewFolderPickerScrollsAndSanitizesNames(t *testing.T) {
	v := mailWithPostings()
	v.vc.height = 8
	for id := int64(1); id <= 20; id++ {
		name := fmt.Sprintf("Label %02d", id)
		if id == 20 {
			name = "Archive\x1b]2;owned\a\n2026"
		}
		v.boxes = append(v.boxes, mail.Source{ID: id + 100, Kind: mail.KindFolder, Name: name})
	}

	v.HandleContentKey(keyPress("b"))
	for range 19 {
		v.HandleContentKey(keyPress("down"))
	}
	// The window-title sequence goes with its payload, rather than leaving the payload
	// behind as debris with its escape defaced: a name is one line of plain text.
	view := v.View()
	if strings.Contains(view, "Label 01") || !strings.Contains(view, "Archive 2026") {
		t.Errorf("scrolled folder picker = %q", view)
	}
	if strings.Contains(view, "owned") {
		t.Errorf("unsafe folder name reached picker: %q", view)
	}

	v.boxIndex = len(v.boxes) - 1
	_, _, label, _ := v.SubnavItems()
	if strings.Contains(label, "owned") || !strings.Contains(label, "Archive 2026") {
		t.Errorf("folder navigation label = %q", label)
	}
}

func TestMailViewFolderPickerTruncatesLongNamesToOneRow(t *testing.T) {
	posting := mail.Posting{ID: 100, Summary: "Receipt"}
	picker := newFolderPicker(posting, []mail.Source{{
		ID: 12, Kind: mail.KindFolder,
		Name: "A label name that is much wider than the picker",
	}})
	picker.resize(24, 8)

	view := picker.view(testVC().styles, 24)
	if !strings.Contains(view, "…") || strings.Contains(view, "much wider than the picker") {
		t.Errorf("long label was not truncated: %q", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 20 {
			t.Errorf("picker line width = %d, want at most 20: %q", width, line)
		}
	}
}

func TestMailViewFolderActionFailureKeepsThread(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusInternalServerError)
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"})

	v.HandleContentKey(keyPress("b"))
	done, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(folderActionDoneMsg)
	if !ok || done.err == nil {
		t.Fatalf("folder action returned %#v, want an error", done)
	}
	cmd, consumed := v.Update(done)
	if !consumed || cmd != nil {
		t.Error("failed folder action should stay in the current view")
	}
	if v.postingIndex(100) < 0 || !strings.Contains(v.notice, "Could not update labels") {
		t.Errorf("posting present=%v notice=%q", v.postingIndex(100) >= 0, v.notice)
	}
	if v.pendingMutations != 0 {
		t.Errorf("pending mutations = %d, want 0", v.pendingMutations)
	}
}

func TestMailViewFolderPickerRequiresNameAndCancels(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("b"))
	v.HandleContentKey(keyPress("enter"))
	if cmd := v.HandleContentKey(keyPress("enter")); cmd != nil {
		t.Fatal("empty folder name should not submit")
	}
	if folderModal(v) == nil || !strings.Contains(v.View(), "Enter a label name") {
		t.Error("empty folder name should keep the form open with guidance")
	}
	v.HandleContentKey(keyPress("esc"))
	if folderModal(v) == nil || folderModal(v).creating {
		t.Error("first escape should return to the folder choices")
	}
	v.HandleContentKey(keyPress("esc"))
	if folderModal(v) != nil || v.CapturingInput() {
		t.Error("second escape should close the folder picker")
	}
}

func TestMailViewMoveFromFolderKeepsFiledThreadVisible(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.boxes = []mail.Source{
		{Kind: mail.KindFolder, ID: 12, Name: "Receipts"},
		{Kind: mail.KindBox, ID: 2, BoxKind: hey.BoxKindFeed, Name: "The Feed"},
	}
	v.boxIndex = 0

	done, ok := runCmd(v.HandleContentKey(keyPress("d"))).(postingActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("move command returned %#v", done)
	}
	if done.effect != postingActionNone {
		t.Errorf("folder move effect = %v, want postingActionNone", done.effect)
	}
	v.Update(done)
	if v.postingIndex(100) < 0 {
		t.Error("moving boxes should preserve a thread's folder label")
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
			v.boxes = []mail.Source{{Kind: mail.KindBox, ID: tt.boxID, BoxKind: tt.kind, Name: tt.name}}
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
	staleRequestID := v.requests.id
	stale := currentPostingsLoaded(v, testPostings())

	refresh, consumed := v.Update(postingActionDoneMsg{
		action: "Thread moved to Trash", boxID: 1, postingID: 100, effect: postingActionRemove,
	})
	if !consumed || refresh == nil {
		t.Fatal("action completion should replace a concurrent reload with a fresh reload")
	}
	if v.requests.id == staleRequestID {
		t.Fatal("action completion should invalidate the concurrent reload")
	}

	v.Update(stale)
	if v.postingIndex(100) >= 0 {
		t.Error("stale reload restored the posting removed by the completed action")
	}
	if !v.requests.loading {
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
	v.Update(currentPostingsLoaded(v, []mail.Posting{{ID: 200, Summary: "Other box"}}))
	v.Update(done)

	if len(v.postingList.postings) != 1 || v.postingList.postings[0].ID != 200 {
		t.Errorf("stale completion changed the new box: %v", v.postingList.postings)
	}
	if v.notice != "" {
		t.Errorf("stale completion notice = %q, want empty", v.notice)
	}
}

func TestMailViewIgnoresUnseenFailureAfterBoxSwitch(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusInternalServerError)
	v.postingList.postings[0].Seen = true

	msg := runCmd(v.HandleContentKey(keyPress("u")))
	done, ok := msg.(postingActionDoneMsg)
	if !ok || done.err == nil {
		t.Fatalf("posting command returned %#v, want an action error", msg)
	}
	v.SubnavRight()
	v.Update(currentPostingsLoaded(v, []mail.Posting{{ID: 200, Summary: "Other box"}}))
	cmd, consumed := v.Update(done)

	if !consumed || cmd != nil {
		t.Fatal("failed action from the old box should be ignored")
	}
	if len(v.postingList.postings) != 1 || v.postingList.postings[0].ID != 200 {
		t.Errorf("failed action changed the new box: %v", v.postingList.postings)
	}
	if v.pendingMutations != 0 {
		t.Errorf("pending mutations = %d, want 0", v.pendingMutations)
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

	wantRequests := "GET /topics/100/entries.json,GET /messages/501.json"
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

func TestMailViewDirectTopicDerivesItsTitleAndIgnoresTheCurrentBox(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusOK)

	loaded, ok := runCmd(v.requestTopic(0, 100, 0, "")).(topicLoadedMsg)
	if !ok {
		t.Fatal("direct topic did not return topicLoadedMsg")
	}
	if loaded.title != "Hello world" || loaded.boxID != 0 {
		t.Fatalf("direct topic = title %q box %d", loaded.title, loaded.boxID)
	}
	if _, consumed := v.Update(loaded); !consumed || !v.inThread || v.topicID != 100 {
		t.Fatalf("direct topic was not opened: consumed=%v open=%v id=%d", consumed, v.inThread, v.topicID)
	}
}

func TestMailViewDownloadsImageDataOnlyForKittyRenderer(t *testing.T) {
	var imageRequests atomic.Int64
	imageData := testPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/topics/100/entries.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":501,"kind":"message"}]`))
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
	loaded := v.fetchTopic(context.Background(), 1, 1, 100, 500, "Latest image")().(topicLoadedMsg)

	if loaded.err != nil || len(loaded.attachments) != 1 {
		t.Fatalf("text fallback topic = attachments:%d error:%v", len(loaded.attachments), loaded.err)
	}
	if len(loaded.images) != 0 || imageRequests.Load() != 0 {
		t.Errorf("text fallback downloaded %d discarded images and returned %d", imageRequests.Load(), len(loaded.images))
	}

	vc.imageRenderer = kittyImageRenderer{}
	v = newMailView(vc)
	loaded = v.fetchTopic(context.Background(), 2, 1, 100, 500, "Latest image")().(topicLoadedMsg)
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
		case "/topics/100/entries.json":
			_, _ = w.Write([]byte(`[{"id":501,"kind":"message"}]`))
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

	loaded := v.fetchTopic(context.Background(), 1, 1, 100, 500, "Untrusted image")().(topicLoadedMsg)

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
		if r.URL.Path == "/topics/100/entries.json" {
			// The index is served newest first, as HEY serves it.
			entries := make([]map[string]any, entryCount)
			for i := range entries {
				entries[i] = map[string]any{"id": 501 + entryCount - 1 - i, "kind": "message"}
			}
			_ = json.NewEncoder(w).Encode(entries)
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
		case "/topics/100/entries.json":
			_, _ = w.Write([]byte(`[{"id":501,"kind":"message"}]`))
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
	if !v.requests.loading {
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
	if !v.requests.loading {
		t.Error("an earlier thread load should not stop the newer open request")
	}
}

func TestMailViewIgnoresReplyLoadAfterBoxSwitch(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	_ = v.loadReplyContext(100, "Hello world")
	loaded := replyContextLoadedMsg{
		requestID: v.requests.id,
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
	if cmd != nil || composeModal(v) != nil {
		t.Error("stale reply context should not open the reply form")
	}
}

func TestMailViewIgnoresForwardLoadAfterBoxSwitch(t *testing.T) {
	v := mailWithPostings()
	_ = v.loadForwardContext(100, "Hello world")
	loaded := forwardContextLoadedMsg{
		requestID: v.requests.id,
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
	if composeModal(v) != nil {
		t.Error("stale forward context should not open the forward form")
	}
}

func TestMailViewIgnoresReplyLoadAfterThreadExit(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	_ = v.loadReplyContext(100, "Hello world")
	loaded := replyContextLoadedMsg{
		requestID: v.requests.id,
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
	if composeModal(v) != nil {
		t.Error("a canceled reply load should not open the reply form")
	}
	if v.requests.loading {
		t.Error("a canceled reply load should not keep loading")
	}
}

func TestMailViewContentNavigationKeys(t *testing.T) {
	for _, keys := range []struct {
		name string
		down string
		up   string
	}{
		{name: "arrows", down: "down", up: "up"},
		{name: "vim", down: "j", up: "k"},
	} {
		t.Run(keys.name, func(t *testing.T) {
			v := mailWithPostings()

			if v.postingList.cursor != 0 {
				t.Fatalf("initial cursor = %d, want 0", v.postingList.cursor)
			}

			v.HandleContentKey(keyPress(keys.down))
			if v.postingList.cursor != 1 {
				t.Errorf("after %s: cursor = %d, want 1", keys.down, v.postingList.cursor)
			}

			v.HandleContentKey(keyPress(keys.up))
			if v.postingList.cursor != 0 {
				t.Errorf("after %s: cursor = %d, want 0", keys.up, v.postingList.cursor)
			}
		})
	}
}

func TestMailViewSearchNavigationAcceptsJAndK(t *testing.T) {
	v := mailWithPostings()
	v.searchActive = true
	v.searchList.setPostings(testPostings())

	v.HandleContentKey(keyPress("j"))
	if v.searchList.cursor != 1 {
		t.Errorf("after j: cursor = %d, want 1", v.searchList.cursor)
	}

	v.HandleContentKey(keyPress("k"))
	if v.searchList.cursor != 0 {
		t.Errorf("after k: cursor = %d, want 0", v.searchList.cursor)
	}
}

func TestMailViewJLoadsMoreListResults(t *testing.T) {
	postings := make([]mail.Posting, 20)
	for i := range postings {
		postings[i] = mail.Posting{ID: int64(i + 1), Seen: true}
	}

	t.Run("mail", func(t *testing.T) {
		v := mailWithPostings()
		v.postingList.hideSeenState = true
		v.postingList.setPostings(postings)
		v.postingList.cursor = len(postings) - loadMoreThreshold - 1
		v.postingPaging.nextPage = "cursor-2"

		if cmd := v.HandleContentKey(keyPress("j")); cmd == nil || !v.postingPaging.loading {
			t.Fatal("j near the bottom should read the next mail page")
		}
	})

	t.Run("search", func(t *testing.T) {
		v := mailWithPostings()
		v.searchActive = true
		v.searchQuery = "quarterly planning"
		v.searchList.setPostings(postings)
		v.searchList.cursor = len(postings) - loadMoreThreshold - 1
		v.searchNextPage = 2

		if cmd := v.HandleContentKey(keyPress("j")); cmd == nil || !v.searchLoadingMore {
			t.Fatal("j near the bottom should read the next search page")
		}
	})
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

	if len(items) != 4 || items[3].label != "Previously Seen" || items[3].shortcut != "9" {
		t.Errorf("expected the boxes and Previously Seen, got %+v", items)
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
	if !v.requests.loading {
		t.Error("SubnavRight should set loading")
	}
	if v.inThread {
		t.Error("SubnavRight should close the open thread")
	}
	if v.notice != "" {
		t.Errorf("SubnavRight should clear the previous notice, got %q", v.notice)
	}

	v.requests.loading = false
	v.SubnavRight()
	if v.boxIndex != 2 {
		t.Errorf("after second SubnavRight: boxIndex = %d, want 2", v.boxIndex)
	}

	// Can't go right past last box
	v.requests.loading = false
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
		entries: []mail.Entry{{ID: 501, Creator: mail.Contact{Name: "Alice"}}, {ID: 502, Creator: mail.Contact{Name: "Bob"}}},
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
		entries: []mail.Entry{{ID: 501, Creator: mail.Contact{Name: "Alice"}}},
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
	answer, consumed := v.Update(save())
	if !consumed {
		t.Fatal("attachment save result was not consumed")
	}
	if saveCalls != 1 || savedDestination != "chart.png" || savedURL != "/rails/blobs/chart.png" || savedForce {
		t.Errorf("save call = count:%d destination:%q URL:%q force:%v", saveCalls, savedDestination, savedURL, savedForce)
	}
	if toast := deliverToView(v, answer); toast != "Saved attachment to chart.png" {
		t.Errorf("save toast = %q", toast)
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
		entries:     []mail.Entry{{ID: 501, Creator: mail.Contact{Name: "Alice"}}},
		attachments: []messageAttachment{{ID: "501:1", MessageID: 501, Filename: "agenda.pdf", URL: "/rails/blobs/agenda.pdf"}},
	})

	save := v.HandleContentKey(keyPress("s"))
	answer, _ := v.Update(save())
	if toast := deliverToView(v, answer); toast != "Attachment already exists: agenda.pdf" {
		t.Errorf("existing attachment toast = %q", toast)
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
		entries:     []mail.Entry{{ID: 501, Creator: mail.Contact{Name: "Alice"}}},
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
	answer, consumed := v.Update(open())
	if !consumed {
		t.Fatal("attachment open result was not consumed")
	}
	if fmt.Sprint(events) != "[save open]" {
		t.Errorf("open events = %v", events)
	}
	if openedPath != filepath.Join(temporaryDirectory, "chart.png") {
		t.Errorf("opened path = %q", openedPath)
	}
	if toast := deliverToView(v, answer); toast != "Opened attachment chart.png" {
		t.Errorf("open toast = %q", toast)
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
		entries:     []mail.Entry{{ID: 501, Creator: mail.Contact{Name: "Alice"}}},
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
		entries: []mail.Entry{{
			ID:      501,
			Creator: mail.Contact{Name: "Alice"},
			Body: htmlutil.ToMarkdown(
				`<p>Review this image:</p><action-text-attachment url="/rails/blobs/chart.png" filename="chart.png" content-type="image/png" filesize="1536"></action-text-attachment><p>Thank you.</p>`,
			),
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
	for _, want := range []string{"Image: ", "Attachments", "chart.png", "image/png", "1.5 KB"} {
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
	v.requests.loading = true
	if cmd := v.HandleContentKey(keyPress("/")); cmd != nil || searchModal(v) != nil {
		t.Error("search should not start before boxes load")
	}

	v.Update(boxesLoadedMsg(testBoxes()))
	if cmd := v.HandleContentKey(keyPress("/")); cmd != nil || searchModal(v) != nil {
		t.Error("search should not start while the first box loads")
	}

	v.Update(currentPostingsLoaded(v, testPostings()))
	if cmd := v.HandleContentKey(keyPress("/")); cmd == nil || searchModal(v) == nil {
		t.Error("search should start after the mailbox finishes loading")
	}
}

func TestMailViewSearchFormSubmitsQueryAndRendersResults(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)
	if cmd := v.HandleContentKey(keyPress("/")); cmd == nil {
		t.Fatal("opening search should focus the input")
	}
	if searchModal(v) == nil || !v.CapturingInput() {
		t.Fatal("search form should capture input")
	}
	searchModal(v).input.SetValue("quarterly planning")

	cmd := v.HandleContentKey(keyPress("enter"))
	if cmd == nil || !v.requests.loading {
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
	if !v.searchActive || v.searchQuery != "quarterly planning" || v.searchNextPage != 0 {
		t.Errorf("search state = active:%v query:%q next:%d", v.searchActive, v.searchQuery, v.searchNextPage)
	}
	if len(v.searchList.postings) != 1 || v.searchList.postings[0].TopicID != 100 {
		t.Errorf("search postings = %+v", v.searchList.postings)
	}
	view := v.View()
	if !strings.Contains(view, "Hello world") || !strings.Contains(view, "Matching message summary") {
		t.Errorf("search result does not show thread and matching-message context: %q", view)
	}
	if strings.Contains(view, "●") {
		t.Errorf("search result shows an unread marker without a read state: %q", view)
	}
	if _, _, label, _ := v.SubnavItems(); label != "Search: quarterly planning" {
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
	if searchModal(v) == nil || !strings.Contains(searchModal(v).view(), "Enter words to search for") {
		t.Error("empty search should keep the form open with guidance")
	}
}

func TestMailViewSearchEscapeClosesForm(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("/"))
	v.HandleContentKey(keyPress("esc"))
	if searchModal(v) != nil || v.CapturingInput() {
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
	v.requests.id = 2
	v.Update(searchResultsLoadedMsg{
		requestID: 1,
		query:     "stale",
		postings:  []mail.Posting{{ID: 99}},
	})
	if v.searchActive || len(v.searchList.postings) != 0 {
		t.Error("stale search results changed the view")
	}
}

func TestMailViewSearchGrowsAsTheReaderScrolls(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"matches":[{"topic":{"id":101,"name":"Cabinet estimate"},"posting_id":11}]}`))
			return
		}
		w.Header().Set("Link", `<http://`+r.Host+`/advanced_search.json?page=2>; rel="next"`)
		_, _ = w.Write([]byte(`{"matches":[{"topic":{"id":100,"name":"Hello world"},"posting_id":10}]}`))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	v := newMailView(vc)
	v.Resize(vc.width, vc.height)
	v.boxes = orderBoxes(testBoxes())

	first := runCmd(v.requestSearch("cabinets")).(searchResultsLoadedMsg)
	more, _ := v.Update(first)
	if v.searchNextPage != 2 {
		t.Fatalf("next page = %d, want 2", v.searchNextPage)
	}
	if more == nil {
		t.Fatal("results the reader can see the end of should read on")
	}

	appended := runCmd(more).(searchResultsAppendedMsg)
	if cmd, _ := v.Update(appended); cmd != nil {
		t.Error("a page with no next page should end the results")
	}
	if len(v.searchList.postings) != 2 || v.searchList.postings[1].TopicID != 101 {
		t.Errorf("grown results = %+v", v.searchList.postings)
	}
	if v.searchNextPage != 0 {
		t.Errorf("next page = %d, want none", v.searchNextPage)
	}
	// The first page is the one you get for asking for nothing, so only the second is named.
	if strings.Join(pages, ",") != ",2" {
		t.Errorf("page requests = %v", pages)
	}
}

// An empty page ends the results whatever the server says comes next, or the search would
// ask the same question forever.
func TestMailViewEmptySearchPageEndsTheResults(t *testing.T) {
	v := mailWithPostings()
	v.searchActive = true
	v.searchQuery = "quarterly planning"
	v.searchNextPage = 2
	v.searchList.setPostings([]mail.Posting{{ID: 10, TopicID: 100}})

	cmd, _ := v.Update(searchResultsAppendedMsg{requestID: v.searchMoreID, query: v.searchQuery, nextPage: 3})
	if cmd != nil || v.searchNextPage != 0 || len(v.searchList.postings) != 1 {
		t.Errorf("empty page = next:%d postings:%v", v.searchNextPage, v.searchList.postings)
	}
}

func TestMailViewCancelPendingSearchResultPreservesResults(t *testing.T) {
	v := mailWithPostings()
	v.searchActive = true
	v.searchQuery = "quarterly planning"
	v.searchList.setPostings([]mail.Posting{{ID: 10, TopicID: 100, Name: "Hello world"}})

	if cmd := v.HandleContentKey(keyPress("enter")); cmd == nil {
		t.Fatal("enter should start loading the selected thread")
	}
	if v.requests.kind != mailRequestTopic || !v.requests.loading {
		t.Fatalf("request state = kind:%d loading:%v", v.requests.kind, v.requests.loading)
	}
	v.ExitThread()
	if !v.searchActive || v.searchQuery != "quarterly planning" || len(v.searchList.postings) != 1 {
		t.Error("canceling a pending searched thread should preserve search results")
	}
	if v.requests.loading || v.requests.kind != mailRequestNone {
		t.Errorf("pending request was not canceled: kind=%d loading=%v", v.requests.kind, v.requests.loading)
	}
}

func TestMailViewSearchResultOpensThreadAndReturnsToResults(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.searchActive = true
	v.searchQuery = "quarterly planning"
	v.searchList.setPostings([]mail.Posting{{ID: 10, TopicID: 100, Name: "Hello world"}})

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

func TestSearchMatchToPostingPreservesActionAndThreadIDs(t *testing.T) {
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
	posting := searchMatchToPosting(match)
	if posting.ID != 10 || posting.TopicID != 100 || posting.Name != "Hello world" || posting.Summary != "Matching message summary" || posting.Creator.Name != "Alice" {
		t.Errorf("posting = %+v", posting)
	}
}

// --- Help bindings ---

func TestMailViewHaystackSearchAndMessageAliases(t *testing.T) {
	for _, key := range []string{"/", "s", "S"} {
		t.Run("search "+key, func(t *testing.T) {
			v := mailWithPostings()
			v.HandleContentKey(keyPress(key))
			if searchModal(v) == nil {
				t.Fatalf("%q did not open search", key)
			}
		})
	}

	for _, testCase := range []struct {
		key  string
		kind mailRequestKind
	}{
		{"r", mailRequestReply}, {"R", mailRequestReply},
		{"f", mailRequestForward}, {"F", mailRequestForward},
	} {
		t.Run("message "+testCase.key, func(t *testing.T) {
			v := mailWithPostings()
			if cmd := v.HandleContentKey(keyPress(testCase.key)); cmd == nil || v.requests.kind != testCase.kind {
				t.Fatalf("%q did not start request %v", testCase.key, testCase.kind)
			}
		})
	}
}

func TestMailViewHaystackPickerAliases(t *testing.T) {
	for _, key := range []string{"v", "V"} {
		v := mailWithPostings()
		v.HandleContentKey(keyPress(key))
		if moveModal(v) == nil {
			t.Errorf("%q did not open move picker", key)
		}
	}
	for _, key := range []string{"b", "B"} {
		v := mailWithPostings()
		v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"})
		v.HandleContentKey(keyPress(key))
		if folderModal(v) == nil {
			t.Errorf("%q did not open labels picker", key)
		}
	}
	for _, key := range []string{"n", "N"} {
		v := mailWithPostings()
		v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindCollection, Name: "Kitchen remodel"})
		v.postingList.postings[0].TopicID = 100
		v.HandleContentKey(keyPress(key))
		if collectionModal(v) == nil {
			t.Errorf("%q did not open collections picker", key)
		}
	}
}

func TestMailViewCoverPeekUsesX(t *testing.T) {
	v := mailWithPostings()
	v.postingList.setCover(coverTopo)
	v.HandleContentKey(keyPress("x"))
	if !v.postingList.coverPeeked {
		t.Fatal("x did not lift the cover")
	}
	v.HandleContentKey(keyPress("x"))
	if v.postingList.coverPeeked {
		t.Fatal("x did not replace the cover")
	}
}

func TestMailViewRemovedPostingAliasesStayUnused(t *testing.T) {
	for _, key := range []string{"m", "g"} {
		v := mailWithPostings()
		if cmd := v.HandleContentKey(keyPress(key)); cmd != nil {
			t.Errorf("removed alias %q returned a command", key)
		}
		if v.modal != nil {
			t.Errorf("removed alias %q opened %T", key, v.modal)
		}
	}
}

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
	for _, expected := range []string{"/", "r", "f", "v", "b", "n", "e", "u", "i", "l", "a", "t", "!", "-"} {
		if !keys[expected] {
			t.Errorf("missing help binding for key %q", expected)
		}
	}
}

// A label scrolls rather than paging, so it advertises no page keys and p keeps meaning
// paper trail.
func TestMailViewLabelHelpOffersNoPageKeys(t *testing.T) {
	v := mailWithPostings()
	v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"})
	v.boxIndex = len(v.boxes) - 1
	v.postingPaging.nextPage = "next-cursor"

	var paperTrail int
	for _, binding := range v.HelpBindings() {
		if binding.desc == "next page" || binding.desc == "previous page" {
			t.Errorf("label advertised a page binding: %+v", binding)
		}
		if binding.key == "p" && binding.desc == "paper trail" {
			paperTrail++
		}
	}
	if paperTrail != 1 {
		t.Errorf("label p bindings = paper-trail:%d; all=%v", paperTrail, v.HelpBindings())
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
	v.HandleContentKey(keyPress("v"))

	bindings := v.HelpBindings()
	if len(bindings) != 3 || bindings[0].key != "↑↓" || bindings[1].key != "enter" || bindings[2].key != "esc" {
		t.Errorf("move picker help = %v", bindings)
	}
}

func TestMailViewHelpBindingsInFolderPicker(t *testing.T) {
	v := mailWithPostings()
	v.HandleContentKey(keyPress("b"))

	bindings := v.HelpBindings()
	if len(bindings) != 3 || bindings[0].key != "↑↓" || bindings[1].key != "enter" || bindings[2].key != "esc" {
		t.Errorf("folder picker help = %v", bindings)
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
	for _, expected := range []string{"enter", "/"} {
		if !keys[expected] {
			t.Errorf("search results missing help binding %q: %v", expected, bindings)
		}
	}
	// Results scroll on rather than paging, so there are no page keys to advertise.
	for _, gone := range []string{"n", "p"} {
		if keys[gone] {
			t.Errorf("search results advertised a page binding %q: %v", gone, bindings)
		}
	}
}

// --- Box shortcuts ---

func TestMailViewBoxShortcut(t *testing.T) {
	v := mailWithPostings()
	v.inThread = true
	v.notice = "previous action"
	cmd := v.handleBoxShortcut("2") // The Feed
	if cmd == nil {
		t.Fatal("box shortcut 'F' should return a command")
	}
	if v.boxIndex == 0 {
		t.Error("boxIndex should have changed")
	}
	if !v.requests.loading {
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
	cmd := v.handleBoxShortcut("1") // Imbox — already selected
	if cmd != nil {
		t.Error("shortcut for current box should be no-op")
	}
}

func mailWithLabels() *mailView {
	v := newMailView(testVC())
	v.vc.width = 60
	v.vc.height = 20
	v.boxes = orderBoxes(append(testBoxes(),
		mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"},
		mail.Source{ID: 13, Kind: mail.KindFolder, Name: "Travel Plans"},
	))
	v.boxIndex = 0
	v.postingList.setSize(60, 20)
	v.Update(currentPostingsLoaded(v, testPostings()))
	return v
}

func TestMailViewLabelsTabAndPicker(t *testing.T) {
	v := mailWithLabels()

	items, selected, _, _ := v.SubnavItems()
	if last := items[len(items)-1]; last.label != "Labels" || last.shortcut != "L" {
		t.Fatalf("the last tab should be Labels with the L shortcut: %+v", items)
	}
	if len(items) != len(testBoxes())+2 {
		t.Errorf("labels should not appear as their own tabs: %+v", items)
	}
	if selected != 0 {
		t.Errorf("selected tab = %d, want 0", selected)
	}

	// Moving right from the last box lands on Previously Seen; right again opens
	// the picker instead of switching.
	v.boxIndex = len(v.tabBoxIndexes()) - 1
	if cmd := v.SubnavRight(); cmd == nil || !v.seenActive {
		t.Fatal("moving right past the last box should land on Previously Seen")
	}
	if cmd := v.SubnavRight(); cmd != nil || labelsModal(v) == nil {
		t.Fatal("moving right past Previously Seen should open the picker")
	}
	if !v.CapturingInput() {
		t.Error("the open picker should capture input")
	}

	view := v.View()
	if !strings.Contains(view, "Labels") || !strings.Contains(view, "Receipts") || !strings.Contains(view, "Travel Plans") {
		t.Errorf("the picker should list the labels: %q", view)
	}

	// Choose the second label.
	v.HandleContentKey(keyPress("down"))
	if cmd := v.HandleContentKey(keyPress("enter")); cmd == nil {
		t.Fatal("choosing a label should load it")
	}
	if labelsModal(v) != nil {
		t.Error("choosing should close the picker")
	}
	if got := v.currentSource(); got == nil || got.Name != "Travel Plans" {
		t.Errorf("current source = %+v, want Travel Plans", got)
	}

	// The Labels tab is now the selected one.
	items, selected, _, _ = v.SubnavItems()
	if selected != len(items)-1 {
		t.Errorf("selected tab = %d, want the Labels tab %d", selected, len(items)-1)
	}

	// Escape closes the picker without switching.
	v.openLabels()
	v.HandleContentKey(keyPress("esc"))
	if labelsModal(v) != nil || v.currentSource().Name != "Travel Plans" {
		t.Error("escape should close the picker and keep the current label")
	}

	// Left from the Labels tab lands on Previously Seen, and left again on the
	// last box tab.
	if cmd := v.SubnavLeft(); cmd == nil || !v.seenActive {
		t.Error("left from Labels should land on Previously Seen")
	}
	if cmd := v.SubnavLeft(); cmd == nil || v.seenActive || v.currentSourceKind() != mail.KindBox {
		t.Error("left from Previously Seen should return to the last box tab")
	}

	// Shift+L opens the Labels picker, the way Shift+K opens Collections.
	// Reply Later keeps lowercase l.
	if cmd := v.handleBoxShortcut("L"); cmd == nil || labelsModal(v) == nil {
		t.Error("Shift+L should open the Labels picker")
	}
	items, _, _, _ = v.SubnavItems()
	labelsTab := items[len(items)-1]
	if labelsTab.label != "Labels" || labelsTab.shortcut != "L" {
		t.Errorf("Labels tab = %+v, want the L shortcut on it", labelsTab)
	}
}

func TestSectionsAndUnreadDotAreImboxOnly(t *testing.T) {
	v := mailWithPostings() // box 0 is the Imbox
	v.vc.width, v.vc.height = 80, 30
	v.Resize(80, 30)
	view := stripANSI(v.View())
	if !strings.Contains(view, "New for You") || !strings.Contains(view, "●") {
		t.Errorf("the Imbox should show sections and the unread dot: %q", view)
	}

	feed := -1
	for i, b := range v.boxes {
		if b.Name == "The Feed" {
			feed = i
		}
	}
	if feed < 0 {
		t.Fatalf("test boxes lack The Feed: %+v", v.boxes)
	}
	v.switchBox(feed)
	v.Update(currentPostingsLoaded(v, testPostings()))

	view = stripANSI(v.View())
	if strings.Contains(view, "New for You") || strings.Contains(view, "Previously Seen") {
		t.Errorf("The Feed should be one flat list: %q", view)
	}
	if strings.Contains(view, "●") {
		t.Errorf("The Feed should not show the unread dot: %q", view)
	}
	if !strings.Contains(view, "Hello world") || !strings.Contains(view, "Meeting notes") {
		t.Errorf("The Feed should still list every thread: %q", view)
	}
}

func TestLabelNamedImboxDoesNotGetImboxSeenSections(t *testing.T) {
	v := mailWithPostings()
	v.vc.width, v.vc.height = 80, 30
	v.Resize(80, 30)
	v.boxes = orderBoxes(append(v.boxes,
		mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Imbox"},
	))

	label := len(v.boxes) - 1
	v.switchBox(label)
	v.Update(currentPostingsLoaded(v, testPostings()))

	view := stripANSI(v.View())
	if strings.Contains(view, "New for You") || strings.Contains(view, "Previously Seen") || strings.Contains(view, "●") {
		t.Errorf("a label named Imbox should remain a flat list without unread dots: %q", view)
	}
}

// Two entries the same afternoon are two rows the reader has to tell apart, so a
// thread dates its entries to the minute rather than to the day.
func TestThreadEntriesCarryTheTimeOfDay(t *testing.T) {
	v := mailWithPostings()
	v.Resize(80, 30)
	entries := []mail.Entry{
		{ID: 501, Creator: mail.Contact{Name: "Alice"}, CreatedAt: time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC)},
		{ID: 502, Creator: mail.Contact{Name: "Bob"}, CreatedAt: time.Date(2026, 8, 18, 16, 5, 0, 0, time.UTC)},
	}

	renderedEntries, _ := v.renderEntries(entries)
	rendered := stripANSI(renderedEntries)

	for _, want := range []string{
		formatDisplayDateTime(entries[0].CreatedAt),
		formatDisplayDateTime(entries[1].CreatedAt),
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("thread is missing the entry time %q:\n%s", want, rendered)
		}
	}
	if formatDisplayDateTime(entries[0].CreatedAt) == formatDisplayDateTime(entries[1].CreatedAt) {
		t.Error("two entries on the same day rendered identically, so the reader cannot order them")
	}
}

// A thread longer than one geared page is read whole in the TUI, oldest first, through
// the same loader the CLI uses; a body that could not be read is marked, not faked.
func TestMailViewReadsEveryPageOfAThreadAndMarksUnreadBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/topics/100/entries.json" && r.URL.Query().Get("page") == "":
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/topics/100/entries.json?page=eyJwYWdlIjoy>; rel="next"`, r.Host))
			_, _ = w.Write([]byte(`[{"id":503,"kind":"message","summary":"third"},{"id":502,"kind":"message","summary":"second"}]`))
		case r.URL.Path == "/topics/100/entries.json":
			_, _ = w.Write([]byte(`[{"id":501,"kind":"message","summary":"first"}]`))
		case r.URL.Path == "/messages/502.json":
			http.Error(w, `{"error":"gone"}`, http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, "/messages/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/messages/"), ".json")
			fmt.Fprintf(w, `{"id":%s,"content":"<p>body %s</p>"}`, id, id)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	v := newMailView(vc)
	loaded := v.fetchTopic(context.Background(), 1, 1, 100, 500, "Long thread")().(topicLoadedMsg)
	if loaded.err != nil {
		t.Fatalf("fetch topic: %v", loaded.err)
	}
	if len(loaded.entries) != 3 || loaded.entries[0].ID != 501 || loaded.entries[2].ID != 503 {
		t.Fatalf("entries = %+v, want three, oldest first", loaded.entries)
	}
	if loaded.entries[1].BodyState != "failed" || !loaded.entries[1].Body.IsEmpty() {
		t.Errorf("entry 502 = %+v, want failed with no body", loaded.entries[1])
	}
	if !strings.Contains(loaded.notice, "1 of 3 bodies could not be read") {
		t.Errorf("notice = %q", loaded.notice)
	}

	view, _ := v.renderEntries(loaded.entries)
	if !strings.Contains(view, "body 501") || !strings.Contains(view, "body 503") {
		t.Errorf("view lacks the read bodies: %q", view)
	}
	if !strings.Contains(view, "(body not read: failed)") {
		t.Errorf("view does not mark the unread body: %q", view)
	}
}

func TestRenderEntriesReportsEveryMessageHeaderOffset(t *testing.T) {
	v := newMailView(testVC())
	v.vc.width = 60
	v.attachments = []messageAttachment{{
		ID:          "2:1",
		MessageID:   2,
		Filename:    "quarterly-plan.pdf",
		ContentType: "application/pdf",
	}}
	entries := []mail.Entry{
		{ID: 1, Creator: mail.Contact{Name: "Maria Gonzalez"}, Summary: "Opening summary", Body: htmlutil.ToMarkdown("<p>First body.</p><p>Second paragraph.</p>")},
		{ID: 2, Creator: mail.Contact{Name: "Sam Rivera"}, Body: htmlutil.ToMarkdown("<p>Message with an attachment.</p>")},
		{ID: 3, Creator: mail.Contact{Name: "Ana Ortiz"}, Summary: "Closing summary"},
	}

	rendered, offsets := v.renderEntries(entries)
	searchFrom := 0
	for i, entry := range entries {
		name := entry.Creator.Name
		relative := strings.Index(rendered[searchFrom:], name)
		if relative < 0 {
			t.Fatalf("rendered thread is missing header %q: %q", name, rendered)
		}
		headerStart := searchFrom + relative
		want := strings.Count(rendered[:headerStart], "\n")
		if offsets[i] != want {
			t.Errorf("offset[%d] = %d, want header line %d", i, offsets[i], want)
		}
		searchFrom = headerStart + len(name)
	}
}

func TestThreadJKeepsLineScrollingForOneMessage(t *testing.T) {
	v := newMailView(testVC())
	v.vc.width = 80
	v.vc.height = 10
	v.topicViewport.SetWidth(80)
	v.topicViewport.SetHeight(10)
	v.inThread = true
	v.entries = []mail.Entry{{
		ID:      1,
		Creator: mail.Contact{Name: "Maria Gonzalez"},
		Body:    htmlutil.ToMarkdown(strings.Repeat("<p>One long message paragraph.</p>", 20)),
	}}
	v.rebuildTopicContent()

	v.HandleContentKey(keyPress("j"))
	if got := v.topicViewport.YOffset(); got != 1 {
		t.Errorf("j should scroll one line in a one-message thread: offset %d", got)
	}
	v.HandleContentKey(keyPress("k"))
	if got := v.topicViewport.YOffset(); got != 0 {
		t.Errorf("k should scroll one line back in a one-message thread: offset %d", got)
	}
}

func TestThreadJKJumpsBetweenMessages(t *testing.T) {
	v := newMailView(testVC())
	v.vc.width = 80
	v.vc.height = 10
	v.topicViewport.SetWidth(80)
	v.topicViewport.SetHeight(10)
	v.inThread = true
	v.entries = []mail.Entry{
		{ID: 1, Creator: mail.Contact{Name: "Maria Gonzalez"}, Body: htmlutil.ToMarkdown(strings.Repeat("<p>First message paragraph.</p>", 8))},
		{ID: 2, Creator: mail.Contact{Name: "Sam Rivera"}, Body: htmlutil.ToMarkdown(strings.Repeat("<p>Second message paragraph.</p>", 8))},
		{ID: 3, Creator: mail.Contact{Name: "Ana Ortiz"}, Body: htmlutil.ToMarkdown(strings.Repeat("<p>Third message paragraph.</p>", 8))},
	}
	v.rebuildTopicContent()

	if len(v.entryOffsets) != 3 || v.entryOffsets[0] != 0 {
		t.Fatalf("entry offsets = %v", v.entryOffsets)
	}
	if v.entryOffsets[1] <= v.entryOffsets[0] || v.entryOffsets[2] <= v.entryOffsets[1] {
		t.Fatalf("entry offsets should grow: %v", v.entryOffsets)
	}

	v.HandleContentKey(keyPress("j"))
	if got := v.topicViewport.YOffset(); got != v.entryOffsets[1] {
		t.Errorf("j should jump to the second message: offset %d, want %d", got, v.entryOffsets[1])
	}
	v.HandleContentKey(keyPress("j"))
	if got := v.topicViewport.YOffset(); got != v.entryOffsets[2] {
		t.Errorf("j should jump to the third message: offset %d, want %d", got, v.entryOffsets[2])
	}
	v.HandleContentKey(keyPress("j"))
	if got := v.topicViewport.YOffset(); got != v.entryOffsets[2] {
		t.Errorf("j at the final message should stay on its header: offset %d, want %d", got, v.entryOffsets[2])
	}
	v.HandleContentKey(keyPress("k"))
	if got := v.topicViewport.YOffset(); got != v.entryOffsets[1] {
		t.Errorf("k should jump back to the second message: offset %d, want %d", got, v.entryOffsets[1])
	}
	v.HandleContentKey(keyPress("k"))
	v.HandleContentKey(keyPress("k"))
	if got := v.topicViewport.YOffset(); got != 0 {
		t.Errorf("k at the first message should go to the top: offset %d", got)
	}
}

// bundleRow is a bundle posting the way HEY lists one: several unseen threads from one
// contact, so no topic of its own — its app_url names the contact, not a thread. It is
// built through mail.NewPosting so the tests exercise the real kind mapping: a bundle is
// kind "bundle", while HEY's `bundled` flag means filed inside one.
func bundleRow() mail.Posting {
	return mail.NewPosting(generated.Posting{
		Id:      311,
		Kind:    "bundle",
		Name:    "Deploy failed on main • Nightly build is green again",
		AppUrl:  "https://app.hey.com/contacts/88",
		Creator: generated.Contact{Id: 88, Name: "GitHub"},
	})
}

func TestMailViewOpensABundle(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.postingList.postings[0] = bundleRow()

	loaded, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(bundleLoadedMsg)
	if !ok || loaded.err != nil {
		t.Fatalf("opening a bundle returned %#v", loaded)
	}
	more, _ := v.Update(loaded)
	if !v.bundleActive || v.bundleTitle != "New from GitHub" {
		t.Fatalf("bundle view: active %v title %q", v.bundleActive, v.bundleTitle)
	}
	if len(v.bundleList.postings) != 1 || v.bundleList.postings[0].TopicID != 100 {
		t.Fatalf("bundle postings = %+v", v.bundleList.postings)
	}
	if _, _, label, _ := v.SubnavItems(); !strings.Contains(label, "New from GitHub") {
		t.Errorf("subnav label = %q", label)
	}

	// The list is shorter than the window with a page below, so it grows at once.
	appended, ok := runCmd(more).(bundleAppendedMsg)
	if !ok || appended.err != nil {
		t.Fatalf("growing the bundle returned %#v", appended)
	}
	v.Update(appended)
	if len(v.bundleList.postings) != 2 || v.bundleList.postings[1].TopicID != 101 {
		t.Fatalf("grown bundle postings = %+v", v.bundleList.postings)
	}
	if v.bundleNextPage != "" {
		t.Errorf("nextPage = %q, want none after the last page", v.bundleNextPage)
	}

	// Enter on a member opens its thread the normal way, and esc steps back out
	// through the bundle to the box.
	topic, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(topicLoadedMsg)
	if !ok || topic.err != nil || topic.topicID != 100 {
		t.Fatalf("opening a member returned %#v", topic)
	}
	v.Update(topic)
	if !v.inThread {
		t.Fatal("a bundle member should open as a thread")
	}
	v.ExitThread()
	if v.inThread || !v.bundleActive {
		t.Fatalf("leaving the thread should return to the bundle: inThread %v bundleActive %v", v.inThread, v.bundleActive)
	}
	v.ExitThread()
	if v.bundleActive {
		t.Fatal("leaving the bundle should return to the box list")
	}
}

func TestMailViewOpensAReadBundleAsContactThreads(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	row := bundleRow()
	row.Seen = true
	v.postingList.postings[0] = row

	loaded, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(bundleLoadedMsg)
	if !ok || loaded.err != nil || loaded.contactID != 88 {
		t.Fatalf("opening a read bundle returned %#v", loaded)
	}
	v.Update(loaded)
	if !v.bundleActive || v.bundleTitle != "All threads with GitHub" {
		t.Fatalf("contact threads view: active %v title %q", v.bundleActive, v.bundleTitle)
	}
	if len(v.bundleList.postings) != 2 || v.bundleList.postings[0].TopicID != 100 || v.bundleList.postings[1].TopicID != 101 {
		t.Fatalf("contact threads = %+v", v.bundleList.postings)
	}

	topic, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(topicLoadedMsg)
	if !ok || topic.err != nil || topic.topicID != 100 {
		t.Fatalf("opening a thread returned %#v", topic)
	}
}

func TestMailViewSaysWhyABundleListIsEmpty(t *testing.T) {
	v := mailWithPostings()
	v.bundleActive = true
	v.bundleTitle = "All threads with GitHub"
	v.bundleContactID = 88
	v.bundleList.setPostings(nil)

	if view := v.View(); !strings.Contains(view, "No emails with this contact") {
		t.Errorf("empty contact threads view = %q", view)
	}

	v.bundleContactID = 0
	if view := v.View(); !strings.Contains(view, "Nothing unseen here any more") {
		t.Errorf("empty unseen bundle view = %q", view)
	}
}

func TestMailViewReplyOnABundleOpensIt(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.postingList.postings[0] = bundleRow()

	loaded, ok := runCmd(v.HandleContentKey(keyPress("r"))).(bundleLoadedMsg)
	if !ok || loaded.err != nil {
		t.Fatalf("reply on a bundle returned %#v", loaded)
	}
	v.Update(loaded)
	if !v.bundleActive {
		t.Fatal("reply on a bundle should open the bundle")
	}
}

func TestMailViewJumpsToPreviouslySeen(t *testing.T) {
	v, recorded := mailWithTestServer(t, http.StatusNoContent)

	loaded, ok := runCmd(v.handleBoxShortcut("9")).(seenLoadedMsg)
	if !ok || loaded.err != nil {
		t.Fatalf("jumping to Previously Seen returned %#v", loaded)
	}
	if recorded.path != "/imbox/seen.json" {
		t.Errorf("read %s, want /imbox/seen.json", recorded.path)
	}
	more, _ := v.Update(loaded)
	if !v.seenActive {
		t.Fatal("9 should open the Previously Seen screen")
	}
	if len(v.seenList.postings) != 1 || v.seenList.postings[0].TopicID != 100 {
		t.Fatalf("seen postings = %+v", v.seenList.postings)
	}
	// The list is shorter than the window with a page below, so it grows at once,
	// following the cursor out of next_history_url back into the seen route.
	appended, ok := runCmd(more).(seenAppendedMsg)
	if !ok || appended.err != nil {
		t.Fatalf("growing the seen list returned %#v", appended)
	}
	if recorded.path != "/imbox/seen.json" || recorded.rawQueries[len(recorded.rawQueries)-1] != "page=seen-page-2" {
		t.Errorf("read %s?%s, want /imbox/seen.json?page=seen-page-2", recorded.path, recorded.rawQueries[len(recorded.rawQueries)-1])
	}
	v.Update(appended)
	if len(v.seenList.postings) != 2 || v.seenList.postings[1].TopicID != 101 {
		t.Fatalf("grown seen postings = %+v", v.seenList.postings)
	}
	if v.seenNextPage != "" {
		t.Errorf("nextPage = %q, want none after the last page", v.seenNextPage)
	}
	// The box row stays under the screen's own label, with the screen's own tab —
	// after the boxes, before Labels — selected.
	if items, selected, label, _ := v.SubnavItems(); label != "Previously Seen" || selected != len(items)-1 || items[selected].label != "Previously Seen" {
		t.Errorf("subnav = %+v, selected %d, label %q", items, selected, label)
	}

	// Enter on a thread opens it the normal way, and esc steps back out through
	// the seen screen to the box.
	topic, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(topicLoadedMsg)
	if !ok || topic.err != nil || topic.topicID != 100 {
		t.Fatalf("opening a seen thread returned %#v", topic)
	}
	v.Update(topic)
	if !v.inThread {
		t.Fatal("a seen thread should open as a thread")
	}
	v.ExitThread()
	if v.inThread || !v.seenActive {
		t.Fatalf("leaving the thread should return to the seen screen: inThread %v seenActive %v", v.inThread, v.seenActive)
	}
	v.ExitThread()
	if v.seenActive {
		t.Fatal("leaving the seen screen should return to the box list")
	}
}

func TestMailViewPreviouslySeenIsANoOpWhileOpen(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)

	loaded, _ := runCmd(v.handleBoxShortcut("9")).(seenLoadedMsg)
	v.Update(loaded)
	if cmd := v.handleBoxShortcut("9"); cmd != nil {
		t.Error("9 on the seen screen should do nothing")
	}
}

func TestMailViewBoxShortcutClosesPreviouslySeen(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)

	loaded, _ := runCmd(v.handleBoxShortcut("9")).(seenLoadedMsg)
	v.Update(loaded)
	if cmd := v.handleBoxShortcut("2"); cmd == nil {
		t.Fatal("a box shortcut should leave the seen screen for the box")
	}
	if v.seenActive {
		t.Error("switching boxes should close the seen screen")
	}
}

func TestMailViewOwnBoxShortcutClosesPreviouslySeen(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)

	loaded, _ := runCmd(v.handleBoxShortcut("9")).(seenLoadedMsg)
	v.Update(loaded)
	v.handleBoxShortcut("1") // the box the screen was opened over
	if v.seenActive {
		t.Error("the box's own shortcut should close the seen screen")
	}
	if len(v.postingList.postings) != 2 {
		t.Errorf("box postings = %+v", v.postingList.postings)
	}
}

func TestMailViewSubnavStepsOutOfPreviouslySeen(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)

	loaded, _ := runCmd(v.handleBoxShortcut("9")).(seenLoadedMsg)
	v.Update(loaded)
	if cmd := v.SubnavLeft(); cmd == nil {
		t.Fatal("subnav left should leave the seen screen for the last box")
	}
	if v.seenActive {
		t.Error("subnav navigation should close the seen screen")
	}
}

func TestMailViewSaysWhyTheSeenScreenIsEmpty(t *testing.T) {
	v := mailWithPostings()
	v.seenActive = true
	v.seenList.setPostings(nil)

	if view := v.View(); !strings.Contains(view, "Nothing has been seen yet") {
		t.Errorf("empty seen view = %q", view)
	}
}

func TestMailViewSeenScreenSurvivesAStaleAppend(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)

	loaded, _ := runCmd(v.handleBoxShortcut("9")).(seenLoadedMsg)
	v.Update(loaded)
	stale := seenAppendedMsg{requestID: v.seenMoreID - 1, postings: []mail.Posting{{ID: 699}}}
	v.Update(stale)
	for _, posting := range v.seenList.postings {
		if posting.ID == 699 {
			t.Fatal("a stale append should not grow the seen list")
		}
	}
}

func TestMailViewTriagesFromTheSeenScreen(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	loaded, _ := runCmd(v.handleBoxShortcut("9")).(seenLoadedMsg)
	more, _ := v.Update(loaded)
	appended, _ := runCmd(more).(seenAppendedMsg)
	v.Update(appended)
	if len(v.seenList.postings) != 2 {
		t.Fatalf("seen postings = %+v", v.seenList.postings)
	}

	// A seen thread is already in the Imbox, so i has nowhere to move it.
	if cmd := v.HandleContentKey(keyPress("i")); cmd != nil || v.notice != "Already in Imbox" {
		t.Fatalf("i on the seen screen: cmd %v notice %q", cmd, v.notice)
	}

	// Ignoring marks the row and keeps it; stop ignoring clears it.
	muted, ok := runCmd(v.HandleContentKey(keyPress("-"))).(postingActionDoneMsg)
	if !ok || muted.err != nil || !muted.seen {
		t.Fatalf("ignoring returned %#v", muted)
	}
	v.Update(muted)
	if len(v.seenList.postings) != 2 || !v.seenList.postings[0].Muted {
		t.Fatalf("ignored seen postings = %+v", v.seenList.postings)
	}
	unmuted, _ := runCmd(v.HandleContentKey(keyPress("+"))).(postingActionDoneMsg)
	v.Update(unmuted)
	if v.seenList.postings[0].Muted {
		t.Fatal("stop ignoring should clear the mark")
	}

	// Setting aside moves the thread out of the Imbox, so it leaves the screen.
	aside, ok := runCmd(v.HandleContentKey(keyPress("a"))).(postingActionDoneMsg)
	if !ok || aside.err != nil || !aside.seen || aside.effect != postingActionRemove {
		t.Fatalf("set aside returned %#v", aside)
	}
	v.Update(aside)
	if len(v.seenList.postings) != 1 || v.seenList.postings[0].ID != 612 {
		t.Fatalf("seen postings after set aside = %+v", v.seenList.postings)
	}

	// Marked unseen, a thread is not previously seen any more.
	unseen, ok := runCmd(v.HandleContentKey(keyPress("u"))).(postingActionDoneMsg)
	if !ok || unseen.err != nil || unseen.effect != postingActionUnseen {
		t.Fatalf("mark unseen returned %#v", unseen)
	}
	v.Update(unseen)
	if len(v.seenList.postings) != 0 {
		t.Fatalf("seen postings after unseen = %+v", v.seenList.postings)
	}

	// The box list underneath was never touched.
	if len(v.postingList.postings) != 2 {
		t.Errorf("box postings = %+v", v.postingList.postings)
	}
}

func TestMailViewSeenScreenActionAfterClosingLandsNowhere(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	loaded, _ := runCmd(v.handleBoxShortcut("9")).(seenLoadedMsg)
	v.Update(loaded)

	done, ok := runCmd(v.HandleContentKey(keyPress("a"))).(postingActionDoneMsg)
	if !ok || !done.seen {
		t.Fatalf("set aside returned %#v", done)
	}
	v.ExitThread()
	v.Update(done)
	if len(v.postingList.postings) != 2 {
		t.Errorf("a seen-screen action landed on the box list: %+v", v.postingList.postings)
	}
}

func TestMailViewMovePickerOnTheSeenScreenLeavesTheImboxOut(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.SubnavRight() // The Feed under the screen, so the Imbox would otherwise be offered
	loaded, _ := runCmd(v.handleBoxShortcut("9")).(seenLoadedMsg)
	v.Update(loaded)

	v.HandleContentKey(keyPress("v"))
	picker := modalOf[*movePicker](v)
	if picker == nil {
		t.Fatal("v on the seen screen should open the move picker")
	}
	for _, destination := range picker.destinations {
		if destination.BoxKind == hey.BoxKindImbox {
			t.Error("the move picker should not offer the Imbox to a seen thread")
		}
	}
}

func TestMailViewHelpBindingsNamePreviouslySeen(t *testing.T) {
	v := mailWithPostings()
	if !hasHelpBinding(v.HelpBindings(), "9") {
		t.Error("the list help should name 9")
	}

	v.seenActive = true
	bindings := v.HelpBindings()
	for _, key := range []string{"enter", "a", "l", "u", "t", "v", "b", "n"} {
		if !hasHelpBinding(bindings, key) {
			t.Errorf("seen screen help misses %q: %+v", key, bindings)
		}
	}
	if hasHelpBinding(bindings, "9") {
		t.Error("the seen screen help should not name 9")
	}
}

func TestMailViewRefusesAPostingWithoutAThread(t *testing.T) {
	v := mailWithPostings()
	v.postingList.postings[0].TopicID = 0

	failed, ok := runCmd(v.HandleContentKey(keyPress("enter"))).(errMsg)
	if !ok || failed.err == nil {
		t.Fatalf("opening a topicless posting returned %#v, want errMsg", failed)
	}
	if v.bundleActive || v.inThread {
		t.Error("a topicless posting should open nothing")
	}
}

func TestMailViewBundleSurvivesAStaleAppend(t *testing.T) {
	v, _ := mailWithTestServer(t, http.StatusNoContent)
	v.postingList.postings[0] = bundleRow()

	loaded, _ := runCmd(v.HandleContentKey(keyPress("enter"))).(bundleLoadedMsg)
	v.Update(loaded)
	stale := bundleAppendedMsg{requestID: v.bundleMoreID - 1, postingID: 311, postings: []mail.Posting{{ID: 599}}}
	v.Update(stale)
	for _, posting := range v.bundleList.postings {
		if posting.ID == 599 {
			t.Fatal("a stale append should not grow the bundle")
		}
	}
}
