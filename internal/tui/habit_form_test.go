package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	habitvalues "github.com/basecamp/hey-cli/internal/habit"
)

// openHabits opens the habits modal, which is where a habit is created, edited and
// deleted: the calendar's own keys stayed with the calendar.
func openHabits(t *testing.T, view *calendarView) {
	t.Helper()
	view.HandleContentKey(keyPress("b"))
	if view.habitPicker == nil {
		t.Fatal("b did not open the habits modal")
	}
}

// fillHabitForm sets what the form's pickers would be left on, which is the state the
// mutation reads — the keys that get there are covered by the picker test.
func fillHabitForm(form *habitForm, name, icon, color string, days ...int32) {
	form.name.SetValue(name)
	form.icon = indexOfIcon(icon)
	form.color = indexOfColor(color)
	form.days = [7]bool{}
	for _, day := range days {
		form.days[day] = true
	}
}

func TestHabitFormValidationAndKeyRouting(t *testing.T) {
	view := newCalendarView(testVC())
	view.calendars = []Calendar{{ID: 10, Name: "Rob Zolkos", Personal: true}}
	view.Resize(80, 30)
	openHabits(t, view)
	if cmd := view.HandleContentKey(keyPress("a")); cmd == nil || view.habitForm == nil || !view.CapturingInput() {
		t.Fatal("a should open and focus the habit form")
	}
	view.HandleContentKey(keyPress("ctrl+s"))
	if view.habitForm.status != "Name is required" || view.habitForm.saving {
		t.Errorf("empty save status = %q, saving=%v", view.habitForm.status, view.habitForm.saving)
	}
	view.HandleContentKey(keyPress("R"))
	if got := view.habitForm.name.Value(); got != "R" {
		t.Errorf("form key was not routed to name input: %q", got)
	}

	// A new habit is on every day, so clearing all seven is the only way to reach the
	// days error — an icon or a color cannot be wrong now that neither is typed.
	view.habitForm.name.SetValue("Read before bed")
	view.habitForm.days = [7]bool{}
	view.HandleContentKey(keyPress("ctrl+s"))
	if view.habitForm.status != "Pick at least one day" {
		t.Errorf("no-days status = %q", view.habitForm.status)
	}

	// Escape steps back to the habits modal the form was opened from, and again out of
	// the modal to the calendar.
	view.HandleContentKey(keyPress("esc"))
	if view.habitForm != nil || view.habitPicker == nil {
		t.Error("escape should close a form that is not saving and leave the habits modal")
	}
	view.HandleContentKey(keyPress("esc"))
	if view.habitPicker != nil || view.CapturingInput() {
		t.Error("escape should close the habits modal")
	}
}

func TestHabitFormPickersStepThroughHEYsOwnValues(t *testing.T) {
	form := newHabitForm(habitFormCreate, Recording{}, testVC().styles)
	form.resize(60, 30)

	// A new habit starts on HEY's defaults, on every day.
	name, icon, color, days := form.values()
	if name != "" || icon != habitvalues.DefaultIcon || color != habitvalues.DefaultColor {
		t.Errorf("new habit = name:%q icon:%q color:%q", name, icon, color)
	}
	if len(days) != 7 {
		t.Errorf("new habit days = %v, want every day", days)
	}

	// The icon picker walks HEY's list and comes round the other side.
	form.focus = habitFieldIcon
	form.choose(keyPress("left"))
	if _, icon, _, _ := form.values(); icon != habitvalues.Icons[len(habitvalues.Icons)-1].Name {
		t.Errorf("stepping back from the first icon = %q", icon)
	}
	form.choose(keyPress("right"))
	if _, icon, _, _ := form.values(); icon != habitvalues.DefaultIcon {
		t.Errorf("stepping forward again = %q", icon)
	}

	form.focus = habitFieldColor
	form.choose(keyPress("right"))
	if _, _, color, _ := form.values(); color != habitvalues.Colors[1] {
		t.Errorf("next color = %q, want %q", color, habitvalues.Colors[1])
	}

	// Days are toggled where they sit rather than stepped through, since a habit is on
	// any set of the seven.
	form.focus = habitFieldDays
	form.choose(keyPress("right"))
	form.choose(keyPress(" "))
	if _, _, _, days := form.values(); len(days) != 6 || days[0] != 0 || days[1] != 2 {
		t.Errorf("days after clearing Monday = %v", days)
	}

	// The chosen icon shows as its emoji, named, since HEY's own icon is an SVG a
	// terminal cannot draw.
	rendered := stripANSI(form.view())
	if !strings.Contains(rendered, habitvalues.EmojiFor(habitvalues.DefaultIcon)) ||
		!strings.Contains(rendered, habitvalues.DefaultIcon) {
		t.Errorf("icon field does not show the emoji and the name: %q", rendered)
	}
	if strings.Contains(rendered, habitvalues.IconValues) {
		t.Errorf("form still prints the whole list of accepted icons: %q", rendered)
	}
	for _, label := range habitDayNames {
		if !strings.Contains(rendered, label) {
			t.Errorf("days field is missing %q: %q", label, rendered)
		}
	}
}

func TestCalendarHabitCreateRequiresPersonalCalendarMetadata(t *testing.T) {
	view := newCalendarView(testVC())
	view.calendars = []Calendar{{ID: 10, Name: "Personal", Personal: false}}
	view.habits = []Recording{{ID: 7, Title: "Read before bed"}}
	openHabits(t, view)

	if cmd := view.HandleContentKey(keyPress("a")); cmd != nil || view.habitForm != nil {
		t.Fatalf("non-personal create = cmd:%v form:%v", cmd, view.habitForm)
	}
	if view.habitPicker.status != "Habits can only be created from the personal calendar" {
		t.Errorf("status = %q", view.habitPicker.status)
	}
}

func TestCalendarHabitSelectionAndEditPrefill(t *testing.T) {
	view := newCalendarView(testVC())
	view.habits = []Recording{
		{ID: 7, Title: "Read before bed", Icon: "read", Color: "blue", Days: []int32{1, 3, 5}},
		{ID: 8, Title: "Evening walk", Icon: "walk", Color: "green", Days: []int32{0, 6}},
		{ID: 7, Title: "Read before bed"},
	}
	openHabits(t, view)
	if selected := view.habitPicker.selected(); selected == nil || selected.ID != 7 {
		t.Fatalf("initial selection = %+v", selected)
	}
	view.HandleContentKey(keyPress("down"))
	if selected := view.habitPicker.selected(); selected == nil || selected.ID != 8 {
		t.Fatalf("next selection = %+v", selected)
	}
	view.HandleContentKey(keyPress("e"))
	if view.habitForm == nil || view.habitForm.mode != habitFormEdit || view.habitForm.habitID != 8 {
		t.Fatal("e should edit the selected visible habit")
	}
	name, icon, color, days := view.habitForm.values()
	if name != "Evening walk" || icon != "walk" || color != "green" {
		t.Errorf("prefilled = name:%q icon:%q color:%q", name, icon, color)
	}
	if len(days) != 2 || days[0] != 0 || days[1] != 6 {
		t.Errorf("prefilled days = %v, want Sunday and Saturday", days)
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
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/habits/7/completions.json"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":99,"type":"Calendar::Habit::Completion","parent_id":7}`)
		case req.Method == http.MethodDelete && strings.HasSuffix(req.URL.Path, "/habits/7/completions.json"):
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
	view.calendars = []Calendar{{ID: 10, Name: "Rob Zolkos", Personal: true}}
	view.habits = []Recording{{ID: 7, Title: "Read before bed", Icon: "read", Color: "blue", Days: []int32{1, 3, 5}}}
	view.Resize(vc.width, vc.height)
	// Every habit mutation starts from the habits modal, so these tests open it too.
	view.HandleContentKey(keyPress("b"))
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
	view.calendars = []Calendar{{ID: 10, Name: "Rob Zolkos", Personal: true}}
	view.habits = []Recording{{ID: 7, Title: "Read before bed", Icon: "read", Color: "blue", Days: []int32{1, 3, 5}}}
	view.Resize(vc.width, vc.height)
	view.HandleContentKey(keyPress("b"))
	return view
}

// finishHabitMutation settles a habit mutation and answers the toast it raised. What a
// mutation answers with is a batch — the toast, and the day read again behind it — so
// this walks the batch the way the runtime does, handing the view everything but the
// toast, which belongs to the model.
func finishHabitMutation(t *testing.T, view *calendarView, cmd tea.Cmd) string {
	t.Helper()
	msg := cmd()
	mutation, ok := msg.(habitMutationMsg)
	if !ok {
		t.Fatalf("mutation command returned %T", msg)
	}
	answer, consumed := view.Update(mutation)
	if !consumed || answer == nil {
		t.Fatalf("mutation update = consumed:%v answer:%v", consumed, answer)
	}
	toast := deliverToView(view, answer)
	if view.requests.loading || view.requests.kind != calendarRequestNone {
		t.Errorf("mutation did not finish: loading=%v kind=%v", view.requests.loading, view.requests.kind)
	}
	return toast
}

func TestCalendarHabitCreateMutationAndRefresh(t *testing.T) {
	view, recorded := calendarHabitsWithServer(t)
	view.HandleContentKey(keyPress("a"))
	fillHabitForm(view.habitForm, "Practice piano", "music", "green", 1, 3, 5)
	cmd := view.HandleContentKey(keyPress("ctrl+s"))
	if cmd == nil || view.requests.kind != calendarRequestHabitMutation {
		t.Fatal("ctrl+s should start habit creation")
	}
	if toast := finishHabitMutation(t, view, cmd); toast != "Habit created" || view.habitForm != nil {
		t.Errorf("create state = toast:%q form:%v", toast, view.habitForm)
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
	fillHabitForm(view.habitForm, "Read every evening", "read", "purple", 0, 6)
	cmd := view.HandleContentKey(keyPress("ctrl+s"))
	if toast := finishHabitMutation(t, view, cmd); toast != "Habit updated" {
		t.Errorf("toast = %q", toast)
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
			fillHabitForm(view.habitForm, "Practice piano", "piano", "gold", 1, 3, 5)

			cmd := view.HandleContentKey(keyPress("ctrl+s"))
			if cmd == nil {
				t.Fatal("save did not return a mutation command")
			}
			refresh, consumed := view.Update(cmd())
			if !consumed || refresh != nil {
				t.Fatalf("failed mutation update = consumed:%v refresh:%v", consumed, refresh)
			}
			if view.habitForm == nil || view.habitForm.saving || view.requests.loading || view.requests.kind != calendarRequestNone {
				t.Fatalf("failed save state = form:%v saving:%v kind:%v loading:%v", view.habitForm, view.habitForm != nil && view.habitForm.saving, view.requests.kind, view.requests.loading)
			}
			if !strings.Contains(view.habitForm.status, "Save failed") {
				t.Errorf("status = %q", view.habitForm.status)
			}
			name, icon, color, days := view.habitForm.values()
			if name != "Practice piano" || icon != "piano" || color != "gold" || len(days) != 3 {
				t.Errorf("form after failure = name:%q icon:%q color:%q days:%v", name, icon, color, days)
			}
		})
	}
}

func TestCalendarHabitEnterCompletesAndClearsForTheDayOnScreen(t *testing.T) {
	view, recorded := calendarHabitsWithServer(t)
	view.now = func() time.Time { return time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local) }

	cmd := view.HandleContentKey(keyPress("enter"))
	if cmd == nil || view.requests.kind != calendarRequestHabitMutation {
		t.Fatal("enter should complete the selected habit")
	}
	// Ticking a habit off reads the day again behind the modal, and a spinner for that
	// is a flash of nothing where the day used to be.
	if !view.requests.loading || view.Loading() {
		t.Errorf("completing a habit claimed the spinner: loading=%v spinner=%v", view.requests.loading, view.Loading())
	}
	if toast := finishHabitMutation(t, view, cmd); toast != "Habit done for today" {
		t.Errorf("toast = %q", toast)
	}
	requests, _ := recorded.snapshot()
	if requests[0] != "POST /calendar/days/2026-08-22/habits/7/completions.json" {
		t.Errorf("completion request = %q", requests[0])
	}

	// A habit already done for the day is cleared by the same key.
	view.habitPicker.setHabits([]Recording{{ID: 7, Title: "Read before bed", CompletedAt: "2026-08-22T00:00:00Z"}})
	cmd = view.HandleContentKey(keyPress("enter"))
	if cmd == nil {
		t.Fatal("enter should clear a habit that is already done")
	}
	if toast := finishHabitMutation(t, view, cmd); toast != "Habit cleared for today" {
		t.Errorf("toast = %q", toast)
	}
	requests, _ = recorded.snapshot()
	if got := requests[len(requests)-2]; got != "DELETE /calendar/days/2026-08-22/habits/7/completions.json" {
		t.Errorf("clearing request = %q", got)
	}
}

func TestCalendarHabitDeleteFailurePreservesConfirmationAndSelection(t *testing.T) {
	view := calendarHabitsWithFailingServer(t, http.StatusUnprocessableEntity)
	picker := view.habitPicker
	selected := picker.selected()
	view.HandleContentKey(keyPress("x"))
	cmd := view.HandleContentKey(keyPress("x"))
	if cmd == nil {
		t.Fatal("confirmed delete did not return a mutation command")
	}
	answer, consumed := view.Update(cmd())
	if !consumed || answer == nil {
		t.Fatalf("failed delete update = consumed:%v answer:%v", consumed, answer)
	}
	if toast := deliverToView(view, answer); !strings.Contains(toast, "Delete failed") {
		t.Errorf("toast = %q", toast)
	}
	if picker.confirmed != selected.ID || view.requests.loading || view.requests.kind != calendarRequestNone {
		t.Errorf("failed delete state = confirmed ID:%d kind:%v loading:%v", picker.confirmed, view.requests.kind, view.requests.loading)
	}
	if current := picker.selected(); current == nil || current.ID != selected.ID || picker.cursor != 0 {
		t.Errorf("selection changed after delete failure: before=%+v after=%+v cursor=%d", selected, current, picker.cursor)
	}
}

func TestCalendarHabitDeleteConfirmationIsBoundToSelectedHabit(t *testing.T) {
	view, _ := calendarHabitsWithServer(t)
	picker := view.habitPicker
	view.HandleContentKey(keyPress("x"))
	if picker.confirmed != 7 {
		t.Fatalf("confirmed habit ID = %d, want 7", picker.confirmed)
	}

	view.Update(recordingsLoadedMsg{recordings: []Recording{{
		ID: 8, Title: "Evening walk", Type: "CalendarHabit", Icon: "walk", Color: "gold", Days: []int32{1, 3, 5},
	}}})
	if picker.confirmed != 0 {
		t.Fatalf("recordings reload preserved confirmed habit ID %d", picker.confirmed)
	}
	if cmd := view.HandleContentKey(keyPress("x")); cmd != nil || picker.confirmed != 8 {
		t.Fatalf("first x for reloaded habit = cmd:%v confirmed ID:%d", cmd, picker.confirmed)
	}
}

func TestCalendarHabitDeleteRequiresConfirmationAndRefresh(t *testing.T) {
	view, recorded := calendarHabitsWithServer(t)
	picker := view.habitPicker
	if cmd := view.HandleContentKey(keyPress("x")); cmd != nil || picker.confirmed != 7 || !strings.Contains(picker.status, "Press x again") {
		t.Fatalf("first x = cmd:%v confirmed ID:%d status:%q", cmd, picker.confirmed, picker.status)
	}
	cmd := view.HandleContentKey(keyPress("x"))
	if cmd == nil || view.requests.kind != calendarRequestHabitMutation {
		t.Fatal("second x should start deletion")
	}
	if toast := finishHabitMutation(t, view, cmd); toast != "Habit deleted" || picker.confirmed != 0 {
		t.Errorf("delete state = toast:%q confirmed ID:%d", toast, picker.confirmed)
	}
	requests, _ := recorded.snapshot()
	if len(requests) < 2 || requests[0] != "DELETE /calendar/habits/7.json" || requests[1] != "GET /calendars/10/recordings.json" {
		t.Errorf("requests = %v", requests)
	}
}
