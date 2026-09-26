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

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/timezone"
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
	// The machine is put in Tokyo through the widget's own seam, so what Local means here does
	// not depend on where the suite runs.
	onMachine(form, mustZone(t, "Asia/Tokyo"))
	for _, picker := range []*dateTimePicker{form.starts, form.ends} {
		picker.focusField(dateTimeFieldZone)
		typeInto(t, picker, "local")
		picker.handleKey(keyPress("enter"))
	}
	if form.starts.zoneName() != "" || form.ends.zoneName() != "" {
		t.Fatalf("zones = %q → %q, want Local chosen", form.starts.zoneName(), form.ends.zoneName())
	}

	// The form still reads 04:00 → 05:00 on the 14th, now on Tokyo's clock, and it goes as UTC
	// with no zone. HEY reads 2026-10-14 04:00 Asia/Tokyo as 2026-10-13 19:00 UTC.
	values := form.values()
	if values.StartTimeZone != "" || values.EndTimeZone != "" {
		t.Errorf("zones = %q → %q, want none — a moment on Local goes as UTC", values.StartTimeZone, values.EndTimeZone)
	}
	wantPlaced(t, "start", values.StartsAt, values.StartTime, values.StartTimeZone, time.Date(2026, 10, 13, 19, 0, 0, 0, time.UTC))
	wantPlaced(t, "end", values.EndsAt, values.EndTime, values.EndTimeZone, time.Date(2026, 10, 13, 20, 0, 0, 0, time.UTC))
}

// onMachine puts both of a form's moments on a machine in zone, through the widget's seam.
func onMachine(form *eventForm, zone *time.Location) {
	form.starts.local = zone
	form.ends.local = zone
}

// wantPlaced reads an end on the wire the way HEY reads it — the clock time in the zone sent
// with it, or UTC without one — and checks it is the instant wanted. The expectations are
// ActiveSupport's own: Time.zone = "UTC"; Time.zone.parse(clock).change(zone:).utc.
func wantPlaced(t *testing.T, end, date, clock, zone string, want time.Time) {
	t.Helper()
	var loc *time.Location
	if zone != "" {
		loc = mustZone(t, zone)
	}
	got, ok := timezone.Placed(date, clock, loc)
	if !ok || !got.Equal(want) {
		t.Errorf("%s sent as %s %s %q, which HEY places at %s, want %s", end, date, clock, zone, got.UTC(), want.UTC())
	}
}

// HEY keeps a zone for both ends or neither, so one end moved to Local beside the account's
// zone is written on the account's clock: the same instant, on the clock HEY will read it on.
func TestOneEndOnLocalIsWrittenInTheOtherEndsZone(t *testing.T) {
	on := time.Date(2026, 10, 14, 9, 41, 0, 0, mustZone(t, "Europe/Madrid"))
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, indianapolis)
	onMachine(form, time.UTC)
	form.ends.setZoneName("")
	form.ends.setMoment(time.Date(2026, 10, 14, 10, 15, 0, 0, time.UTC))

	values := form.values()
	if values.StartTimeZone != indianapolis || values.EndTimeZone != indianapolis {
		t.Errorf("zones = %q → %q, want the account's for both", values.StartTimeZone, values.EndTimeZone)
	}
	// 2026-10-14 04:00 America/Indiana/Indianapolis => 08:00 UTC; the end is 10:15 UTC itself.
	wantPlaced(t, "start", values.StartsAt, values.StartTime, values.StartTimeZone, time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC))
	wantPlaced(t, "end", values.EndsAt, values.EndTime, values.EndTimeZone, time.Date(2026, 10, 14, 10, 15, 0, 0, time.UTC))
	if got := form.validate(); got != "Name is required" {
		t.Errorf("validate = %q, want only the missing name", got)
	}
}

// A Local end beside a zoned start is written on the start's clock, and at the second 01:30 of
// the night New York falls back that clock time is one HEY reads as the first: the form refuses
// rather than moving the end an hour. The first 01:30 goes through.
func TestALocalEndHEYWouldMoveIsRefused(t *testing.T) {
	on := time.Date(2026, 11, 1, 0, 10, 0, 0, mustZone(t, "America/New_York"))
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, "America/New_York")
	form.title.SetValue("Night shift handover")
	onMachine(form, time.UTC)
	form.ends.setZoneName("")

	// 06:30 UTC is the second 01:30 in New York; HEY reads 2026-11-01 01:30 as 05:30 UTC.
	form.ends.setMoment(time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC))
	want := "Ends — HEY reads 2026-11-01 01:30 America/New_York as another moment, so saving would move it an hour earlier. Retype the time or choose another"
	if got := form.validate(); got != want {
		t.Errorf("validate = %q, want %q", got, want)
	}

	form.ends.setMoment(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC))
	if got := form.validate(); got != "" {
		t.Errorf("validate = %q, want the first 01:30 taken", got)
	}
	values := form.values()
	wantPlaced(t, "end", values.EndsAt, values.EndTime, values.EndTimeZone, time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC))
}

// A title-only edit sends the times back as they are shown, and an end HEY imported at the
// second 01:30 of the night New York falls back would come back at the first: the form refuses
// rather than moving it, as `hey event edit` does. Retyping the time is the reader choosing
// HEY's reading of it.
func TestEditingAnEventHEYWouldMoveIsRefusedUntilRetyped(t *testing.T) {
	event := Recording{
		ID: 4821, Title: "Night shift handover", Type: "Calendar::Event",
		StartsAt:     time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC),  // 01:00 EDT
		EndsAt:       time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), // the second 01:30, EST
		StartsAtZone: "America/New_York", EndsAtZone: "America/New_York",
	}
	form := newAccountZoneForm(eventFormEdit, event, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), indianapolis)
	form.title.SetValue("Night shift handover, west door")

	want := "Ends — HEY reads 2026-11-01 01:30 America/New_York as another moment, so saving would move it an hour earlier. Retype the time or choose another"
	if got := form.validate(); got != want {
		t.Errorf("validate = %q, want %q", got, want)
	}

	form.ends.focusField(dateTimeFieldTime)
	form.ends.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	typeInto(t, form.ends, "0")
	if got := form.validate(); got != "" {
		t.Errorf("validate = %q, want a retyped time taken", got)
	}
	values := form.values()
	// 2026-11-01 01:00 and 01:30 America/New_York => 05:00 and 05:30 UTC.
	wantPlaced(t, "start", values.StartsAt, values.StartTime, values.StartTimeZone, time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC))
	wantPlaced(t, "end", values.EndsAt, values.EndTime, values.EndTimeZone, time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC))
}

// HEY is sent whole minutes, so an imported time with seconds cannot be kept by an edit that
// never touched it; the form says so rather than dropping them.
func TestEditingAnEventWithSecondsIsRefused(t *testing.T) {
	event := Recording{
		ID: 4822, Title: "Standup", Type: "Calendar::Event",
		StartsAt: time.Date(2026, 8, 20, 13, 30, 30, 0, time.UTC),
		EndsAt:   time.Date(2026, 8, 20, 13, 45, 0, 0, time.UTC),
	}
	form := newAccountZoneForm(eventFormEdit, event, time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), indianapolis)
	onMachine(form, time.UTC)

	want := "Starts — HEY is sent whole minutes, so saving would move it 30 seconds earlier. Retype the time"
	if got := form.validate(); got != want {
		t.Errorf("validate = %q, want %q", got, want)
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
	v.accountZone = indianapolis // what an earlier visit was told

	// Entering the calendar starts a fresh read, and what an earlier visit was told does not
	// stand in for it while it is on its way, nor after it fails.
	v.Init()
	v.HandleContentKey(keyPress("a"))
	if v.eventForm == nil {
		t.Fatal("a did not open the event form")
	}
	v.Update(v.fetchIdentity()())
	if v.eventForm.starts.zoneName() != "" || v.eventForm.ends.zoneName() != "" {
		t.Errorf("zones = %q → %q, want Local", v.eventForm.starts.zoneName(), v.eventForm.ends.zoneName())
	}
}

// A new event opened before the identity read lands takes the account's zone when it does, on
// the next whole hour of that clock — unless the reader has already touched when it is.
func TestANewEventTakesTheAccountZoneWhenTheIdentityLandsLate(t *testing.T) {
	v, _ := calendarWithEventServer(t)
	v.Init()
	identity := v.fetchIdentity()()

	v.HandleContentKey(keyPress("a"))
	if v.eventForm == nil || v.eventForm.starts.zoneName() != "" {
		t.Fatal("a form opened before the read did not open on Local")
	}
	v.eventForm.title.SetValue("Design review")
	v.Update(identity)

	want := newEventStart(v.day(), mustZone(t, indianapolis))
	form := v.eventForm
	if form.starts.zoneName() != indianapolis || form.ends.zoneName() != indianapolis {
		t.Errorf("zones = %q → %q, want the account's", form.starts.zoneName(), form.ends.zoneName())
	}
	if form.starts.date() != want.Format("2006-01-02") || form.starts.clock() != want.Format("15:04") {
		t.Errorf("start = %s %s, want %s", form.starts.date(), form.starts.clock(), want)
	}
	if got := form.title.Value(); got != "Design review" {
		t.Errorf("title = %q, want what the reader typed kept", got)
	}

	// A reader who has changed a time has answered the question; the read does not move it.
	v.eventForm = nil
	v.Init()
	v.HandleContentKey(keyPress("a"))
	v.eventForm.starts.timeInput.SetValue("15:00")
	v.Update(v.fetchIdentity()())
	if v.eventForm.starts.zoneName() != "" || v.eventForm.starts.clock() != "15:00" {
		t.Errorf("start = %s %q, want 15:00 left on Local", v.eventForm.starts.clock(), v.eventForm.starts.zoneName())
	}
}

// Each visit to the calendar reads the identity again, and only the latest read is taken: an
// earlier visit's answer landing late does not put its zone back, and a form opened on Local
// does not take it.
func TestAnEarlierVisitsIdentityReadIsDropped(t *testing.T) {
	v, _ := calendarWithEventServer(t)
	v.Init()
	earlier := v.fetchIdentity()()
	v.Init()

	v.HandleContentKey(keyPress("a"))
	v.Update(earlier)
	if v.accountZone != "" {
		t.Errorf("accountZone = %q, want the earlier visit's answer dropped", v.accountZone)
	}
	if v.eventForm.starts.zoneName() != "" {
		t.Errorf("zone = %q, want the form left on Local", v.eventForm.starts.zoneName())
	}

	v.Update(v.fetchIdentity()())
	if v.accountZone != indianapolis || v.eventForm.starts.zoneName() != indianapolis {
		t.Errorf("zone = %q / %q, want the latest read's", v.accountZone, v.eventForm.starts.zoneName())
	}
}

// An hour on from the first 01:00 of the night New York falls back is the second 01:00, which
// is sent as the same clock and placed by HEY at the first: a zero-length event. The form
// offers 01:00 to 02:00 instead.
func TestNewEventFormEndsAfterItStartsAcrossAFallBack(t *testing.T) {
	newYork := mustZone(t, "America/New_York")
	on := time.Date(2026, 11, 1, 0, 41, 0, 0, newYork)
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, "America/New_York")

	values := form.values()
	if values.StartsAt != "2026-11-01" || values.StartTime != "01:00" || values.EndTime != "02:00" {
		t.Errorf("times = %s %s → %s, want 01:00 → 02:00", values.StartsAt, values.StartTime, values.EndTime)
	}
	starts, _ := form.starts.moment()
	ends, _ := form.ends.moment()
	if !ends.After(starts) {
		t.Errorf("the end %s is not after the start %s", ends, starts)
	}
}

// An hour the clocks skip is not offered: 01:41 on the morning New York springs forward opens
// on 03:00, the next whole hour that exists, where Go would put 02:00 back at 01:00.
func TestNewEventFormSkipsTheHourTheClocksSkip(t *testing.T) {
	newYork := mustZone(t, "America/New_York")
	on := time.Date(2026, 3, 8, 1, 41, 0, 0, newYork)
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, "America/New_York")

	values := form.values()
	if values.StartsAt != "2026-03-08" || values.StartTime != "03:00" || values.EndTime != "04:00" {
		t.Errorf("times = %s %s → %s, want 03:00 → 04:00", values.StartsAt, values.StartTime, values.EndTime)
	}
}

// Times are ordered where HEY will place them. HEY moves 02:30 on the morning New York springs
// forward on to 03:30, so an end at 03:00 is before it — Go alone would read 02:30 as 01:30 and
// let it through, and HEY would refuse the write.
func TestEventFormOrdersASkippedTimeAsHEYPlacesIt(t *testing.T) {
	on := time.Date(2026, 3, 8, 1, 0, 0, 0, mustZone(t, "America/New_York"))
	form := newAccountZoneForm(eventFormCreate, Recording{}, on, "America/New_York")
	form.title.SetValue("Night shift handover")
	form.starts.timeInput.SetValue("02:30")
	form.ends.timeInput.SetValue("03:00")

	if got := form.validate(); got != "The end is before the start" {
		t.Errorf("validate = %q, want the end refused as before the start", got)
	}
	form.ends.timeInput.SetValue("03:30")
	if got := form.validate(); got != "" {
		t.Errorf("validate = %q, want an end at the start HEY places taken", got)
	}
}

// A time typed on Local is read the way HEY and `hey event` read it too: on a machine in New
// York, 02:30 on the morning the clocks spring forward is 03:30, sent as 07:30 UTC, not an
// 01:30 Go would make of it.
func TestATimeTypedOnLocalIsReadAsHEYReadsIt(t *testing.T) {
	newYork := mustZone(t, "America/New_York")
	form := newAccountZoneForm(eventFormCreate, Recording{}, time.Date(2026, 3, 8, 0, 20, 0, 0, newYork), "")
	onMachine(form, newYork)
	form.title.SetValue("Night shift handover")
	form.starts.timeInput.SetValue("02:30")
	form.ends.timeInput.SetValue("04:00")

	if got := form.validate(); got != "" {
		t.Fatalf("validate = %q, want the form taken", got)
	}
	values := form.values()
	// 2026-03-08 02:30 and 04:00 America/New_York => 07:30 and 08:00 UTC.
	wantPlaced(t, "start", values.StartsAt, values.StartTime, values.StartTimeZone, time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC))
	wantPlaced(t, "end", values.EndsAt, values.EndTime, values.EndTimeZone, time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC))
}

// An all-day event opened for editing before the identity read lands takes the account's zone
// when it does, for the times it would get by no longer being all day, as it would have had
// the read come first.
func TestAnAllDayEditTakesTheAccountZoneWhenTheIdentityLandsLate(t *testing.T) {
	event := Recording{
		ID: 3, Title: "Offsite", Type: "Calendar::Event", AllDay: true,
		StartsAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
	}
	form := newAccountZoneForm(eventFormEdit, event, time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), "")
	form.adoptAccountZone(indianapolis)
	form.setAllDay(false)

	values := form.values()
	if values.StartTimeZone != indianapolis || values.EndTimeZone != indianapolis || values.StartsAt != "2026-08-20" {
		t.Errorf("made timed = %s %q → %q, want the day in the account's zone",
			values.StartsAt, values.StartTimeZone, values.EndTimeZone)
	}

	// A timed event keeps its own terms whenever the read lands.
	timed := Recording{
		ID: 100, Title: "Standup", Type: "Calendar::Event",
		StartsAt: time.Date(2026, 8, 20, 13, 30, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 8, 20, 13, 45, 0, 0, time.UTC),
	}
	form = newAccountZoneForm(eventFormEdit, timed, time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), "")
	form.adoptAccountZone(indianapolis)
	if form.starts.zoneName() != "" || form.ends.zoneName() != "" {
		t.Errorf("a zoneless event took the zones %q → %q", form.starts.zoneName(), form.ends.zoneName())
	}
}

// Choosing Local is an answer even though it leaves the form showing what it showed, and an
// identity read landing after it does not take it back.
func TestChoosingLocalOutlastsALateIdentityRead(t *testing.T) {
	form := newAccountZoneForm(eventFormCreate, Recording{}, time.Date(2026, 10, 14, 9, 41, 0, 0, time.UTC), "")
	form.starts.focusField(dateTimeFieldZone)
	typeInto(t, form.starts, "local")
	form.starts.handleKey(keyPress("enter"))
	shown := form.starts.clock()

	form.adoptAccountZone(indianapolis)
	if form.starts.zoneName() != "" || form.ends.zoneName() != "" {
		t.Errorf("zones = %q → %q, want Local kept", form.starts.zoneName(), form.ends.zoneName())
	}
	if form.starts.clock() != shown {
		t.Errorf("start = %s, want %s left as it was", form.starts.clock(), shown)
	}
}

// At Lord Howe the clocks go back half an hour, so an hour on from 01:00 is the second 01:30,
// which HEY reads as the first: a half-hour event. The form offers 02:00, the first end at
// least an hour on that HEY places where it is shown.
func TestNewEventFormRunsAnHourAcrossLordHowesHalfHourFallBack(t *testing.T) {
	lordHowe := mustZone(t, "Australia/Lord_Howe")
	form := newAccountZoneForm(eventFormCreate, Recording{}, time.Date(2026, 4, 5, 0, 41, 0, 0, lordHowe), "Australia/Lord_Howe")

	values := form.values()
	if values.StartTime != "01:00" || values.EndTime != "02:00" {
		t.Errorf("times = %s → %s, want 01:00 → 02:00", values.StartTime, values.EndTime)
	}
	// 2026-04-05 01:00 and 02:00 Australia/Lord_Howe => 2026-04-04 14:00 and 15:30 UTC.
	wantPlaced(t, "start", values.StartsAt, values.StartTime, values.StartTimeZone, time.Date(2026, 4, 4, 14, 0, 0, 0, time.UTC))
	wantPlaced(t, "end", values.EndsAt, values.EndTime, values.EndTimeZone, time.Date(2026, 4, 4, 15, 30, 0, 0, time.UTC))
}

// A zone name comes from HEY — an event's own zone, the account's — and is shown with its
// escape sequences stripped, on the form and in the open list, while the name itself is kept
// for the checks and the write.
func TestZoneNamesAreShownSanitized(t *testing.T) {
	const hostile = "Europe/Madrid\x1b]0;owned\x07"
	form := newAccountZoneForm(eventFormCreate, Recording{}, time.Date(2026, 10, 14, 9, 41, 0, 0, time.UTC), "")
	form.starts.setZoneName(hostile)

	if got := form.view(); strings.Contains(got, "\x1b]0;") {
		t.Errorf("the form shows the zone's escape sequence: %q", got)
	}
	form.starts.focusField(dateTimeFieldZone)
	typeInto(t, form.starts, "madrid")
	if got := form.view(); strings.Contains(got, "\x1b]0;") {
		t.Errorf("the zone list shows the escape sequence: %q", got)
	}
	if form.starts.zone != hostile {
		t.Errorf("zone = %q, want the name kept as it came", form.starts.zone)
	}
	if got := form.starts.problem(); got != "That is not a time zone" {
		t.Errorf("problem = %q, want the name refused", got)
	}
}

// Monrovia's clocks kept thirty seconds in their offset until 7 January 1972: 23:00 on the 6th
// was 23:44:30 UTC, and at midnight they jumped to 00:44:30 UTC. An hour on from that start is
// 00:44:30, which no clock time names; 00:44 is one HEY moves an hour on (01:44 UTC), so the
// first end at least an hour on is 00:45 (=> 00:45 UTC).
func TestNewEventFormRunsAnHourAcrossMonroviasHalfMinute(t *testing.T) {
	monrovia := mustZone(t, "Africa/Monrovia")
	form := newAccountZoneForm(eventFormCreate, Recording{}, time.Date(1972, 1, 6, 22, 30, 0, 0, monrovia), "Africa/Monrovia")

	values := form.values()
	if values.StartTime != "23:00" || values.EndsAt != "1972-01-07" || values.EndTime != "00:45" {
		t.Errorf("times = %s %s → %s %s, want 23:00 → 00:45 the next day", values.StartsAt, values.StartTime, values.EndsAt, values.EndTime)
	}
	wantPlaced(t, "start", values.StartsAt, values.StartTime, values.StartTimeZone, time.Date(1972, 1, 6, 23, 44, 30, 0, time.UTC))
	wantPlaced(t, "end", values.EndsAt, values.EndTime, values.EndTimeZone, time.Date(1972, 1, 7, 0, 45, 0, 0, time.UTC))
	if got := form.validate(); got != "Name is required" {
		t.Errorf("validate = %q, want only the missing name", got)
	}
}
