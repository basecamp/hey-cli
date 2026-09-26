package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// These tests never set time.Local: every moment they build is in a zone named here, so
// what they assert about the account's zone holds whichever zone the machine running them
// keeps.

const indianapolis = "America/Indiana/Indianapolis"

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return zone
}

func newAccountZoneForm(mode eventFormMode, event Recording, on time.Time, accountZone string) *eventForm {
	return newEventForm(mode, event, on, eventFormCalendars(), 0, accountZone, newStyles())
}

// A new event is written in the HEY account's zone — the one HEY's web app and `hey event add`
// read a typed time in — shown by its name and sent with it, so the three agree on when the
// event is. The next whole hour is the next one on the account's clock.
func TestNewEventFormOpensOnTheAccountZone(t *testing.T) {
	// 09:41 in Madrid is 03:41 in Indianapolis.
	on := time.Date(2026, 10, 14, 9, 41, 0, 0, mustZone(t, "Europe/Madrid"))
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, indianapolis)

	if got := stripANSI(form.view()); strings.Count(got, indianapolis) != 2 {
		t.Errorf("the form shows %q, want both moments on the account's zone", got)
	}
	values := form.values()
	if values.StartsAt != "2026-10-14" || values.StartTime != "04:00" {
		t.Errorf("start = %q %q, want 2026-10-14 04:00 on the account's clock", values.StartsAt, values.StartTime)
	}
	if values.EndsAt != "2026-10-14" || values.EndTime != "05:00" {
		t.Errorf("end = %q %q, want 2026-10-14 05:00 on the account's clock", values.EndsAt, values.EndTime)
	}
	if values.StartTimeZone != indianapolis || values.EndTimeZone != indianapolis {
		t.Errorf("zones = %q → %q, want the account's named for both", values.StartTimeZone, values.EndTimeZone)
	}
	if got := form.validate(); got != "Name is required" {
		t.Errorf("validate = %q, want only the missing name", got)
	}
}

// The day is the one on screen, even where the account's clock has already moved on to the
// next: a reader looking at the 14th late in the evening gets an event on the 14th.
func TestNewEventFormKeepsTheDayInViewOnTheAccountsClock(t *testing.T) {
	// 17:30 in Los Angeles on the 14th is 02:30 in Madrid on the 15th.
	on := time.Date(2026, 10, 14, 17, 30, 0, 0, mustZone(t, "America/Los_Angeles"))
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, "Europe/Madrid")

	values := form.values()
	if values.StartsAt != "2026-10-14" || values.StartTime != "03:00" {
		t.Errorf("start = %q %q, want 03:00 on the 14th", values.StartsAt, values.StartTime)
	}
}

// Half-hour zones have whole hours of their own: the next one in Kolkata is on the hour there,
// not half past, which is where a whole UTC hour lands.
func TestNewEventFormTakesTheNextWholeHourOfAHalfHourZone(t *testing.T) {
	// 09:41 UTC is 15:11 in Kolkata.
	on := time.Date(2026, 10, 14, 9, 41, 0, 0, time.UTC)
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, "Asia/Kolkata")

	if values := form.values(); values.StartTime != "16:00" || values.EndTime != "17:00" {
		t.Errorf("times = %q → %q, want 16:00 → 17:00 in Kolkata", values.StartTime, values.EndTime)
	}
}

// An account with no zone, a read that failed and a name HEY would not look up all leave the
// form on Local rather than refusing to open it: the choice is on screen for the reader.
func TestNewEventFormOpensOnLocalWithoutAUsableAccountZone(t *testing.T) {
	on := time.Date(2026, 10, 14, 9, 41, 0, 0, time.Local)
	for _, accountZone := range []string{"", "Mars/Olympus_Mons", "Local", "America//New_York", "Eastern Time (US & Canada)"} {
		form := newAccountZoneForm(eventFormCreate, Recording{}, on, accountZone)

		if form.starts.zoneName() != "" || form.ends.zoneName() != "" {
			t.Errorf("%q: zones = %q → %q, want Local", accountZone, form.starts.zoneName(), form.ends.zoneName())
		}
		if got := stripANSI(form.view()); strings.Count(got, localZoneLabel) != 2 {
			t.Errorf("%q: the form shows %q, want both moments on Local", accountZone, got)
		}
		if values := form.values(); values.StartTimeZone != "" || values.EndTimeZone != "" {
			t.Errorf("%q: zones on the wire = %q → %q, want none", accountZone, values.StartTimeZone, values.EndTimeZone)
		}
	}
}

// Local and the rest of the list are still there to choose, and choosing Local sends the time
// as UTC with no zone, as it always has.
func TestChoosingLocalOnANewEventSendsUTC(t *testing.T) {
	on := time.Date(2026, 10, 14, 9, 41, 0, 0, mustZone(t, "Europe/Madrid"))
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, indianapolis)

	if choices := form.starts.choices; len(choices) < 3 || choices[0] != localZoneLabel {
		t.Fatalf("choices = %v, want Local first and the full list after it", choices)
	}
	for _, picker := range []*dateTimePicker{form.starts, form.ends} {
		picker.focusField(dateTimeFieldZone)
		typeInto(t, picker, "local")
		picker.handleKey(keyPress("enter"))
	}
	if form.starts.zoneName() != "" || form.ends.zoneName() != "" {
		t.Fatalf("zones = %q → %q, want Local chosen", form.starts.zoneName(), form.ends.zoneName())
	}

	// The clock on the form is now read on the machine's, and goes as that instant in UTC.
	starts, _ := time.ParseInLocation("2006-01-02 15:04", form.starts.date()+" "+form.starts.clock(), time.Local)
	values := form.values()
	if values.StartsAt != starts.UTC().Format("2006-01-02") || values.StartTime != starts.UTC().Format("15:04") {
		t.Errorf("start = %q %q, want %s", values.StartsAt, values.StartTime, starts.UTC())
	}
	if values.StartTimeZone != "" || values.EndTimeZone != "" {
		t.Errorf("zones = %q → %q, want none — a moment on Local goes as UTC", values.StartTimeZone, values.EndTimeZone)
	}
}

// HEY keeps a zone for both ends or neither, so one end moved to Local beside the account's
// zone is written on the account's clock: the same instant, on the clock HEY will read it on.
func TestOneEndOnLocalIsWrittenInTheOtherEndsZone(t *testing.T) {
	on := time.Date(2026, 10, 14, 9, 41, 0, 0, mustZone(t, "Europe/Madrid"))
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, indianapolis)
	form.ends.setZoneName("")

	ends, _ := time.ParseInLocation("2006-01-02 15:04", form.ends.date()+" "+form.ends.clock(), time.Local)
	ends = ends.In(mustZone(t, indianapolis))
	values := form.values()
	if values.EndTimeZone != indianapolis || values.EndsAt != ends.Format("2006-01-02") || values.EndTime != ends.Format("15:04") {
		t.Errorf("end = %q %q %q, want %s", values.EndsAt, values.EndTime, values.EndTimeZone, ends)
	}
	if values.StartTimeZone != indianapolis || values.StartTime != "04:00" {
		t.Errorf("start = %q %q, want 04:00 in the account's zone", values.StartTime, values.StartTimeZone)
	}
}

// An edit keeps the event's own terms rather than the account's, as `hey event edit` does: a
// zoned event keeps its zone, and a timed event saved without one stays on Local and goes back
// as UTC with no zone. An all-day event has no clock of its own, so given a time it takes the
// account's zone, as a new event would.
func TestEditingAnEventKeepsItsOwnZoneOverTheAccounts(t *testing.T) {
	on := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	zoned := Recording{
		ID: 99, Title: "Product review", Type: "Calendar::Event",
		StartsAt:     time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC),
		EndsAt:       time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC),
		StartsAtZone: "Europe/Madrid", EndsAtZone: "Europe/Madrid",
	}
	form := newAccountZoneForm(eventFormEdit, zoned, on, indianapolis)
	values := form.values()
	if values.StartTimeZone != "Europe/Madrid" || values.EndTimeZone != "Europe/Madrid" ||
		values.StartTime != "09:00" || values.EndTime != "10:00" {
		t.Errorf("zoned edit = %s %s → %s %s, want 09:00 → 10:00 in Madrid",
			values.StartTime, values.StartTimeZone, values.EndTime, values.EndTimeZone)
	}

	zoneless := Recording{
		ID: 100, Title: "Standup", Type: "Calendar::Event",
		StartsAt: time.Date(2026, 8, 20, 13, 30, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 8, 20, 13, 45, 0, 0, time.UTC),
	}
	form = newAccountZoneForm(eventFormEdit, zoneless, on, indianapolis)
	values = form.values()
	if values.StartTimeZone != "" || values.EndTimeZone != "" {
		t.Errorf("zoneless edit zones = %q → %q, want none", values.StartTimeZone, values.EndTimeZone)
	}
	if values.StartsAt != "2026-08-20" || values.StartTime != "13:30" || values.EndTime != "13:45" {
		t.Errorf("zoneless edit = %s %s → %s, want its own instants in UTC", values.StartsAt, values.StartTime, values.EndTime)
	}

	allDay := Recording{
		ID: 101, Title: "Offsite", Type: "Calendar::Event", AllDay: true,
		StartsAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
	}
	form = newAccountZoneForm(eventFormEdit, allDay, on, indianapolis)
	form.setAllDay(false)
	values = form.values()
	if values.StartTimeZone != indianapolis || values.EndTimeZone != indianapolis || values.StartsAt != "2026-08-20" {
		t.Errorf("all-day made timed = %s %s → %s, want the day kept in the account's zone",
			values.StartsAt, values.StartTimeZone, values.EndTimeZone)
	}
}

// The order of the two moments is read on the clock each was chosen on: 10:00 in Madrid is
// before 09:00 in Indianapolis, and 09:00 in Madrid is before 08:00 in Indianapolis.
func TestEventFormOrdersMomentsOnTheirChosenClocks(t *testing.T) {
	on := time.Date(2026, 10, 14, 9, 41, 0, 0, mustZone(t, "Europe/Madrid"))

	form := newAccountZoneForm(eventFormCreate, Recording{}, on, indianapolis)
	form.title.SetValue("Design review")
	form.starts.timeInput.SetValue("09:00")
	form.ends.setZoneName("Europe/Madrid")
	form.ends.timeInput.SetValue("10:00")
	if got := form.validate(); got != "The end is before the start" {
		t.Errorf("validate = %q, want the end refused as before the start", got)
	}

	form = newAccountZoneForm(eventFormCreate, Recording{}, on, indianapolis)
	form.title.SetValue("Design review")
	form.starts.setZoneName("Europe/Madrid")
	form.starts.timeInput.SetValue("09:00")
	form.ends.timeInput.SetValue("08:00")
	if got := form.validate(); got != "" {
		t.Errorf("validate = %q, want 09:00 Madrid → 08:00 Indianapolis taken", got)
	}

	// And a zone is one when HEY would look it up by that name, not merely when Go loads it.
	form.starts.setZoneName("America//New_York")
	if got := form.validate(); got != "Starts — That is not a time zone" {
		t.Errorf("validate = %q, want a name HEY cannot look up refused", got)
	}
}

// The calendar reads the account's zone with the rest of the identity when it opens, and a new
// event's write names it.
func TestNewEventSendsTheAccountZoneTheIdentityServed(t *testing.T) {
	v, recorded := calendarWithEventServer(t)
	v.Update(v.fetchIdentity()())

	v.HandleContentKey(keyPress("a"))
	if v.eventForm == nil {
		t.Fatal("a did not open the event form")
	}
	v.eventForm.title.SetValue("Design review")
	wantDate, wantClock := v.eventForm.starts.date(), v.eventForm.starts.clock()

	cmd := v.HandleContentKey(keyPress("ctrl+s"))
	if cmd == nil {
		t.Fatal("ctrl+s did not save")
	}
	if msg, ok := cmd().(calendarMutationMsg); !ok || msg.err != nil {
		t.Fatalf("save = %T %v", msg, msg.err)
	}

	requests, bodies := recorded.snapshot()
	if len(requests) != 2 || requests[1] != "POST /calendar/events.json" {
		t.Fatalf("requests = %v, want the identity read and then the create", requests)
	}
	form, err := url.ParseQuery(bodies[1])
	if err != nil {
		t.Fatalf("body is not form-encoded: %v", err)
	}
	for field, want := range map[string]string{
		"calendar_event[set_time_zone]":            "1",
		"calendar_event[starts_at_time_zone_name]": indianapolis,
		"calendar_event[ends_at_time_zone_name]":   indianapolis,
		"calendar_event[starts_at]":                wantDate,
		"calendar_event[starts_at_time]":           wantClock + ":00",
	} {
		if got := form.Get(field); got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
}

// A failed identity read costs the account's zone and nothing else: the form still opens, on
// Local.
func TestAFailedIdentityReadLeavesANewEventOnLocal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"Something went wrong"}`)
	}))
	t.Cleanup(server.Close)

	v := dayWithEvents(t)
	v.vc.ctx = context.Background()
	v.vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0))
	v.calendars = eventFormCalendars()
	v.accountZone = indianapolis // what an earlier read served

	v.Update(v.fetchIdentity()())
	v.HandleContentKey(keyPress("a"))
	if v.eventForm == nil {
		t.Fatal("a did not open the event form")
	}
	if v.eventForm.starts.zoneName() != "" || v.eventForm.ends.zoneName() != "" {
		t.Errorf("zones = %q → %q, want Local", v.eventForm.starts.zoneName(), v.eventForm.ends.zoneName())
	}
}
