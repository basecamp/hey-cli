package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	habitvalues "github.com/basecamp/hey-cli/internal/habit"
	"github.com/basecamp/hey-cli/internal/models"
)

func TestHabitFormValidationAndKeyRouting(t *testing.T) {
	view := newCalendarView(testVC())
	view.calendars = []models.Calendar{{ID: 10, Name: "Rob Zolkos", Personal: true}}
	view.Resize(80, 30)
	if cmd := view.HandleContentKey(keyPress("a")); cmd == nil || view.habitForm == nil || !view.CapturingInput() {
		t.Fatal("a should open and focus the habit form")
	}
	view.HandleContentKey(keyPress("ctrl+s"))
	if view.habitForm.status != "Name is required" || view.habitForm.saving {
		t.Errorf("empty save status = %q, saving=%v", view.habitForm.status, view.habitForm.saving)
	}
	view.HandleContentKey(keyPress("R"))
	if got := view.habitForm.inputs[habitFieldName].Value(); got != "R" {
		t.Errorf("form key was not routed to name input: %q", got)
	}
	view.habitForm.inputs[habitFieldName].SetValue("Read before bed")
	view.habitForm.inputs[habitFieldIcon].SetValue("walking")
	view.HandleContentKey(keyPress("ctrl+s"))
	if !strings.Contains(view.habitForm.status, "icon must be one of") {
		t.Errorf("invalid icon status = %q", view.habitForm.status)
	}
	view.habitForm.inputs[habitFieldIcon].SetValue("read")
	view.habitForm.inputs[habitFieldColor].SetValue("orange")
	view.HandleContentKey(keyPress("ctrl+s"))
	if !strings.Contains(view.habitForm.status, "color must be one of") {
		t.Errorf("invalid color status = %q", view.habitForm.status)
	}
	view.habitForm.inputs[habitFieldColor].SetValue("blue")
	view.habitForm.inputs[habitFieldDays].SetValue("Monday, someday")
	view.HandleContentKey(keyPress("ctrl+s"))
	if !strings.Contains(view.habitForm.status, "invalid weekday") {
		t.Errorf("invalid days status = %q", view.habitForm.status)
	}
	view.HandleContentKey(keyPress("esc"))
	if view.habitForm != nil || view.CapturingInput() {
		t.Error("escape should close a form that is not saving")
	}
}

func TestHabitFormGuidanceListsAcceptedIconsAndColors(t *testing.T) {
	form := newHabitForm(habitFormCreate, models.Recording{}, testVC().styles)
	form.resize(50, 30)
	rendered := form.view()
	for _, value := range strings.Split(habitvalues.IconValues, ", ") {
		if !strings.Contains(rendered, value) {
			t.Errorf("form guidance is missing icon %q", value)
		}
	}
	for _, value := range strings.Split(habitvalues.ColorValues, ", ") {
		if !strings.Contains(rendered, value) {
			t.Errorf("form guidance is missing color %q", value)
		}
	}
}

func TestCalendarHabitCreateRequiresPersonalCalendarMetadata(t *testing.T) {
	view := newCalendarView(testVC())
	view.calendars = []models.Calendar{{ID: 10, Name: "Personal", Personal: false}}

	for _, binding := range view.HelpBindings() {
		if binding.key == "a" {
			t.Errorf("non-personal calendar offers create: %v", view.HelpBindings())
		}
	}
	if cmd := view.HandleContentKey(keyPress("a")); cmd != nil || view.habitForm != nil {
		t.Fatalf("non-personal create = cmd:%v form:%v", cmd, view.habitForm)
	}
	if view.notice != "Habits can only be created from the personal calendar" {
		t.Errorf("notice = %q", view.notice)
	}
}

func TestCalendarHabitSelectionAndEditPrefill(t *testing.T) {
	view := newCalendarView(testVC())
	view.habits = []models.Recording{
		{ID: 7, Title: "Read before bed", Icon: "read", Color: "blue", Days: []int32{1, 3, 5}},
		{ID: 8, Title: "Evening walk", Icon: "walk", Color: "green", Days: []int32{0, 6}},
		{ID: 7, Title: "Read before bed"},
	}
	if selected := view.selectedHabit(); selected == nil || selected.ID != 7 {
		t.Fatalf("initial selection = %+v", selected)
	}
	view.HandleContentKey(keyPress("]"))
	if selected := view.selectedHabit(); selected == nil || selected.ID != 8 {
		t.Fatalf("next selection = %+v", selected)
	}
	view.HandleContentKey(keyPress("e"))
	if view.habitForm == nil || view.habitForm.mode != habitFormEdit || view.habitForm.habitID != 8 {
		t.Fatal("e should edit the selected visible habit")
	}
	if got := view.habitForm.inputs[habitFieldName].Value(); got != "Evening walk" {
		t.Errorf("prefilled name = %q", got)
	}
	if got := view.habitForm.inputs[habitFieldDays].Value(); got != "0,6" {
		t.Errorf("prefilled days = %q", got)
	}
}

type recordedHabitRequests struct {
	mu       sync.Mutex
	requests []string
	bodies   []string
}

func (r *recordedHabitRequests) add(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req.Method+" "+req.URL.Path)
	r.bodies = append(r.bodies, strings.TrimSpace(string(body)))
}

func (r *recordedHabitRequests) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...), append([]string(nil), r.bodies...)
}

func calendarHabitsWithServer(t *testing.T) (*calendarView, *recordedHabitRequests) {
	t.Helper()
	recorded := &recordedHabitRequests{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		recorded.add(req)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/calendar/habits.json":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":9,"title":"Practice piano","type":"CalendarHabit","icon":"music","color":"green","days":[1,3,5]}`)
		case req.Method == http.MethodPatch && req.URL.Path == "/calendar/habits/7.json":
			_, _ = io.WriteString(w, `{"id":7,"title":"Read every evening","type":"CalendarHabit","icon":"read","color":"purple","days":[0,6]}`)
		case req.Method == http.MethodDelete && req.URL.Path == "/calendar/habits/7.json":
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodGet && req.URL.Path == "/calendars/10/recordings.json":
			_, _ = io.WriteString(w, `{"Calendar::Habit":[{"id":7,"title":"Read before bed","type":"CalendarHabit","icon":"read","color":"blue","days":[1,3,5]}]}`)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	view := newCalendarView(vc)
	view.calendars = []models.Calendar{{ID: 10, Name: "Rob Zolkos", Personal: true}}
	view.habits = []models.Recording{{ID: 7, Title: "Read before bed", Icon: "read", Color: "blue", Days: []int32{1, 3, 5}}}
	view.Resize(vc.width, vc.height)
	return view, recorded
}

func calendarHabitsWithFailingServer(t *testing.T, status int) *calendarView {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":"habit mutation failed"}`)
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc := testVC()
	vc.sdk = client
	view := newCalendarView(vc)
	view.calendars = []models.Calendar{{ID: 10, Name: "Rob Zolkos", Personal: true}}
	view.habits = []models.Recording{{ID: 7, Title: "Read before bed", Icon: "read", Color: "blue", Days: []int32{1, 3, 5}}}
	view.Resize(vc.width, vc.height)
	return view
}

func finishHabitMutation(t *testing.T, view *calendarView, cmd tea.Cmd) {
	t.Helper()
	msg := cmd()
	mutation, ok := msg.(habitMutationMsg)
	if !ok {
		t.Fatalf("mutation command returned %T", msg)
	}
	refresh, consumed := view.Update(mutation)
	if !consumed || refresh == nil {
		t.Fatalf("mutation update = consumed:%v refresh:%v", consumed, refresh)
	}
	view.Update(refresh())
	if view.loading || view.habitMutating {
		t.Errorf("mutation did not finish: loading=%v mutating=%v", view.loading, view.habitMutating)
	}
}

func TestCalendarHabitCreateMutationAndRefresh(t *testing.T) {
	view, recorded := calendarHabitsWithServer(t)
	view.HandleContentKey(keyPress("a"))
	view.habitForm.inputs[habitFieldName].SetValue("Practice piano")
	view.habitForm.inputs[habitFieldIcon].SetValue("music")
	view.habitForm.inputs[habitFieldColor].SetValue("green")
	view.habitForm.inputs[habitFieldDays].SetValue("Mon,Wed,Fri")
	cmd := view.HandleContentKey(keyPress("ctrl+s"))
	if cmd == nil || !view.habitMutating {
		t.Fatal("ctrl+s should start habit creation")
	}
	finishHabitMutation(t, view, cmd)
	if view.notice != "Habit created" || view.habitForm != nil {
		t.Errorf("create state = notice:%q form:%v", view.notice, view.habitForm)
	}
	requests, bodies := recorded.snapshot()
	if len(requests) < 2 || requests[0] != "POST /calendar/habits.json" || requests[1] != "GET /calendars/10/recordings.json" {
		t.Errorf("requests = %v", requests)
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["calendar_habit"]["name"] != "Practice piano" {
		t.Errorf("create payload = %v", payload)
	}
}

func TestCalendarHabitEditMutationAndRefresh(t *testing.T) {
	view, recorded := calendarHabitsWithServer(t)
	view.HandleContentKey(keyPress("e"))
	view.habitForm.inputs[habitFieldName].SetValue("Read every evening")
	view.habitForm.inputs[habitFieldColor].SetValue("purple")
	view.habitForm.inputs[habitFieldDays].SetValue("0,6")
	cmd := view.HandleContentKey(keyPress("ctrl+s"))
	finishHabitMutation(t, view, cmd)
	if view.notice != "Habit updated" {
		t.Errorf("notice = %q", view.notice)
	}
	requests, _ := recorded.snapshot()
	if len(requests) < 2 || requests[0] != "PATCH /calendar/habits/7.json" || requests[1] != "GET /calendars/10/recordings.json" {
		t.Errorf("requests = %v", requests)
	}
}

func TestCalendarHabitSaveFailuresUnlockAndPreserveFormValues(t *testing.T) {
	tests := []struct {
		name   string
		status int
		open   func(*calendarView)
	}{
		{name: "create 422", status: http.StatusUnprocessableEntity, open: func(view *calendarView) { view.HandleContentKey(keyPress("a")) }},
		{name: "edit 500", status: http.StatusInternalServerError, open: func(view *calendarView) { view.HandleContentKey(keyPress("e")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := calendarHabitsWithFailingServer(t, tt.status)
			tt.open(view)
			values := []string{"Practice piano", "piano", "gold", "Mon,Wed,Fri"}
			for i, value := range values {
				view.habitForm.inputs[i].SetValue(value)
			}

			cmd := view.HandleContentKey(keyPress("ctrl+s"))
			if cmd == nil {
				t.Fatal("save did not return a mutation command")
			}
			refresh, consumed := view.Update(cmd())
			if !consumed || refresh != nil {
				t.Fatalf("failed mutation update = consumed:%v refresh:%v", consumed, refresh)
			}
			if view.habitForm == nil || view.habitForm.saving || view.habitMutating || view.loading {
				t.Fatalf("failed save state = form:%v saving:%v mutating:%v loading:%v", view.habitForm, view.habitForm != nil && view.habitForm.saving, view.habitMutating, view.loading)
			}
			if !strings.Contains(view.habitForm.status, "Save failed") {
				t.Errorf("status = %q", view.habitForm.status)
			}
			for i, want := range values {
				if got := view.habitForm.inputs[i].Value(); got != want {
					t.Errorf("field %d after failure = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestCalendarHabitDeleteFailurePreservesConfirmationAndSelection(t *testing.T) {
	view := calendarHabitsWithFailingServer(t, http.StatusUnprocessableEntity)
	selected := view.selectedHabit()
	view.HandleContentKey(keyPress("x"))
	cmd := view.HandleContentKey(keyPress("x"))
	if cmd == nil {
		t.Fatal("confirmed delete did not return a mutation command")
	}
	refresh, consumed := view.Update(cmd())
	if !consumed || refresh != nil {
		t.Fatalf("failed delete update = consumed:%v refresh:%v", consumed, refresh)
	}
	if !view.habitDeleteConfirmed() || view.habitMutating || view.loading {
		t.Errorf("failed delete state = confirmed ID:%d mutating:%v loading:%v", view.confirmedHabitDeleteID, view.habitMutating, view.loading)
	}
	if current := view.selectedHabit(); current == nil || selected == nil || current.ID != selected.ID || view.habitIndex != 0 {
		t.Errorf("selection changed after delete failure: before=%+v after=%+v index=%d", selected, current, view.habitIndex)
	}
	if !strings.Contains(view.notice, "Delete failed") {
		t.Errorf("notice = %q", view.notice)
	}
}

func TestCalendarHabitDeleteConfirmationIsBoundToSelectedHabit(t *testing.T) {
	view, _ := calendarHabitsWithServer(t)
	view.HandleContentKey(keyPress("x"))
	if view.confirmedHabitDeleteID != 7 {
		t.Fatalf("confirmed habit ID = %d, want 7", view.confirmedHabitDeleteID)
	}

	view.Update(recordingsLoadedMsg{recordings: []models.Recording{{
		ID: 8, Title: "Evening walk", Type: "CalendarHabit", Icon: "walk", Color: "gold", Days: []int32{1, 3, 5},
	}}})
	if view.confirmedHabitDeleteID != 0 {
		t.Fatalf("recordings reload preserved confirmed habit ID %d", view.confirmedHabitDeleteID)
	}
	if cmd := view.HandleContentKey(keyPress("x")); cmd != nil || view.confirmedHabitDeleteID != 8 {
		t.Fatalf("first x for reloaded habit = cmd:%v confirmed ID:%d", cmd, view.confirmedHabitDeleteID)
	}
}

func TestCalendarHabitDeleteRequiresConfirmationAndRefresh(t *testing.T) {
	view, recorded := calendarHabitsWithServer(t)
	if cmd := view.HandleContentKey(keyPress("x")); cmd != nil || !view.habitDeleteConfirmed() || !strings.Contains(view.notice, "Press x again") {
		t.Fatalf("first x = cmd:%v confirmed ID:%d notice:%q", cmd, view.confirmedHabitDeleteID, view.notice)
	}
	cmd := view.HandleContentKey(keyPress("x"))
	if cmd == nil || !view.habitMutating {
		t.Fatal("second x should start deletion")
	}
	finishHabitMutation(t, view, cmd)
	if view.notice != "Habit deleted" || view.confirmedHabitDeleteID != 0 {
		t.Errorf("delete state = notice:%q confirmed ID:%d", view.notice, view.confirmedHabitDeleteID)
	}
	requests, _ := recorded.snapshot()
	if len(requests) < 2 || requests[0] != "DELETE /calendar/habits/7.json" || requests[1] != "GET /calendars/10/recordings.json" {
		t.Errorf("requests = %v", requests)
	}
}
