package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEventCardAdoptsLateClockPreference(t *testing.T) {
	v := cardDay(t)
	v.HandleContentKey(keyPress("right"))
	v.HandleContentKey(keyPress("enter"))
	v.Update(identityLoadedMsg{read: v.identityRead + 1, firstWeekDay: time.Monday, use24Hour: true})
	if got := stripANSI(v.detail.view()); !strings.Contains(got, "2:00pm") {
		t.Fatalf("stale identity changed the card's clock: %s", got)
	}
	v.Update(identityLoadedMsg{read: v.identityRead, firstWeekDay: time.Monday, use24Hour: true})
	if got := stripANSI(v.detail.view()); !strings.Contains(got, "14:00") {
		t.Fatalf("card did not adopt 24-hour preference: %s", got)
	}
}

func TestEventCardContentFitsNarrowWidths(t *testing.T) {
	for _, width := range []int{7, 8, 10, 16, 40, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			d := newEventDetail(Recording{
				StartsAt: atLocal("2026-08-20T14:00:00"), EndsAt: atLocal("2026-08-20T15:00:00"),
				Recurring: true, RepeatKind: "every_third_thursday", Location: "Conference room",
				Link: "https://meet.example.com/roadmap", Attendees: []string{"ana@example.com"},
				Notes: "Bring the September report\n\nDiscuss the October milestones.",
			}, "Design Team", false, testVC().styles, width, 30)
			for _, line := range strings.Split(d.content(), "\n") {
				plain := stripANSI(line)
				if displayWidth(plain) > modalContentWidth(width) {
					t.Errorf("line exceeds %d cells: %q", modalContentWidth(width), plain)
				}
			}
		})
	}
}

func TestEventCardOmitsWhitespaceOnlyFields(t *testing.T) {
	d := newEventDetail(Recording{
		Location: " \t\x1b[31m ", Link: "   ", Attendees: []string{" ", "\t"}, Notes: " \n\t",
	}, strings.Repeat(" ", 100), false, testVC().styles, 40, 30)
	content := stripANSI(d.content())
	for _, label := range []string{"Calendar", "Location", "Link", "Guests", "Notes"} {
		if strings.Contains(content, label) {
			t.Errorf("whitespace-only %s field shown: %q", label, content)
		}
	}
}

func TestEventCardAllDayEndAcrossDaylightSaving(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	local := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = local })
	d := &eventDetail{event: Recording{
		AllDay:   true,
		StartsAt: time.Date(2026, time.March, 8, 0, 0, 0, 0, zone),
		EndsAt:   time.Date(2026, time.March, 9, 0, 0, 0, 0, zone),
	}}
	if got, want := d.when(), "Sunday, March 8 · all day"; got != want {
		t.Fatalf("when() = %q, want %q", got, want)
	}
}
