package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// calendarTodosWithServer is a calendar showing one open and one finished to-do, with
// the modal already open — every to-do write starts from there.
func calendarTodosWithServer(t *testing.T) (*calendarView, *recordedHabitRequests) {
	t.Helper()
	recorded := &recordedHabitRequests{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		recorded.add(req)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/calendar/todos.json":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":9,"title":"Renew passport","type":"Calendar::Todo"}`)
		case req.Method == http.MethodPatch && req.URL.Path == "/calendar/todos/7.json":
			_, _ = io.WriteString(w, `{"id":7,"title":"Clean the attic properly","type":"Calendar::Todo"}`)
		case req.Method == http.MethodPost && req.URL.Path == "/calendar/todos/7/completions.json":
			_, _ = io.WriteString(w, `{"id":7,"title":"Clean the attic","type":"Calendar::Todo"}`)
		case req.Method == http.MethodDelete && req.URL.Path == "/calendar/todos/8/completions.json":
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodDelete && req.URL.Path == "/calendar/todos/7.json":
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodGet && req.URL.Path == "/calendars/10/recordings.json":
			_, _ = io.WriteString(w, `{"Calendar::Todo":[{"id":7,"title":"Clean the attic","type":"Calendar::Todo"}]}`)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	view := newCalendarView(vc)
	view.calendars = []Calendar{{ID: 10, Name: "Rob Zolkos", Personal: true}}
	view.now = func() time.Time { return time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local) }
	view.todos = []Recording{
		{ID: 7, Title: "Clean the attic", Type: "Calendar::Todo"},
		{ID: 8, Title: "Send the invoice", Type: "Calendar::Todo", CompletedAt: "2026-08-21T08:00:00Z"},
	}
	view.Resize(vc.width, vc.height)
	view.HandleContentKey(keyPress("s"))
	if view.todoPicker == nil {
		t.Fatal("s did not open the to-dos modal")
	}
	return view, recorded
}

func TestTodosModalListsTheWeekAndWhatIsDone(t *testing.T) {
	view, _ := calendarTodosWithServer(t)

	rendered := stripANSI(view.View())
	if !strings.Contains(rendered, todosSectionLabel) {
		t.Errorf("the modal is not titled after the section it opens from: %q", rendered)
	}
	if !strings.Contains(rendered, "□ Clean the attic") || !strings.Contains(rendered, "■ Send the invoice") {
		t.Errorf("the modal does not mark what is done: %q", rendered)
	}
	if !strings.Contains(rendered, "╭") {
		t.Errorf("the to-dos modal drew no frame: %q", rendered)
	}

	view.HandleContentKey(keyPress("esc"))
	if view.todoPicker != nil || view.CapturingInput() {
		t.Error("esc did not close the to-dos modal")
	}
}

func TestTodosModalTicksOffAndClears(t *testing.T) {
	view, recorded := calendarTodosWithServer(t)

	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil || view.requests.kind != calendarRequestMutation {
		t.Fatal("enter should tick the selected to-do off")
	}
	if toast := finishCalendarMutation(t, view, cmd); toast != "To-do done" {
		t.Errorf("toast = %q", toast)
	}
	if requests, _ := recorded.snapshot(); requests[0] != "POST /calendar/todos/7/completions.json" {
		t.Errorf("completion request = %q", requests[0])
	}

	// A to-do already done is cleared by the same key.
	view.todoPicker.setTodos([]Recording{
		{ID: 8, Title: "Send the invoice", CompletedAt: "2026-08-21T08:00:00Z"},
	})
	cmd = view.HandleContentKey(keyPress("enter"))
	if toast := finishCalendarMutation(t, view, cmd); toast != "To-do cleared" {
		t.Errorf("toast = %q", toast)
	}
	requests, _ := recorded.snapshot()
	if got := requests[len(requests)-2]; got != "DELETE /calendar/todos/8/completions.json" {
		t.Errorf("clearing request = %q", got)
	}
}

func TestTodosModalAddsOnTheDayOnScreen(t *testing.T) {
	view, recorded := calendarTodosWithServer(t)

	if cmd := view.HandleContentKey(keyPress("a")); cmd == nil || view.todoPicker.mode != todoAdding {
		t.Fatal("a should open the input for a new to-do")
	}
	// While the input is open every key is the input's, so a is a letter.
	view.HandleContentKey(keyPress("a"))
	if got := view.todoPicker.input.Value(); got != "a" {
		t.Errorf("the key was not routed to the input: %q", got)
	}

	// An empty to-do is refused rather than sent.
	view.todoPicker.input.SetValue("  ")
	if cmd := view.HandleContentKey(keyPress("enter")); cmd != nil {
		t.Error("an unnamed to-do was sent")
	}
	if view.todoPicker.status == "" {
		t.Error("an unnamed to-do said nothing")
	}

	view.todoPicker.input.SetValue("Renew passport")
	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil || view.todoPicker.editing() {
		t.Fatal("enter should add the to-do and close the input")
	}
	if toast := finishCalendarMutation(t, view, cmd); toast != "To-do added" {
		t.Errorf("toast = %q", toast)
	}

	requests, bodies := recorded.snapshot()
	if requests[0] != "POST /calendar/todos.json" {
		t.Fatalf("requests = %v", requests)
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatal(err)
	}
	// A to-do is filed on the day the reader is looking at, as a bare date so the day
	// is theirs rather than UTC's.
	if payload["calendar_todo"]["title"] != "Renew passport" || payload["calendar_todo"]["starts_at"] != "2026-08-22" {
		t.Errorf("create payload = %v", payload)
	}
}

func TestTodosModalRenamesWithoutMovingTheDay(t *testing.T) {
	view, recorded := calendarTodosWithServer(t)

	if cmd := view.HandleContentKey(keyPress("e")); cmd == nil || view.todoPicker.mode != todoRenaming {
		t.Fatal("e should open the input on the selected to-do")
	}
	if got := view.todoPicker.input.Value(); got != "Clean the attic" {
		t.Errorf("the input was not filled with the to-do's title: %q", got)
	}

	view.todoPicker.input.SetValue("Clean the attic properly")
	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil || view.todoPicker.editing() {
		t.Fatal("enter should rename the to-do and close the input")
	}
	if toast := finishCalendarMutation(t, view, cmd); toast != "To-do renamed" {
		t.Errorf("toast = %q", toast)
	}

	requests, bodies := recorded.snapshot()
	if requests[0] != "PATCH /calendar/todos/7.json" {
		t.Fatalf("requests = %v", requests)
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatal(err)
	}
	// A rename carries the title and nothing else, so the day it is filed on stays.
	if payload["calendar_todo"]["title"] != "Clean the attic properly" {
		t.Errorf("rename payload = %v", payload)
	}
	if _, ok := payload["calendar_todo"]["starts_at"]; ok {
		t.Errorf("a rename moved the day: %v", payload)
	}
}

func TestTodosModalTreatsAnUneditedRenameAsNoRename(t *testing.T) {
	view, recorded := calendarTodosWithServer(t)

	view.HandleContentKey(keyPress("e"))
	if cmd := view.HandleContentKey(keyPress("enter")); cmd != nil {
		t.Error("an unedited rename was sent")
	}
	if view.todoPicker.editing() {
		t.Error("enter left the input open")
	}
	if requests, _ := recorded.snapshot(); len(requests) != 0 {
		t.Errorf("an unedited rename made requests: %v", requests)
	}
}

func TestTodosModalDeletesOnlyAfterConfirmation(t *testing.T) {
	view, recorded := calendarTodosWithServer(t)
	picker := view.todoPicker

	if cmd := view.HandleContentKey(keyPress("x")); cmd != nil || picker.confirmed != 7 {
		t.Fatalf("first x = cmd:%v confirmed:%d", cmd, picker.confirmed)
	}
	if !strings.Contains(picker.status, "Press x again") {
		t.Errorf("status = %q", picker.status)
	}
	// Moving off the to-do takes the question with it.
	view.HandleContentKey(keyPress("down"))
	if picker.confirmed != 0 {
		t.Error("the delete question survived the cursor moving")
	}
	view.HandleContentKey(keyPress("up"))

	view.HandleContentKey(keyPress("x"))
	cmd := view.HandleContentKey(keyPress("x"))
	if cmd == nil {
		t.Fatal("second x should delete the to-do")
	}
	if toast := finishCalendarMutation(t, view, cmd); toast != "To-do deleted" {
		t.Errorf("toast = %q", toast)
	}
	if requests, _ := recorded.snapshot(); requests[0] != "DELETE /calendar/todos/7.json" {
		t.Errorf("delete request = %q", requests[0])
	}
}
