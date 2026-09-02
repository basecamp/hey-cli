package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
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
		{ID: 240334, Name: "Errands", Color: "blue", OwnerEmail: "amelia@example.com"},
		{ID: 486532, Name: "Design Team", Color: "teal", OwnerEmail: "amelia@example.org"},
	}
}

// newTestEventForm is the form as the calendar view opens it, with nothing remembered about
// which calendar was filed on last.
func newTestEventForm(mode eventFormMode, event Recording, on time.Time, calendars []Calendar) *eventForm {
	return newEventForm(mode, event, on, calendars, 0, newStyles())
}

// focusOn puts the form on a field the way tab would, so a picker underneath is focused too.
func focusOn(form *eventForm, field int) {
	form.focus = field
	form.takeFocus(1)
}

// formLabels is the label column of every row on screen, which is what the form's shape reads
// as: the fields it is asking about, in the order it asks them.
func formLabels(form *eventForm) []string {
	var labels []string
	for _, line := range strings.Split(stripANSI(form.view()), "\n") {
		if len(line) < eventFieldLabelWidth {
			continue
		}
		if label := strings.TrimSpace(line[:eventFieldLabelWidth]); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
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
	form := newTestEventForm(eventFormCreate, Recording{}, on, eventFormCalendars())

	if got := stripANSI(form.view()); !strings.Contains(got, "10:00") || !strings.Contains(got, "11:00") {
		t.Errorf("the form shows %q, want the next whole hour for an hour", got)
	}

	// And what goes on the wire is that same hour in UTC, which is the zone HEY reads a
	// submitted clock time in.
	starts := time.Date(2026, 8, 20, 10, 0, 0, 0, time.Local).UTC()
	ends := starts.Add(time.Hour)
	values := form.values()
	if values.StartsAt != starts.Format("2006-01-02") || values.StartTime != starts.Format("15:04") {
		t.Errorf("start = %q %q, want %s in UTC", values.StartsAt, values.StartTime, starts)
	}
	if values.EndsAt != ends.Format("2006-01-02") || values.EndTime != ends.Format("15:04") {
		t.Errorf("end = %q %q, want %s in UTC", values.EndsAt, values.EndTime, ends)
	}
	if values.AllDay {
		t.Error("a new event should not be all day")
	}
	if values.CalendarID != 240334 {
		t.Errorf("calendar = %d, want the first offered", values.CalendarID)
	}
	if len(values.Reminders) != 0 {
		t.Errorf("reminders = %v, want none until asked for", values.Reminders)
	}
}

// All day comes directly under the name, above the two moments it governs: reading down the
// form, whether the event has a time of day is settled before it says what that time is.
func TestEventFormAsksAboutAllDayBeforeTheTimes(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())

	labels := formLabels(form)
	want := []string{"Calendar", "Name", "All day", "Starts", "Ends", "Notify", "More"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Errorf("fields read %v, want %v", labels, want)
	}

	// Tab reads the same way: name, then all day, then the moments.
	focusOn(form, eventFieldTitle)
	form.step(1)
	if form.focus != eventFieldAllDay {
		t.Errorf("tab from the name landed on %d, want all day", form.focus)
	}
	form.step(1)
	if form.focus != eventFieldStarts {
		t.Errorf("tab from all day landed on %d, want the start", form.focus)
	}
}

// Editing prefills from the event, including which calendar it is on.
func TestEditEventFormPrefillsFromTheEvent(t *testing.T) {
	event := Recording{
		ID: 99, Title: "Amelia & Kevin", CalendarID: 486532, CalendarColor: "teal", Type: "Calendar::Event",
		StartsAt: atLocal("2026-08-20T14:00:00"), EndsAt: atLocal("2026-08-20T15:30:00"),
	}
	form := newTestEventForm(eventFormEdit, event, time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local),
		eventFormCalendars())

	values := form.values()
	if values.Title != "Amelia & Kevin" {
		t.Errorf("title = %q", values.Title)
	}
	if got := stripANSI(form.view()); !strings.Contains(got, "14:00") || !strings.Contains(got, "15:30") {
		t.Errorf("the form shows %q, want the event's own times", got)
	}
	starts := time.Date(2026, 8, 20, 14, 0, 0, 0, time.Local).UTC()
	if values.StartsAt != starts.Format("2006-01-02") || values.StartTime != starts.Format("15:04") {
		t.Errorf("start = %q %q, want %s in UTC", values.StartsAt, values.StartTime, starts)
	}
	if values.CalendarID != 486532 {
		t.Errorf("calendar = %d, want the teal one the event is on", values.CalendarID)
	}
	if form.formTitle() != "Edit event" {
		t.Errorf("title = %q", form.formTitle())
	}

	// And an edit can move it: the picker steps, and the calendar goes with the save.
	focusOn(form, eventFieldCalendar)
	form.handleKey(keyPress("left"))
	if got := form.values().CalendarID; got != 240334 {
		t.Errorf("calendar = %d, want the one the picker stepped to", got)
	}
}

// Two calendars can wear the same color, so an edit finds the one the event is filed on by
// its id. Matching on color alone put the event on whichever shared it first, and saving then
// moved it there.
func TestEditEventFormFindsTheCalendarByID(t *testing.T) {
	calendars := []Calendar{
		{ID: 240334, Name: "Errands", Color: "blue"},
		{ID: 486532, Name: "Design Team", Color: "blue"},
	}
	event := Recording{ID: 99, Title: "Design review", CalendarID: 486532, CalendarColor: "blue"}

	form := newTestEventForm(eventFormEdit, event, time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), calendars)
	if got := form.values().CalendarID; got != 486532 {
		t.Errorf("calendar = %d, want the one the event names", got)
	}
}

// Two calendars can also share a name — a work account and a personal one both keep a
// "Maybe" — so the form says which account the highlighted one belongs to.
func TestEventFormNamesTheAccountACalendarBelongsTo(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), []Calendar{
			{ID: 240334, Name: "Maybe", Color: "blue", OwnerEmail: "amelia@example.com"},
			{ID: 486532, Name: "Maybe", Color: "teal", OwnerEmail: "amelia\x1b[31m@example.org"},
		})

	if got := stripANSI(form.view()); !strings.Contains(got, "amelia@example.com") {
		t.Errorf("the form does not name the account: %q", got)
	}

	// The address is somebody else's text, so the escape sequence in it is stripped rather
	// than printed at the terminal.
	focusOn(form, eventFieldCalendar)
	form.handleKey(keyPress("right"))
	if got := form.view(); !strings.Contains(got, "amelia@example.org") || strings.Contains(got, "\x1b[31m") {
		t.Errorf("the second account reads %q", stripANSI(got))
	}
}

// A reader who keeps a work calendar and a personal one files on the same one all week, so a
// new event opens on the one they used last.
func TestNewEventFormOpensOnTheRememberedCalendar(t *testing.T) {
	on := time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local)

	form := newEventForm(eventFormCreate, Recording{}, on, eventFormCalendars(), 486532, newStyles())
	if got := form.values().CalendarID; got != 486532 {
		t.Errorf("calendar = %d, want the remembered one", got)
	}

	// A calendar the reader has since lost access to is not in the list, and the form falls
	// back to the first rather than filing somewhere HEY would refuse.
	form = newEventForm(eventFormCreate, Recording{}, on, eventFormCalendars(), 999999, newStyles())
	if got := form.values().CalendarID; got != 240334 {
		t.Errorf("calendar = %d, want the first offered", got)
	}

	// An edit opens on the event's own calendar whatever was remembered.
	event := Recording{ID: 99, CalendarID: 240334, Type: "Calendar::Event"}
	form = newEventForm(eventFormEdit, event, on, eventFormCalendars(), 486532, newStyles())
	if got := form.values().CalendarID; got != 240334 {
		t.Errorf("calendar = %d, want the one the event is on", got)
	}
}

// An all-day event has no time of day, so both moments offer a date only and tab walks
// straight from one to the other.
func TestAllDayEventFormOffersDatesOnly(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())

	focusOn(form, eventFieldAllDay)
	form.handleKey(keyPress(" "))
	if !form.values().AllDay {
		t.Fatal("space did not turn all day on")
	}

	focusOn(form, eventFieldStarts)
	form.step(1)
	if form.focus != eventFieldEnds {
		t.Errorf("tab landed on %d, want it past the start's time of day", form.focus)
	}

	// And the times are gone from the form altogether.
	if got := stripANSI(form.view()); strings.Contains(got, "09:00") || strings.Contains(got, "10:00") {
		t.Errorf("an all-day form still shows a time: %q", got)
	}
}

// The reader's clock is not HEY's: a submitted time is parsed in UTC, so the form converts
// before it sends. Pinned to a zone of its own so the assertion means something wherever the
// suite runs.
func TestEventFormSendsTheReadersClockInUTC(t *testing.T) {
	inZone(t, "Asia/Tokyo")

	on := time.Date(2026, 8, 20, 22, 30, 0, 0, time.Local)
	form := newTestEventForm(eventFormCreate, Recording{}, on, eventFormCalendars())

	// The form offers 23:00 to midnight, which is the 20th and then the 21st on the reader's
	// calendar and 14:00 to 15:00 on the same UTC day.
	values := form.values()
	if values.StartsAt != "2026-08-20" || values.StartTime != "14:00" {
		t.Errorf("start = %q %q, want 2026-08-20 14:00", values.StartsAt, values.StartTime)
	}
	if values.EndsAt != "2026-08-20" || values.EndTime != "15:00" {
		t.Errorf("end = %q %q, want 2026-08-20 15:00", values.EndsAt, values.EndTime)
	}
	if values.StartTimeZone != "" || values.EndTimeZone != "" {
		t.Errorf("zones = %q → %q, want none — a moment on Local goes as UTC",
			values.StartTimeZone, values.EndTimeZone)
	}

	// An all-day event is a date rather than a moment, and haystack reads it as one. Convert
	// it and a birthday lands on the day before for everybody east of UTC.
	focusOn(form, eventFieldAllDay)
	form.handleKey(keyPress(" "))
	if values := form.values(); values.StartsAt != "2026-08-20" || values.EndsAt != "2026-08-21" {
		t.Errorf("all-day dates = %q → %q, want the days as typed", values.StartsAt, values.EndsAt)
	}
}

// A moment opens on Local, which is the reader's own clock and the choice that needs no name
// on the wire at all.
func TestEventFormMomentsOpenOnLocal(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())

	if got := stripANSI(form.view()); strings.Count(got, localZoneLabel) != 2 {
		t.Errorf("the form shows %q, want both moments on Local", got)
	}
	if form.starts.zoneName() != "" || form.ends.zoneName() != "" {
		t.Errorf("zones = %q → %q, want nothing to send", form.starts.zoneName(), form.ends.zoneName())
	}
}

// With a zone chosen, the times go as the reader wrote them and the zones go with them — no
// conversion, because HEY is being told which clock to read them on.
func TestZonedEventFormSendsTheClockItWasWrittenOn(t *testing.T) {
	inZone(t, "Europe/Zagreb")

	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 8, 15, 0, 0, time.Local), eventFormCalendars())

	form.starts.setZoneName("Europe/Madrid")
	form.ends.setZoneName("America/New_York")
	values := form.values()
	if values.StartTime != "09:00" || values.EndTime != "10:00" {
		t.Errorf("times = %q → %q, want them unconverted", values.StartTime, values.EndTime)
	}
	if values.StartsAt != "2026-08-20" || values.EndsAt != "2026-08-20" {
		t.Errorf("dates = %q → %q, want the day in view", values.StartsAt, values.EndsAt)
	}
	if values.StartTimeZone != "Europe/Madrid" || values.EndTimeZone != "America/New_York" {
		t.Errorf("zones = %q → %q", values.StartTimeZone, values.EndTimeZone)
	}

	// Going back to Local is a decision too: the times convert to UTC again and the zones go
	// as empty, which is what clears them on an event that had some.
	form.starts.setZoneName("")
	form.ends.setZoneName("")
	values = form.values()
	if values.StartTimeZone != "" || values.EndTimeZone != "" {
		t.Errorf("zones = %q → %q, want them cleared", values.StartTimeZone, values.EndTimeZone)
	}
	if values.StartTime != "07:00" {
		t.Errorf("start = %q, want 09:00 in Zagreb as UTC", values.StartTime)
	}
}

// An event HEY serves with zones of its own is shown on the clock it was written on, not the
// reader's — 09:00 in Madrid is a 09:00 event for whoever opens it.
func TestEditingAZonedEventKeepsItsZones(t *testing.T) {
	inZone(t, "Pacific/Auckland")

	event := Recording{
		ID: 99, Title: "Product review", Type: "Calendar::Event",
		StartsAt:     time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC),
		EndsAt:       time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC),
		StartsAtZone: "Europe/Madrid", EndsAtZone: "Europe/Madrid",
	}
	form := newTestEventForm(eventFormEdit, event, time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local),
		eventFormCalendars())

	if got := stripANSI(form.view()); !strings.Contains(got, "Europe/Madrid") {
		t.Errorf("the form does not name the zone: %q", got)
	}

	values := form.values()
	if values.StartTime != "09:00" || values.EndTime != "10:00" {
		t.Errorf("times = %q → %q, want the Madrid clock the event was written on",
			values.StartTime, values.EndTime)
	}
	if values.StartTimeZone != "Europe/Madrid" || values.EndTimeZone != "Europe/Madrid" {
		t.Errorf("zones = %q → %q, want the event's own back", values.StartTimeZone, values.EndTimeZone)
	}
}

// An all-day event is a date rather than a moment, so it has no zone to set and the form does
// not offer one.
func TestAllDayEventHasNoTimeZone(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	form.starts.setZoneName("Europe/Madrid")
	form.setAllDay(true)

	if values := form.values(); values.StartTimeZone != "" {
		t.Errorf("an all-day event carried the zone %q", values.StartTimeZone)
	}
	if got := stripANSI(form.view()); strings.Contains(got, "Europe/Madrid") {
		t.Errorf("an all-day form still offers a zone: %q", got)
	}

	focusOn(form, eventFieldEnds)
	form.step(1)
	if form.focus != eventFieldNotify {
		t.Errorf("tab landed on %d, want it past the zone", form.focus)
	}
}

// While the zone list is open it owns tab, enter and esc, so the form has to say so — esc in
// the list closes the list, not the form.
func TestEventFormLetsTheZoneListKeepTheKeys(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())

	focusOn(form, eventFieldStarts)
	form.step(1)
	form.step(1)
	if form.starts.focus != dateTimeFieldZone {
		t.Fatalf("two tabs landed on %d, want the zone", form.starts.focus)
	}
	if form.capturesKeys() {
		t.Error("the form gave away its keys before the list was open")
	}

	form.handleKey(keyPress("m"))
	if !form.capturesKeys() {
		t.Fatal("typing at the zone did not open the list")
	}
	form.handleKey(keyPress("tab"))
	if form.focus != eventFieldStarts || !form.capturesKeys() {
		t.Error("tab left the zone list instead of filtering in it")
	}
}

// inZone puts the test on somebody's clock, so an assertion about what a zone does means the
// same thing wherever the suite runs.
func inZone(t *testing.T, name string) {
	t.Helper()

	zone, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no zone database here: %v", err)
	}
	local := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = local })
}

func TestEventFormRefusesWhatTheServerWould(t *testing.T) {
	on := time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local)

	for _, tt := range []struct {
		name  string
		spoil func(*eventForm)
		want  string
	}{
		{"no name", func(f *eventForm) { f.title.SetValue("  ") }, "Name is required"},
		{"bad start date", func(f *eventForm) {
			f.starts.dateInput.SetValue("20th Aug")
		}, "Starts — Date must be YYYY-MM-DD"},
		{"bad end date", func(f *eventForm) {
			f.ends.dateInput.SetValue("tomorrow")
		}, "Ends — Date must be YYYY-MM-DD"},
		{"bad start time", func(f *eventForm) {
			f.starts.timeInput.SetValue("9am")
		}, "Starts — Time must be HH:MM"},
		{"bad end time", func(f *eventForm) {
			f.ends.timeInput.SetValue("half ten")
		}, "Ends — Time must be HH:MM"},
		{"ends before it starts", func(f *eventForm) {
			f.starts.timeInput.SetValue("15:00")
			f.ends.timeInput.SetValue("14:00")
		}, "The end is before the start"},
		{"ends a day before it starts", func(f *eventForm) {
			f.setAllDay(true)
			f.ends.dateInput.SetValue("2026-08-19")
		}, "The end is before the start"},
		{"a zone nobody has heard of", func(f *eventForm) {
			f.starts.setZoneName("Europe/Zagrebb")
		}, "Starts — That is not a time zone"},
		{"an offset instead of a zone", func(f *eventForm) {
			f.ends.setZoneName("+02:00")
		}, "Ends — That is not a time zone"},
		// Eight in the morning in Auckland is the evening before in Madrid, and the two
		// clocks on their own do not show it.
		{"ends before it starts once the zones are read", func(f *eventForm) {
			f.starts.setZoneName("Europe/Madrid")
			f.starts.timeInput.SetValue("09:00")
			f.ends.setZoneName("Pacific/Auckland")
			f.ends.timeInput.SetValue("08:00")
		}, "The end is before the start"},
	} {
		form := newTestEventForm(eventFormCreate, Recording{}, on, eventFormCalendars())
		form.title.SetValue("Design review")
		tt.spoil(form)

		if got := form.validate(); got != tt.want {
			t.Errorf("%s: validate = %q, want %q", tt.name, got, tt.want)
		}
	}

	// And a form that is filled in properly says nothing.
	form := newTestEventForm(eventFormCreate, Recording{}, on, eventFormCalendars())
	form.title.SetValue("Design review")
	if got := form.validate(); got != "" {
		t.Errorf("a good form was refused: %q", got)
	}
}

// The reminders are the web form's own presets, and any number of them can be on: HEY takes
// the whole set, so a day's warning and a nudge on the hour are both reachable.
func TestEventFormNotifyTakesSeveralReminders(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())

	if got := form.values().Reminders; len(got) != 0 {
		t.Errorf("a new event starts with %v", got)
	}

	// The arrows walk the presets and space turns the one under them on: the third along is
	// 15 minutes, and the last is a day.
	focusOn(form, eventFieldNotify)
	form.handleKey(keyPress("right"))
	form.handleKey(keyPress("right"))
	form.handleKey(keyPress(" "))
	form.handleKey(keyPress("left"))
	form.handleKey(keyPress("left"))
	form.handleKey(keyPress("left"))
	form.handleKey(keyPress(" "))

	want := []time.Duration{15 * time.Minute, 24 * time.Hour}
	got := form.values().Reminders
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("reminders = %v, want %v", got, want)
	}
	if field := stripANSI(form.notifyField()); !strings.Contains(field, "◉15m") || !strings.Contains(field, "◉1d") {
		t.Errorf("the field reads %q, want both chosen", field)
	}

	// And space takes one off again.
	form.handleKey(keyPress(" "))
	if got := form.values().Reminders; len(got) != 1 || got[0] != 15*time.Minute {
		t.Errorf("reminders = %v, want the day's warning gone", got)
	}
}

// --- Behind the More row ---

// The everyday event is a name, a time and a calendar, so the other seven fields are behind a
// row the reader opens with space.
func TestEventFormKeepsTheRestBehindTheMoreRow(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	form.resize(80, 40)

	if slices.Contains(formLabels(form), "Location") {
		t.Errorf("a new event opens with the details showing: %v", formLabels(form))
	}
	if got := form.fields(); got[len(got)-1] != eventFieldMore {
		t.Errorf("tab order ends on %d, want the More row", got[len(got)-1])
	}

	focusOn(form, eventFieldMore)
	form.handleKey(keyPress(" "))

	want := []string{"Calendar", "Name", "All day", "Starts", "Ends", "Notify", "More",
		"Location", "Link", "Invites", "Notes", "Repeat", "Countdown", "Circle event"}
	if got := formLabels(form); !slices.Equal(got, want) {
		t.Errorf("the open form reads %v, want %v", got, want)
	}

	// And space puts it away again.
	form.handleKey(keyPress(" "))
	if slices.Contains(formLabels(form), "Notes") {
		t.Error("space did not close the row again")
	}
}

// An edit never hides what the event already has: a form that shows a name and a time for an
// event with notes and a guest list invites a save that throws both away.
func TestEditEventFormOpensTheMoreRowOnWhatIsThere(t *testing.T) {
	on := time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local)
	event := Recording{ID: 99, Title: "Design review", Type: "Calendar::Event"}

	form := newTestEventForm(eventFormEdit, event, on, eventFormCalendars())
	form.setDetails(eventDetails{})
	if form.revealed {
		t.Error("an event with none of them opened the row")
	}

	for _, details := range []eventDetails{
		{Notes: "Bring the roadmap"},
		{Location: "Studio, 3rd floor"},
		{Link: "https://meet.example.com/design-review"},
		{Invites: []string{"amelia@example.com"}},
		{Circled: true},
		{Repeats: true, RepeatKind: "every_week"},
		{AttachedEntryID: 4088},
	} {
		form := newTestEventForm(eventFormEdit, event, on, eventFormCalendars())
		form.setDetails(details)
		if !form.revealed {
			t.Errorf("%+v did not open the More row", details)
		}
	}
}

// Every field behind the row reaches the save, and the ones HEY replaces wholesale carry what
// the event arrived with rather than nothing.
func TestEventFormDetailsReachTheSave(t *testing.T) {
	form := newTestEventForm(eventFormEdit,
		Recording{ID: 99, Title: "Design review", Type: "Calendar::Event"},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	form.setDetails(eventDetails{
		Notes:           "Bring the roadmap",
		Location:        "Studio, 3rd floor",
		Link:            "https://meet.example.com/design-review",
		Invites:         []string{"amelia@example.com", "kevin@example.org"},
		Circled:         true,
		AttachedEntryID: 4088,
	})

	values := form.values()
	if values.Notes != "<div>Bring the roadmap</div>" {
		t.Errorf("notes = %q, want the rich text HEY stores", values.Notes)
	}
	if values.Location != "Studio, 3rd floor" {
		t.Errorf("location = %q", values.Location)
	}
	if values.Link != "https://meet.example.com/design-review" {
		t.Errorf("link = %q", values.Link)
	}
	if values.AttachedEntryID != 4088 {
		t.Errorf("attached entry = %d, want the one the event arrived with", values.AttachedEntryID)
	}
	if !values.Circled {
		t.Error("the circle came off an event that had one")
	}

	// The notes are typed as text and sent as rich text, with a line break where the reader
	// pressed enter and nothing a terminal or a browser would act on.
	form.notes.SetValue("Bring the roadmap\n<script>alert(1)</script> & the budget")
	if got := form.values().Notes; got != "<div>Bring the roadmap<br>&lt;script&gt;alert(1)&lt;/script&gt; &amp; the budget</div>" {
		t.Errorf("notes = %q", got)
	}

	// A bare host is completed rather than refused: nobody types a scheme for a meeting link,
	// and HEY validates the field as a URL.
	form.link.SetValue("meet.example.com/design-review")
	if got := form.values().Link; got != "https://meet.example.com/design-review" {
		t.Errorf("link = %q, want a scheme on it", got)
	}
}

// HEY replaces an event's guest list with whatever it is sent, so an untouched list is left out
// of the write altogether — and a cleared one is sent empty, which is how every guest goes.
func TestEventFormInvitesLeaveTheRosterAloneUntilTouched(t *testing.T) {
	form := newTestEventForm(eventFormEdit,
		Recording{ID: 99, Title: "Design review", Type: "Calendar::Event"},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	form.setDetails(eventDetails{Invites: []string{"amelia@example.com", "kevin@example.org"}})

	if got := form.values().Invites; got != nil {
		t.Errorf("invites = %v, want nothing sent for a list nobody touched", got)
	}

	form.invites.SetValue("amelia@example.com, joan@example.org kevin@example.org")
	want := []string{"amelia@example.com", "joan@example.org", "kevin@example.org"}
	if got := form.values().Invites; !slices.Equal(got, want) {
		t.Errorf("invites = %v, want %v", got, want)
	}

	// Emptying the field is a decision of its own, and it needs a list to say so with.
	form.invites.SetValue("  ")
	got := form.values().Invites
	if got == nil || len(got) != 0 {
		t.Errorf("invites = %v, want an empty list to clear the roster", got)
	}

	// A new event with nobody on it leaves the roster out: submitting one makes the reader the
	// organizer and sends invitations.
	fresh := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	if got := fresh.values().Invites; got != nil {
		t.Errorf("invites = %v, want none on a new event", got)
	}
}

// The repeat is a frequency and an end, and the end takes three shapes.
func TestEventFormRepeatSaysWhenItStops(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	form.revealed = true

	if got := form.values().Repeat; got != nil {
		t.Errorf("repeat = %+v, want a one-off event to say nothing about recurrence", got)
	}

	// The frequency steps with the arrows, and the end appears once there is something to end.
	focusOn(form, eventFieldRepeat)
	form.handleKey(keyPress("right"))
	form.handleKey(keyPress("right"))
	form.handleKey(keyPress("right"))
	want := &hey.RepeatParams{Frequency: hey.RepeatEveryWeek, Until: hey.RepeatUntilForever}
	if got := form.values().Repeat; got == nil || *got != *want {
		t.Fatalf("repeat = %+v, want %+v", got, want)
	}
	if !slices.Contains(form.fields(), eventFieldRepeatUntil) {
		t.Error("a repeating event was not asked when it stops")
	}

	// Until a date.
	focusOn(form, eventFieldRepeatUntil)
	form.handleKey(keyPress("right"))
	form.repeat.untilDate.SetValue("2026-12-31")
	want = &hey.RepeatParams{
		Frequency: hey.RepeatEveryWeek, Until: hey.RepeatUntilDate, UntilDate: "2026-12-31",
	}
	if got := form.values().Repeat; got == nil || *got != *want {
		t.Errorf("repeat = %+v, want %+v", got, want)
	}

	// Or for a number of times.
	form.handleKey(keyPress("right"))
	form.repeat.count.SetValue("12")
	want = &hey.RepeatParams{Frequency: hey.RepeatEveryWeek, Until: hey.RepeatUntilCount, Count: 12}
	if got := form.values().Repeat; got == nil || *got != *want {
		t.Errorf("repeat = %+v, want %+v", got, want)
	}
	if got := stripANSI(form.repeatField()); !strings.Contains(got, "times") {
		t.Errorf("the row reads %q, want the count to say what it counts", got)
	}
}

// An event that already repeats opens on keeping the schedule it has, and says which one that
// is: HEY serves the kind of schedule and neither the date nor the count it runs until, so
// sending the frequency back would rewrite a series that stops in December as one that never
// stops.
func TestEditingARepeatingEventOffersToKeepItsSchedule(t *testing.T) {
	form := newTestEventForm(eventFormEdit,
		Recording{ID: 99, Title: "Design review", Type: "Calendar::Event"},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	form.setDetails(eventDetails{Repeats: true, RepeatKind: "every_other_week"})

	if got := stripANSI(form.repeatField()); got != "keeps every other week" {
		t.Errorf("the row reads %q, want the schedule it is leaving alone", got)
	}
	if got := form.values().Repeat; got != nil {
		t.Errorf("repeat = %+v, want nothing sent for a schedule nobody changed", got)
	}

	// A schedule this build has no name for is still kept rather than described wrongly.
	form.setDetails(eventDetails{Repeats: true, RepeatKind: "every_third_thursday"})
	if got := stripANSI(form.repeatField()); got != "keeps its schedule" {
		t.Errorf("the row reads %q", got)
	}
}

// A countdown is only sent when there is one to send, and HEY reads the absence as "delete the
// one this event had" — which is the whole reason the form says so on an edit.
func TestEventFormCountdownIsNoneUntilItIsAskedFor(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	form.revealed = true

	if got := form.values().Countdown; got != (hey.CountdownParams{}) {
		t.Errorf("countdown = %+v, want none", got)
	}
	if slices.Contains(form.fields(), eventFieldCountdownUnit) {
		t.Error("the form asked for a unit with no number to give it")
	}

	focusOn(form, eventFieldCountdown)
	form.handleKey(keyPress("3"))
	if !slices.Contains(form.fields(), eventFieldCountdownUnit) {
		t.Fatal("a number did not bring out the unit")
	}
	focusOn(form, eventFieldCountdownUnit)
	form.handleKey(keyPress("right"))

	want := hey.CountdownParams{Value: 3, Unit: hey.CountdownUnitWeeks}
	if got := form.values().Countdown; got != want {
		t.Errorf("countdown = %+v, want %+v", got, want)
	}

	// An edit says out loud what an empty field costs, since HEY serves a countdown on nothing
	// and the form cannot show what the event has.
	edit := newTestEventForm(eventFormEdit,
		Recording{ID: 99, Title: "Design review", Type: "Calendar::Event"},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	edit.setDetails(eventDetails{Location: "Studio, 3rd floor"})
	if !strings.Contains(edit.caution(), "countdown") {
		t.Errorf("an edit says %q about the countdown", edit.caution())
	}
	if strings.Contains(edit.caution(), "Notes") {
		t.Errorf("an event with no notes was warned about them: %q", edit.caution())
	}

	// And an event that arrived with notes is told they save as typed.
	edit.setDetails(eventDetails{Notes: "Bring the roadmap"})
	if !strings.Contains(edit.caution(), "Notes save as typed") {
		t.Errorf("an event with notes says %q", edit.caution())
	}
	if form.caution() != "" {
		t.Errorf("a new event has nothing to lose, but says %q", form.caution())
	}
}

// The circle is HEY's highlighted flag, and the form calls it what the web app calls it.
func TestEventFormCirclesTheEvent(t *testing.T) {
	form := newTestEventForm(eventFormCreate, Recording{},
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local), eventFormCalendars())
	form.revealed = true

	if form.values().Circled {
		t.Error("a new event arrived circled")
	}
	focusOn(form, eventFieldCircle)
	form.handleKey(keyPress(" "))
	if !form.values().Circled {
		t.Error("space did not circle the event")
	}
}

// What the reader typed behind the row is refused here rather than after a round trip, and the
// row opens on the refusal: a message about a field nobody can see is no help at all.
func TestEventFormRefusesWhatIsBehindTheMoreRow(t *testing.T) {
	on := time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local)

	for _, tt := range []struct {
		name  string
		spoil func(*eventForm)
		want  string
	}{
		{"a link that is not one", func(f *eventForm) {
			f.link.SetValue("ask amelia for the link")
		}, "Link must be a web address"},
		{"an invite that is not an address", func(f *eventForm) {
			f.invites.SetValue("amelia@example.com, kevin")
		}, `"kevin" is not an email address`},
		{"a countdown of nonsense", func(f *eventForm) {
			f.countdown.SetValue("soon")
		}, "Countdown must be 1 to 30"},
		{"a countdown HEY will not run", func(f *eventForm) {
			f.countdown.SetValue("90")
		}, "Countdown must be 1 to 30"},
		{"repeating until a day that is not one", func(f *eventForm) {
			f.repeat.choice = 1
			f.repeat.until = 1
			f.repeat.untilDate.SetValue("next Christmas")
		}, "Repeat until must be YYYY-MM-DD"},
		{"repeating a number of times nobody counted", func(f *eventForm) {
			f.repeat.choice = 1
			f.repeat.until = 2
		}, "Repeat for must be 1 to 9999 times"},
	} {
		form := newTestEventForm(eventFormCreate, Recording{}, on, eventFormCalendars())
		form.title.SetValue("Design review")
		tt.spoil(form)

		if got := form.validate(); got != tt.want {
			t.Errorf("%s: validate = %q, want %q", tt.name, got, tt.want)
		}

		// The row was closed while it was typed into, which only takes hiding it again — the
		// refusal brings it back so the reader can see the field it is about.
		form.revealed = false
		form.handleKey(keyPress("ctrl+s"))
		if !form.revealed {
			t.Errorf("%s: the refusal left the field hidden", tt.name)
		}
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
	// The clock times on the wire are UTC. HEY parses one with Time.zone.parse, and
	// ApiRequest#set_utc_timezone makes that UTC for an API request, so a local 09:00 sent as
	// it stands is stored an offset away and shifted again on every save.
	starts := time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local).UTC()
	for field, want := range map[string]string{
		"calendar_event[summary]":        "Design review",
		"calendar_event[starts_at]":      starts.Format("2006-01-02"),
		"calendar_event[all_day]":        "0",
		"calendar_event[starts_at_time]": starts.Format("15:04:05"),
	} {
		if got := form.Get(field); got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
	if zone := form.Get("calendar_event[starts_at_time_zone_name]"); zone != "" {
		t.Errorf("zone = %q, want none — the times are already UTC", zone)
	}
}

// Editing patches the event it was opened on.
func TestEditEventPatchesIt(t *testing.T) {
	v, recorded := calendarWithEventServer(t)
	v.selectedEvent = "1"

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
	form, _ := url.ParseQuery(bodies[0])
	if form.Get("calendar_event[summary]") != "Renamed" {
		t.Errorf("body = %q", bodies[0])
	}
	// The calendar rides along on an update, which is what lets an edit move the event.
	if got := form.Get("calendar_event[calendar_id]"); got != "240334" {
		t.Errorf("calendar_id = %q, want the calendar the form is on", got)
	}
}

// Deleting asks twice, because an event off a shared calendar is gone for everybody on it.
func TestDeleteEventAsksTwice(t *testing.T) {
	v, recorded := calendarWithEventServer(t)
	v.selectedEvent = "1"

	if cmd := v.HandleContentKey(keyPress("x")); cmd != nil {
		t.Fatal("the first x deleted without asking")
	}
	if v.confirmDelete != "1" {
		t.Errorf("confirmDelete = %q, want the selected event", v.confirmDelete)
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
	if requests, _ := recorded.snapshot(); len(requests) != 1 || requests[0] != "DELETE /calendar/events/1.json" {
		t.Errorf("requests = %v", requests)
	}
}

// Any other key is the reader changing their mind, so x has to be asked again.
func TestAnotherKeyForgetsTheDeleteAsk(t *testing.T) {
	v, _ := calendarWithEventServer(t)
	v.selectedEvent = "1"

	v.HandleContentKey(keyPress("x"))
	v.HandleContentKey(keyPress("right"))
	if v.confirmDelete != "" {
		t.Errorf("confirmDelete = %q, want it forgotten", v.confirmDelete)
	}
}
