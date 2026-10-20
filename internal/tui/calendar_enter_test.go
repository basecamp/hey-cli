package tui

import (
	"strings"
	"testing"
	"time"
)

func TestCalendarEnterShowsEventNotes(t *testing.T) {
	for _, test := range []struct {
		name      string
		mode      calendarViewMode
		selectKey string
	}{
		{name: "day", mode: viewDay, selectKey: "right"},
		{name: "week", mode: viewWeek, selectKey: "down"},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := newCalendarView(testVC())
			v.Resize(100, 30)
			v.viewMode = test.mode
			v.now = func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local) }
			v.Update(calendarsLoadedMsg{calendars: testCalendars()})
			v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
				{ID: 5, Title: "Roadmap review", Type: "Calendar::Event", CalendarID: 10,
					StartsAt: atLocal("2026-08-20T14:00:00"), EndsAt: atLocal("2026-08-20T15:00:00"),
					Notes: "Bring the September report"},
			}})
			v.HandleContentKey(keyPress(test.selectKey))
			v.HandleContentKey(keyPress("enter"))
			if !strings.Contains(stripANSI(v.View()), "Bring the September report") {
				t.Fatal("Enter did not show the selected event's notes")
			}
		})
	}
}

func TestCalendarOpensAllDayEventOnInclusiveEndDate(t *testing.T) {
	inZone(t, "America/New_York")
	v := newCalendarView(testVC())
	v.Resize(100, 30)
	v.now = func() time.Time { return time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local) }
	v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 5, Title: "Design offsite", Type: "Calendar::Event", CalendarID: 10, AllDay: true,
			StartsAt: at("2026-08-20T00:00:00Z"), EndsAt: at("2026-08-22T00:00:00Z")},
	}})
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("enter"))
	if !strings.Contains(stripANSI(v.View()), "Thursday, August 20 – Saturday, August 22 · all day") {
		t.Fatal("the event could not be opened on its inclusive final date")
	}
}

func TestTimedMidnightEndDoesNotOccupyTheNextDay(t *testing.T) {
	inZone(t, "Etc/UTC")
	event := Recording{ID: 5, Title: "Overnight maintenance",
		StartsAt: at("2026-08-20T22:00:00Z"), EndsAt: at("2026-08-22T00:00:00Z")}
	days := eventsByDate([]Recording{event})
	for _, day := range []string{"2026-08-20", "2026-08-21"} {
		if len(days[day]) != 1 {
			t.Errorf("timed event missing from %s", day)
		}
	}
	if len(days["2026-08-22"]) != 0 {
		t.Fatal("a timed midnight end occupied the next day")
	}
}
