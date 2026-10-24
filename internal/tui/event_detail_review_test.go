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
	inZone(t, "America/New_York")
	for _, test := range []struct {
		name, start, end, want string
	}{
		{"spring", "2026-03-08T00:00:00Z", "2026-03-09T00:00:00Z", "Sunday, March 8 – Monday, March 9 · all day"},
		{"fall", "2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z", "Sunday, November 1 – Monday, November 2 · all day"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := &eventDetail{event: Recording{AllDay: true, StartsAt: at(test.start), EndsAt: at(test.end)}}
			if got := d.when(); got != test.want {
				t.Fatalf("when() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEventCardScrollsLongNotes(t *testing.T) {
	v := cardDay(t)
	v.events[0].Notes = strings.Repeat("Review the milestones.\n", 40) + "Final decision"
	v.HandleContentKey(keyPress("right"))
	v.HandleContentKey(keyPress("enter"))
	if strings.Contains(stripANSI(v.detail.view()), "Final decision") {
		t.Fatal("long notes already fit without scrolling")
	}
	for range 4 {
		v.HandleContentKey(keyPress("pgdown"))
	}
	if got := stripANSI(v.detail.view()); !strings.Contains(got, "Final decision") {
		t.Fatalf("page down did not reveal the end of the notes: %s", got)
	}
	v.HandleContentKey(keyPress("pgup"))
	if strings.Contains(stripANSI(v.detail.view()), "Final decision") {
		t.Fatal("page up did not move back through the notes")
	}
}

func TestEventCardKeepsDetailsWhenEditUnavailable(t *testing.T) {
	v := cardDay(t)
	v.calendars = nil
	v.HandleContentKey(keyPress("right"))
	v.HandleContentKey(keyPress("enter"))
	v.HandleContentKey(keyPress("e"))
	if v.eventForm != nil || v.detail == nil {
		t.Fatal("failed edit replaced the read-only card")
	}
}

func TestEventCardSanitizesServerFields(t *testing.T) {
	d := newEventDetail(Recording{
		Title: "Roadmap\x1b[2J review", Location: "Design\u202e studio",
		Link:      "https://meet.example.com/roadmap\x1b[2J",
		Attendees: []string{"ana@example.com\u202e"}, Notes: "Bring\x1b[2J the report",
	}, "Design\u202e Team", false, testVC().styles, 100, 30)
	view := d.view()
	for _, unsafe := range []string{"\x1b[2J", "\u202e"} {
		if strings.Contains(view, unsafe) {
			t.Errorf("card contains unsafe sequence %q", unsafe)
		}
	}
	if !strings.Contains(stripANSI(view), "Bring the report") {
		t.Fatal("sanitizing discarded readable notes")
	}
}

func TestEventCardEditUsesTheLatestRecording(t *testing.T) {
	v := cardDay(t)
	v.HandleContentKey(keyPress("right"))
	v.requestRecordings()
	v.HandleContentKey(keyPress("enter"))
	latest := v.events[0]
	latest.Notes = "Bring the updated October report"
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{latest}})
	v.HandleContentKey(keyPress("e"))
	if v.eventForm == nil || v.editing.Notes != latest.Notes {
		t.Fatal("edit reused the card's stale event after an in-flight read completed")
	}
}

func TestEventCardCannotEditARemovedRecording(t *testing.T) {
	v := cardDay(t)
	v.HandleContentKey(keyPress("right"))
	v.requestRecordings()
	v.HandleContentKey(keyPress("enter"))
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v)})
	v.HandleContentKey(keyPress("e"))
	if v.eventForm != nil || v.detail == nil {
		t.Fatal("edit opened a form for an event removed by an in-flight read")
	}
}
