package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// eventFormCalendars is what the form is handed: the calendars an event can be filed on,
// already sifted by the view. The personal one and a subscription are not among them —
// see TestEventFormIsOnlyOfferedCalendarsHEYWillTake.
func eventFormCalendars() []Calendar {
	return []Calendar{
		{ID: 240334, Name: "Errands", Color: "blue"},
		{ID: 486532, Name: "Design Team", Color: "teal"},
	}
}

// HEY looks a submitted calendar_id up in `accessible_calendars.internal`, and the personal
// calendar is not in it: `Identity#calendars` has to add its id by hand. Offering it as
// somewhere to file an event is what made every create answer 404.
func TestEventFormIsOnlyOfferedCalendarsHEYWillTake(t *testing.T) {
	v := dayWithEvents(t)
	v.calendars = []Calendar{
		{ID: 240332, Personal: true},
		{ID: 240334, Name: "Errands", Color: "blue"},
		{ID: 700001, Name: "Croatian holidays", External: true},
	}

	fileable := v.fileableCalendars()
	if len(fileable) != 1 || fileable[0].ID != 240334 {
		t.Fatalf("fileable = %+v, want the one internal calendar the reader owns", fileable)
	}

	v.HandleContentKey(keyPress("a"))
	if v.eventForm == nil {
		t.Fatal("a did not open the form")
	}
	if got := v.eventForm.values().CalendarID; got != 240334 {
		t.Errorf("the form would file on %d, want 240334", got)
	}

	// With nowhere to file one, the form does not open on a promise it cannot keep.
	v.eventForm = nil
	v.calendars = []Calendar{{ID: 240332, Personal: true}}
	if cmd := v.HandleContentKey(keyPress("a")); cmd == nil {
		t.Error("a should say why it cannot add an event")
	}
	if v.eventForm != nil {
		t.Error("the form opened with nowhere to file the event")
	}
}

// A new event opens on the day the reader is looking at, at the next whole hour, for an
// hour — the same guess the web form makes rather than an empty pair of fields.
func TestNewEventFormOpensOnTheDayInView(t *testing.T) {
	on := time.Date(2026, 8, 20, 9, 41, 0, 0, time.Local)
	form := newEventForm(eventFormCreate, Recording{}, on, eventFormCalendars(), newStyles())

	values := form.values()
	if values.StartsAt != "2026-08-20" || values.EndsAt != "2026-08-20" {
		t.Errorf("dates = %q → %q, want the day in view", values.StartsAt, values.EndsAt)
	}
	if values.StartTime != "10:00" || values.EndTime != "11:00" {
		t.Errorf("times = %q → %q, want the next whole hour for an hour", values.StartTime, values.EndTime)
	}
	if values.AllDay {
		t.Error("a new event should not be all day")
	}
	if values.CalendarID != 240334 {
		t.Errorf("calendar = %d, want the first offered", values.CalendarID)
	}
	if values.TimeZone != time.Local.String() {
		t.Errorf("time zone = %q, want the reader's own", values.TimeZone)
	}
	if len(values.Reminders) != 0 {
		t.Errorf("reminders = %v, want none until asked for", values.Reminders)
	}
}

// Editing prefills from the event, and says which calendar it is on without offering to
// move it: HEY takes a calendar_id on create and not on update.
func TestEditEventFormPrefillsFromTheEvent(t *testing.T) {
	event := Recording{
		ID: 99, Title: "Stanko & Kevin", CalendarColor: "teal", Type: "Calendar::Event",
		StartsAt: atLocal("2026-08-20T14:00:00"), EndsAt: atLocal("2026-08-20T15:30:00"),
	}
	form := newEventForm(eventFormEdit, event, time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local),
		eventFormCalendars(), newStyles())

	values := form.values()
	if values.Title != "Stanko & Kevin" {
		t.Errorf("title = %q", values.Title)
	}
	if values.StartTime != "14:00" || values.EndTime != "15:30" {
		t.Errorf("times = %q → %q, want the event's own", values.StartTime, values.EndTime)
	}
	if values.CalendarID != 486532 {
		t.Errorf("calendar = %d, want the teal one the event is on", values.CalendarID)
	}
	if got := stripANSI(form.calendarField()); !strings.Contains(got, "cannot be moved here") {
		t.Errorf("calendar field = %q, want it to say an edit cannot move the event", got)
	}
	if form.formTitle() != "Edit event" {
		t.Errorf("title = %q", form.formTitle())
	}
}

// An all-day event has no times, so tab steps over the two fields that would say nothing.
func TestAllDayEventFormSkipsTheTimeFields(t *testing.T) {
	form := newEventForm(eventFormCreate, Recording{}, time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local),
		eventFormCalendars(), newStyles())

	form.focus = eventFieldAllDay
	form.handleKey(keyPress(" "))
	if !form.values().AllDay {
		t.Fatal("space did not turn all day on")
	}

	form.focus = eventFieldStartsDate
	form.step(1)
	if form.focus != eventFieldEndsDate {
		t.Errorf("tab landed on %d, want it to step over the start time", form.focus)
	}

	// And the times are gone from the form altogether.
	if got := stripANSI(form.view()); strings.Contains(got, "at ") {
		t.Errorf("an all-day form still shows a time field: %q", got)
	}
}

func TestEventFormRefusesWhatTheServerWould(t *testing.T) {
	on := time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local)

	for _, tt := range []struct {
		name  string
		spoil func(*eventForm)
		want  string
	}{
		{"no name", func(f *eventForm) { f.title.SetValue("  ") }, "Name is required"},
		{"bad start date", func(f *eventForm) { f.startsDate.SetValue("20th Aug") }, "Start date must be YYYY-MM-DD"},
		{"bad end date", func(f *eventForm) { f.endsDate.SetValue("tomorrow") }, "End date must be YYYY-MM-DD"},
		{"bad start time", func(f *eventForm) { f.startsTime.SetValue("9am") }, "Start time must be HH:MM"},
		{"bad end time", func(f *eventForm) { f.endsTime.SetValue("half ten") }, "End time must be HH:MM"},
		{"ends before it starts", func(f *eventForm) {
			f.startsTime.SetValue("15:00")
			f.endsTime.SetValue("14:00")
		}, "The end is before the start"},
		{"ends a day before it starts", func(f *eventForm) {
			f.allDay = true
			f.endsDate.SetValue("2026-08-19")
		}, "The end is before the start"},
	} {
		form := newEventForm(eventFormCreate, Recording{}, on, eventFormCalendars(), newStyles())
		form.title.SetValue("Design review")
		tt.spoil(form)

		if got := form.validate(); got != tt.want {
			t.Errorf("%s: validate = %q, want %q", tt.name, got, tt.want)
		}
	}

	// And a form that is filled in properly says nothing.
	form := newEventForm(eventFormCreate, Recording{}, on, eventFormCalendars(), newStyles())
	form.title.SetValue("Design review")
	if got := form.validate(); got != "" {
		t.Errorf("a good form was refused: %q", got)
	}
}

// The reminders are a shortlist of what the web form offers, and stepping the picker is
// what puts one on the event.
func TestEventFormNotifyPicker(t *testing.T) {
	form := newEventForm(eventFormCreate, Recording{}, time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local),
		eventFormCalendars(), newStyles())

	if got := form.notifyField(); got != "no reminder" {
		t.Errorf("a new event starts on %q", got)
	}
	form.focus = eventFieldNotify
	for range 3 {
		form.handleKey(keyPress("right"))
	}
	if got := form.notifyField(); got != "30 minutes before" {
		t.Errorf("three steps = %q, want the web form's own default", got)
	}
	if got := form.values().Reminders; len(got) != 1 || got[0] != 30*time.Minute {
		t.Errorf("reminders = %v", got)
	}
}

// --- Writing to the server ---

type recordedEventRequests struct {
	mu       sync.Mutex
	requests []string
	bodies   []string
}

func (r *recordedEventRequests) add(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req.Method+" "+req.URL.Path)
	r.bodies = append(r.bodies, string(body))
}

func (r *recordedEventRequests) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...), append([]string(nil), r.bodies...)
}

func calendarWithEventServer(t *testing.T) (*calendarView, *recordedEventRequests) {
	t.Helper()

	recorded := &recordedEventRequests{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		recorded.add(req)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/calendar/events.json":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":501,"type":"Calendar::Event","title":"Design review"}`)
		case req.Method == http.MethodPatch && strings.HasPrefix(req.URL.Path, "/calendar/events/"):
			_, _ = io.WriteString(w, `{"id":1,"type":"Calendar::Event","title":"Renamed"}`)
		case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/calendar/events/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = io.WriteString(w, `{"starts_at":"2026-08-20T00:00:00Z","ends_at":"2026-08-20T23:59:59Z","kind":"day","recordings":{}}`)
		}
	}))
	t.Cleanup(server.Close)

	v := dayWithEvents(t)
	v.vc.ctx = context.Background()
	v.vc.sdk = hey.NewClient(
		&hey.Config{BaseURL: server.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	v.calendars = eventFormCalendars()
	return v, recorded
}

// Creating an event posts the form to HEY as the form-encoded body its endpoint parses.
func TestCreateEventPostsTheForm(t *testing.T) {
	v, recorded := calendarWithEventServer(t)

	// The form opens focused on the calendar picker, which takes no cursor, so opening it
	// answers no command.
	v.HandleContentKey(keyPress("a"))
	if v.eventForm == nil {
		t.Fatal("a did not open the event form")
	}
	v.eventForm.title.SetValue("Design review")

	cmd := v.HandleContentKey(keyPress("ctrl+s"))
	if cmd == nil {
		t.Fatal("ctrl+s did not save")
	}
	msg, ok := cmd().(calendarMutationMsg)
	if !ok {
		t.Fatalf("save answered %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("save failed: %v", msg.err)
	}
	if msg.action != "Event created" {
		t.Errorf("action = %q", msg.action)
	}

	requests, bodies := recorded.snapshot()
	if len(requests) == 0 || requests[0] != "POST /calendar/events.json" {
		t.Fatalf("requests = %v", requests)
	}
	form, err := url.ParseQuery(bodies[0])
	if err != nil {
		t.Fatalf("body is not form-encoded: %v", err)
	}
	for field, want := range map[string]string{
		"calendar_event[summary]":                  "Design review",
		"calendar_event[starts_at]":                "2026-08-20",
		"calendar_event[all_day]":                  "0",
		"calendar_event[starts_at_time]":           "09:00:00",
		"calendar_event[starts_at_time_zone_name]": time.Local.String(),
	} {
		if got := form.Get(field); got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
}

// Editing patches the event it was opened on.
func TestEditEventPatchesIt(t *testing.T) {
	v, recorded := calendarWithEventServer(t)
	v.selectedEvent = 1

	v.HandleContentKey(keyPress("e"))
	if v.eventForm == nil || v.eventForm.eventID != 1 {
		t.Fatalf("form = %+v, want it opened on event 1", v.eventForm)
	}
	v.eventForm.title.SetValue("Renamed")

	cmd := v.HandleContentKey(keyPress("ctrl+s"))
	msg, ok := cmd().(calendarMutationMsg)
	if !ok || msg.err != nil {
		t.Fatalf("save = %T %v", cmd(), msg.err)
	}
	if msg.action != "Event updated" {
		t.Errorf("action = %q", msg.action)
	}

	requests, bodies := recorded.snapshot()
	if len(requests) == 0 || requests[0] != "PATCH /calendar/events/1.json" {
		t.Fatalf("requests = %v", requests)
	}
	if form, _ := url.ParseQuery(bodies[0]); form.Get("calendar_event[summary]") != "Renamed" {
		t.Errorf("body = %q", bodies[0])
	}
}

// Deleting asks twice, because an event off a shared calendar is gone for everybody on it.
func TestDeleteEventAsksTwice(t *testing.T) {
	v, recorded := calendarWithEventServer(t)
	v.selectedEvent = 1

	if cmd := v.HandleContentKey(keyPress("x")); cmd != nil {
		t.Fatal("the first x deleted without asking")
	}
	if v.confirmDelete != 1 {
		t.Errorf("confirmDelete = %d, want the selected event", v.confirmDelete)
	}
	if requests, _ := recorded.snapshot(); len(requests) != 0 {
		t.Fatalf("the first x sent %v", requests)
	}

	cmd := v.HandleContentKey(keyPress("x"))
	if cmd == nil {
		t.Fatal("the second x did not delete")
	}
	msg, ok := cmd().(calendarMutationMsg)
	if !ok || msg.err != nil {
		t.Fatalf("delete = %T %v", cmd(), msg.err)
	}
	if msg.action != "Event deleted" {
		t.Errorf("action = %q", msg.action)
	}
	if requests, _ := recorded.snapshot(); len(requests) != 1 || requests[0] != "DELETE /calendar/events/1" {
		t.Errorf("requests = %v", requests)
	}
}

// Any other key is the reader changing their mind, so x has to be asked again.
func TestAnotherKeyForgetsTheDeleteAsk(t *testing.T) {
	v, _ := calendarWithEventServer(t)
	v.selectedEvent = 1

	v.HandleContentKey(keyPress("x"))
	v.HandleContentKey(keyPress("right"))
	if v.confirmDelete != 0 {
		t.Errorf("confirmDelete = %d, want it forgotten", v.confirmDelete)
	}
}
