package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func typeInto(t *testing.T, picker *dateTimePicker, text string) {
	t.Helper()
	for _, r := range text {
		picker.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func testPicker() *dateTimePicker {
	at := time.Date(2026, 8, 22, 9, 30, 0, 0, time.UTC)
	picker := newDateTimePicker(at, false)
	picker.choices = []string{localZoneLabel, "Europe/Zagreb", "Europe/Madrid", "America/New_York"}
	return picker
}

func TestDateTimePickerReadsAndWritesAMoment(t *testing.T) {
	picker := testPicker()

	if got := picker.date(); got != "2026-08-22" {
		t.Errorf("date() = %q, want 2026-08-22", got)
	}
	if got := picker.clock(); got != "09:30" {
		t.Errorf("clock() = %q, want 09:30", got)
	}
	if got := picker.problem(); got != "" {
		t.Errorf("problem() = %q, want none", got)
	}

	picker.dateInput.SetValue("2026-12-01")
	picker.timeInput.SetValue("17:45")
	at, ok := picker.moment()
	if !ok {
		t.Fatal("moment() refused a filled-in picker")
	}
	if got := at.Format("2006-01-02 15:04"); got != "2026-12-01 17:45" {
		t.Errorf("moment() = %q, want 2026-12-01 17:45", got)
	}
}

func TestDateTimePickerDefaultsToLocal(t *testing.T) {
	picker := testPicker()

	if got := picker.zoneName(); got != "" {
		t.Errorf("zoneName() = %q, want empty for the reader's own clock", got)
	}
	if !strings.Contains(picker.view(), localZoneLabel) {
		t.Errorf("view() does not show the zone:\n%s", picker.view())
	}
}

func TestDateTimePickerStepsTheDateByADay(t *testing.T) {
	picker := testPicker()
	picker.focusFirst()

	picker.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := picker.date(); got != "2026-08-23" {
		t.Errorf("after up, date() = %q, want 2026-08-23", got)
	}

	picker.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	picker.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := picker.date(); got != "2026-08-21" {
		t.Errorf("after two downs, date() = %q, want 2026-08-21", got)
	}

	typeInto(t, picker, "+")
	if got := picker.date(); got != "2026-08-22" {
		t.Errorf("after +, date() = %q, want 2026-08-22", got)
	}

	picker.dateInput.SetValue("next tuesday")
	picker.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := picker.date(); got != "next tuesday" {
		t.Errorf("stepping an unreadable date changed it to %q", got)
	}
}

func TestDateTimePickerAllDayHidesTheTimeAndTheZone(t *testing.T) {
	picker := testPicker()
	picker.setZoneName("Europe/Zagreb")
	picker.setAllDay(true)

	if got := picker.clock(); got != "" {
		t.Errorf("clock() = %q, want empty for an all-day moment", got)
	}
	if got := picker.zoneName(); got != "" {
		t.Errorf("zoneName() = %q, want empty for an all-day moment", got)
	}
	if got := picker.fieldCount(); got != 1 {
		t.Errorf("fieldCount() = %d, want 1", got)
	}

	view := picker.view()
	if strings.Contains(view, "09:30") || strings.Contains(view, "Zagreb") {
		t.Errorf("all-day view shows a time or a zone:\n%s", view)
	}
	if !strings.Contains(view, "2026-08-22") {
		t.Errorf("all-day view does not show the date:\n%s", view)
	}
	if got := picker.problem(); got != "" {
		t.Errorf("problem() = %q, want none", got)
	}
}

func TestDateTimePickerZoneFilterPicksAMatch(t *testing.T) {
	picker := testPicker()
	picker.focusField(dateTimeFieldZone)

	typeInto(t, picker, "zag")
	if !picker.capturesKeys() {
		t.Fatal("typing at the zone did not open the list")
	}
	if got := picker.zoneMatches; !slices.Equal(got, []string{"Europe/Zagreb"}) {
		t.Fatalf("zoneMatches = %v, want [Europe/Zagreb]", got)
	}

	picker.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := picker.zoneName(); got != "Europe/Zagreb" {
		t.Errorf("zoneName() = %q, want Europe/Zagreb", got)
	}
	if picker.capturesKeys() {
		t.Error("the list stayed open after enter")
	}
	if got := picker.problem(); got != "" {
		t.Errorf("problem() = %q, want none", got)
	}
}

func TestDateTimePickerZoneListStepsAndCancels(t *testing.T) {
	picker := testPicker()
	picker.focusField(dateTimeFieldZone)
	picker.setZoneName("Europe/Madrid")

	typeInto(t, picker, "europe")
	picker.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	picker.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	if got := picker.zoneName(); got != "Europe/Madrid" {
		t.Errorf("esc changed the zone to %q", got)
	}
	if picker.capturesKeys() {
		t.Error("esc left the list open")
	}
}

func TestMatchingZonesReadsUnderscoresAsSpaces(t *testing.T) {
	choices := []string{localZoneLabel, "America/New_York", "Europe/Madrid"}

	if got := matchingZones(choices, "new york"); !slices.Equal(got, []string{"America/New_York"}) {
		t.Errorf("matchingZones(%q) = %v", "new york", got)
	}
	if got := matchingZones(choices, "  "); !slices.Equal(got, choices) {
		t.Errorf("an empty filter narrowed the list to %v", got)
	}
	if got := matchingZones(choices, "atlantis"); len(got) != 0 {
		t.Errorf("matchingZones(%q) = %v, want nothing", "atlantis", got)
	}
}

func TestDateTimePickerProblems(t *testing.T) {
	picker := testPicker()

	picker.dateInput.SetValue("22 August")
	if got := picker.problem(); got != "Date must be YYYY-MM-DD" {
		t.Errorf("problem() = %q, want the date complaint", got)
	}

	picker.dateInput.SetValue("2026-08-22")
	picker.timeInput.SetValue("half nine")
	if got := picker.problem(); got != "Time must be HH:MM" {
		t.Errorf("problem() = %q, want the time complaint", got)
	}

	picker.timeInput.SetValue("09:30")
	picker.setZoneName("Mars/Olympus_Mons")
	if got := picker.problem(); got != "That is not a time zone" {
		t.Errorf("problem() = %q, want the zone complaint", got)
	}
	if _, ok := picker.moment(); ok {
		t.Error("moment() answered on a zone that does not exist")
	}
}

func TestDateTimePickerStepsOffItsOwnEnds(t *testing.T) {
	picker := testPicker()
	if cmd := picker.focusFirst(); cmd == nil {
		t.Error("focusing the date gave no cursor command")
	}

	if cmd := picker.handleKey(tea.KeyPressMsg{Code: '3', Text: "3"}); cmd == nil {
		t.Error("typing into the date gave no cursor command")
	}

	cmd, within := picker.step(1)
	if !within {
		t.Error("stepping from the date left the picker")
	}
	if cmd == nil {
		t.Error("focusing the time gave no cursor command")
	}
	picker.focusLast()
	if cmd, within := picker.step(1); within || cmd != nil {
		t.Error("stepping past the last field stayed in the picker")
	}
	picker.focusFirst()
	if cmd, within := picker.step(-1); within || cmd != nil {
		t.Error("stepping before the first field stayed in the picker")
	}
}

func TestDateTimePickerHelpFollowsTheField(t *testing.T) {
	picker := testPicker()
	picker.focusField(dateTimeFieldZone)
	typeInto(t, picker, "eu")

	if got := picker.helpBindings(); len(got) == 0 || got[0].key != "type" {
		t.Errorf("helpBindings() = %v, want the zone list's keys", got)
	}
	picker.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := picker.helpBindings(); len(got) == 0 || got[0].desc != "choose zone" {
		t.Errorf("helpBindings() = %v, want the zone field's keys", got)
	}
}

func TestZoneNamesFallBackWhenZoneinfoIsUnreadable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-zoneinfo-here")

	names := zoneNamesFrom([]string{missing})
	if !slices.Equal(names, fallbackZoneNames) {
		t.Fatalf("zoneNamesFrom(%q) = %v, want the fallback list", missing, names)
	}
	if !slices.IsSorted(fallbackZoneNames) {
		t.Error("the fallback list is not sorted")
	}
	for _, name := range fallbackZoneNames {
		if !strings.Contains(name, "/") {
			t.Errorf("fallback zone %q is not an IANA area/location name", name)
		}
	}
}

func TestZoneChoicesLeadWithLocal(t *testing.T) {
	choices := zoneChoices()

	if len(choices) == 0 || choices[0] != localZoneLabel {
		t.Fatalf("zoneChoices() starts with %v, want %q first", choices, localZoneLabel)
	}
	if local := localZoneName(); local != "" {
		if len(choices) < 2 || choices[1] != local {
			t.Errorf("zoneChoices() = %v..., want %q second", choices[:min(len(choices), 3)], local)
		}
		if slices.Contains(choices[2:], local) {
			t.Errorf("%q is listed twice", local)
		}
	}
	for _, name := range choices[1:] {
		if !strings.Contains(name, "/") {
			t.Errorf("zone choice %q is not an IANA area/location name", name)
		}
	}
}
