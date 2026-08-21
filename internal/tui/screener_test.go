package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// --- Test helpers ---

type screenerRequest struct {
	method string
	path   string
	query  string
	body   string
}

type screenerServerState struct {
	mu         sync.Mutex
	requests   []screenerRequest
	pending    string
	screened   string
	failScreen bool
}

func (s *screenerServerState) snapshot() []screenerRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]screenerRequest(nil), s.requests...)
}

func screenerTestView(t *testing.T) (*screenerView, *screenerServerState) {
	t.Helper()
	state := &screenerServerState{
		pending: `{"pending_clearances_count":2,"clearances":[
			{"id":91,"status":"pending","petitioner":{"id":11,"name":"Jane Doe","email_address":"jane@example.com"},
			 "most_recent_entry":{"id":501,"subject":"Quarterly planning","summary":"Can we meet Thursday?","topic_id":700}},
			{"id":92,"status":"pending","petitioner":{"id":12,"name":"Bob Smith","email_address":"bob@example.org"},
			 "most_recent_entry":{"id":502,"subject":"Invoice 4102","summary":"Attached for your records","topic_id":701}}
		]}`,
		screened: `{"clearances":[
			{"id":80,"status":"approved","petitioner":{"id":13,"name":"Alice Jones","email_address":"alice@example.com"},"updated_at":"2026-08-18T10:00:00Z"},
			{"id":81,"status":"denied","petitioner":{"id":14,"name":"Carl Reed","email_address":"carl@example.org"},"updated_at":"2026-08-17T09:00:00Z"}
		]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		state.mu.Lock()
		state.requests = append(state.requests, screenerRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: body.String()})
		pending, screened, failScreen := state.pending, state.screened, state.failScreen
		state.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/clearances.json":
			_, _ = w.Write([]byte(pending))
		case r.Method == http.MethodGet && r.URL.Path == "/my/clearances.json":
			_, _ = w.Write([]byte(screened))
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/clearances/"):
			if failScreen {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"Not allowed"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":91,"status":"approved","petitioner":{"id":11,"name":"Jane Doe","email_address":"jane@example.com"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/clearances/punt.json":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))
	return newScreenerView(vc), state
}

// loadedScreener returns a view showing the two senders waiting to be screened.
func loadedScreener(t *testing.T) (*screenerView, *screenerServerState) {
	t.Helper()
	view, state := screenerTestView(t)
	loaded, ok := runCmd(view.Init()).(screenerPendingLoadedMsg)
	if !ok {
		t.Fatalf("Init returned %T, want screenerPendingLoadedMsg", loaded)
	}
	view.Update(loaded)
	return view, state
}

// --- Loading ---

func TestScreenerLoadsPendingSenders(t *testing.T) {
	view, _ := loadedScreener(t)

	if view.pendingCount != 2 || len(view.pending.rows) != 2 {
		t.Fatalf("pending = count:%d rows:%d, want 2 and 2", view.pendingCount, len(view.pending.rows))
	}
	first := view.pending.rows[0]
	if first.id != 91 || first.name != "Jane Doe" || first.subject != "Quarterly planning" || first.summary != "Can we meet Thursday?" {
		t.Errorf("first sender = %+v", first)
	}
	if view.loading {
		t.Error("loading should be false once the senders arrive")
	}
}

func TestScreenerViewCarriesHeyWording(t *testing.T) {
	view, _ := loadedScreener(t)
	rendered := plainText(view.View())

	for _, want := range []string{
		"The people below are trying to email you for the first time.",
		"You get to decide if you want to hear from them.",
		"Jane Doe",
		"Quarterly planning",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("screener view is missing %q:\n%s", want, rendered)
		}
	}

	// The screening question lives in the help bar, with Yes and No as
	// underlined hotkeys.
	bar := newHelpBar(newStyles())
	bar.setWidth(120)
	bar.setBindings(view.HelpBindings())
	barView := bar.view()
	if !strings.Contains(plainText(barView), "Want to get emails from them? Yes No") {
		t.Errorf("help bar is missing the screening question: %q", barView)
	}
	if !strings.Contains(barView, "\x1b[1;4;34;4mY\x1b[m") || !strings.Contains(barView, "\x1b[1;4;34;4mN\x1b[m") {
		t.Errorf("Y and N should be underlined hotkeys: %q", barView)
	}
}

func TestScreenerEmptyQueue(t *testing.T) {
	view, state := screenerTestView(t)
	state.pending = `{"pending_clearances_count":0,"clearances":[]}`
	view.Update(runCmd(view.Init()))

	if rendered := view.View(); !strings.Contains(rendered, "Nobody is waiting to be screened") {
		t.Errorf("empty screener rendered:\n%s", rendered)
	}
}

// --- Screening in and out ---

func TestScreenerScreensSenderIn(t *testing.T) {
	view, state := loadedScreener(t)

	done, ok := runCmd(view.HandleContentKey(keyPress("y"))).(screenerDecisionDoneMsg)
	if !ok {
		t.Fatalf("y returned %T, want screenerDecisionDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("screening in failed: %v", done.err)
	}
	view.Update(done)

	if len(view.pending.rows) != 1 || view.pending.rows[0].id != 92 {
		t.Errorf("screened sender should leave the queue: %+v", view.pending.rows)
	}
	if view.pendingCount != 1 {
		t.Errorf("pendingCount = %d, want 1", view.pendingCount)
	}
	if view.notice != "Jane Doe screened in" {
		t.Errorf("notice = %q", view.notice)
	}

	requests := state.snapshot()
	last := requests[len(requests)-1]
	if last.method != http.MethodPatch || last.path != "/clearances/91.json" || !strings.Contains(last.body, `"status":"approved"`) {
		t.Errorf("screen in request = %+v", last)
	}
}

func TestScreenerScreensSenderOut(t *testing.T) {
	view, state := loadedScreener(t)
	view.HandleContentKey(keyPress("down"))

	done := runCmd(view.HandleContentKey(keyPress("n"))).(screenerDecisionDoneMsg)
	if done.status != hey.ClearanceDenied || done.name != "Bob Smith" {
		t.Fatalf("decision = %+v", done)
	}
	view.Update(done)
	if view.notice != "Bob Smith screened out" {
		t.Errorf("notice = %q", view.notice)
	}

	requests := state.snapshot()
	last := requests[len(requests)-1]
	if last.path != "/clearances/92.json" || !strings.Contains(last.body, `"status":"denied"`) {
		t.Errorf("screen out request = %+v", last)
	}
}

func TestScreenerReportsFailedDecision(t *testing.T) {
	view, state := loadedScreener(t)
	state.failScreen = true

	done := runCmd(view.HandleContentKey(keyPress("y"))).(screenerDecisionDoneMsg)
	if done.err == nil {
		t.Fatal("a refused decision should carry the error")
	}
	view.Update(done)

	if len(view.pending.rows) != 2 {
		t.Error("a refused decision should leave the queue alone")
	}
	if !strings.HasPrefix(view.notice, "Could not screen Jane Doe:") {
		t.Errorf("notice = %q", view.notice)
	}
	if view.notice != "Could not screen Jane Doe: "+apierr.AsError(apierr.FromSDK(done.err)).Message {
		t.Errorf("notice = %q, want the words the CLI prints for the same failure", view.notice)
	}
}

func TestScreenerNoticesSayWhatTheCLISays(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "a refusal carries the hint the server sent with it",
			err:  &hey.Error{Code: hey.CodeValidation, Message: "that sender is no longer waiting", Hint: "Reload The Screener", HTTPStatus: 422},
			want: "Could not screen Jane Doe: that sender is no longer waiting — Reload The Screener",
		},
		{
			name: "a rate limit reads as a wait rather than as a status",
			err:  &hey.Error{Code: hey.CodeRateLimit, Message: "429 Too Many Requests", Hint: "30 seconds", HTTPStatus: 429},
			want: "Could not screen Jane Doe: rate limited — retry after 30 seconds",
		},
		{
			name: "an expired token does not send somebody in a full-screen app to a shell prompt",
			err:  &hey.Error{Code: hey.CodeAuth, Message: "not authenticated", HTTPStatus: 401},
			want: "Could not screen Jane Doe: not authenticated",
		},
		{
			name: "an error that never went near the API keeps its own text",
			err:  errors.New("no route to host"),
			want: "Could not screen Jane Doe: no route to host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorNotice("Could not screen Jane Doe", tt.err); got != tt.want {
				t.Errorf("notice = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Screener history ---

func TestScreenerTabsOverToPreviousDecisions(t *testing.T) {
	view, state := loadedScreener(t)

	loaded, ok := runCmd(view.HandleContentKey(keyPress("tab"))).(screenerScreenedLoadedMsg)
	if !ok {
		t.Fatalf("tab returned %T, want screenerScreenedLoadedMsg", loaded)
	}
	view.Update(loaded)

	if view.tab != screenerHistoryTab {
		t.Fatal("tab should move to the history pane")
	}
	if len(view.history.rows) != 2 {
		t.Fatalf("history rows = %+v", view.history.rows)
	}
	if view.history.rows[0].trailing != "screened in · Aug 18, 2026" || view.history.rows[1].trailing != "screened out · Aug 17, 2026" {
		t.Errorf("history decisions = %q, %q", view.history.rows[0].trailing, view.history.rows[1].trailing)
	}

	rendered := plainText(view.View())
	for _, want := range []string{"All the contacts you've screened in or out.", "Alice Jones", "Aug 18, 2026"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("history view is missing %q:\n%s", want, rendered)
		}
	}

	if cmd := view.HandleContentKey(keyPress("tab")); cmd != nil {
		t.Error("tabbing back to a loaded pane should not refetch")
	}
	if view.tab != screenerPendingTab {
		t.Error("tab should move back to the pending pane")
	}

	requests := state.snapshot()
	if requests[len(requests)-1].path != "/my/clearances.json" {
		t.Errorf("history request = %+v", requests)
	}
}

func TestScreenerHistoryDoesNotScreen(t *testing.T) {
	view, _ := loadedScreener(t)
	view.Update(runCmd(view.HandleContentKey(keyPress("tab"))))

	if cmd := view.HandleContentKey(keyPress("y")); cmd != nil {
		t.Error("the history pane should not decide anything")
	}
}

// --- Clearing ---

func TestScreenerClearsTheQueueAfterConfirmation(t *testing.T) {
	view, state := loadedScreener(t)

	view.HandleContentKey(keyPress("X"))
	if !view.confirmingClear {
		t.Fatal("X should ask before clearing")
	}
	confirmation := plainText(view.View())
	for _, want := range []string{
		"Not sure what to do with these?",
		"you can clear them all",
		"All emails currently in the Screener will go to the trash.",
		"You'll be asked to screen each sender again if they email you in the future.",
	} {
		if !strings.Contains(confirmation, want) {
			t.Errorf("confirmation is missing %q:\n%s", want, confirmation)
		}
	}

	cleared, ok := runCmd(view.HandleContentKey(keyPress("y"))).(screenerClearedMsg)
	if !ok || cleared.err != nil {
		t.Fatalf("clearing returned %#v", cleared)
	}
	view.Update(cleared)

	if len(view.pending.rows) != 0 || view.pendingCount != 0 {
		t.Errorf("clearing should empty the queue: rows:%d count:%d", len(view.pending.rows), view.pendingCount)
	}
	if !strings.HasPrefix(view.notice, "The Screener is clearing.") {
		t.Errorf("notice = %q", view.notice)
	}

	requests := state.snapshot()
	last := requests[len(requests)-1]
	if last.method != http.MethodPost || last.path != "/clearances/punt.json" {
		t.Errorf("punt request = %+v", last)
	}
}

func TestScreenerClearCanBeCanceled(t *testing.T) {
	view, state := loadedScreener(t)
	before := len(state.snapshot())

	view.HandleContentKey(keyPress("X"))
	if cmd := view.HandleContentKey(keyPress("n")); cmd != nil {
		t.Error("canceling should not clear anything")
	}
	if view.confirmingClear {
		t.Error("n should close the confirmation")
	}
	if len(state.snapshot()) != before {
		t.Error("canceling should not make a request")
	}
	if len(view.pending.rows) != 2 {
		t.Error("canceling should leave the queue alone")
	}
}

func TestScreenerOffersClearOnBothPanes(t *testing.T) {
	view, _ := loadedScreener(t)
	for _, pane := range []string{"pending", "history"} {
		if !hasBinding(view.HelpBindings(), "X") {
			t.Errorf("%s pane does not offer the clear shortcut: %v", pane, view.HelpBindings())
		}
		view.Update(runCmd(view.HandleContentKey(keyPress("tab"))))
	}
}

// --- Growing the queue ---

func TestScreenerGrowsTheQueueAsTheReaderScrolls(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "cursor-2" {
			_, _ = w.Write([]byte(`{"pending_clearances_count":3,"clearances":[
				{"id":93,"status":"pending","petitioner":{"id":13,"name":"Ann Wu"}}]}`))
			return
		}
		w.Header().Set("Link", `<http://`+r.Host+`/clearances.json?page=cursor-2>; rel="next"`)
		_, _ = w.Write([]byte(`{"pending_clearances_count":3,"clearances":[
			{"id":91,"status":"pending","petitioner":{"id":11,"name":"Jane Doe"}},
			{"id":92,"status":"pending","petitioner":{"id":12,"name":"Bob Smith"}}]}`))
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "t"}, hey.WithMaxRetries(0))
	view := newScreenerView(vc)

	loaded := runCmd(view.Init()).(screenerPendingLoadedMsg)
	more, _ := view.Update(loaded)
	if more == nil {
		t.Fatal("a queue the reader can see the end of should read on")
	}

	appended := runCmd(more).(screenerRowsAppendedMsg)
	if cmd, _ := view.Update(appended); cmd != nil {
		t.Error("a page with no next cursor should end the queue")
	}
	if len(view.pending.rows) != 3 || view.pending.rows[2].name != "Ann Wu" {
		t.Errorf("grown queue = %+v", view.pending.rows)
	}
	if view.pending.paging.hasMore() {
		t.Errorf("next page = %q, want none", view.pending.paging.nextPage)
	}
	if strings.Join(pages, ",") != ",cursor-2" {
		t.Errorf("page requests = %v", pages)
	}
}

// A sender the queue shows already is not shown twice when the page below turns them up
// again, which is what happens when someone else's decision shifts the ordering between
// two reads.
func TestScreenerGrowingSkipsSendersAlreadyShown(t *testing.T) {
	view, _ := loadedScreener(t)
	view.pending.paging.nextPage = "cursor-2"

	view.Update(screenerRowsAppendedMsg{
		requestID: view.moreRequestID,
		tab:       screenerPendingTab,
		count:     3,
		rows: []screenerRow{
			{id: 92, name: "Bob Smith"},
			{id: 93, name: "Ann Wu"},
		},
	})

	if len(view.pending.rows) != 3 || view.pending.rows[2].id != 93 {
		t.Errorf("grown queue = %+v", view.pending.rows)
	}
}

// Screening the senders off the bottom of a queue the reader has scrolled down must not
// leave the window past the last row. It used to: the pane drew nothing at all, which
// reads as an empty queue rather than as one with senders still in it.
func TestScreenerKeepsDrawingAfterScreeningOffTheBottom(t *testing.T) {
	view, _ := screenerTestView(t)
	view.vc.height = 12
	rows := make([]screenerRow, 0, 20)
	for index := range 20 {
		rows = append(rows, screenerRow{
			id:      int64(300 + index),
			name:    fmt.Sprintf("Sender %02d", index),
			email:   fmt.Sprintf("sender%02d@example.com", index),
			subject: "Wants to hear back",
		})
	}
	view.pending.setRows(rows, "")
	view.pendingCount = len(rows)
	for range len(rows) {
		view.pending.moveDown(view.visibleRows())
	}
	if view.pending.scroll == 0 {
		t.Fatal("the reader is still on the first screenful of the queue")
	}

	for range 5 {
		row := view.pending.selected()
		if row == nil {
			t.Fatalf("the cursor left the queue with %d senders in it", len(view.pending.rows))
		}
		view.Update(screenerDecisionDoneMsg{clearanceID: row.id, name: row.name, status: hey.ClearanceApproved})

		rendered := plainText(view.View())
		if !strings.Contains(rendered, view.pending.rows[view.pending.cursor].name) {
			t.Fatalf("the pane drew nothing where the queue is: scroll %d, cursor %d, %d rows",
				view.pending.scroll, view.pending.cursor, len(view.pending.rows))
		}
	}
	if len(view.pending.rows) != 15 {
		t.Errorf("queue holds %d senders, want 15", len(view.pending.rows))
	}
}

// --- Leaving ---

func TestScreenerEscapeAsksToClose(t *testing.T) {
	view, _ := loadedScreener(t)

	if _, ok := runCmd(view.HandleContentKey(keyPress("esc"))).(screenerClosedMsg); !ok {
		t.Error("esc should close The Screener")
	}
	if _, ok := runCmd(view.HandleContentKey(keyPress("q"))).(screenerClosedMsg); !ok {
		t.Error("q should close The Screener")
	}
}

func TestScreenerStaysOpenWhileADecisionIsInFlight(t *testing.T) {
	view, _ := loadedScreener(t)
	view.HandleContentKey(keyPress("y"))

	if cmd := view.HandleContentKey(keyPress("esc")); cmd != nil {
		t.Error("The Screener should stay open until the decision lands")
	}
}

// plainText strips styling and line breaks so a wording assertion reads the sentence
// rather than the wrapping.
func plainText(rendered string) string {
	return strings.Join(strings.Fields(ansi.Strip(rendered)), " ")
}

func hasBinding(bindings []helpBinding, key string) bool {
	for _, binding := range bindings {
		if binding.key == key {
			return true
		}
	}
	return false
}

// --- Mail integration ---

func TestMailShowsScreenerInvitation(t *testing.T) {
	view := mailWithPostings()

	if strings.Contains(view.View(), "first-time") {
		t.Error("an empty Screener should not be advertised")
	}

	view.Update(screenerCountLoadedMsg{count: 1})
	if !strings.Contains(plainText(view.View()), "Screen 1 first-time sender · ctrl+s") {
		t.Errorf("one waiting sender rendered:\n%s", view.View())
	}

	view.Update(screenerCountLoadedMsg{count: 3})
	if !strings.Contains(plainText(view.View()), "Screen 3 first-time senders · ctrl+s") {
		t.Errorf("three waiting senders rendered:\n%s", view.View())
	}

	view.Update(screenerCountLoadedMsg{count: 0, err: context.Canceled})
	if !strings.Contains(plainText(view.View()), "Screen 3 first-time senders") {
		t.Error("a failed count should leave the last known one alone")
	}
}

func TestModelOpensAndClosesScreener(t *testing.T) {
	m := modelWithBoxes()

	updated, cmd := m.Update(keyPress("ctrl+s"))
	m = updated.(model)
	if m.activeView != m.screenerView {
		t.Fatal("ctrl+s should open The Screener over the mail list")
	}
	if cmd == nil {
		t.Error("opening The Screener should load the queue")
	}
	if !hasBinding(m.help.bindings, "X") {
		t.Errorf("help should offer the clear shortcut: %v", m.help.bindings)
	}

	updated, _ = m.Update(screenerClosedMsg{})
	m = updated.(model)
	if m.activeView != m.mailView {
		t.Error("closing The Screener should put the mail list back")
	}
	if m.section != sectionMail {
		t.Error("The Screener should not change the section")
	}
}

func TestModelKeepsScreenerClosedInsideAThread(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.inThread = true

	updated, _ := m.Update(keyPress("ctrl+s"))
	m = updated.(model)
	if m.activeView == m.screenerView {
		t.Error("ctrl+s should do nothing while a thread is open")
	}
}

func TestModelRoutesScreenerKeysToTheScreener(t *testing.T) {
	m := modelWithBoxes()
	updated, _ := m.Update(keyPress("ctrl+s"))
	m = updated.(model)
	m.screenerView.Update(screenerPendingLoadedMsg{
		requestID: m.screenerView.requestID,
		count:     1,
		rows:      []screenerRow{{id: 91, name: "Jane Doe", subject: "Quarterly planning"}},
	})
	m.loading = false

	updated, _ = m.Update(keyPress("shift+tab"))
	m = updated.(model)
	if m.focus != rowContent {
		t.Error("The Screener should keep tab to itself instead of moving the focus row")
	}

	updated, _ = m.Update(keyPress("q"))
	m = updated.(model)
	if m.activeView != m.screenerView {
		t.Error("q reaches The Screener, which closes itself through a message")
	}
}

var _ tea.Model = model{}

func TestScreenerShiftFTogglesEmphaticNo(t *testing.T) {
	view, _ := loadedScreener(t)

	view.HandleContentKey(keyPress("F"))
	if !strings.Contains(plainText(view.screenQuestion()), "Fuck no!") {
		t.Errorf("Shift+F should swap the No label: %q", view.screenQuestion())
	}
	if !strings.Contains(view.screenQuestion(), "\x1b[1;4;34;4mn\x1b[m") {
		t.Errorf("the n hotkey should stay underlined in the swapped label: %q", view.screenQuestion())
	}

	view.HandleContentKey(keyPress("F"))
	if strings.Contains(plainText(view.screenQuestion()), "Fuck no!") {
		t.Errorf("Shift+F should toggle back to No: %q", view.screenQuestion())
	}
}
