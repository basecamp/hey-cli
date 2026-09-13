package tui

import (
	"strings"
	"testing"
	"time"
)

func TestEventDetailOpensOnlyHostedWebLinks(t *testing.T) {
	for _, test := range []struct {
		name string
		link string
		want bool
	}{
		{name: "http link", link: "http://meet.example.com/roadmap", want: true},
		{name: "HTTPS link (uppercase scheme)", link: "HTTPS://meet.example.com/roadmap", want: true},
		{name: "hostless HTTPS link", link: "https:roadmap", want: false},
		{name: "empty HTTPS host", link: "https://", want: false},
		{name: "file link", link: "file:///etc/passwd", want: false},
		{name: "application scheme", link: "zoomus://zoom.us/join/123", want: false},
		{name: "empty link", link: "", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			detail := &eventDetail{event: Recording{Link: test.link}}
			_, got := detail.openableLink()
			if got != test.want {
				t.Errorf("openableLink(%q) = %v, want %v", test.link, got, test.want)
			}
		})
	}
}

// Enter on the week view opens the selected event's card — the same as on the day view.
func TestEnterOpensTheEventCardOnWeekView(t *testing.T) {
	v := newCalendarView(testVC())
	v.Resize(100, 30)
	v.viewMode = viewWeek
	v.now = func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local) }
	v.Update(calendarsLoadedMsg{calendars: testCalendars()})
	v.Update(recordingsLoadedMsg{requestResult: currentRequest(v), recordings: []Recording{
		{ID: 8, Title: "Team sync", Type: "Calendar::Event",
			StartsAt: atLocal("2026-08-20T10:00:00"), EndsAt: atLocal("2026-08-20T11:00:00")},
	}})

	// In the week view ↑/↓ walk events within the current day.
	v.HandleContentKey(keyPress("down"))
	if v.selectedEvent != "8" {
		t.Fatalf("down did not select the event in week view (selectedEvent=%q)", v.selectedEvent)
	}
	v.HandleContentKey(keyPress("enter"))
	if v.detail == nil {
		t.Fatal("enter on week view did not open the event card")
	}
	if !strings.Contains(stripANSI(v.detail.view()), "Team sync") {
		t.Errorf("week-view card does not show the event title:\n%s", stripANSI(v.detail.view()))
	}
	v.HandleContentKey(keyPress("esc"))
	if v.detail != nil {
		t.Fatal("esc did not close the card on week view")
	}
}

// An all-day event with a same-day end should show as a single day, not a range.
func TestAllDayEventSameDayEndShowsAsSingleDay(t *testing.T) {
	d := &eventDetail{event: Recording{
		AllDay:   true,
		StartsAt: at("2026-08-21T00:00:00Z"),
		EndsAt:   at("2026-08-21T23:59:59Z"),
	}}
	got := d.when()
	if strings.Contains(got, "–") {
		t.Errorf("same-day all-day event showed a range: %q", got)
	}
	if !strings.Contains(got, "all day") {
		t.Errorf("all-day event missing 'all day': %q", got)
	}
}

// An all-day event whose end is exactly midnight of the next day (exclusive) should show as
// a single day, not "Aug 21 – Aug 22".
func TestAllDayEventExclusiveMidnightEndShowsAsSingleDay(t *testing.T) {
	d := &eventDetail{event: Recording{
		AllDay:   true,
		StartsAt: at("2026-08-21T00:00:00Z"),
		EndsAt:   at("2026-08-22T00:00:00Z"), // exclusive midnight
	}}
	got := d.when()
	if strings.Contains(got, "–") {
		t.Errorf("exclusive-midnight all-day event showed a range: %q", got)
	}
	if !strings.Contains(got, "all day") {
		t.Errorf("all-day event missing 'all day': %q", got)
	}
}

// A multi-day all-day event should still show the range.
func TestAllDayEventMultiDayShowsRange(t *testing.T) {
	d := &eventDetail{event: Recording{
		AllDay:   true,
		StartsAt: at("2026-08-21T00:00:00Z"),
		EndsAt:   at("2026-08-23T00:00:00Z"), // exclusive — event covers Aug 21 & 22
	}}
	got := d.when()
	if !strings.Contains(got, "–") {
		t.Errorf("multi-day all-day event did not show a range: %q", got)
	}
}

// wrapText must hard-wrap a single token that exceeds maxWidth.
func TestWrapTextHardWrapsLongTokens(t *testing.T) {
	long := "https://meet.example.com/a-very-long-room-name-that-exceeds-the-modal-width"
	lines := wrapText(long, 20)
	for _, line := range lines {
		if len(line) > 20 {
			t.Errorf("wrapText produced a line longer than maxWidth: %q (len=%d)", line, len(line))
		}
	}
	rejoined := strings.Join(lines, "")
	if rejoined != long {
		t.Errorf("wrapText lost characters: got %q, want %q", rejoined, long)
	}
}
