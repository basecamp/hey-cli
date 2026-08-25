package tui

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// --- The form on its own ---

func TestCalendarSettingsFormWalksTheWeekDays(t *testing.T) {
	form := newCalendarSettingsForm(time.Monday, false)

	form.handleKey(keyPress("down"))
	if form.weekStart != time.Tuesday {
		t.Errorf("down from Monday = %v, want Tuesday", form.weekStart)
	}
	form.handleKey(keyPress("up"))
	form.handleKey(keyPress("up"))
	if form.weekStart != time.Sunday {
		t.Errorf("two up from Tuesday = %v, want Sunday", form.weekStart)
	}
	// The week wraps at either end rather than stopping.
	form.handleKey(keyPress("left"))
	if form.weekStart != time.Saturday {
		t.Errorf("left from Sunday = %v, want Saturday", form.weekStart)
	}
	form.handleKey(keyPress("right"))
	if form.weekStart != time.Sunday {
		t.Errorf("right from Saturday = %v, want Sunday", form.weekStart)
	}
}

func TestCalendarSettingsFormTogglesTheClock(t *testing.T) {
	form := newCalendarSettingsForm(time.Monday, false)

	// Space belongs to the clock field, so it does nothing while the weekday has the focus.
	form.handleKey(keyPress(" "))
	if form.use24 {
		t.Error("space on the weekday field should not touch the clock")
	}

	form.handleKey(keyPress("tab"))
	form.handleKey(keyPress(" "))
	if !form.use24 {
		t.Error("space on the clock field should toggle it on")
	}
	form.handleKey(keyPress(" "))
	if form.use24 {
		t.Error("space again should toggle it back off")
	}
}

func TestCalendarSettingsFormSubmitsOnlyWhatChanged(t *testing.T) {
	form := newCalendarSettingsForm(time.Monday, false)

	if form.handleKey(keyPress("ctrl+s")) {
		t.Fatal("an untouched form should not submit")
	}
	if form.status != "Nothing changed" || form.saving {
		t.Errorf("untouched form said %q, saving=%v", form.status, form.saving)
	}

	form.handleKey(keyPress("down"))
	if !form.handleKey(keyPress("ctrl+s")) {
		t.Fatal("a changed form should submit")
	}
	if !form.saving {
		t.Error("a submitted form should say it is saving")
	}
	// A saving form is deaf: nothing may change under a write already on the wire.
	form.handleKey(keyPress("down"))
	if form.weekStart != time.Tuesday {
		t.Errorf("a saving form moved its week start to %v", form.weekStart)
	}
}

func TestCalendarSettingsFormViewNamesBothSettings(t *testing.T) {
	form := newCalendarSettingsForm(time.Wednesday, true)
	form.resize(80, 24)
	view := stripANSI(form.view())
	if !strings.Contains(view, "Start weeks on") || !strings.Contains(view, "Wednesday") {
		t.Errorf("the week start row is missing: %q", view)
	}
	if !strings.Contains(view, "Use 24-hour time") || !strings.Contains(view, "◉ yes") {
		t.Errorf("the clock row is missing: %q", view)
	}
}

// --- The form on the calendar ---

func TestCalendarViewOpensSettingsWithComma(t *testing.T) {
	v := calendarWithRecordings()
	v.Update(identityLoadedMsg{firstWeekDay: time.Sunday, use24Hour: true})

	v.HandleContentKey(keyPress(","))
	if v.settings == nil {
		t.Fatal(", should open the settings form")
	}
	// The model routes every key to a capturing view — without this, tab keeps moving the
	// focus between the lanes behind the modal instead of between the form's fields.
	if !v.CapturingInput() {
		t.Error("an open settings form should capture input")
	}
	if v.settings.weekStart != time.Sunday || !v.settings.use24 {
		t.Errorf("the form opened on %v/%v rather than what the identity holds",
			v.settings.weekStart, v.settings.use24)
	}
	if !strings.Contains(stripANSI(v.View()), "Calendar settings") {
		t.Error("the open form is not drawn")
	}

	v.HandleContentKey(keyPress("esc"))
	if v.settings != nil {
		t.Error("esc should close the settings form")
	}
}

func TestCalendarViewSavesSettings(t *testing.T) {
	v := calendarWithRecordings()
	v.Update(identityLoadedMsg{firstWeekDay: time.Monday, use24Hour: false})

	v.HandleContentKey(keyPress(","))
	v.HandleContentKey(keyPress("tab"))
	v.HandleContentKey(keyPress(" "))
	cmd := v.HandleContentKey(keyPress("ctrl+s"))
	if cmd == nil {
		t.Fatal("saving should return a command")
	}
	if v.requests.kind != calendarRequestMutation {
		t.Errorf("the save rides %v rather than the mutation lane", v.requests.kind)
	}

	_, consumed := v.Update(calendarSettingsSavedMsg{
		requestResult: currentRequest(v),
		firstWeekDay:  time.Monday,
		use24Hour:     true,
	})
	if !consumed {
		t.Fatal("calendarSettingsSavedMsg should be consumed")
	}
	if v.settings != nil {
		t.Error("a landed save should close the form")
	}
	if !v.use24Hour {
		t.Error("a landed save should move the view onto the new clock")
	}
}

func TestCalendarViewKeepsSettingsFormOnAFailedSave(t *testing.T) {
	v := calendarWithRecordings()
	v.Update(identityLoadedMsg{firstWeekDay: time.Monday, use24Hour: false})

	v.HandleContentKey(keyPress(","))
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("ctrl+s"))

	v.Update(calendarSettingsSavedMsg{
		requestResult: requestResult{requestID: v.requests.id, err: errors.New("boom")},
		firstWeekDay:  time.Tuesday,
	})
	if v.settings == nil {
		t.Fatal("a failed save should keep the form open")
	}
	if v.settings.saving || !v.settings.isError || v.settings.status == "" {
		t.Errorf("the failure is not on the form: saving=%v isError=%v status=%q",
			v.settings.saving, v.settings.isError, v.settings.status)
	}
	if v.firstWeekDay != time.Monday {
		t.Error("a failed save should leave the view's week start alone")
	}
}

func TestIdentityLoadedCarriesTheClock(t *testing.T) {
	v := calendarWithRecordings()
	v.Update(identityLoadedMsg{firstWeekDay: time.Saturday, use24Hour: true})
	if v.firstWeekDay != time.Saturday || !v.use24Hour {
		t.Errorf("identityLoadedMsg landed as %v/%v", v.firstWeekDay, v.use24Hour)
	}
}

// --- The clock the views draw ---

func TestClockTimeReadsBothClocks(t *testing.T) {
	moment := time.Date(2026, 8, 20, 15, 5, 0, 0, time.Local)
	if got := clockTime(moment, true); got != "15:05" {
		t.Errorf("24-hour = %q", got)
	}
	if got := clockTime(moment, false); got != "3:05pm" {
		t.Errorf("12-hour = %q", got)
	}
	midnight := time.Date(2026, 8, 20, 0, 30, 0, 0, time.Local)
	if got := clockTime(midnight, false); got != "12:30am" {
		t.Errorf("12-hour midnight = %q", got)
	}
}

func TestEventTimeSpanOnTheTwelveHourClock(t *testing.T) {
	event := Recording{
		StartsAt: atLocal("2026-08-20T14:00:00"),
		EndsAt:   atLocal("2026-08-20T15:30:00"),
	}
	if got := eventTimeSpan(event, 20, false); got != "2:00pm–3:30pm" {
		t.Errorf("wide column = %q", got)
	}
	// A column too narrow for the span gets the start alone, as it does on the 24-hour clock.
	if got := eventTimeSpan(event, 10, false); got != "2:00pm" {
		t.Errorf("narrow column = %q", got)
	}
}

func TestHourAxisLabels(t *testing.T) {
	h24 := hourAxisLabels(true)
	if h24[0] != "00" || h24[13] != "13" || h24[23] != "23" {
		t.Errorf("24-hour axis = %v", h24)
	}
	h12 := hourAxisLabels(false)
	if h12[0] != "12a" || h12[1] != "1a" || h12[12] != "12p" || h12[23] != "11p" {
		t.Errorf("12-hour axis = %v", h12)
	}
}

func TestDayViewAxisFollowsTheClockSetting(t *testing.T) {
	day := time.Date(2026, 8, 20, 0, 0, 0, 0, time.Local)
	twelve := stripANSI(renderDayView(nil, nil, nil, day, calendarFixtureNow(), "", 100, 16, selection{}, nil, false))
	if !strings.Contains(twelve, "12a") || !strings.Contains(twelve, "12p") {
		t.Errorf("the 12-hour axis is missing its halves:\n%s", twelve)
	}
	twentyFour := stripANSI(renderDayView(nil, nil, nil, day, calendarFixtureNow(), "", 100, 16, selection{}, nil, true))
	if !strings.Contains(twentyFour, "13") || strings.Contains(twentyFour, "12a") {
		t.Errorf("the 24-hour axis is not two digits an hour:\n%s", twentyFour)
	}
}

func TestNowClockOnTheTwelveHourClock(t *testing.T) {
	on := time.Date(2026, 8, 20, 15, 5, 0, 0, time.Local)
	if got := nowClock(on, false); got != "3:05pm" {
		t.Errorf("lit = %q", got)
	}
	if got := nowClock(on.Add(time.Second), false); got != "3 05pm" {
		t.Errorf("blinked = %q", got)
	}
}

func TestTrackedTimeRowsFollowTheClockSetting(t *testing.T) {
	track := trackedTime{
		ID:       1,
		StartsAt: time.Date(2026, 8, 20, 13, 0, 0, 0, time.Local),
		EndsAt:   time.Date(2026, 8, 20, 14, 30, 0, 0, time.Local),
		Category: "Client work",
	}
	screen := newTrackedTimeScreen(false)
	screen.resize(100, 20)
	screen.setTracks([]trackedTime{track}, nil)
	if view := stripANSI(screen.view()); !strings.Contains(view, "1:00pm – 2:30pm") {
		t.Errorf("the 12-hour row is missing: %q", view)
	}

	screen = newTrackedTimeScreen(true)
	screen.resize(100, 20)
	screen.setTracks([]trackedTime{track}, nil)
	if view := stripANSI(screen.view()); !strings.Contains(view, "13:00 – 14:30") {
		t.Errorf("the 24-hour row is missing: %q", view)
	}
}
